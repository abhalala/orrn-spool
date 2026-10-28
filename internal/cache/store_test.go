package cache

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/orrn/spool/internal/db"
)

type canonicalChunkFixture struct {
	payload  ChunkPayload
	unsigned []byte
}

func newTestStore(t *testing.T) *Store {
	t.Helper()
	sqlDB, err := db.Open(filepath.Join(t.TempDir(), "edge.db"))
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })

	store, err := NewStore(context.Background(), sqlDB)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	return store
}

func newChunkDocument(snapshotID string, sequence int, records []map[string]any) map[string]any {
	if records == nil {
		records = []map[string]any{}
	}
	return map[string]any{
		"protocolVersion":    1,
		"cacheSchemaVersion": CacheSchemaVersion,
		"companyId":          "comp-1",
		"section":            "stock",
		"snapshotId":         snapshotID,
		"sequence":           sequence,
		"recordCount":        len(records),
		"records":            records,
		"summaries":          []map[string]any{},
	}
}

// newCanonicalChunkFixture calculates hashes independently from the store's
// canonicalization code. The returned wire JSON is deliberately indented.
func newCanonicalChunkFixture(t *testing.T, document map[string]any) canonicalChunkFixture {
	t.Helper()
	delete(document, "sha256")
	unsigned := fixtureJSON(t, document, false)
	sum := sha256.Sum256(unsigned)
	sha256Hex := hex.EncodeToString(sum[:])
	document["sha256"] = sha256Hex

	sequence, sequenceOK := document["sequence"].(int)
	recordCount, recordCountOK := document["recordCount"].(int)
	if !sequenceOK || !recordCountOK {
		t.Fatal("fixture sequence and recordCount must be integers")
	}
	return canonicalChunkFixture{
		payload: ChunkPayload{
			Sequence:    sequence,
			SHA256:      sha256Hex,
			RecordCount: recordCount,
			RawJSON:     fixtureJSON(t, document, true),
		},
		unsigned: unsigned,
	}
}

func fixtureJSON(t *testing.T, value any, indent bool) []byte {
	t.Helper()
	var encoded bytes.Buffer
	encoder := json.NewEncoder(&encoded)
	encoder.SetEscapeHTML(false)
	if indent {
		encoder.SetIndent("", "  ")
	}
	if err := encoder.Encode(value); err != nil {
		t.Fatalf("encode fixture: %v", err)
	}
	return bytes.TrimSuffix(encoded.Bytes(), []byte{'\n'})
}

func newSectionMeta(snapshotID string, chunks ...canonicalChunkFixture) SectionMeta {
	var canonical bytes.Buffer
	canonical.WriteByte('[')
	totalRecords := 0
	for idx, chunk := range chunks {
		if idx > 0 {
			canonical.WriteByte(',')
		}
		canonical.Write(chunk.unsigned)
		totalRecords += chunk.payload.RecordCount
	}
	canonical.WriteByte(']')
	sum := sha256.Sum256(canonical.Bytes())

	return SectionMeta{
		Section:       "stock",
		SnapshotID:    snapshotID,
		CompanyID:     "comp-1",
		GeneratedAt:   "2026-10-26T12:00:00Z",
		ContentSHA256: hex.EncodeToString(sum[:]),
		RecordCount:   totalRecords,
	}
}

func TestCacheStoreUsesCanonicalUnsignedChunkAndOrderedSectionHashes(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)

	first := newCanonicalChunkFixture(t, newChunkDocument("snap-1", 0, []map[string]any{
		{
			"entityId": "b-1",
			"sortKey":  "001",
			"dto": map[string]any{
				"serial":   "26I4086",
				"html":     "<b>&</b>",
				"quantity": json.Number("1.20"),
				"sha256":   "nested-sha256-is-part-of-the-content",
			},
		},
	}))
	second := newCanonicalChunkFixture(t, newChunkDocument("snap-1", 1, []map[string]any{
		{
			"entityId": "b-2",
			"sortKey":  "002",
			"dto":      map[string]any{"serial": "26I4087"},
		},
	}))

	// This wire object has noncanonical key order at every level, whitespace,
	// unescaped HTML, a noncanonical number token, and a nested sha256 field.
	first.payload.RawJSON = []byte(`{
  "summaries": [],
  "sha256": "` + first.payload.SHA256 + `",
  "snapshotId": "snap-1",
  "sequence": 0,
  "section": "stock",
  "records": [{
    "sortKey": "001",
    "entityId": "b-1",
    "dto": {
      "sha256": "nested-sha256-is-part-of-the-content",
      "quantity": 1.20,
      "html": "<b>&</b>",
      "serial": "26I4086"
    }
  }],
  "recordCount": 1,
  "protocolVersion": 1,
  "companyId": "comp-1",
  "cacheSchemaVersion": 1
}`)
	meta := newSectionMeta("snap-1", first, second)

	rawSum := sha256.Sum256(first.payload.RawJSON)
	if first.payload.SHA256 == hex.EncodeToString(rawSum[:]) {
		t.Fatal("fixture does not distinguish canonical hashing from raw hashing")
	}
	if err := store.ApplySection(ctx, meta, []ChunkPayload{second.payload, first.payload}); err != nil {
		t.Fatalf("ApplySection: %v", err)
	}

	records, gotMeta, err := store.ListRecords(ctx, "stock", 50, 0)
	if err != nil {
		t.Fatalf("ListRecords: %v", err)
	}
	if len(records) != 2 || records[0].EntityID != "b-1" || records[1].EntityID != "b-2" {
		t.Fatalf("unexpected records: %+v", records)
	}
	if gotMeta.SnapshotID != meta.SnapshotID || gotMeta.ContentSHA256 != meta.ContentSHA256 || gotMeta.RecordCount != 2 {
		t.Fatalf("unexpected section metadata: %+v", gotMeta)
	}
}

func TestCacheStoreMatchesTypeScriptDigestVector(t *testing.T) {
	document := newChunkDocument("snap-fixed", 0, []map[string]any{
		{
			"entityId": "b-1",
			"sortKey":  "001",
			"dto":      map[string]any{"serial": "26I4086", "html": "<b>&</b>", "quantity": json.Number("1.2")},
		},
	})
	fixture := newCanonicalChunkFixture(t, document)
	if fixture.payload.SHA256 != "c0f04cab73f8d6e75aa331f774a02e7274a7dacb7b1199905879daf45f384c43" {
		t.Fatalf("chunk digest = %s", fixture.payload.SHA256)
	}
	meta := newSectionMeta("snap-fixed", fixture)
	if meta.ContentSHA256 != "7f19b0f63e40ebdd2bf0fb99ea7beba16fad3722cf5e6863a10c3772bc57f6d9" {
		t.Fatalf("section digest = %s", meta.ContentSHA256)
	}
	if err := newTestStore(t).ApplySection(context.Background(), meta, []ChunkPayload{fixture.payload}); err != nil {
		t.Fatalf("ApplySection: %v", err)
	}
}

func TestCacheStoreRejectsChunkMetadataAndCountMismatch(t *testing.T) {
	tests := []struct {
		name           string
		wantError      string
		mutateDocument func(map[string]any)
		mutatePayload  func(*ChunkPayload)
		mutateMeta     func(*SectionMeta)
	}{
		{
			name:      "protocol version",
			wantError: "protocolVersion",
			mutateDocument: func(document map[string]any) {
				document["protocolVersion"] = 2
			},
		},
		{
			name:      "cache schema version",
			wantError: "cacheSchemaVersion",
			mutateDocument: func(document map[string]any) {
				document["cacheSchemaVersion"] = CacheSchemaVersion + 1
			},
		},
		{
			name:      "company",
			wantError: "companyId",
			mutateDocument: func(document map[string]any) {
				document["companyId"] = "comp-2"
			},
		},
		{
			name:      "section",
			wantError: "section \"orders\"",
			mutateDocument: func(document map[string]any) {
				document["section"] = "orders"
			},
		},
		{
			name:      "snapshot",
			wantError: "snapshotId",
			mutateDocument: func(document map[string]any) {
				document["snapshotId"] = "snap-2"
			},
		},
		{
			name:      "document sequence",
			wantError: "document sequence",
			mutateDocument: func(document map[string]any) {
				document["sequence"] = 1
			},
			mutatePayload: func(payload *ChunkPayload) {
				payload.Sequence = 0
			},
		},
		{
			name:      "descriptor sequence",
			wantError: "non-contiguous chunk sequence",
			mutatePayload: func(payload *ChunkPayload) {
				payload.Sequence = 1
			},
		},
		{
			name:      "document record count",
			wantError: "document record count",
			mutateDocument: func(document map[string]any) {
				document["recordCount"] = 2
			},
		},
		{
			name:      "descriptor record count",
			wantError: "descriptor record count",
			mutatePayload: func(payload *ChunkPayload) {
				payload.RecordCount = 2
			},
		},
		{
			name:      "section record count",
			wantError: "total record count",
			mutateMeta: func(meta *SectionMeta) {
				meta.RecordCount = 2
			},
		},
		{
			name:      "descriptor hash",
			wantError: "sha256 mismatch",
			mutatePayload: func(payload *ChunkPayload) {
				payload.SHA256 = strings.Repeat("0", 64)
			},
		},
		{
			name:      "wire hash",
			wantError: "wire sha256 mismatch",
			mutatePayload: func(payload *ChunkPayload) {
				payload.RawJSON = bytes.Replace(payload.RawJSON, []byte(payload.SHA256), []byte(strings.Repeat("0", 64)), 1)
			},
		},
		{
			name:      "section content hash",
			wantError: "section contentSha256 mismatch",
			mutateMeta: func(meta *SectionMeta) {
				meta.ContentSHA256 = strings.Repeat("0", 64)
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := newTestStore(t)
			document := newChunkDocument("snap-1", 0, []map[string]any{
				{"entityId": "b-1", "sortKey": "001", "dto": map[string]any{"serial": "26I4086"}},
			})
			if test.mutateDocument != nil {
				test.mutateDocument(document)
			}
			fixture := newCanonicalChunkFixture(t, document)
			if test.mutatePayload != nil {
				test.mutatePayload(&fixture.payload)
			}
			meta := newSectionMeta("snap-1", fixture)
			if test.mutateMeta != nil {
				test.mutateMeta(&meta)
			}

			err := store.ApplySection(context.Background(), meta, []ChunkPayload{fixture.payload})
			if !errors.Is(err, ErrSectionCorrupt) {
				t.Fatalf("expected ErrSectionCorrupt, got %v", err)
			}
			if !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("expected error containing %q, got %v", test.wantError, err)
			}
			if _, ok, err := store.GetSectionMeta(context.Background(), "stock"); err != nil || ok {
				t.Fatalf("corrupt section must not be committed: ok=%v err=%v", ok, err)
			}
		})
	}
}

func TestCacheStoreRequiresEveryChunkIncludingEmptyCompleteSection(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)

	empty := newCanonicalChunkFixture(t, newChunkDocument("snap-empty", 0, nil))
	emptyMeta := newSectionMeta("snap-empty", empty)
	if err := store.ApplySection(ctx, emptyMeta, []ChunkPayload{empty.payload}); err != nil {
		t.Fatalf("ApplySection empty complete section: %v", err)
	}
	records, meta, err := store.ListRecords(ctx, "stock", 50, 0)
	if err != nil || len(records) != 0 || meta.RecordCount != 0 || meta.SnapshotID != "snap-empty" {
		t.Fatalf("unexpected empty section: records=%+v meta=%+v err=%v", records, meta, err)
	}

	noChunkMeta := newSectionMeta("snap-no-chunks")
	if err := store.ApplySection(ctx, noChunkMeta, nil); !errors.Is(err, ErrSectionCorrupt) {
		t.Fatalf("expected a complete section without chunks to fail, got %v", err)
	}

	first := newCanonicalChunkFixture(t, newChunkDocument("snap-incomplete", 0, nil))
	second := newCanonicalChunkFixture(t, newChunkDocument("snap-incomplete", 1, nil))
	incompleteMeta := newSectionMeta("snap-incomplete", first, second)
	if err := store.ApplySection(ctx, incompleteMeta, []ChunkPayload{first.payload}); !errors.Is(err, ErrSectionCorrupt) {
		t.Fatalf("expected a missing final chunk to fail, got %v", err)
	}

	_, currentMeta, err := store.ListRecords(ctx, "stock", 50, 0)
	if err != nil || currentMeta.SnapshotID != "snap-empty" {
		t.Fatalf("failed replacements must preserve the empty section: meta=%+v err=%v", currentMeta, err)
	}
}

func TestCacheStorePreservesPreviousGenerationOnCorruptChunkAndWipesOnRevoke(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)

	initial := newCanonicalChunkFixture(t, newChunkDocument("snap-1", 0, []map[string]any{
		{"entityId": "b-1", "sortKey": "26I4086", "dto": map[string]any{"serial": "26I4086"}},
	}))
	if err := store.ApplySection(ctx, newSectionMeta("snap-1", initial), []ChunkPayload{initial.payload}); err != nil {
		t.Fatalf("ApplySection snap-1: %v", err)
	}

	corrupt := newCanonicalChunkFixture(t, newChunkDocument("snap-2", 0, []map[string]any{
		{"entityId": "b-2", "sortKey": "26I4087", "dto": map[string]any{"serial": "26I4087"}},
	}))
	corrupt.payload.SHA256 = "bad"
	if err := store.ApplySection(ctx, newSectionMeta("snap-2", corrupt), []ChunkPayload{corrupt.payload}); err == nil {
		t.Fatal("expected corrupt snapshot to fail")
	}

	records, meta, err := store.ListRecords(ctx, "stock", 50, 0)
	if err != nil || meta.SnapshotID != "snap-1" || len(records) != 1 || records[0].EntityID != "b-1" {
		t.Fatalf("expected snap-1 to remain active, got records=%+v meta=%+v err=%v", records, meta, err)
	}

	if err := store.WipeCompanyCache(ctx); err != nil {
		t.Fatalf("WipeCompanyCache: %v", err)
	}
	recordsAfter, _, err := store.ListRecords(ctx, "stock", 50, 0)
	if err != nil {
		t.Fatalf("ListRecords after wipe: %v", err)
	}
	if len(recordsAfter) != 0 {
		t.Fatalf("expected 0 cached records after revocation wipe, got %d", len(recordsAfter))
	}
}
