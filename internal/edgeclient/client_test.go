package edgeclient

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/orrn/spool/internal/api/middleware"
	"github.com/orrn/spool/internal/cache"
	"github.com/orrn/spool/internal/db"
	"github.com/orrn/spool/internal/jobs"
	"github.com/orrn/spool/internal/node"
)

func TestClientSignsRequestsAndConfirmsPendingRotation(t *testing.T) {
	confirmed := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/edge/v1/commands/poll":
			if got := r.Header.Get("X-ORRN-Edge-Version"); got != "1.0.0" {
				t.Errorf("X-ORRN-Edge-Version = %q, want 1.0.0", got)
			}
			if got := r.Header.Get("X-ORRN-Agent-Version"); got != "" {
				t.Errorf("legacy agent-version header still sent: %q", got)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"commands":                 []any{},
				"configVersion":            1,
				"pendingCredentialVersion": 2,
				"snapshotManifestVersion":  "snap-1",
			})
		case "/api/edge/v1/config":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"protocolVersion":          1,
				"configSchemaVersion":      1,
				"deploymentId":             "019a0000-0000-7000-8000-000000000001",
				"instanceId":               "019a0000-0000-7000-8000-000000000002",
				"configVersion":            1,
				"pendingCredentialVersion": 2,
				"pendingSecret":            "secret-v2-1111111111111111111111111111",
				"printers":                 []any{},
				"profiles":                 []any{},
				"templates":                []any{},
			})
		case "/api/edge/v1/credentials/confirm":
			tok := r.Header.Get("Authorization")[len("Bearer "):]
			if err := middleware.NewORRNVerifier("secret-v2-1111111111111111111111111111").Verify(r.Method, r.URL.RequestURI(), []byte(`{"credentialVersion":2}`), tok); err != nil {
				t.Fatalf("new secret signature verify failed: %v", err)
			}
			confirmed = true
			_ = json.NewEncoder(w).Encode(map[string]any{"credentialVersion": 2})
		}
	}))
	defer srv.Close()

	store := node.NewStore(filepath.Join(t.TempDir(), "node.json"))
	_ = store.Save(node.State{
		ProtocolVersion: 1,
		DeploymentID:    "019a0000-0000-7000-8000-000000000001",
		InstanceID:      "019a0000-0000-7000-8000-000000000002",
		APIOrigin:       srv.URL,
		Active:          node.Credential{Version: 1, Secret: "secret-v1-0000000000000000000000000000"},
	})

	client := New(store, "1.0.0", srv.Client())
	if err := client.PollOnce(context.Background()); err != nil {
		t.Fatalf("PollOnce: %v", err)
	}
	if !confirmed {
		t.Fatalf("expected pending credential to be staged and confirmed")
	}
	st, _ := store.Load()
	if st.Active.Version != 2 || st.Pending != nil {
		t.Fatalf("expected active version 2 after confirm, got %+v", st)
	}
}

func TestClientRevocationDisablesLocalAccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"code":"node_revoked"}`))
	}))
	defer srv.Close()
	store := node.NewStore(filepath.Join(t.TempDir(), "node.json"))
	if err := store.Save(node.State{
		ProtocolVersion: 1, DeploymentID: "deployment", InstanceID: "instance", APIOrigin: srv.URL,
		Active: node.Credential{Version: 1, Secret: "secret-v1-0000000000000000000000000000"},
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	called := false
	client := New(store, "1.0.0", srv.Client()).WithRevocationHandler(func(context.Context) error {
		called = true
		return nil
	})
	if err := client.PollOnce(context.Background()); err == nil {
		t.Fatal("PollOnce succeeded after node_revoked")
	}
	st, err := store.Load()
	if err != nil || !st.Revoked || st.Active.Secret != "" || !called {
		t.Fatalf("revocation cleanup incomplete: state=%+v called=%v err=%v", st, called, err)
	}
}

type snapshotSyncFixture struct {
	manifest       map[string]any
	rawManifest    []byte
	sectionHeaders map[string]map[string]any
	chunks         map[string][]byte
	documents      map[string]map[string]any
	chunkRequests  int
}

func newSnapshotSyncFixture(t *testing.T, generation string) *snapshotSyncFixture {
	t.Helper()
	f := &snapshotSyncFixture{
		manifest: map[string]any{
			"protocolVersion":    1,
			"cacheSchemaVersion": cache.CacheSchemaVersion,
			"companyId":          "company-snapshot-sync",
			"minAgentVersion":    "1.0.0",
			"generatedAt":        "2026-01-02T03:04:05Z",
		},
		sectionHeaders: make(map[string]map[string]any),
		chunks:         make(map[string][]byte),
		documents:      make(map[string]map[string]any),
	}
	record := func(id, sortKey string, dto map[string]any) map[string]any {
		return map[string]any{"entityId": id, "sortKey": sortKey, "dto": dto}
	}
	specs := []struct {
		name      string
		chunks    [][]map[string]any
		summaries any
	}{
		{
			name: "stock",
			chunks: [][]map[string]any{
				{record(generation+"-stock-1", "001", map[string]any{"sku": "WIDGET", "quantity": 1, "label": "<stock>&"})},
				{record(generation+"-stock-2", "002", map[string]any{"sku": "GADGET", "quantity": 2})},
			},
			summaries: []any{map[string]any{"key": "totalQuantity", "value": 3}},
		},
		{
			name: "orders",
			chunks: [][]map[string]any{
				{record(generation+"-order-1", "001", map[string]any{"status": "open", "total": json.Number("1.20")})},
			},
			summaries: []any{map[string]any{"key": "openOrders", "value": 1}},
		},
		{
			name:      "dies",
			chunks:    [][]map[string]any{{}},
			summaries: []any{map[string]any{"key": "empty", "value": true}},
		},
		{name: "print_status", chunks: [][]map[string]any{{}}},
		{name: "company_format", chunks: [][]map[string]any{{}}},
	}

	sectionList := make([]any, 0, len(specs))
	for _, spec := range specs {
		snapshotID := generation + "-" + spec.name
		header := map[string]any{
			"section":       spec.name,
			"snapshotId":    snapshotID,
			"generatedAt":   "2026-01-02T03:04:05Z",
			"contentSha256": "",
			"recordCount":   0,
			"chunkCount":    len(spec.chunks),
			"complete":      true,
		}
		chunkHeaders := make([]any, len(spec.chunks))
		totalRecords := 0
		for sequence, records := range spec.chunks {
			if records == nil {
				records = []map[string]any{}
			}
			totalRecords += len(records)
			summaries := any([]any{})
			if sequence == 0 && spec.summaries != nil {
				summaries = spec.summaries
			}
			document := map[string]any{
				"protocolVersion":    1,
				"cacheSchemaVersion": cache.CacheSchemaVersion,
				"companyId":          f.manifest["companyId"],
				"section":            spec.name,
				"snapshotId":         snapshotID,
				"sequence":           sequence,
				"recordCount":        len(records),
				"records":            records,
				"summaries":          summaries,
			}
			path := fmt.Sprintf("/api/edge/v1/snapshots/%s/chunks/%d", snapshotID, sequence)
			f.documents[path] = document
			chunkHeaders[sequence] = map[string]any{
				"sequence":    sequence,
				"sha256":      "",
				"recordCount": len(records),
				"byteLength":  0,
			}
		}
		header["recordCount"] = totalRecords
		header["chunks"] = chunkHeaders
		f.sectionHeaders[spec.name] = header
		sectionList = append(sectionList, header)
		for sequence := range spec.chunks {
			f.refreshChunk(t, spec.name, sequence, true)
		}
	}
	f.manifest["sections"] = sectionList
	return f
}

func (f *snapshotSyncFixture) sectionHeader(section string) map[string]any {
	return f.sectionHeaders[section]
}

func (f *snapshotSyncFixture) chunkHeader(section string, sequence int) map[string]any {
	return f.sectionHeader(section)["chunks"].([]any)[sequence].(map[string]any)
}

func (f *snapshotSyncFixture) chunkPath(section string, sequence int) string {
	return fmt.Sprintf("/api/edge/v1/snapshots/%s/chunks/%d", f.sectionHeader(section)["snapshotId"], sequence)
}

func (f *snapshotSyncFixture) document(section string, sequence int) map[string]any {
	return f.documents[f.chunkPath(section, sequence)]
}

// refreshChunk deliberately uses fixture-local canonicalization rather than any
// production hashing helper. Wire JSON is indented so its byte length and hash
// cover different representations of the same document.
func (f *snapshotSyncFixture) refreshChunk(t *testing.T, section string, sequence int, rehash bool) {
	t.Helper()
	document := f.document(section, sequence)
	header := f.chunkHeader(section, sequence)
	if rehash {
		unsigned := snapshotSyncUnsignedChunk(t, document)
		digest := sha256.Sum256(unsigned)
		document["sha256"] = hex.EncodeToString(digest[:])
		header["sha256"] = document["sha256"]
	}
	wire, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		t.Fatalf("marshal snapshot chunk: %v", err)
	}
	path := f.chunkPath(section, sequence)
	f.chunks[path] = wire
	header["byteLength"] = len(wire)
	if !rehash {
		return
	}

	sectionHash := sha256.New()
	_, _ = sectionHash.Write([]byte("["))
	for index := range f.sectionHeader(section)["chunks"].([]any) {
		if index > 0 {
			_, _ = sectionHash.Write([]byte(","))
		}
		_, _ = sectionHash.Write(snapshotSyncUnsignedChunk(t, f.document(section, index)))
	}
	_, _ = sectionHash.Write([]byte("]"))
	f.sectionHeader(section)["contentSha256"] = hex.EncodeToString(sectionHash.Sum(nil))
}

func snapshotSyncUnsignedChunk(t *testing.T, document map[string]any) []byte {
	t.Helper()
	canonical := snapshotSyncCanonicalJSON(t, document)
	decoder := json.NewDecoder(bytes.NewReader(canonical))
	decoder.UseNumber()
	var object map[string]any
	if err := decoder.Decode(&object); err != nil {
		t.Fatalf("decode fixture chunk: %v", err)
	}
	delete(object, "sha256")
	return snapshotSyncCanonicalJSON(t, object)
}

func snapshotSyncCanonicalJSON(t *testing.T, value any) []byte {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal canonical fixture input: %v", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var decoded any
	if err := decoder.Decode(&decoded); err != nil {
		t.Fatalf("decode canonical fixture input: %v", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		t.Fatalf("canonical fixture has trailing JSON: %v", err)
	}
	var encoded bytes.Buffer
	encoder := json.NewEncoder(&encoded)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(decoded); err != nil {
		t.Fatalf("encode canonical fixture: %v", err)
	}
	return bytes.TrimSuffix(encoded.Bytes(), []byte{'\n'})
}

func (f *snapshotSyncFixture) serveHTTP(t *testing.T, w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/api/edge/v1/snapshots/manifest" {
		w.Header().Set("Content-Type", "application/json")
		if f.rawManifest != nil {
			_, _ = w.Write(f.rawManifest)
			return
		}
		if err := json.NewEncoder(w).Encode(f.manifest); err != nil {
			t.Errorf("encode snapshot manifest: %v", err)
		}
		return
	}
	raw, ok := f.chunks[r.URL.Path]
	if !ok {
		http.NotFound(w, r)
		return
	}
	f.chunkRequests++
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(raw)
}

func newSnapshotSyncClient(t *testing.T, srv *httptest.Server) (*Client, *cache.Store) {
	t.Helper()
	ctx := context.Background()
	sqlDB, err := db.Open(filepath.Join(t.TempDir(), "edge.db"))
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	cacheStore, err := cache.NewStore(ctx, sqlDB)
	if err != nil {
		t.Fatalf("cache.NewStore: %v", err)
	}
	nodeStore := node.NewStore(filepath.Join(t.TempDir(), "node.json"))
	if err := nodeStore.Save(node.State{
		ProtocolVersion: 1,
		DeploymentID:    "deployment-snapshot-sync",
		InstanceID:      "instance-snapshot-sync",
		APIOrigin:       srv.URL,
		Active:          node.Credential{Version: 1, Secret: "secret-v1-0000000000000000000000000000"},
	}); err != nil {
		t.Fatalf("nodeStore.Save: %v", err)
	}
	return New(nodeStore, "1.0.0", srv.Client()).WithStores(nil, cacheStore), cacheStore
}

func assertSnapshotSyncSection(t *testing.T, store *cache.Store, fixture *snapshotSyncFixture, section string) {
	t.Helper()
	ctx := context.Background()
	records, meta, err := store.ListRecords(ctx, section, 100, 0)
	if err != nil {
		t.Fatalf("ListRecords(%s): %v", section, err)
	}
	header := fixture.sectionHeader(section)
	if meta.SnapshotID != header["snapshotId"] || meta.CompanyID != fixture.manifest["companyId"] ||
		meta.GeneratedAt != header["generatedAt"] || meta.ContentSHA256 != header["contentSha256"] ||
		meta.RecordCount != header["recordCount"] {
		t.Fatalf("unexpected %s metadata: got %+v, want header %+v", section, meta, header)
	}

	expected := make(map[string]map[string]any)
	for sequence := range header["chunks"].([]any) {
		for _, record := range fixture.document(section, sequence)["records"].([]map[string]any) {
			expected[record["entityId"].(string)] = record
		}
	}
	if len(records) != len(expected) {
		t.Fatalf("%s record count: got %d, want %d", section, len(records), len(expected))
	}
	for _, record := range records {
		want, ok := expected[record.EntityID]
		if !ok {
			t.Fatalf("unexpected %s record %q", section, record.EntityID)
		}
		if record.Section != section || record.SortKey != want["sortKey"] {
			t.Fatalf("unexpected %s record identity: %+v", section, record)
		}
		if gotDTO, wantDTO := snapshotSyncCanonicalJSON(t, record.DTO), snapshotSyncCanonicalJSON(t, want["dto"]); !bytes.Equal(gotDTO, wantDTO) {
			t.Fatalf("%s record %s DTO: got %s, want %s", section, record.EntityID, gotDTO, wantDTO)
		}
	}
	gotSummaries := snapshotSyncCanonicalJSON(t, meta.SummariesJSON)
	wantSummaries := snapshotSyncCanonicalJSON(t, fixture.document(section, 0)["summaries"])
	if !bytes.Equal(gotSummaries, wantSummaries) {
		t.Fatalf("%s summaries: got %s, want %s", section, gotSummaries, wantSummaries)
	}
}

func TestSyncSnapshotsOnceAppliesCompleteManifestIncludingEmptySectionsAndSummaries(t *testing.T) {
	fixture := newSnapshotSyncFixture(t, "complete")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fixture.serveHTTP(t, w, r)
	}))
	defer srv.Close()
	client, cacheStore := newSnapshotSyncClient(t, srv)

	if err := client.SyncSnapshotsOnce(context.Background()); err != nil {
		t.Fatalf("SyncSnapshotsOnce: %v", err)
	}
	for _, section := range []string{"stock", "orders", "dies", "print_status", "company_format"} {
		assertSnapshotSyncSection(t, cacheStore, fixture, section)
	}
	if fixture.chunkRequests != 6 {
		t.Fatalf("chunk requests: got %d, want 6", fixture.chunkRequests)
	}
}

func TestSyncSnapshotsOnceRejectsMalformedOrIncompleteManifestWithoutDefaults(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*snapshotSyncFixture)
	}{
		{name: "malformed JSON", mutate: func(f *snapshotSyncFixture) { f.rawManifest = []byte(`{"sections":`) }},
		{name: "missing protocol version", mutate: func(f *snapshotSyncFixture) { delete(f.manifest, "protocolVersion") }},
		{name: "missing cache schema version", mutate: func(f *snapshotSyncFixture) { delete(f.manifest, "cacheSchemaVersion") }},
		{name: "missing company", mutate: func(f *snapshotSyncFixture) { delete(f.manifest, "companyId") }},
		{name: "missing generated at", mutate: func(f *snapshotSyncFixture) { delete(f.manifest, "generatedAt") }},
		{name: "missing required section", mutate: func(f *snapshotSyncFixture) {
			sections := f.manifest["sections"].([]any)
			filtered := make([]any, 0, len(sections)-1)
			for _, raw := range sections {
				if raw.(map[string]any)["section"] != "company_format" {
					filtered = append(filtered, raw)
				}
			}
			f.manifest["sections"] = filtered
		}},
		{name: "unknown section cannot replace required section", mutate: func(f *snapshotSyncFixture) {
			f.sectionHeader("company_format")["section"] = "unexpected"
		}},
		{name: "incomplete section", mutate: func(f *snapshotSyncFixture) { f.sectionHeader("stock")["complete"] = false }},
		{name: "missing section snapshot", mutate: func(f *snapshotSyncFixture) { delete(f.sectionHeader("orders"), "snapshotId") }},
		{name: "missing chunk descriptors", mutate: func(f *snapshotSyncFixture) { delete(f.sectionHeader("stock"), "chunks") }},
		{name: "missing chunk hash", mutate: func(f *snapshotSyncFixture) { delete(f.chunkHeader("stock", 0), "sha256") }},
		{name: "missing chunk byte length", mutate: func(f *snapshotSyncFixture) { delete(f.chunkHeader("stock", 0), "byteLength") }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newSnapshotSyncFixture(t, "invalid")
			test.mutate(fixture)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				fixture.serveHTTP(t, w, r)
			}))
			defer srv.Close()
			client, cacheStore := newSnapshotSyncClient(t, srv)

			if err := client.SyncSnapshotsOnce(context.Background()); err == nil {
				t.Fatal("SyncSnapshotsOnce succeeded with invalid manifest")
			}
			if fixture.chunkRequests != 0 {
				t.Fatalf("invalid manifest fetched %d chunks", fixture.chunkRequests)
			}
			for _, section := range []string{"stock", "orders", "dies", "print_status", "company_format"} {
				if _, ok, err := cacheStore.GetSectionMeta(context.Background(), section); err != nil || ok {
					t.Fatalf("%s cache changed after invalid manifest: present=%v err=%v", section, ok, err)
				}
			}
		})
	}
}

func TestSyncSnapshotsOnceRejectsInvalidChunksWithoutReplacingCachedSection(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*testing.T, *snapshotSyncFixture)
	}{
		{name: "tampered second chunk of multi-chunk section", mutate: func(t *testing.T, f *snapshotSyncFixture) {
			document := f.document("stock", 1)
			dto := document["records"].([]map[string]any)[0]["dto"].(map[string]any)
			dto["quantity"] = 99
			f.refreshChunk(t, "stock", 1, false)
		}},
		{name: "wrong snapshot binding with recomputed hashes", mutate: func(t *testing.T, f *snapshotSyncFixture) {
			document := f.document("stock", 0)
			document["snapshotId"] = "unbound-snapshot"
			f.refreshChunk(t, "stock", 0, true)
		}},
		{name: "byte length shorter than response", mutate: func(_ *testing.T, f *snapshotSyncFixture) {
			f.chunkHeader("stock", 0)["byteLength"] = len(f.chunks[f.chunkPath("stock", 0)]) - 1
		}},
		{name: "byte length longer than response", mutate: func(_ *testing.T, f *snapshotSyncFixture) {
			f.chunkHeader("stock", 0)["byteLength"] = len(f.chunks[f.chunkPath("stock", 0)]) + 1
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			initial := newSnapshotSyncFixture(t, "before")
			attack := newSnapshotSyncFixture(t, "after")
			test.mutate(t, attack)
			current := initial
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				current.serveHTTP(t, w, r)
			}))
			defer srv.Close()
			client, cacheStore := newSnapshotSyncClient(t, srv)

			if err := client.SyncSnapshotsOnce(context.Background()); err != nil {
				t.Fatalf("seed SyncSnapshotsOnce: %v", err)
			}
			assertSnapshotSyncSection(t, cacheStore, initial, "stock")
			current = attack
			if err := client.SyncSnapshotsOnce(context.Background()); err == nil {
				t.Fatal("SyncSnapshotsOnce succeeded with invalid chunk")
			}
			if attack.chunkRequests == 0 {
				t.Fatal("invalid chunk was not requested")
			}
			assertSnapshotSyncSection(t, cacheStore, initial, "stock")
		})
	}
}

func TestEnrollSameDeploymentPreservesRuntimeAndAppliesInitialState(t *testing.T) {
	ctx := context.Background()
	sqlDB, err := db.Open(filepath.Join(t.TempDir(), "edge.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	jobStore, err := jobs.NewStore(ctx, sqlDB)
	if err != nil {
		t.Fatal(err)
	}
	cacheStore, err := cache.NewStore(ctx, sqlDB)
	if err != nil {
		t.Fatal(err)
	}
	legacy, err := jobStore.ResolvePrintCommand(ctx, jobs.PrintCommandInput{
		CommandID: "legacy-command", JobKey: "legacy-job", PayloadHash: "hash", PrinterID: "legacy-printer", TSPL: "CLS\n", Copies: 1,
	})
	if err != nil {
		t.Fatal(err)
	}

	const newDeployment = "019a0000-0000-7000-8000-000000000101"
	const newInstance = "019a0000-0000-7000-8000-000000000102"
	fixture := newSnapshotSyncFixture(t, "enrolled")
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/edge/v1/enroll":
			_ = json.NewEncoder(w).Encode(EnrollResponse{
				ProtocolVersion: 1, DeploymentID: newDeployment, InstanceID: newInstance,
				NodeName: "New site", SiteLabel: "New", APIOrigin: srv.URL,
				NodeSecret: "secret-v1-1111111111111111111111111111", CredentialVersion: 1, ConfigVersion: 1,
			})
		case "/api/edge/v1/config":
			_ = json.NewEncoder(w).Encode(ConfigResponse{
				ProtocolVersion: 1, ConfigSchemaVersion: 1, DeploymentID: newDeployment, InstanceID: newInstance,
				ConfigVersion: 1, Printers: []json.RawMessage{}, Profiles: []json.RawMessage{}, Templates: []json.RawMessage{},
			})
		default:
			fixture.serveHTTP(t, w, r)
		}
	}))
	defer srv.Close()

	nodeStore := node.NewStore(filepath.Join(t.TempDir(), "node.json"))
	if err := nodeStore.Save(node.State{
		ProtocolVersion: 1, DeploymentID: newDeployment, InstanceID: newInstance, InstallID: "same-install", APIOrigin: srv.URL,
		Active: node.Credential{Version: 1, Secret: "secret-v1-0000000000000000000000000000"},
	}); err != nil {
		t.Fatal(err)
	}
	accessRetired := false
	client := New(nodeStore, "1.0.0", srv.Client()).WithStores(jobStore, cacheStore).
		WithRevocationHandler(func(context.Context) error { accessRetired = true; return nil })
	if _, err := client.Enroll(ctx, srv.URL, "enr_new_token_1234567890", "same-install"); err != nil {
		t.Fatalf("Enroll: %v", err)
	}
	if accessRetired {
		t.Fatal("same-node re-enrollment retired local access")
	}
	claimed, err := jobStore.ClaimNextJob(ctx, "legacy-printer")
	if err != nil || claimed == nil || claimed.ID != legacy.AgentJobID {
		t.Fatalf("same-node job was not preserved: %+v %v", claimed, err)
	}
	events, err := jobStore.ListPendingOutboxEvents(ctx, 50)
	if err != nil || len(events) == 0 {
		t.Fatalf("same-node events missing: %+v %v", events, err)
	}
	if _, err := jobStore.GetJob(ctx, legacy.AgentJobID); err != nil {
		t.Fatalf("legacy audit history deleted: %v", err)
	}
	if version, _, err := jobStore.LoadAppliedConfig(ctx); err != nil || version != 1 {
		t.Fatalf("initial config not applied: version=%d err=%v", version, err)
	}
	assertSnapshotSyncSection(t, cacheStore, fixture, "stock")
	state, err := nodeStore.Load()
	if err != nil || state.DeploymentID != newDeployment || state.InstanceID != newInstance {
		t.Fatalf("new identity not active: %+v %v", state, err)
	}
}
