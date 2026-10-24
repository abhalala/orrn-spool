package scale

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"strings"
	"sync"
	"time"

	"go.bug.st/serial"
)

// Config is the `scale:` block of config.yaml (env overrides in config pkg).
type Config struct {
	// Device is the serial port ("/dev/ttyUSB0", "COM3") or "tcp://host:port"
	// for a serial-to-Ethernet converter. Empty means no scale on this node.
	Device string `yaml:"device"`
	// Baud rate, default 9600.
	Baud int `yaml:"baud"`
	// DataBits (default 8), Parity ("none", "even", "odd"; default none),
	// StopBits (1 or 2; default 1).
	DataBits int    `yaml:"data_bits"`
	Parity   string `yaml:"parity"`
	StopBits int    `yaml:"stop_bits"`
	// Frame is the regular expression for one line; it needs a named group
	// "grams" or "kg". Empty means DefaultFramePattern.
	Frame string `yaml:"frame"`
	// Simulate runs the built-in simulator instead of opening Device.
	Simulate bool `yaml:"simulate"`
	// SimulateSpeed speeds the simulator up (tests, demos). Default 1.
	SimulateSpeed float64 `yaml:"simulate_speed"`
}

// Configured reports whether this node has a scale (real or simulated).
func (c Config) Configured() bool {
	return c.Simulate || strings.TrimSpace(c.Device) != ""
}

// Validate checks the block without opening anything.
func (c Config) Validate() error {
	if !c.Configured() {
		return nil
	}
	if _, err := NewParser(c.Frame); err != nil {
		return err
	}
	if c.Baud < 0 {
		return errors.New("scale.baud must be positive")
	}
	switch strings.ToLower(c.Parity) {
	case "", "none", "n", "even", "e", "odd", "o":
	default:
		return fmt.Errorf("scale.parity %q: use none, even or odd", c.Parity)
	}
	if c.StopBits != 0 && c.StopBits != 1 && c.StopBits != 2 {
		return fmt.Errorf("scale.stop_bits %d: use 1 or 2", c.StopBits)
	}
	return nil
}

// Status is what the heartbeat and GET /scale/status report.
type Status struct {
	Configured  bool       `json:"configured"`
	Simulated   bool       `json:"simulated"`
	Device      string     `json:"device,omitempty"`
	Connected   bool       `json:"connected"`
	LastFrameAt *time.Time `json:"lastFrameAt"`
	Frames      uint64     `json:"frames"`
	Rejected    uint64     `json:"rejected"`
	LastError   string     `json:"lastError,omitempty"`
}

// Opener opens the byte source. It must return a read-only stream: the
// service never writes to it, and the default opener does not expose a writer.
type Opener func(ctx context.Context) (io.ReadCloser, error)

// Service owns the reader loop. Zero or one per process.
type Service struct {
	cfg      Config
	parser   *Parser
	detector *Detector
	open     Opener
	now      func() time.Time
	onFrame  func(Frame)

	mu        sync.Mutex
	connected bool
	frames    uint64
	rejected  uint64
	lastErr   string
}

// Option adjusts a Service (tests).
type Option func(*Service)

// WithClock replaces time.Now.
func WithClock(now func() time.Time) Option { return func(s *Service) { s.now = now } }

// WithOpener replaces the device/simulator opener.
func WithOpener(o Opener) Option { return func(s *Service) { s.open = o } }

// WithFrameHook is called after every accepted frame (tests).
func WithFrameHook(f func(Frame)) Option { return func(s *Service) { s.onFrame = f } }

// NewService builds a service for cfg. It does not start reading.
func NewService(cfg Config, opts ...Option) (*Service, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	parser, err := NewParser(cfg.Frame)
	if err != nil {
		return nil, err
	}
	s := &Service{cfg: cfg, parser: parser, detector: NewDetector(), now: time.Now}
	s.open = s.defaultOpener()
	for _, o := range opts {
		o(s)
	}
	return s, nil
}

// Configured reports whether a scale is set up on this node.
func (s *Service) Configured() bool { return s != nil && s.cfg.Configured() }

// Reading applies the stability rule now.
func (s *Service) Reading() Reading {
	if !s.Configured() {
		return Reading{Stable: false, Reason: ReasonNotConfigured, Configured: false}
	}
	r := s.detector.Reading(s.now())
	r.Simulated = s.cfg.Simulate
	return r
}

// Status reports presence for the heartbeat and the status endpoint.
func (s *Service) Status() Status {
	if !s.Configured() {
		return Status{Configured: false}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	st := Status{
		Configured: true,
		Simulated:  s.cfg.Simulate,
		Device:     s.cfg.Device,
		Connected:  s.connected,
		Frames:     s.frames,
		Rejected:   s.rejected,
		LastError:  s.lastErr,
	}
	if at := s.detector.LastFrameAt(); !at.IsZero() {
		utc := at.UTC()
		st.LastFrameAt = &utc
	}
	return st
}

// Run reads until ctx ends, reopening the source with backoff on errors.
func (s *Service) Run(ctx context.Context) {
	if !s.Configured() {
		return
	}
	backoff := time.Second
	for ctx.Err() == nil {
		src, err := s.open(ctx)
		if err != nil {
			s.setState(false, err)
			log.Printf("scale: open %s: %v (retry in %s)", s.describe(), err, backoff)
		} else {
			s.setState(true, nil)
			backoff = time.Second
			err = s.consume(ctx, src)
			_ = src.Close()
			s.setState(false, err)
			s.detector.Reset()
			if ctx.Err() != nil {
				return
			}
			log.Printf("scale: %s closed: %v (reopen in %s)", s.describe(), err, backoff)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		if backoff < 30*time.Second {
			backoff *= 2
		}
	}
}

// consume reads lines (CR, LF or CR LF terminated) and feeds the detector.
func (s *Service) consume(ctx context.Context, src io.Reader) error {
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			if c, ok := src.(io.Closer); ok {
				_ = c.Close()
			}
		case <-done:
		}
	}()
	scanner := bufio.NewScanner(src)
	scanner.Buffer(make([]byte, 0, 256), 4096)
	scanner.Split(scanLines)
	for scanner.Scan() {
		s.Feed(scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	return io.EOF
}

// Feed processes one line as if it had just arrived.
func (s *Service) Feed(line string) {
	if strings.TrimSpace(line) == "" {
		return
	}
	frame, err := s.parser.Parse(line)
	s.mu.Lock()
	if err != nil {
		s.rejected++
		s.mu.Unlock()
		return
	}
	s.frames++
	s.mu.Unlock()
	s.detector.Add(s.now(), frame.Grams)
	if s.onFrame != nil {
		s.onFrame(frame)
	}
}

func (s *Service) setState(connected bool, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.connected = connected
	if err != nil && !errors.Is(err, context.Canceled) {
		s.lastErr = err.Error()
	} else if connected {
		s.lastErr = ""
	}
}

func (s *Service) describe() string {
	if s.cfg.Simulate {
		return "simulator"
	}
	return s.cfg.Device
}

// defaultOpener opens the simulator, a TCP stream or a serial port. Every
// branch hands back only a reader.
func (s *Service) defaultOpener() Opener {
	cfg := s.cfg
	return func(ctx context.Context) (io.ReadCloser, error) {
		switch {
		case cfg.Simulate:
			pr, pw := io.Pipe()
			sim := &Simulator{Seed: time.Now().UnixNano(), Speed: cfg.SimulateSpeed}
			simCtx, cancel := context.WithCancel(ctx)
			go func() {
				err := sim.Run(simCtx, pw)
				_ = pw.CloseWithError(err)
			}()
			return &readOnly{r: pr, close: func() error { cancel(); return pr.Close() }}, nil
		case strings.HasPrefix(cfg.Device, "tcp://"):
			var d net.Dialer
			conn, err := d.DialContext(ctx, "tcp", strings.TrimPrefix(cfg.Device, "tcp://"))
			if err != nil {
				return nil, err
			}
			if tcp, ok := conn.(*net.TCPConn); ok {
				_ = tcp.CloseWrite() // receive-only: half-close our sending side
			}
			return &readOnly{r: conn, close: conn.Close}, nil
		default:
			port, err := serial.Open(cfg.Device, serialMode(cfg))
			if err != nil {
				return nil, err
			}
			return &readOnly{r: port, close: port.Close}, nil
		}
	}
}

func serialMode(cfg Config) *serial.Mode {
	mode := &serial.Mode{BaudRate: 9600, DataBits: 8, Parity: serial.NoParity, StopBits: serial.OneStopBit}
	if cfg.Baud > 0 {
		mode.BaudRate = cfg.Baud
	}
	if cfg.DataBits > 0 {
		mode.DataBits = cfg.DataBits
	}
	switch strings.ToLower(cfg.Parity) {
	case "even", "e":
		mode.Parity = serial.EvenParity
	case "odd", "o":
		mode.Parity = serial.OddParity
	}
	if cfg.StopBits == 2 {
		mode.StopBits = serial.TwoStopBits
	}
	return mode
}

// readOnly hides any Write method of the underlying port.
type readOnly struct {
	r     io.Reader
	close func() error
	once  sync.Once
	err   error
}

func (r *readOnly) Read(p []byte) (int, error) { return r.r.Read(p) }

func (r *readOnly) Close() error {
	r.once.Do(func() { r.err = r.close() })
	return r.err
}

// scanLines splits on CR, LF or CR LF; indicators disagree on the terminator.
func scanLines(data []byte, atEOF bool) (int, []byte, error) {
	for i, b := range data {
		if b == '\n' || b == '\r' {
			adv := i + 1
			if b == '\r' && i+1 < len(data) && data[i+1] == '\n' {
				adv++
			}
			return adv, data[:i], nil
		}
	}
	if atEOF && len(data) > 0 {
		return len(data), data, nil
	}
	return 0, nil, nil
}
