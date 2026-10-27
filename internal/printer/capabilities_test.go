package printer

import (
	"context"
	"io"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestCapabilities(t *testing.T) {
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
		command, _ := io.ReadAll(io.LimitReader(conn, 4))
		if !strings.HasPrefix(string(command), "~!I") {
			return
		}
		_, _ = io.WriteString(conn, "MODEL: TA220\r\nMAC ADDRESS: A4-B1-C2-D3-E4-F5\r\nINSTALLEDFEATURES: RTC,CUTTER,USB\r\nMEDIA TYPE: GAP\r\nMEDIA WIDTH: 102.25 mm\r\nMEDIA HEIGHT: 76.2 mm\r\nGAP: 2.0 mm\r\nDPI: 203\r\nSPEED: 5\r\nDENSITY: 8\r\nCUTTER: OFF\r\n")
	}()
	host, portText, _ := net.SplitHostPort(ln.Addr().String())
	port, _ := strconv.Atoi(portText)
	before := time.Now().UTC()
	got, err := Discover(context.Background(), Target{PrinterID: "packing", Host: host, Port: port})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if got.MACAddress != "a4:b1:c2:d3:e4:f5" {
		t.Fatalf("MAC = %q", got.MACAddress)
	}
	if got.Host != host || got.Port != port || got.VerifiedAt.Before(before) {
		t.Fatalf("endpoint evidence = %+v", got)
	}
	if got.MediaType != "gap" || got.MediaWidthMM != 102.25 || got.MediaHeightMM != 76.2 || got.GapMM != 2 || got.DPI != 203 || got.SpeedInSec != 5 || got.Density != 8 {
		t.Fatalf("media evidence = %+v", got)
	}
	if got.Cutter == nil || *got.Cutter {
		t.Fatalf("cutter = %v", got.Cutter)
	}
	if len(got.Features) != 3 || got.Features[1] != "cutter" {
		t.Fatalf("features = %#v", got.Features)
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
