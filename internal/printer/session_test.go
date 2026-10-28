package printer

import (
	"context"
	"errors"
	"io"
	"net"
	"sync"
	"testing"
	"time"
)

func TestSessionHandlesOneByteTA220StatusAndClassifiesShortWriteAsOutcomeUnknown(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	var mu sync.Mutex
	mode := "ok_one_byte"
	setMode := func(m string) {
		mu.Lock()
		mode = m
		mu.Unlock()
	}
	getMode := func() string {
		mu.Lock()
		defer mu.Unlock()
		return mode
	}

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				buf := make([]byte, 3)
				if _, err := io.ReadFull(c, buf); err != nil {
					return
				}
				m := getMode()
				if string(buf) == "\x1b!?" {
					switch m {
					case "paper_out":
						_, _ = c.Write([]byte{0x04})
						return
					case "ok_four_byte":
						_, _ = c.Write([]byte{0x40, 0x40, 0x40, 0x40})
					default:
						_, _ = c.Write([]byte{0x00})
					}
				}
				if m == "reset_mid_write" {
					one := make([]byte, 4)
					_, _ = io.ReadFull(c, one)
					_ = c.(*net.TCPConn).SetLinger(0)
					_ = c.Close()
					return
				}
				_, _ = io.Copy(io.Discard, c)
			}(conn)
		}
	}()

	host, portStr, _ := net.SplitHostPort(ln.Addr().String())
	sess := NewSession(Config{
		PrinterID: "tsc-1",
		Host:      host,
		Port:      portStr,
		Timeout:   2 * time.Second,
	})

	res := sess.Transmit(context.Background(), "SIZE 102.25 mm, 76.20 mm\r\nCLS\r\nPRINT 1,1\r\n")
	if res.Outcome != OutcomeSent {
		t.Fatalf("expected OutcomeSent, got %+v", res)
	}

	setMode("ok_four_byte")
	res4 := sess.Transmit(context.Background(), "SIZE 102.25 mm, 76.20 mm\r\nCLS\r\nPRINT 1,1\r\n")
	if res4.Outcome != OutcomeSent {
		t.Fatalf("expected OutcomeSent for 4-byte ready, got %+v", res4)
	}

	setMode("paper_out")
	resPaper := sess.Transmit(context.Background(), "SIZE 102.25 mm, 76.20 mm\r\nCLS\r\nPRINT 1,1\r\n")
	if resPaper.Outcome != OutcomeRetryable || resPaper.BytesWritten != 0 {
		t.Fatalf("expected OutcomeRetryable with 0 bytes on paper_out, got %+v", resPaper)
	}

	setMode("reset_mid_write")
	res2 := sess.Transmit(context.Background(), "SIZE 102.25 mm, 76.20 mm\r\nCLS\r\nPRINT 1,1\r\n")
	if res2.Outcome != OutcomeUnknown {
		t.Fatalf("expected OutcomeUnknown on mid-stream reset, got %+v", res2)
	}
}

func TestTransmitBeforeWriteAbortSendsNoPayload(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	payloadBytes := make(chan int, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			payloadBytes <- -1
			return
		}
		defer conn.Close()
		query := make([]byte, 3)
		if _, err := io.ReadFull(conn, query); err != nil {
			payloadBytes <- -1
			return
		}
		_, _ = conn.Write([]byte{0x00})
		n, _ := io.Copy(io.Discard, conn)
		payloadBytes <- int(n)
	}()
	host, port, _ := net.SplitHostPort(ln.Addr().String())
	sess := NewSession(Config{Host: host, Port: port, Timeout: time.Second})
	abort := errors.New("cancelled before write")
	called := false
	res := sess.TransmitBeforeWrite(context.Background(), "CLS\r\nPRINT 1,1\r\n", func() error {
		called = true
		return abort
	})
	if !called || res.Outcome != OutcomeCancelled || !errors.Is(res.Cause, abort) || res.BytesWritten != 0 {
		t.Fatalf("unexpected pre-write abort result: %+v called=%v", res, called)
	}
	if got := <-payloadBytes; got != 0 {
		t.Fatalf("printer received %d payload bytes after aborted durable transition", got)
	}
}
