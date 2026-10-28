package core

import (
	"errors"
	"io"
	"net"
	"strconv"
	"testing"
	"time"

	"github.com/orrn/spool/internal/config"
	"github.com/orrn/spool/internal/db"
)

func TestCheckStatusAcceptsOneByteTA220Reply(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer listener.Close()

	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		_, _ = io.ReadFull(conn, make([]byte, len(statusCommand)))
		_, _ = conn.Write([]byte{0x00})
		time.Sleep(50 * time.Millisecond)
	}()

	host, portText, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		t.Fatalf("SplitHostPort: %v", err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		t.Fatalf("Atoi: %v", err)
	}
	newTestQueue(t)
	manager := NewPrinterManager(db.GetDB(), &config.PrintersConfig{ConnectionTimeout: time.Second}, nil)
	manager.printers[1] = &Printer{ID: 1, IPAddress: host, Port: port}

	status, err := manager.CheckStatus(1)
	if err != nil {
		t.Fatalf("CheckStatus rejected one-byte TA220 status: %v", err)
	}
	if !status.IsOnline || !status.CanPrint || status.PrinterState != "normal" {
		t.Fatalf("unexpected TA220 status: %+v", status)
	}
}

type writeFailureConn struct{ written int }

func (c *writeFailureConn) Read([]byte) (int, error)         { return 0, io.EOF }
func (c *writeFailureConn) Write(p []byte) (int, error)      { return c.written, errors.New("socket reset") }
func (c *writeFailureConn) Close() error                     { return nil }
func (c *writeFailureConn) LocalAddr() net.Addr              { return &net.TCPAddr{} }
func (c *writeFailureConn) RemoteAddr() net.Addr             { return &net.TCPAddr{} }
func (c *writeFailureConn) SetDeadline(time.Time) error      { return nil }
func (c *writeFailureConn) SetReadDeadline(time.Time) error  { return nil }
func (c *writeFailureConn) SetWriteDeadline(time.Time) error { return nil }

func TestSendCommandDistinguishesZeroByteAndPartialWrites(t *testing.T) {
	for _, test := range []struct {
		name    string
		written int
		want    error
	}{
		{name: "zero bytes remain retryable", written: 0, want: ErrConnectionFailed},
		{name: "partial write is outcome unknown", written: 2, want: ErrPrintOutcomeUnknown},
	} {
		t.Run(test.name, func(t *testing.T) {
			manager := NewPrinterManager(nil, &config.PrintersConfig{ConnectionTimeout: time.Second}, nil)
			manager.printers[1] = &Printer{ID: 1, IPAddress: "127.0.0.1", Port: 9100}
			manager.connections[1] = &writeFailureConn{written: test.written}

			err := manager.SendCommand(1, "CLS\r\n")
			if !errors.Is(err, test.want) {
				t.Fatalf("SendCommand error = %v, want %v", err, test.want)
			}
			if test.written > 0 && errors.Is(err, ErrConnectionFailed) {
				t.Fatalf("partial write was incorrectly classified as safe retry: %v", err)
			}
		})
	}
}
