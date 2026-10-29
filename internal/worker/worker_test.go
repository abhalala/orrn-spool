package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/orrn/spool/internal/db"
	"github.com/orrn/spool/internal/jobs"
	"github.com/orrn/spool/internal/printer"
)

type fakeTA220 struct {
	ln       net.Listener
	mu       sync.Mutex
	received bytes.Buffer
}

func startFakeTA220(t *testing.T) *fakeTA220 {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	f := &fakeTA220{ln: ln}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				query := make([]byte, 3)
				if _, err := io.ReadFull(c, query); err != nil {
					return
				}
				if string(query) == "~!T" {
					// The real TA220 answers the model with no line ending and
					// keeps the socket open.
					_, _ = io.WriteString(c, "TA220")
					_, _ = io.Copy(io.Discard, c)
					return
				}
				_, _ = c.Write([]byte{0x00})
				data, _ := io.ReadAll(c)
				f.mu.Lock()
				f.received.Write(data)
				f.mu.Unlock()
			}(conn)
		}
	}()
	t.Cleanup(func() { _ = ln.Close() })
	return f
}

func (f *fakeTA220) target(id string) printer.Target {
	host, port, _ := net.SplitHostPort(f.ln.Addr().String())
	p, _ := strconv.Atoi(port)
	return printer.Target{PrinterID: id, Name: "Packing TA220", Host: host, Port: p}
}

func (f *fakeTA220) got() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.received.String()
}

func newJobStore(t *testing.T) *jobs.Store {
	t.Helper()
	sqlDB, err := db.Open(filepath.Join(t.TempDir(), "edge.db"))
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	s, err := jobs.NewStore(context.Background(), sqlDB)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	return s
}

func accept(t *testing.T, s *jobs.Store, printerID, key, tspl string) string {
	t.Helper()
	res, err := s.ResolvePrintCommand(context.Background(), jobs.PrintCommandInput{
		CommandID: "cmd-" + key, JobKey: key, PayloadHash: "h-" + key, PrinterID: printerID, TSPL: tspl, Copies: 1,
	})
	if err != nil || res.Outcome != "accepted" {
		t.Fatalf("accept %s: %+v %v", key, res, err)
	}
	return res.AgentJobID
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition not met before deadline")
}

func TestWorkerSendsAcceptedJobsInOrderAndReportsOnline(t *testing.T) {
	ctx := context.Background()
	fake := startFakeTA220(t)
	s := newJobStore(t)
	first := accept(t, s, "tsc-1", "a", "SIZE 100.00 mm, 75.00 mm\r\nPRINT 1,1\r\n")
	second := accept(t, s, "tsc-1", "b", "SIZE 100.00 mm, 75.00 mm\r\nPRINT 1,2\r\n")

	w := New(s, func() []printer.Target { return []printer.Target{fake.target("tsc-1")} })
	w.Tick(ctx)

	for _, id := range []string{first, second} {
		job, err := s.GetJob(ctx, id)
		if err != nil || job.State != jobs.StateSent {
			t.Fatalf("job %s: %+v %v", id, job, err)
		}
	}
	waitFor(t, func() bool {
		return fake.got() == "SIZE 100.00 mm, 75.00 mm\r\nPRINT 1,1\r\nSIZE 100.00 mm, 75.00 mm\r\nPRINT 1,2\r\n"
	})

	reports := w.Reports()
	if len(reports) != 1 || reports[0].Status != "online" || !reports[0].CanPrint || reports[0].LastSeenAt == nil {
		t.Fatalf("unexpected reports: %+v", reports)
	}
}

func TestWorkerReportsOnlyVerifiedCapabilities(t *testing.T) {
	fake := startFakeTA220(t)
	host := fake.target("tsc-1").Host
	t.Cleanup(printer.SetNeighborLookupForTesting(func(context.Context) ([]printer.Neighbor, error) {
		return []printer.Neighbor{{IPAddress: host, MACAddress: "A4B1C2D3E4F5"}}, nil
	}))
	w := New(newJobStore(t), func() []printer.Target { return []printer.Target{fake.target("tsc-1")} })
	w.Tick(context.Background())
	reports := w.Reports()
	if len(reports) != 1 || reports[0].Capabilities == nil {
		t.Fatalf("missing capabilities: %+v", reports)
	}
	capabilities := reports[0].Capabilities
	if capabilities.Model != "TA220" || capabilities.MACAddress != "a4:b1:c2:d3:e4:f5" || capabilities.VerifiedAt == "" || capabilities.DPI != 0 {
		t.Fatalf("unexpected capabilities: %+v", capabilities)
	}
	// The poll body ORRN validates: features must be an array even when the printer reports none.
	wire, err := json.Marshal(capabilities)
	if err != nil || !strings.Contains(string(wire), `"features":[]`) {
		t.Fatalf("capabilities wire form = %s (%v)", wire, err)
	}
}

func TestWorkerKeepsJobRetryableWhilePrinterIsOfflineAndBacksOff(t *testing.T) {
	ctx := context.Background()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	host, port, _ := net.SplitHostPort(ln.Addr().String())
	_ = ln.Close()
	p, _ := strconv.Atoi(port)
	target := printer.Target{PrinterID: "tsc-1", Name: "Offline", Host: host, Port: p}

	s := newJobStore(t)
	id := accept(t, s, "tsc-1", "a", "PRINT 1,1\r\n")

	w := New(s, func() []printer.Target { return []printer.Target{target} })

	for i := range 2 {
		if i > 0 {
			time.Sleep(1300 * time.Millisecond)
		}
		w.Tick(ctx)
	}

	job, err := s.GetJob(ctx, id)
	if err != nil || job.State != jobs.StateFailedRetryable {
		t.Fatalf("expected failed_retryable after repeated outages, got %+v %v", job, err)
	}
	if job.Attempts != 2 {
		t.Fatalf("attempts = %d, want 2", job.Attempts)
	}

	w.Tick(ctx)
	again, _ := s.GetJob(ctx, id)
	if again.Attempts != 2 {
		t.Fatalf("backoff should skip the immediate retry, attempts = %d", again.Attempts)
	}

	reports := w.Reports()
	if len(reports) != 1 || reports[0].Status != "offline" || reports[0].LastError == nil {
		t.Fatalf("unexpected reports: %+v", reports)
	}
}

func TestPausedPrinterDoesNotClaimQueuedJobs(t *testing.T) {
	ctx := context.Background()
	s := newJobStore(t)
	id := accept(t, s, "tsc-1", "paused-job", "PRINT 1,1\r\n")
	target := printer.Target{PrinterID: "tsc-1", Name: "Paused", Host: "127.0.0.1", Port: 1, Paused: true}
	w := New(s, func() []printer.Target { return []printer.Target{target} })
	w.Tick(ctx)

	job, err := s.GetJob(ctx, id)
	if err != nil || job.State != jobs.StateAccepted || job.Attempts != 0 {
		t.Fatalf("paused printer claimed queued job: %+v %v", job, err)
	}
	reports := w.Reports()
	if len(reports) != 1 || reports[0].Status != "paused" || reports[0].CanPrint {
		t.Fatalf("paused printer report = %+v", reports)
	}
}

func TestWorkerRedirectsAfterVerifiedDHCPRecoveryWithoutDuplicatePrint(t *testing.T) {
	ctx := context.Background()
	newPrinter := startFakeTA220(t)
	oldListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	oldHost, oldPortText, _ := net.SplitHostPort(oldListener.Addr().String())
	oldPort, _ := strconv.Atoi(oldPortText)
	_ = oldListener.Close()
	target := printer.Target{PrinterID: "tsc-1", Name: "Packing", Host: oldHost, Port: oldPort}
	s := newJobStore(t)
	jobID := accept(t, s, "tsc-1", "dhcp", "PRINT 1,1\r\n")
	w := New(s, func() []printer.Target { return []printer.Target{target} })
	claimed := w.sync()
	st := claimed[0]
	st.capabilities = &printer.Capabilities{MACAddress: "a4:b1:c2:d3:e4:f5"}
	w.release(st)
	w.recoverEndpoint = func(context.Context, printer.Target) (printer.EndpointEvidence, error) {
		host, portText, _ := net.SplitHostPort(newPrinter.ln.Addr().String())
		port, _ := strconv.Atoi(portText)
		return printer.EndpointEvidence{PrinterID: "tsc-1", Host: host, Port: port, MACAddress: "a4:b1:c2:d3:e4:f5", Model: "TA220", VerifiedAt: time.Now().UTC()}, nil
	}
	w.Tick(ctx)
	job, err := s.GetJob(ctx, jobID)
	if err != nil || job.State != jobs.StateSent {
		t.Fatalf("redirected job = %+v, %v", job, err)
	}
	if got := newPrinter.got(); got != "PRINT 1,1\r\n" {
		t.Fatalf("payload transmissions = %q", got)
	}
	events, err := s.ListPendingOutboxEvents(ctx, 50)
	foundEndpointEvent := false
	for _, event := range events {
		if event.Kind == "printer_endpoint_verified" {
			foundEndpointEvent = true
		}
	}
	if err != nil || !foundEndpointEvent {
		t.Fatalf("endpoint event missing: %+v, %v", events, err)
	}
}

func TestWorkerNeverRedirectsAfterPayloadByteOne(t *testing.T) {
	ctx := context.Background()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		query := make([]byte, 3)
		if _, err := io.ReadFull(conn, query); err != nil {
			return
		}
		_, _ = conn.Write([]byte{0x00})
		var first [1]byte
		_, _ = conn.Read(first[:])
		if tcp, ok := conn.(*net.TCPConn); ok {
			_ = tcp.SetLinger(0)
		}
	}()
	host, portText, _ := net.SplitHostPort(ln.Addr().String())
	port, _ := strconv.Atoi(portText)
	target := printer.Target{PrinterID: "tsc-1", Name: "Resetting", Host: host, Port: port}
	s := newJobStore(t)
	jobID := accept(t, s, "tsc-1", "reset", "PRINT 1,1\r\nPRINT 1,1\r\nPRINT 1,1\r\nPRINT 1,1\r\n")
	w := New(s, func() []printer.Target { return []printer.Target{target} })
	recoveryCalls := 0
	w.recoverEndpoint = func(context.Context, printer.Target) (printer.EndpointEvidence, error) {
		recoveryCalls++
		return printer.EndpointEvidence{}, io.ErrUnexpectedEOF
	}
	w.Tick(ctx)
	job, err := s.GetJob(ctx, jobID)
	if err != nil || job.State != jobs.StateOutcomeUnknown {
		t.Fatalf("interrupted job = %+v, %v", job, err)
	}
	if recoveryCalls != 0 {
		t.Fatalf("recovery called %d times after payload bytes", recoveryCalls)
	}
}

func TestBackoffDoublesAndCaps(t *testing.T) {
	for failures, base := range map[int]time.Duration{1: time.Second, 3: 4 * time.Second, 20: maxBackoff} {
		d := backoff(failures)
		if d < base*4/5 || d > base*6/5 {
			t.Fatalf("backoff(%d) = %v, want %v ±20%%", failures, d, base)
		}
	}
}
