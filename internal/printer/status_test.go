package printer

import (
	"context"
	"io"
	"net"
	"testing"
	"time"
)

func TestDecodeTA220StatusDocumentedASCII(t *testing.T) {
	for _, test := range []struct {
		name     string
		raw      []byte
		state    string
		canPrint bool
	}{
		{name: "idle", raw: []byte{'I', '@', '@', '@'}, state: "idle", canPrint: true},
		{name: "standby", raw: []byte{'S', '@', '@', '@'}, state: "standby", canPrint: true},
		{name: "paused", raw: []byte{'P', '@', '@', '@'}, state: "paused", canPrint: false},
		{name: "head overheat", raw: []byte{'@', '@', 'A', '@'}, state: "error", canPrint: false},
		{name: "paper empty", raw: []byte{'@', '@', '@', 'A'}, state: "error", canPrint: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			status := DecodeTA220Status(test.raw)
			if !status.Online || status.State != test.state || status.CanPrint != test.canPrint {
				t.Fatalf("DecodeTA220Status(%q) = %+v", test.raw, status)
			}
		})
	}
}

func TestProbeStatusReadsFragmentedFourByteReply(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		_, _ = io.ReadFull(conn, make([]byte, 3))
		_, _ = conn.Write([]byte{'I'})
		time.Sleep(25 * time.Millisecond)
		_, _ = conn.Write([]byte{'@', '@', '@'})
	}()
	host, port, _ := net.SplitHostPort(listener.Addr().String())
	session := NewSession(Config{Host: host, Port: port, Timeout: time.Second})
	status, err := session.ProbeStatus(context.Background())
	if err != nil || !status.CanPrint || status.State != "idle" {
		t.Fatalf("fragmented ASCII status = %+v, %v", status, err)
	}
}

func TestProbeStatusRejectsPartialReply(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		_, _ = io.ReadFull(conn, make([]byte, 3))
		_, _ = conn.Write([]byte{'I', '@'})
	}()
	host, port, _ := net.SplitHostPort(listener.Addr().String())
	session := NewSession(Config{Host: host, Port: port, Timeout: 100 * time.Millisecond})
	if status, err := session.ProbeStatus(context.Background()); err == nil {
		t.Fatalf("partial status was accepted: %+v", status)
	}
}
