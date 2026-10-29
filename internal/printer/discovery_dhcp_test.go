package printer

import (
	"context"
	"io"
	"net"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// startEndpoint emulates a TA220 answering ~!T with model and ESC !? with a
// status byte; the socket stays open like the real device.
func startEndpoint(t *testing.T, model string, delayed bool) (Target, *atomic.Int32) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	var probes atomic.Int32
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				probes.Add(1)
				query := make([]byte, 3)
				if _, err := io.ReadFull(c, query); err != nil {
					return
				}
				if string(query) == "~!T" {
					_, _ = io.WriteString(c, model)
				} else {
					if delayed {
						time.Sleep(20 * time.Millisecond)
					}
					_, _ = c.Write([]byte{0x00})
				}
				_, _ = io.Copy(io.Discard, c)
			}(conn)
		}
	}()
	host, portText, _ := net.SplitHostPort(ln.Addr().String())
	port, _ := strconv.Atoi(portText)
	return Target{PrinterID: "packing", Host: host, Port: port, MACAddress: "a4:b1:c2:d3:e4:f5"}, &probes
}

func TestVerifyEndpoint(t *testing.T) {
	target, _ := startEndpoint(t, "TA220", true)
	stubNeighbors(t, Neighbor{IPAddress: target.Host, MACAddress: "A4B1C2D3E4F5"})
	evidence, err := VerifyEndpoint(context.Background(), target)
	if err != nil {
		t.Fatalf("VerifyEndpoint: %v", err)
	}
	if evidence.Host != target.Host || evidence.Port != target.Port || evidence.MACAddress != target.MACAddress || evidence.Model != "TA220" || evidence.VerifiedAt.IsZero() {
		t.Fatalf("evidence = %+v", evidence)
	}
}

func TestVerifyEndpointRejectsMACMismatch(t *testing.T) {
	target, _ := startEndpoint(t, "TA220", false)
	stubNeighbors(t, Neighbor{IPAddress: target.Host, MACAddress: "001122334455"})
	if _, err := VerifyEndpoint(context.Background(), target); err == nil || !strings.Contains(err.Error(), "MAC mismatch") {
		t.Fatalf("mismatch error = %v", err)
	}
}

func TestVerifyEndpointRejectsUnsupportedModel(t *testing.T) {
	target, _ := startEndpoint(t, "OTHER", false)
	stubNeighbors(t, Neighbor{IPAddress: target.Host, MACAddress: "A4B1C2D3E4F5"})
	if _, err := VerifyEndpoint(context.Background(), target); err == nil || !strings.Contains(err.Error(), "unsupported model") {
		t.Fatalf("model error = %v", err)
	}
}

func TestVerifyEndpointRejectsUnknownEndpoint(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	host, portText, _ := net.SplitHostPort(ln.Addr().String())
	port, _ := strconv.Atoi(portText)
	_ = ln.Close()
	if _, err := VerifyEndpoint(context.Background(), Target{Host: host, Port: port, MACAddress: "a4:b1:c2:d3:e4:f5"}); err == nil {
		t.Fatal("expected unknown endpoint failure")
	}
}

func TestDHCPRecoveryUsesOnlyVerifiedNeighbor(t *testing.T) {
	_, wrongProbes := startEndpoint(t, "TA220", false)
	right, _ := startEndpoint(t, "TA220", false)
	table := []Neighbor{{IPAddress: "192.0.2.77", MACAddress: "00:11:22:33:44:55"}, {IPAddress: right.Host, MACAddress: "A4B1C2D3E4F5"}}
	stubNeighbors(t, table...)
	lookup := func(context.Context) ([]Neighbor, error) { return table, nil }
	evidence, err := recoverEndpoint(context.Background(), Target{PrinterID: "packing", Host: "192.0.2.10", Port: right.Port, MACAddress: "a4:b1:c2:d3:e4:f5"}, lookup)
	if err != nil || evidence.Host != right.Host || evidence.Model != "TA220" || evidence.MACAddress != "a4:b1:c2:d3:e4:f5" {
		t.Fatalf("recovery = %+v, %v", evidence, err)
	}
	if wrongProbes.Load() != 0 {
		t.Fatalf("MAC-mismatched neighbor was probed %d times", wrongProbes.Load())
	}
}
