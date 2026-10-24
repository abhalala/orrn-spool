// Package heartbeat posts this node's presence to ORRN so the Printing screen
// can say, honestly, whether the node and its scale are alive.
package heartbeat

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/orrn/spool/internal/api/middleware"
)

// Path is ORRN's heartbeat endpoint (alias /webhooks/edge/heartbeat).
const Path = "/webhooks/spool/activate"

// ScalePresence is the scale part of the payload.
type ScalePresence struct {
	Configured  bool       `json:"configured"`
	Simulated   bool       `json:"simulated"`
	Connected   bool       `json:"connected"`
	LastFrameAt *time.Time `json:"lastFrameAt"`
}

// Payload is the JSON body. instance_id and version are what ORRN has always
// read; scale is new.
type Payload struct {
	InstanceID string        `json:"instance_id"`
	Version    string        `json:"version"`
	Scale      ScalePresence `json:"scale"`
}

// Config for the sender.
type Config struct {
	ServerURL    string
	InstanceID   string
	Subdomain    string
	SharedSecret string
	Version      string
	Interval     time.Duration
	// Scale is read at every beat; nil means no scale.
	Scale func() ScalePresence
	// Client and Now are replaceable for tests.
	Client *http.Client
	Now    func() time.Time
}

// Enabled reports whether there is enough configuration to send.
func (c Config) Enabled() bool {
	return c.ServerURL != "" && c.InstanceID != "" && c.SharedSecret != ""
}

// Sender posts heartbeats.
type Sender struct{ cfg Config }

// New returns a sender; check cfg.Enabled() first.
func New(cfg Config) *Sender {
	if cfg.Interval <= 0 {
		cfg.Interval = 60 * time.Second
	}
	if cfg.Client == nil {
		cfg.Client = &http.Client{Timeout: 10 * time.Second}
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Version == "" {
		cfg.Version = "dev"
	}
	cfg.ServerURL = strings.TrimRight(cfg.ServerURL, "/")
	return &Sender{cfg: cfg}
}

// Run beats once immediately, then every Interval, until ctx ends.
func (s *Sender) Run(ctx context.Context) {
	ticker := time.NewTicker(s.cfg.Interval)
	defer ticker.Stop()
	for {
		if err := s.Beat(ctx); err != nil && ctx.Err() == nil {
			log.Printf("heartbeat: %v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// Beat sends one signed heartbeat.
func (s *Sender) Beat(ctx context.Context) error {
	payload := Payload{InstanceID: s.cfg.InstanceID, Version: s.cfg.Version}
	if s.cfg.Scale != nil {
		payload.Scale = s.cfg.Scale()
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.cfg.ServerURL+Path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	token := middleware.SignORRN(s.cfg.SharedSecret, http.MethodPost, Path, body, s.cfg.Now())
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	if s.cfg.Subdomain != "" {
		req.Header.Set("X-Spool-Subdomain", s.cfg.Subdomain)
	}
	res, err := s.cfg.Client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(res.Body, 4096))
	if res.StatusCode/100 != 2 {
		return fmt.Errorf("ORRN answered %d", res.StatusCode)
	}
	return nil
}
