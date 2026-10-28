package printer

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"time"
)

type Outcome string

const (
	OutcomeSent      Outcome = "sent"
	OutcomeRetryable Outcome = "retryable"
	OutcomeUnknown   Outcome = "outcome_unknown"
	OutcomeCancelled Outcome = "cancelled"
)

type Config struct {
	PrinterID string
	Host      string
	Port      string
	Timeout   time.Duration
}

var (
	ta220States      = map[byte]string{'@': "normal", 'F': "feeding", 'P': "paused", 'E': "error", 'H': "head_open", 'S': "standby", 'L': "label_waiting", 'I': "idle"}
	ta220Warnings    = map[byte]string{'@': "", 'A': "paper_low", 'B': "ribbon_low", 'C': "paper_and_ribbon_low"}
	ta220Errors      = map[byte]string{'@': "", 'A': "head_overheat", 'B': "motor_overheat", 'C': "head_and_motor_overheat", 'D': "head_error", 'E': "cutter_error", 'F': "rtc_error"}
	ta220MediaErrors = map[byte]string{'@': "", 'A': "paper_empty", 'B': "ribbon_empty", 'C': "paper_and_ribbon_empty", 'D': "takeup_reel_full", '`': "head_open"}
)

type Status struct {
	Online   bool   `json:"online"`
	CanPrint bool   `json:"canPrint"`
	State    string `json:"state"`
	Error    string `json:"error,omitempty"`
	RawBytes []byte `json:"rawBytes,omitempty"`
}

type TransmitResult struct {
	Outcome      Outcome `json:"outcome"`
	BytesWritten int     `json:"bytesWritten"`
	Status       Status  `json:"status"`
	Reason       string  `json:"reason,omitempty"`
	Cause        error   `json:"-"`
}

type Session struct {
	cfg Config
	mu  sync.Mutex
}

func NewSession(cfg Config) *Session {
	if strings.TrimSpace(cfg.Port) == "" {
		cfg.Port = "9100"
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 5 * time.Second
	}
	return &Session{cfg: cfg}
}

func (s *Session) Address() string {
	return net.JoinHostPort(s.cfg.Host, s.cfg.Port)
}

func (s *Session) ProbeStatus(ctx context.Context) (Status, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	conn, err := s.dial(ctx)
	if err != nil {
		return Status{Online: false, CanPrint: false, State: "offline", Error: err.Error()}, err
	}
	defer conn.Close()

	return s.queryStatusLocked(conn)
}

func (s *Session) Transmit(ctx context.Context, tspl string) TransmitResult {
	return s.TransmitBeforeWrite(ctx, tspl, nil)
}

// TransmitBeforeWrite invokes beforeWrite after the device is open and ready,
// immediately before the first payload byte. A failed hook sends no payload.
func (s *Session) TransmitBeforeWrite(ctx context.Context, tspl string, beforeWrite func() error) TransmitResult {
	s.mu.Lock()
	defer s.mu.Unlock()

	payload := []byte(tspl)
	if len(payload) == 0 {
		return TransmitResult{
			Outcome: OutcomeRetryable,
			Reason:  "empty_tspl_payload",
		}
	}

	conn, err := s.dial(ctx)
	if err != nil {
		return TransmitResult{
			Outcome: OutcomeRetryable,
			Status:  Status{Online: false, CanPrint: false, State: "offline", Error: err.Error()},
			Reason:  fmt.Sprintf("connect_failed: %v", err),
		}
	}
	defer conn.Close()

	st, err := s.queryStatusLocked(conn)
	if err != nil {
		return TransmitResult{
			Outcome: OutcomeRetryable,
			Status:  st,
			Reason:  fmt.Sprintf("status_probe_failed: %v", err),
		}
	}
	if !st.CanPrint {
		return TransmitResult{
			Outcome: OutcomeRetryable,
			Status:  st,
			Reason:  fmt.Sprintf("printer_not_ready: %s", st.Error),
		}
	}

	if beforeWrite != nil {
		if err := beforeWrite(); err != nil {
			return TransmitResult{
				Outcome: OutcomeCancelled,
				Status:  st,
				Reason:  "transmission_not_started",
				Cause:   err,
			}
		}
	}
	_ = conn.SetWriteDeadline(time.Now().Add(s.cfg.Timeout))
	written := 0
	for written < len(payload) {
		// Write in small chunks so mid-stream TCP resets or short writes are detected accurately.
		end := written + 16
		if end > len(payload) {
			end = len(payload)
		}
		n, werr := conn.Write(payload[written:end])
		if n > 0 {
			written += n
		}
		if werr != nil {
			if written == 0 {
				return TransmitResult{
					Outcome:      OutcomeRetryable,
					BytesWritten: 0,
					Status:       st,
					Reason:       fmt.Sprintf("write_failed_zero_bytes: %v", werr),
				}
			}
			return TransmitResult{
				Outcome:      OutcomeUnknown,
				BytesWritten: written,
				Status:       st,
				Reason:       fmt.Sprintf("write_interrupted_after_%d_bytes: %v", written, werr),
			}
		}
		if written < len(payload) {
			// Probe socket liveness if peer closed with RST during transmission.
			_ = conn.SetReadDeadline(time.Now().Add(5 * time.Millisecond))
			var probe [1]byte
			if _, rerr := conn.Read(probe[:]); rerr != nil {
				var ne net.Error
				if !errors.As(rerr, &ne) || !ne.Timeout() {
					return TransmitResult{
						Outcome:      OutcomeUnknown,
						BytesWritten: written,
						Status:       st,
						Reason:       fmt.Sprintf("connection_closed_after_%d_bytes: %v", written, rerr),
					}
				}
			}
		}
	}

	return TransmitResult{
		Outcome:      OutcomeSent,
		BytesWritten: written,
		Status:       st,
	}
}

func (s *Session) dial(ctx context.Context) (net.Conn, error) {
	d := net.Dialer{Timeout: s.cfg.Timeout}
	return d.DialContext(ctx, "tcp", s.Address())
}

func (s *Session) queryStatusLocked(conn net.Conn) (Status, error) {
	_ = conn.SetDeadline(time.Now().Add(s.cfg.Timeout))
	// Send TSPL2 immediate status query <ESC>!?
	if _, err := conn.Write([]byte{0x1b, '!', '?'}); err != nil {
		return Status{Online: false, CanPrint: false, State: "offline", Error: err.Error()}, err
	}

	buf := make([]byte, 4)
	_ = conn.SetReadDeadline(time.Now().Add(s.cfg.Timeout))
	total := 0
	shortWait := false
	for total < len(buf) {
		n, readErr := conn.Read(buf[total:])
		if n > 0 {
			total += n
		}
		if total >= len(buf) {
			break
		}
		// One-byte TA220 replies remain valid when the device holds the socket open.
		if total == 1 && !shortWait && readErr == nil {
			shortWait = true
			_ = conn.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
			continue
		}
		if readErr != nil {
			var netErr net.Error
			timedOut := errors.As(readErr, &netErr) && netErr.Timeout()
			if total == 1 && (errors.Is(readErr, io.EOF) || timedOut) {
				break
			}
			if errors.Is(readErr, io.EOF) || timedOut {
				return Status{Online: false, CanPrint: false, State: "error", Error: "incomplete status response"}, io.ErrUnexpectedEOF
			}
			return Status{Online: false, CanPrint: false, State: "error", Error: readErr.Error()}, readErr
		}
	}
	raw := buf[:total]
	return DecodeTA220Status(raw), nil
}

// DecodeTA220Status decodes both 1-byte (<ESC>!?) and 4-byte (<ESC>!S) TSC TA220 status responses.
func DecodeTA220Status(raw []byte) Status {
	if len(raw) == 0 {
		return Status{Online: false, CanPrint: false, State: "unknown", Error: "empty status response"}
	}

	// Four-byte extended status: printer state, warning, error, and media error.
	if len(raw) >= 4 {
		b0, b1, b2, b3 := raw[0], raw[1], raw[2], raw[3]
		if b0 == 0x00 && b1 == 0x00 && b2 == 0x00 && b3 == 0x00 {
			return Status{Online: true, CanPrint: true, State: "online", RawBytes: append([]byte(nil), raw...)}
		}
		state, knownState := ta220States[b0]
		if !knownState {
			state = "error"
		}
		canPrint := b0 == '@' || b0 == 'S' || b0 == 'I'
		reasons := make([]string, 0, 4)
		if !knownState {
			reasons = append(reasons, fmt.Sprintf("unknown_printer_state_0x%02x", b0))
		}
		for _, reason := range []string{ta220Warnings[b1], ta220Errors[b2], ta220MediaErrors[b3]} {
			if reason != "" {
				reasons = append(reasons, reason)
			}
		}
		if ta220Errors[b2] != "" || ta220MediaErrors[b3] != "" {
			canPrint = false
			state = "error"
		}
		if b0 == 'P' || b0 == 'F' || b0 == 'L' {
			canPrint = false
		}
		return Status{
			Online:   true,
			CanPrint: canPrint,
			State:    state,
			Error:    strings.Join(reasons, ","),
			RawBytes: append([]byte(nil), raw...),
		}
	}

	// 1-byte TA220 status (<ESC>!?):
	// 0x00 = Normal / Ready
	// 0x01 = Head open
	// 0x02 = Paper jam
	// 0x04 = Out of paper
	// 0x08 = Out of ribbon
	// 0x10 = Paused
	// 0x20 = Printing
	// 0x80 = Other error
	b := raw[0]
	if b == 0x00 || b == 0x20 {
		state := "online"
		if b == 0x20 {
			state = "printing"
		}
		return Status{
			Online:   true,
			CanPrint: true,
			State:    state,
			RawBytes: []byte{b},
		}
	}

	var reasons []string
	if b&0x01 != 0 {
		reasons = append(reasons, "head_open")
	}
	if b&0x02 != 0 {
		reasons = append(reasons, "paper_jam")
	}
	if b&0x04 != 0 {
		reasons = append(reasons, "out_of_paper")
	}
	if b&0x08 != 0 {
		reasons = append(reasons, "out_of_ribbon")
	}
	if b&0x10 != 0 {
		reasons = append(reasons, "paused")
	}
	if b&0x80 != 0 {
		reasons = append(reasons, "printer_error")
	}
	if len(reasons) == 0 {
		reasons = append(reasons, fmt.Sprintf("status_0x%02x", b))
	}

	state := "error"
	if b == 0x10 {
		state = "paused"
	}
	return Status{
		Online:   true,
		CanPrint: false,
		State:    state,
		Error:    strings.Join(reasons, ","),
		RawBytes: []byte{b},
	}
}
