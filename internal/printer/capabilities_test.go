package printer

import (
	"context"
	"io"
	"net"
	"strconv"
	"testing"
	"time"
)

// startTA220 emulates the real TSC TA220 on port 9100: ~!T answers the model
// with no line ending, ESC !? answers one status byte, and the printer never
// closes the socket itself.
func startTA220(t *testing.T, model string) Target {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
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
				switch string(query) {
				case "~!T":
					_, _ = io.WriteString(c, model)
				case "\x1b!?":
					_, _ = c.Write([]byte{0x00})
				}
				_, _ = io.Copy(io.Discard, c)
			}(conn)
		}
	}()
	host, portText, _ := net.SplitHostPort(ln.Addr().String())
	port, _ := strconv.Atoi(portText)
	return Target{PrinterID: "packing", Host: host, Port: port}
}

func stubNeighbors(t *testing.T, neighbors ...Neighbor) {
	t.Helper()
	restore := SetNeighborLookupForTesting(func(context.Context) ([]Neighbor, error) { return neighbors, nil })
	t.Cleanup(restore)
}

func TestCapabilities(t *testing.T) {
	target := startTA220(t, "TA220")
	stubNeighbors(t, Neighbor{IPAddress: "192.0.2.9", MACAddress: "001122334455"}, Neighbor{IPAddress: target.Host, MACAddress: "A4-B1-C2-D3-E4-F5"})
	before := time.Now().UTC()
	got, err := Discover(context.Background(), target)
	elapsed := time.Since(before)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if got.Model != "TA220" || got.MACAddress != "a4:b1:c2:d3:e4:f5" {
		t.Fatalf("identity = %+v", got)
	}
	if got.Host != target.Host || got.Port != target.Port || got.VerifiedAt.Before(before) {
		t.Fatalf("endpoint evidence = %+v", got)
	}
	if got.MediaType != "" || got.DPI != 0 || got.Cutter != nil || len(got.Features) != 0 {
		t.Fatalf("fabricated media evidence = %+v", got)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("Discover took %v against a printer that never closes", elapsed)
	}
}

func TestCapabilitiesWithoutNeighborEntry(t *testing.T) {
	target := startTA220(t, "TA220")
	stubNeighbors(t, Neighbor{IPAddress: "192.0.2.9", MACAddress: "001122334455"})
	got, err := Discover(context.Background(), target)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if got.Model != "TA220" || got.MACAddress != "" || got.VerifiedAt.IsZero() {
		t.Fatalf("capabilities = %+v", got)
	}
}

func TestCapabilitiesNoModelIsError(t *testing.T) {
	target := startTA220(t, "")
	stubNeighbors(t, Neighbor{IPAddress: target.Host, MACAddress: "A4B1C2D3E4F5"})
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	if got, err := Discover(ctx, target); err == nil {
		t.Fatalf("expected error, got %+v", got)
	}
}

func TestCapabilitiesNoEvidenceWithoutResponse(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	_ = ln.Close()
	host, portText, _ := net.SplitHostPort(ln.Addr().String())
	port, _ := strconv.Atoi(portText)
	got, err := Discover(context.Background(), Target{PrinterID: "offline", Host: host, Port: port, DPI: 203})
	if err == nil {
		t.Fatal("expected discovery failure")
	}
	if got.VerifiedAt.IsZero() == false || got.MACAddress != "" || got.Host != "" {
		t.Fatalf("unverified data returned: %+v", got)
	}
}

func TestNormalizeMAC(t *testing.T) {
	for _, input := range []string{"A4B1C2D3E4F5", "a4-b1-c2-d3-e4-f5", "A4B1.C2D3.E4F5"} {
		if got := NormalizeMAC(input); got != "a4:b1:c2:d3:e4:f5" {
			t.Fatalf("NormalizeMAC(%q) = %q", input, got)
		}
	}
	if got := NormalizeMAC("not-a-mac"); got != "" {
		t.Fatalf("invalid MAC = %q", got)
	}
}
