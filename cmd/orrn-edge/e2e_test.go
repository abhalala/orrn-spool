package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/orrn/spool/internal/api/middleware"
	"github.com/orrn/spool/internal/cache"
	"github.com/orrn/spool/internal/core"
	"github.com/orrn/spool/internal/db"
	"github.com/orrn/spool/internal/jobs"
	"github.com/orrn/spool/internal/localui"
)

type e2eCloud struct {
	mu         sync.Mutex
	secret     string
	revoked    bool
	printIndex int
	agentJobID string
	events     []map[string]any
	printer    *e2ePrinter
	server     *httptest.Server
}

type e2ePrinter struct {
	listener net.Listener
	mu       sync.Mutex
	payloads []string
}

func newE2EPrinter(t *testing.T) *e2ePrinter {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	printer := &e2ePrinter{listener: listener}
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				query := make([]byte, 3)
				_, _ = io.ReadFull(conn, query)
				if string(query) == "~!T" {
					// Like the real TA220: model without line ending, socket kept open.
					_, _ = io.WriteString(conn, "TA220")
					_, _ = io.Copy(io.Discard, conn)
					return
				}
				_, _ = conn.Write([]byte{0x00})
				payload, _ := io.ReadAll(conn)
				if len(payload) > 0 {
					printer.mu.Lock()
					printer.payloads = append(printer.payloads, string(payload))
					printer.mu.Unlock()
				}
			}()
		}
	}()
	return printer
}

func (p *e2ePrinter) address() (string, int) {
	host, port, _ := net.SplitHostPort(p.listener.Addr().String())
	var portNumber int
	_, _ = fmt.Sscanf(port, "%d", &portNumber)
	return host, portNumber
}

func (p *e2ePrinter) count() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.payloads)
}

func (p *e2ePrinter) payload(index int) string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.payloads[index]
}

func newE2ECloud(t *testing.T, printer *e2ePrinter) *e2eCloud {
	t.Helper()
	cloud := &e2eCloud{secret: "secret-v1-0000000000000000000000000000", printer: printer}
	cloud.server = httptest.NewServer(http.HandlerFunc(cloud.serveHTTP))
	t.Cleanup(cloud.server.Close)
	return cloud
}

func (c *e2eCloud) serveHTTP(w http.ResponseWriter, r *http.Request) {
	var body []byte
	if r.URL.Path != "/api/edge/v1/enroll" {
		body, _ = io.ReadAll(r.Body)
		c.mu.Lock()
		revoked := c.revoked
		secret := c.secret
		c.mu.Unlock()
		if revoked {
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"code":"node_revoked"}`))
			return
		}
		token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		verifier := middleware.NewORRNVerifier(secret)
		if r.Header.Get("X-ORRN-Node-ID") != "instance-e2e" || r.Header.Get("X-ORRN-Edge-Version") != "1.0.0" || verifier.Verify(r.Method, r.URL.RequestURI(), body, token) != nil {
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"code":"invalid_signature"}`))
			return
		}
	}

	w.Header().Set("Content-Type", "application/json")
	switch r.URL.Path {
	case "/api/edge/v1/enroll":
		_ = json.NewEncoder(w).Encode(map[string]any{
			"protocolVersion": 1, "deploymentId": "deployment-e2e", "instanceId": "instance-e2e",
			"nodeName": "E2E Plant", "siteLabel": "E2E Site", "apiOrigin": c.server.URL,
			"nodeSecret": c.secret, "credentialVersion": 1, "configVersion": 1,
		})
	case "/api/edge/v1/config":
		host, port := c.printer.address()
		_ = json.NewEncoder(w).Encode(map[string]any{
			"protocolVersion": 1, "configSchemaVersion": 1, "deploymentId": "deployment-e2e", "instanceId": "instance-e2e",
			"configVersion": 1, "pendingCredentialVersion": nil,
			"printers": []any{map[string]any{"printerId": "tsc-1", "name": "Fake TA220", "host": host, "port": port, "paused": false, "labelWidthMm": 102.25, "labelHeightMm": 76.2, "gapMm": 2, "dpi": 203}},
			"profiles": []any{}, "templates": []any{},
		})
	case "/api/edge/v1/commands/poll":
		commands := []any{}
		c.mu.Lock()
		if c.printIndex < 3 {
			index := c.printIndex
			c.printIndex++
			documents := []core.LabelDocument{core.OperationsDense, core.PolishedLabel, core.CompactLabel}
			commandIDs := []string{"019a0000-0000-7000-8000-00000000e201", "019a0000-0000-7000-8000-00000000e202", "019a0000-0000-7000-8000-00000000e203"}
			leaseIDs := []string{"019a0000-0000-7000-8000-00000000e211", "019a0000-0000-7000-8000-00000000e212", "019a0000-0000-7000-8000-00000000e213"}
			copies := []int{2, 1, 3}[index]
			jobKey := []string{"bundle:E2E-B:label:v1:attempt:1", "bundle:E2E-A:label:v1:attempt:1", "bundle:E2E-C:label:v1:attempt:1"}[index]
			payloadHash := strings.Repeat([]string{"a", "b", "c"}[index], 64)
			templateSchema, _ := json.Marshal(documents[index])
			commands = append(commands, map[string]any{
				"commandId": commandIDs[index], "kind": "print_job",
				"leaseId": leaseIDs[index], "leaseExpiresAt": time.Now().Add(time.Minute).UTC().Format(time.RFC3339Nano),
				"jobKey": jobKey, "payloadHash": payloadHash, "deliveryCount": 1,
				"payload": map[string]any{
					"jobKey": jobKey, "payloadHash": payloadHash, "printerId": "tsc-1", "copies": copies,
					"templateSchema": json.RawMessage(templateSchema),
					"variables": map[string]any{
						"serialNumber": "E2E-001", "qr": "https://orrn.in/b/E2E-001", "quantity": "12", "length": "6000 mm",
						"poNumber": "PO-42", "weight12ft": "18.40 kg", "weight_each": "1.53 kg", "seriesName": "6063-T6",
						"cast": "CAST-7", "salesOrder": "SO-9", "standard": "BIS", "cml": "CML-3", "weight": "18.40 kg",
					},
				},
			})
		}
		c.mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]any{"protocolVersion": 1, "configVersion": 1, "pendingCredentialVersion": nil, "commands": commands})
	case "/api/edge/v1/commands/ack":
		var ack struct {
			CommandID  string `json:"commandId"`
			AgentJobID string `json:"agentJobId"`
		}
		_ = json.NewDecoder(bytes.NewReader(body)).Decode(&ack)
		c.mu.Lock()
		c.agentJobID = ack.AgentJobID
		c.mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]any{"commandId": ack.CommandID, "outcome": "accepted", "resolution": "accepted", "agentJobId": ack.AgentJobID})
	case "/api/edge/v1/events":
		var batch struct {
			Events []map[string]any `json:"events"`
		}
		_ = json.NewDecoder(bytes.NewReader(body)).Decode(&batch)
		c.mu.Lock()
		c.events = append(c.events, batch.Events...)
		c.mu.Unlock()
		results := make([]map[string]any, 0, len(batch.Events))
		for _, event := range batch.Events {
			results = append(results, map[string]any{"eventId": event["eventId"], "status": "applied"})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"processed": len(results), "deduplicated": 0, "results": results})
	case "/api/edge/v1/snapshots/manifest":
		sections := make([]map[string]any, 0, 5)
		for _, section := range []string{"stock", "orders", "dies", "print_status", "company_format"} {
			sections = append(sections, e2eSnapshotSection(section))
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"protocolVersion": 1, "cacheSchemaVersion": 1, "companyId": "company-e2e", "minAgentVersion": "1.0.0",
			"generatedAt": "2026-10-27T12:00:00Z", "sections": sections,
		})
	default:
		if strings.HasPrefix(r.URL.Path, "/api/edge/v1/snapshots/") {
			section := strings.TrimPrefix(strings.Split(r.URL.Path, "/")[5], "snap-")
			_, _ = w.Write(e2eSnapshotChunk(section))
			return
		}
		http.NotFound(w, r)
	}
}

func e2eUnsignedChunk(section string) []byte {
	value := map[string]any{
		"protocolVersion": 1, "cacheSchemaVersion": 1, "companyId": "company-e2e", "section": section,
		"snapshotId": "snap-" + section, "sequence": 0, "recordCount": 0, "records": []any{}, "summaries": []any{},
	}
	raw, _ := json.Marshal(value)
	return raw
}

func e2eSnapshotSection(section string) map[string]any {
	unsigned := e2eUnsignedChunk(section)
	chunkDigest := sha256.Sum256(unsigned)
	sectionDigest := sha256.Sum256(append(append([]byte{'['}, unsigned...), ']'))
	return map[string]any{
		"section": section, "snapshotId": "snap-" + section, "generatedAt": "2026-10-27T12:00:00Z",
		"contentSha256": hex.EncodeToString(sectionDigest[:]), "recordCount": 0, "chunkCount": 1, "complete": true,
		"chunks": []any{map[string]any{"sequence": 0, "sha256": hex.EncodeToString(chunkDigest[:]), "recordCount": 0, "byteLength": len(e2eSnapshotChunk(section))}},
	}
}

func e2eSnapshotChunk(section string) []byte {
	unsigned := e2eUnsignedChunk(section)
	digest := sha256.Sum256(unsigned)
	var value map[string]any
	_ = json.Unmarshal(unsigned, &value)
	value["sha256"] = hex.EncodeToString(digest[:])
	raw, _ := json.Marshal(value)
	return raw
}

func TestLocalEdgeEndToEnd(t *testing.T) {
	ctx := context.Background()
	name := "orrn-edge"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	binary := filepath.Join(t.TempDir(), name)
	build := exec.Command("go", "build", "-trimpath", "-ldflags", "-X main.Version=1.0.0", "-o", binary, ".")
	build.Env = append(os.Environ(), "CGO_ENABLED=0")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build agent: %v: %s", err, output)
	}
	printer := newE2EPrinter(t)
	cloud := newE2ECloud(t, printer)
	stateDir := t.TempDir()
	enroll := exec.Command(binary, "enroll", "--state-dir", stateDir, "--api-origin", cloud.server.URL, "--token-file", "-")
	enroll.Stdin = strings.NewReader("enr_e2e_12345678901234567890")
	if output, err := enroll.CombinedOutput(); err != nil {
		t.Fatalf("enroll: %v: %s", err, output)
	}

	sqlDB, err := db.Open(filepath.Join(stateDir, "edge.db"))
	if err != nil {
		t.Fatal(err)
	}
	jobStore, err := jobs.NewStore(ctx, sqlDB)
	if err != nil {
		t.Fatal(err)
	}
	cacheStore, err := cache.NewStore(ctx, sqlDB)
	if err != nil {
		t.Fatal(err)
	}
	ui, err := localui.NewServer(ctx, sqlDB, jobStore, cacheStore)
	if err != nil {
		t.Fatal(err)
	}
	if err := ui.BootstrapAdmin(ctx, "admin-password"); err != nil {
		t.Fatal(err)
	}
	if err := ui.CreateFallbackUser(ctx, "operator", "1234", localui.RolePrinter); err != nil {
		t.Fatal(err)
	}
	_ = sqlDB.Close()

	adminAddr := freeE2EAddress(t)
	fallbackAddr := freeE2EAddress(t)
	startAgent := func() (*exec.Cmd, *bytes.Buffer) {
		var logs bytes.Buffer
		cmd := exec.Command(binary, "run", "--state-dir", stateDir, "--admin-addr", adminAddr, "--fallback-addr", fallbackAddr, "--poll-interval", "50ms", "--snapshot-interval", "1s")
		cmd.Stdout = &logs
		cmd.Stderr = &logs
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		return cmd, &logs
	}
	stopAgent := func(cmd *exec.Cmd) {
		_ = cmd.Process.Signal(syscall.SIGTERM)
		done := make(chan error, 1)
		go func() { done <- cmd.Wait() }()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			_ = cmd.Process.Kill()
			<-done
		}
	}

	agent, logs := startAgent()
	t.Cleanup(func() { t.Logf("agent logs:\n%s", logs.String()) })
	t.Cleanup(func() { t.Logf("printer payloads=%d, cloud events=%+v", printer.count(), cloud.events) })
	defer func() {
		if agent != nil && agent.ProcessState == nil {
			stopAgent(agent)
		}
	}()
	waitE2E(t, 20*time.Second, func() bool {
		return printer.count() >= 3 && cloud.eventCount("job_state_changed") >= 12
	})
	for index, expected := range []struct{ symbology, copies string }{{"QRCODE", "PRINT 1,2\r\n"}, {"QRCODE", "PRINT 1,1\r\n"}, {"BARCODE", "PRINT 1,3\r\n"}} {
		payload := printer.payload(index)
		if !strings.Contains(payload, expected.symbology) || !strings.Contains(payload, expected.copies) || !strings.Contains(payload, "SIZE 102.25 mm, 76.20 mm\r\n") || !strings.Contains(payload, "E2E-001") {
			t.Fatalf("unexpected B/A/C payload %d:\n%s", index, payload)
		}
	}
	stopAgent(agent)
	agent, _ = startAgent()
	time.Sleep(400 * time.Millisecond)
	if got := printer.count(); got != 3 {
		t.Fatalf("restart transmitted a duplicate print: count=%d", got)
	}

	loginBody := strings.NewReader(`{"username":"operator","password":"1234"}`)
	login, err := http.Post("http://"+fallbackAddr+"/fallback/login", "application/json", loginBody)
	if err != nil {
		t.Fatal(err)
	}
	var session struct {
		CSRFToken string `json:"csrfToken"`
	}
	_ = json.NewDecoder(login.Body).Decode(&session)
	_ = login.Body.Close()
	cookies := login.Cookies()
	stock, err := http.NewRequest(http.MethodGet, "http://"+fallbackAddr+"/fallback/stock", nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, cookie := range cookies {
		stock.AddCookie(cookie)
	}
	stockResponse, err := http.DefaultClient.Do(stock)
	if err != nil {
		t.Fatal(err)
	}
	_ = stockResponse.Body.Close()
	if stockResponse.StatusCode != http.StatusOK {
		t.Fatalf("offline cached stock status = %d", stockResponse.StatusCode)
	}

	cloud.mu.Lock()
	jobID := cloud.agentJobID
	cloud.mu.Unlock()
	reprint, err := http.NewRequest(http.MethodPost, "http://"+fallbackAddr+"/fallback/jobs/"+jobID+"/reprint", nil)
	if err != nil {
		t.Fatal(err)
	}
	reprint.Header.Set("Origin", "http://"+fallbackAddr)
	reprint.Header.Set("X-CSRF-Token", session.CSRFToken)
	for _, cookie := range cookies {
		reprint.AddCookie(cookie)
	}
	reprintResponse, err := http.DefaultClient.Do(reprint)
	if err != nil {
		t.Fatal(err)
	}
	_ = reprintResponse.Body.Close()
	if reprintResponse.StatusCode != http.StatusCreated {
		t.Fatalf("offline reprint status = %d", reprintResponse.StatusCode)
	}
	waitE2E(t, 20*time.Second, func() bool { return printer.count() == 4 && cloud.eventCount("offline_reprint") == 1 })

	cloud.mu.Lock()
	cloud.revoked = true
	cloud.mu.Unlock()
	waitE2E(t, 20*time.Second, func() bool {
		response, err := http.Get("http://" + fallbackAddr + "/healthz")
		if err != nil {
			return false
		}
		_ = response.Body.Close()
		return response.StatusCode == http.StatusServiceUnavailable
	})
	stopAgent(agent)
	agent = nil

	sqlDB, err = db.Open(filepath.Join(stateDir, "edge.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	var jobsCount, outboxCount, cacheCount int
	_ = sqlDB.QueryRow(`SELECT COUNT(*) FROM edge_jobs`).Scan(&jobsCount)
	_ = sqlDB.QueryRow(`SELECT COUNT(*) FROM edge_outbox`).Scan(&outboxCount)
	_ = sqlDB.QueryRow(`SELECT COUNT(*) FROM cache_records`).Scan(&cacheCount)
	if jobsCount != 4 || outboxCount == 0 || cacheCount != 0 {
		t.Fatalf("revocation history/cache mismatch: jobs=%d outbox=%d cache=%d", jobsCount, outboxCount, cacheCount)
	}
}

func (c *e2eCloud) eventCount(kind string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	count := 0
	for _, event := range c.events {
		if event["kind"] == kind {
			count++
		}
	}
	return count
}

func freeE2EAddress(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	_ = listener.Close()
	return address
}

func waitE2E(t *testing.T, timeout time.Duration, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("condition was not met before timeout")
}
