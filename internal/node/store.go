package node

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

var (
	ErrNodeNotEnrolled        = errors.New("edge node is not enrolled")
	ErrNoPendingCredential    = errors.New("no pending credential staged")
	ErrPendingVersionMismatch = errors.New("pending credential version mismatch")
)

type Credential struct {
	Version int    `json:"version"`
	Secret  string `json:"secret"`
}

type State struct {
	ProtocolVersion int         `json:"protocolVersion"`
	DeploymentID    string      `json:"deploymentId"`
	InstanceID      string      `json:"instanceId"`
	InstallID       string      `json:"installId,omitempty"`
	NodeName        string      `json:"nodeName"`
	SiteLabel       string      `json:"siteLabel"`
	APIOrigin       string      `json:"apiOrigin"`
	Revoked         bool        `json:"revoked,omitempty"`
	Active          Credential  `json:"active"`
	Pending         *Credential `json:"pending,omitempty"`
}

// Valid reports whether this is a complete, usable V1 enrolled identity.
func (st State) Valid() bool {
	return st.ProtocolVersion == 1 && !st.Revoked &&
		strings.TrimSpace(st.DeploymentID) != "" && strings.TrimSpace(st.InstanceID) != "" &&
		strings.TrimSpace(st.InstallID) != "" && strings.TrimSpace(st.APIOrigin) != "" &&
		st.Active.Version > 0 && strings.TrimSpace(st.Active.Secret) != ""
}

type Store struct {
	path string
	mu   sync.Mutex
}

func NewStore(path string) *Store {
	return &Store{path: path}
}

func (s *Store) Path() string {
	return s.path
}

func (s *Store) Load() (State, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loadUnlocked()
}

func (s *Store) loadUnlocked() (State, error) {
	raw, err := os.ReadFile(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return State{}, fmt.Errorf("%w: %w", ErrNodeNotEnrolled, err)
		}
		return State{}, fmt.Errorf("read node state %s: %w", s.path, err)
	}
	var st State
	if err := json.Unmarshal(raw, &st); err != nil {
		return State{}, fmt.Errorf("decode node state %s: %w", s.path, err)
	}
	if st.ProtocolVersion == 0 {
		st.ProtocolVersion = 1
	}
	return st, nil
}

func (s *Store) Save(st State) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.saveUnlocked(st)
}

func (s *Store) saveUnlocked(st State) error {
	if st.ProtocolVersion == 0 {
		st.ProtocolVersion = 1
	}
	if strings.TrimSpace(st.InstanceID) == "" {
		return errors.New("instanceId is required")
	}
	if strings.TrimSpace(st.APIOrigin) == "" {
		return errors.New("apiOrigin is required")
	}
	if !st.Revoked && (st.Active.Version <= 0 || strings.TrimSpace(st.Active.Secret) == "") {
		return errors.New("active credential version and secret are required")
	}

	dir := filepath.Dir(s.path)
	if dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return fmt.Errorf("mkdir %s: %w", dir, err)
		}
	}

	raw, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return fmt.Errorf("encode node state: %w", err)
	}
	raw = append(raw, '\n')

	tmp, err := os.CreateTemp(dir, ".node.json.tmp-*")
	if err != nil {
		return fmt.Errorf("create temp node state: %w", err)
	}
	tmpPath := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpPath)
		}
	}()

	if err := protectNodeFile(tmpPath); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("protect temp node state: %w", err)
	}
	if _, err := tmp.Write(raw); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write temp node state: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("sync temp node state: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp node state: %w", err)
	}

	if err := os.Rename(tmpPath, s.path); err != nil {
		return fmt.Errorf("rename temp node state to %s: %w", s.path, err)
	}
	cleanup = false
	if err := protectNodeFile(s.path); err != nil {
		return fmt.Errorf("protect node state: %w", err)
	}
	if err := syncNodeDirectory(s.path); err != nil {
		return fmt.Errorf("sync node state directory: %w", err)
	}
	return nil
}

func (s *Store) StagePending(cred Credential) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if cred.Version <= 0 || strings.TrimSpace(cred.Secret) == "" {
		return errors.New("pending credential version and secret are required")
	}

	st, err := s.loadUnlocked()
	if err != nil {
		return err
	}
	if st.Active.Version == cred.Version && st.Active.Secret == cred.Secret {
		return nil
	}
	if st.Pending != nil && st.Pending.Version == cred.Version && st.Pending.Secret == cred.Secret {
		return nil
	}
	cp := cred
	st.Pending = &cp
	return s.saveUnlocked(st)
}

func (s *Store) PromotePending(version int) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	st, err := s.loadUnlocked()
	if err != nil {
		return err
	}
	if st.Active.Version == version && st.Pending == nil {
		return nil
	}
	if st.Pending == nil {
		return ErrNoPendingCredential
	}
	if st.Pending.Version != version {
		return fmt.Errorf("%w: have %d, want %d", ErrPendingVersionMismatch, st.Pending.Version, version)
	}
	st.Active = *st.Pending
	st.Pending = nil
	return s.saveUnlocked(st)
}

func (s *Store) MarkRevoked() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	st, err := s.loadUnlocked()
	if err != nil {
		return err
	}
	st.Revoked = true
	st.Active = Credential{}
	st.Pending = nil
	return s.saveUnlocked(st)
}
