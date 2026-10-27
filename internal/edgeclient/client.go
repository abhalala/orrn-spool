package edgeclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/orrn/spool/internal/api/middleware"
	"github.com/orrn/spool/internal/cache"
	"github.com/orrn/spool/internal/jobs"
	"github.com/orrn/spool/internal/node"
	"github.com/orrn/spool/internal/printer"
)

var (
	ErrNodeRevoked = errors.New("edge node has been revoked")
)

type EnrollRequest struct {
	EnrollmentToken string `json:"enrollmentToken"`
	AgentVersion    string `json:"agentVersion"`
	OS              string `json:"os"`
	Arch            string `json:"arch"`
	InstallID       string `json:"installId"`
}

type EnrollResponse struct {
	ProtocolVersion   int    `json:"protocolVersion"`
	DeploymentID      string `json:"deploymentId"`
	InstanceID        string `json:"instanceId"`
	NodeName          string `json:"nodeName"`
	SiteLabel         string `json:"siteLabel"`
	APIOrigin         string `json:"apiOrigin"`
	NodeSecret        string `json:"nodeSecret"`
	CredentialVersion int    `json:"credentialVersion"`
	ConfigVersion     int    `json:"configVersion"`
}

type ReportedCapabilities struct {
	MACAddress    string   `json:"macAddress,omitempty"`
	Host          string   `json:"host"`
	Port          int      `json:"port"`
	Features      []string `json:"features"`
	MediaType     string   `json:"mediaType,omitempty"`
	MediaWidthMM  float64  `json:"mediaWidthMm,omitempty"`
	MediaHeightMM float64  `json:"mediaHeightMm,omitempty"`
	GapMM         float64  `json:"gapMm,omitempty"`
	DPI           int      `json:"dpi,omitempty"`
	SpeedInSec    int      `json:"speedInSec,omitempty"`
	Density       int      `json:"density,omitempty"`
	Cutter        *bool    `json:"cutter,omitempty"`
	VerifiedAt    string   `json:"verifiedAt"`
}

type ReportedPrinter struct {
	PrinterID    string                `json:"printerId"`
	Name         string                `json:"name"`
	IPAddress    string                `json:"ipAddress"`
	Port         int                   `json:"port"`
	Status       string                `json:"status"`
	CanPrint     bool                  `json:"canPrint"`
	LastSeenAt   *string               `json:"lastSeenAt"`
	LastError    *string               `json:"lastError"`
	Capabilities *ReportedCapabilities `json:"capabilities,omitempty"`
}

type PollRequest struct {
	Limit                   int               `json:"limit"`
	AppliedConfigVersion    int               `json:"appliedConfigVersion"`
	ActiveCredentialVersion int               `json:"activeCredentialVersion"`
	Printers                []ReportedPrinter `json:"printers,omitempty"`
}

type LeasedCommand struct {
	CommandID   string          `json:"commandId"`
	LeaseID     string          `json:"leaseId"`
	Kind        string          `json:"kind"`
	JobKey      string          `json:"jobKey,omitempty"`
	PayloadHash string          `json:"payloadHash,omitempty"`
	Payload     json.RawMessage `json:"payload"`
}

type PollResponse struct {
	Commands                 []LeasedCommand `json:"commands"`
	ConfigVersion            int             `json:"configVersion"`
	PendingCredentialVersion int             `json:"pendingCredentialVersion"`
	SnapshotManifestVersion  string          `json:"snapshotManifestVersion"`
}

type ConfigResponse struct {
	ProtocolVersion          int               `json:"protocolVersion"`
	ConfigSchemaVersion      int               `json:"configSchemaVersion"`
	DeploymentID             string            `json:"deploymentId"`
	InstanceID               string            `json:"instanceId"`
	ConfigVersion            int               `json:"configVersion"`
	PendingCredentialVersion int               `json:"pendingCredentialVersion"`
	PendingSecret            string            `json:"pendingSecret"`
	Printers                 []json.RawMessage `json:"printers"`
	Profiles                 []json.RawMessage `json:"profiles"`
	Templates                []json.RawMessage `json:"templates"`
}

type CommandAckRequest struct {
	CommandID  string  `json:"commandId"`
	LeaseID    string  `json:"leaseId"`
	Outcome    string  `json:"outcome"`
	Resolution string  `json:"resolution"`
	AgentJobID *string `json:"agentJobId"`
	Detail     *string `json:"detail"`
}

type SnapshotManifestResponse struct {
	ProtocolVersion    int                     `json:"protocolVersion"`
	CacheSchemaVersion int                     `json:"cacheSchemaVersion"`
	CompanyID          string                  `json:"companyId"`
	MinAgentVersion    string                  `json:"minAgentVersion"`
	GeneratedAt        string                  `json:"generatedAt"`
	Sections           []SnapshotSectionHeader `json:"sections"`
}

type SnapshotSectionHeader struct {
	Section       string                `json:"section"`
	SnapshotID    string                `json:"snapshotId"`
	GeneratedAt   string                `json:"generatedAt"`
	ContentSHA256 string                `json:"contentSha256"`
	RecordCount   int                   `json:"recordCount"`
	ChunkCount    int                   `json:"chunkCount"`
	Complete      bool                  `json:"complete"`
	Chunks        []SnapshotChunkHeader `json:"chunks"`
}

type SnapshotChunkHeader struct {
	Sequence    int    `json:"sequence"`
	SHA256      string `json:"sha256"`
	RecordCount int    `json:"recordCount"`
	ByteLength  int    `json:"byteLength"`
}

type Client struct {
	store                *node.Store
	version              string
	httpClient           *http.Client
	jobStore             *jobs.Store
	cacheStore           *cache.Store
	reporter             func() []ReportedPrinter
	revocationHandler    func(context.Context) error
	mu                   sync.RWMutex
	appliedConfigVersion int
	printers             []printer.Target
}

// appliedConfig is the complete non-secret desired state persisted locally.
type appliedConfig struct {
	ConfigVersion int               `json:"configVersion"`
	Printers      []printer.Target  `json:"printers"`
	Profiles      []json.RawMessage `json:"profiles"`
	Templates     []json.RawMessage `json:"templates"`
}

func New(store *node.Store, version string, httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 15 * time.Second}
	}
	if strings.TrimSpace(version) == "" {
		version = "1.0.0"
	}
	return &Client{
		store:      store,
		version:    version,
		httpClient: httpClient,
	}
}

func (c *Client) WithStores(jobStore *jobs.Store, cacheStore *cache.Store) *Client {
	c.jobStore = jobStore
	c.cacheStore = cacheStore
	return c
}

// WithRevocationHandler disables local grants and fallback access on revocation
// or an explicit change of enrolled deployment.
func (c *Client) WithRevocationHandler(fn func(context.Context) error) *Client {
	c.revocationHandler = fn
	return c
}

// WithPrinterReporter supplies the printer status snapshot sent on every poll.
func (c *Client) WithPrinterReporter(fn func() []ReportedPrinter) *Client {
	c.reporter = fn
	return c
}

// RestoreConfig reloads the last applied config so printing continues after a
// restart without waiting for the cloud.
func (c *Client) RestoreConfig(ctx context.Context) error {
	if c.jobStore == nil {
		return nil
	}
	version, raw, err := c.jobStore.LoadAppliedConfig(ctx)
	if err != nil || version == 0 {
		return err
	}
	var cfg appliedConfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return fmt.Errorf("decode applied config: %w", err)
	}
	c.mu.Lock()
	c.appliedConfigVersion = version
	c.printers = cfg.Printers
	c.mu.Unlock()
	return nil
}

// Printers returns the printers from the applied desired config.
func (c *Client) Printers() []printer.Target {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return append([]printer.Target(nil), c.printers...)
}

func (c *Client) printerByID(id string) (printer.Target, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	for _, t := range c.printers {
		if t.PrinterID == id {
			return t, true
		}
	}
	return printer.Target{}, false
}

func (c *Client) configVersion() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.appliedConfigVersion
}

func validateAPIOrigin(raw string) error {
	origin, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || origin.Host == "" || origin.User != nil {
		return errors.New("apiOrigin must be an HTTPS origin without user information")
	}
	if origin.Scheme == "https" {
		return nil
	}
	ip := net.ParseIP(origin.Hostname())
	if origin.Scheme == "http" && (strings.EqualFold(origin.Hostname(), "localhost") || (ip != nil && ip.IsLoopback())) {
		return nil
	}
	return errors.New("apiOrigin must use HTTPS; HTTP is allowed only for loopback development")
}

func (c *Client) Enroll(ctx context.Context, apiOrigin, enrollmentToken, installID string) (EnrollResponse, error) {
	apiOrigin = strings.TrimRight(strings.TrimSpace(apiOrigin), "/")
	if apiOrigin == "" {
		return EnrollResponse{}, errors.New("apiOrigin is required")
	}
	if err := validateAPIOrigin(apiOrigin); err != nil {
		return EnrollResponse{}, err
	}
	reqBody := EnrollRequest{
		EnrollmentToken: strings.TrimSpace(enrollmentToken),
		AgentVersion:    c.version,
		OS:              runtime.GOOS,
		Arch:            runtime.GOARCH,
		InstallID:       strings.TrimSpace(installID),
	}
	if reqBody.OS != "windows" && reqBody.OS != "linux" {
		reqBody.OS = "linux"
	}
	if reqBody.Arch != "amd64" && reqBody.Arch != "arm64" {
		reqBody.Arch = "amd64"
	}

	raw, err := json.Marshal(reqBody)
	if err != nil {
		return EnrollResponse{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiOrigin+"/api/edge/v1/enroll", bytes.NewReader(raw))
	if err != nil {
		return EnrollResponse{}, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return EnrollResponse{}, err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return EnrollResponse{}, fmt.Errorf("enroll failed (%d): %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var out EnrollResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return EnrollResponse{}, fmt.Errorf("decode enroll response: %w", err)
	}
	if out.APIOrigin == "" {
		out.APIOrigin = apiOrigin
	}
	if err := validateAPIOrigin(out.APIOrigin); err != nil {
		return EnrollResponse{}, fmt.Errorf("enroll response has invalid apiOrigin: %w", err)
	}
	if out.ProtocolVersion != 1 || strings.TrimSpace(out.DeploymentID) == "" || strings.TrimSpace(out.InstanceID) == "" || strings.TrimSpace(out.NodeSecret) == "" || out.CredentialVersion <= 0 || out.ConfigVersion <= 0 || strings.TrimSpace(installID) == "" {
		return EnrollResponse{}, errors.New("enroll response has invalid node identity or credential")
	}

	previous, previousErr := c.store.Load()
	if previousErr != nil && !errors.Is(previousErr, node.ErrNodeNotEnrolled) {
		return EnrollResponse{}, previousErr
	}
	identityChanged := previousErr == nil && (previous.DeploymentID != out.DeploymentID || previous.InstanceID != out.InstanceID)
	if identityChanged {
		return EnrollResponse{}, errors.New("different node identity requires an explicit local state reset before enrollment")
	}
	if c.jobStore != nil {
		if _, err := c.jobStore.BindIdentity(ctx, out.DeploymentID, out.InstanceID); err != nil {
			return EnrollResponse{}, fmt.Errorf("bind enrolled identity: %w", err)
		}
	}

	newState := node.State{
		ProtocolVersion: 1,
		DeploymentID:    out.DeploymentID,
		InstanceID:      out.InstanceID,
		InstallID:       strings.TrimSpace(installID),
		NodeName:        out.NodeName,
		SiteLabel:       out.SiteLabel,
		APIOrigin:       out.APIOrigin,
		Active: node.Credential{
			Version: out.CredentialVersion,
			Secret:  out.NodeSecret,
		},
	}
	if err := c.store.Save(newState); err != nil {
		return EnrollResponse{}, err
	}
	if c.jobStore != nil && c.cacheStore != nil {
		if err := c.fetchConfigAndRotate(ctx, newState, 0); err != nil {
			return out, fmt.Errorf("apply initial config: %w", err)
		}
		if err := c.SyncSnapshotsOnce(ctx); err != nil {
			return out, fmt.Errorf("apply initial snapshots: %w", err)
		}
	}
	return out, nil
}

func (c *Client) PollOnce(ctx context.Context) error {
	st, err := c.store.Load()
	if err != nil {
		return err
	}
	if st.Revoked {
		return c.revoke(ctx)
	}

	// If a pending credential was staged before a crash, confirm it before polling.
	if st.Pending != nil && st.Pending.Version > st.Active.Version && st.Pending.Secret != "" {
		if err := c.confirmPendingCredential(ctx, st, *st.Pending); err != nil {
			return err
		}
		st, err = c.store.Load()
		if err != nil {
			return err
		}
	}

	pollReq := PollRequest{
		Limit:                   10,
		AppliedConfigVersion:    c.configVersion(),
		ActiveCredentialVersion: st.Active.Version,
	}
	if c.reporter != nil {
		pollReq.Printers = c.reporter()
	}
	rawReq, err := json.Marshal(pollReq)
	if err != nil {
		return err
	}

	respBody, err := c.doSigned(ctx, st, st.Active.Secret, http.MethodPost, "/api/edge/v1/commands/poll", rawReq)
	if err != nil {
		return err
	}

	var pollResp PollResponse
	if err := json.Unmarshal(respBody, &pollResp); err != nil {
		return fmt.Errorf("decode poll response: %w", err)
	}

	if pollResp.PendingCredentialVersion > st.Active.Version || pollResp.ConfigVersion > c.configVersion() {
		configCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		err := c.fetchConfigAndRotate(configCtx, st, pollResp.PendingCredentialVersion)
		cancel()
		if err != nil {
			return err
		}
		st, err = c.store.Load()
		if err != nil {
			return err
		}
	}

	for _, cmd := range pollResp.Commands {
		if err := c.handleLeasedCommand(ctx, st, cmd); err != nil {
			return err
		}
	}

	if c.jobStore != nil {
		if err := c.FlushOutboxOnce(ctx); err != nil {
			return err
		}
	}

	return nil
}

func (c *Client) fetchConfigAndRotate(ctx context.Context, st node.State, advertisedPendingVersion int) error {
	rawCfg, err := c.doSigned(ctx, st, st.Active.Secret, http.MethodGet, "/api/edge/v1/config", nil)
	if err != nil {
		return err
	}
	var cfg ConfigResponse
	if err := json.Unmarshal(rawCfg, &cfg); err != nil {
		return fmt.Errorf("decode config response: %w", err)
	}
	if cfg.ProtocolVersion != 1 || cfg.ConfigSchemaVersion != 1 || cfg.DeploymentID != st.DeploymentID || cfg.InstanceID != st.InstanceID || cfg.ConfigVersion <= 0 || cfg.Printers == nil || cfg.Profiles == nil || cfg.Templates == nil {
		return errors.New("config response is incomplete or bound to another node")
	}

	pendingVer := cfg.PendingCredentialVersion
	if pendingVer == 0 {
		pendingVer = advertisedPendingVersion
	}
	if pendingVer > st.Active.Version && strings.TrimSpace(cfg.PendingSecret) != "" {
		cred := node.Credential{
			Version: pendingVer,
			Secret:  strings.TrimSpace(cfg.PendingSecret),
		}
		if err := c.store.StagePending(cred); err != nil {
			return err
		}
		if err := c.confirmPendingCredential(ctx, st, cred); err != nil {
			return err
		}
	}
	if cfg.ConfigVersion <= c.configVersion() {
		return nil
	}
	targets, err := printer.ParseTargets(cfg.Printers)
	if err != nil {
		return fmt.Errorf("config version %d rejected: %w", cfg.ConfigVersion, err)
	}
	if c.jobStore != nil {
		raw, err := json.Marshal(appliedConfig{ConfigVersion: cfg.ConfigVersion, Printers: targets, Profiles: cfg.Profiles, Templates: cfg.Templates})
		if err != nil {
			return err
		}
		if err := c.jobStore.ApplyConfig(ctx, cfg.ConfigVersion, raw); err != nil {
			return err
		}
	}
	c.mu.Lock()
	c.appliedConfigVersion = cfg.ConfigVersion
	c.printers = targets
	c.mu.Unlock()
	return nil
}

func (c *Client) confirmPendingCredential(ctx context.Context, st node.State, pending node.Credential) error {
	rawBody := []byte(fmt.Sprintf(`{"credentialVersion":%d}`, pending.Version))
	if _, err := c.doSigned(ctx, st, pending.Secret, http.MethodPost, "/api/edge/v1/credentials/confirm", rawBody); err != nil {
		return err
	}
	return c.store.PromotePending(pending.Version)
}

func (c *Client) handleLeasedCommand(ctx context.Context, st node.State, cmd LeasedCommand) error {
	if c.jobStore == nil {
		return nil
	}
	var res jobs.CommandResolution
	var err error

	switch cmd.Kind {
	case "print_job":
		var payload printJobPayload
		in := jobs.PrintCommandInput{CommandID: cmd.CommandID, JobKey: cmd.JobKey, PayloadHash: cmd.PayloadHash}
		if uerr := json.Unmarshal(cmd.Payload, &payload); uerr != nil {
			in.InvalidReason = "Malformed print_job payload"
		} else {
			if payload.JobKey != "" {
				in.JobKey = payload.JobKey
			}
			if payload.PayloadHash != "" {
				in.PayloadHash = payload.PayloadHash
			}
			in.PrinterID = payload.PrinterID
			in.Copies = payload.Copies
			if _, ok := c.printerByID(payload.PrinterID); !ok {
				in.InvalidReason = fmt.Sprintf("Printer %q is not in this node's config", payload.PrinterID)
			} else if tspl, rerr := renderPrintJob(payload); rerr != nil {
				in.InvalidReason = "Label render failed: " + rerr.Error()
			} else {
				in.TSPL = tspl
			}
		}
		res, err = c.jobStore.ResolvePrintCommand(ctx, in)
	case "test_print":
		var payload struct {
			PrinterID string `json:"printerId"`
		}
		in := jobs.PrintCommandInput{CommandID: cmd.CommandID, Kind: "test_print", JobKey: "test:" + cmd.CommandID, Copies: 1}
		_ = json.Unmarshal(cmd.Payload, &payload)
		in.PrinterID = payload.PrinterID
		if target, ok := c.printerByID(payload.PrinterID); ok {
			in.TSPL = renderTestLabel(target, time.Now())
			in.PayloadHash = sha256Hex(in.TSPL)
		} else {
			in.InvalidReason = fmt.Sprintf("Printer %q is not in this node's config", payload.PrinterID)
		}
		res, err = c.jobStore.ResolvePrintCommand(ctx, in)
	case "cancel_job":
		var payload struct {
			AgentJobID string `json:"agentJobId"`
			JobKey     string `json:"jobKey"`
		}
		_ = json.Unmarshal(cmd.Payload, &payload)
		target := payload.AgentJobID
		if target == "" {
			target = payload.JobKey
		}
		if target == "" {
			target = cmd.JobKey
		}
		res, err = c.jobStore.ResolveCancelCommand(ctx, cmd.CommandID, target)
	default:
		res, err = c.jobStore.ResolveInvalidCommand(ctx, cmd.CommandID, cmd.Kind, "Unsupported command kind: "+cmd.Kind)
	}
	if err != nil {
		return err
	}

	var agentJobIDPtr *string
	if res.AgentJobID != "" {
		v := res.AgentJobID
		agentJobIDPtr = &v
	}
	var detailPtr *string
	if res.Detail != "" {
		v := res.Detail
		if len(v) > 512 {
			v = v[:512]
		}
		detailPtr = &v
	}

	ackReq := CommandAckRequest{
		CommandID:  cmd.CommandID,
		LeaseID:    cmd.LeaseID,
		Outcome:    res.Outcome,
		Resolution: res.Resolution,
		AgentJobID: agentJobIDPtr,
		Detail:     detailPtr,
	}
	rawAck, err := json.Marshal(ackReq)
	if err != nil {
		return err
	}
	_, err = c.doSigned(ctx, st, st.Active.Secret, http.MethodPost, "/api/edge/v1/commands/ack", rawAck)
	return err
}

func (c *Client) FlushOutboxOnce(ctx context.Context) error {
	if c.jobStore == nil {
		return nil
	}
	st, err := c.store.Load()
	if err != nil {
		return err
	}
	if st.Revoked {
		return c.revoke(ctx)
	}
	events, err := c.jobStore.ListPendingOutboxEvents(ctx, 50)
	if err != nil || len(events) == 0 {
		return err
	}
	raw, err := json.Marshal(map[string]any{"events": events})
	if err != nil {
		return err
	}
	ids := make([]string, 0, len(events))
	for _, ev := range events {
		ids = append(ids, ev.EventID)
	}
	if _, err := c.doSigned(ctx, st, st.Active.Secret, http.MethodPost, "/api/edge/v1/events", raw); err != nil {
		if scheduleErr := c.jobStore.ScheduleOutboxRetry(ctx, ids, time.Now().Add(5*time.Second)); scheduleErr != nil {
			return errors.Join(err, scheduleErr)
		}
		return err
	}

	return c.jobStore.AckOutboxEvents(ctx, ids)
}

func (c *Client) SyncSnapshotsOnce(ctx context.Context) error {
	if c.cacheStore == nil {
		return nil
	}
	st, err := c.store.Load()
	if err != nil {
		return err
	}
	if st.Revoked {
		return c.revoke(ctx)
	}

	rawManifest, err := c.doSigned(ctx, st, st.Active.Secret, http.MethodGet, "/api/edge/v1/snapshots/manifest", nil)
	if err != nil {
		return err
	}
	var manifest SnapshotManifestResponse
	if err := json.Unmarshal(rawManifest, &manifest); err != nil {
		return fmt.Errorf("decode snapshot manifest: %w", err)
	}
	if err := c.validateSnapshotManifest(manifest); err != nil {
		return err
	}

	var firstErr error
	for _, sec := range manifest.Sections {
		existing, ok, err := c.cacheStore.GetSectionMeta(ctx, sec.Section)
		if err != nil {
			return err
		}
		if ok && existing.CompanyID != manifest.CompanyID {
			return fmt.Errorf("cached %s section belongs to another company", sec.Section)
		}
		if ok && existing.SnapshotID == sec.SnapshotID {
			continue
		}

		payloads := make([]cache.ChunkPayload, 0, sec.ChunkCount)
		for _, hdr := range sec.Chunks {
			path := fmt.Sprintf("/api/edge/v1/snapshots/%s/chunks/%d", url.PathEscape(sec.SnapshotID), hdr.Sequence)
			rawChunk, err := c.doSigned(ctx, st, st.Active.Secret, http.MethodGet, path, nil)
			if err != nil {
				return err
			}
			if len(rawChunk) != hdr.ByteLength || len(rawChunk) > 1024*1024 {
				return fmt.Errorf("snapshot %s chunk %d byte length mismatch: got %d, want %d", sec.SnapshotID, hdr.Sequence, len(rawChunk), hdr.ByteLength)
			}
			payloads = append(payloads, cache.ChunkPayload{
				Sequence:    hdr.Sequence,
				SHA256:      hdr.SHA256,
				RecordCount: hdr.RecordCount,
				RawJSON:     rawChunk,
			})
		}

		if err := c.cacheStore.ApplySection(ctx, cache.SectionMeta{
			Section:       sec.Section,
			SnapshotID:    sec.SnapshotID,
			CompanyID:     manifest.CompanyID,
			GeneratedAt:   sec.GeneratedAt,
			ContentSHA256: sec.ContentSHA256,
			RecordCount:   sec.RecordCount,
		}, payloads); err != nil && firstErr == nil {
			firstErr = fmt.Errorf("apply %s snapshot: %w", sec.Section, err)
		}
	}
	return firstErr
}

func (c *Client) validateSnapshotManifest(manifest SnapshotManifestResponse) error {
	if manifest.ProtocolVersion != 1 || manifest.CacheSchemaVersion != cache.CacheSchemaVersion || strings.TrimSpace(manifest.CompanyID) == "" {
		return errors.New("snapshot manifest has unsupported schema or missing company")
	}
	if !versionAtLeast(c.version, manifest.MinAgentVersion) {
		return fmt.Errorf("snapshot requires agent %s or newer", manifest.MinAgentVersion)
	}
	if _, err := time.Parse(time.RFC3339Nano, manifest.GeneratedAt); err != nil {
		return errors.New("snapshot manifest has invalid generatedAt")
	}
	required := []string{"stock", "orders", "dies", "print_status", "company_format"}
	if len(manifest.Sections) != len(required) {
		return errors.New("snapshot manifest is incomplete")
	}
	seen := make(map[string]bool, len(required))
	for _, sec := range manifest.Sections {
		if seen[sec.Section] {
			return fmt.Errorf("duplicate snapshot section %s", sec.Section)
		}
		seen[sec.Section] = true
		if !sec.Complete || sec.SnapshotID == "" || sec.RecordCount < 0 || sec.ChunkCount <= 0 || len(sec.Chunks) != sec.ChunkCount || !validSHA256(sec.ContentSHA256) {
			return fmt.Errorf("snapshot section %s is incomplete or invalid", sec.Section)
		}
		if _, err := time.Parse(time.RFC3339Nano, sec.GeneratedAt); err != nil {
			return fmt.Errorf("snapshot section %s has invalid generatedAt", sec.Section)
		}
		recordCount := 0
		for seq, chunk := range sec.Chunks {
			if chunk.Sequence != seq || chunk.RecordCount < 0 || chunk.ByteLength <= 0 || chunk.ByteLength > 1024*1024 || !validSHA256(chunk.SHA256) {
				return fmt.Errorf("snapshot section %s has invalid chunk descriptor %d", sec.Section, seq)
			}
			recordCount += chunk.RecordCount
		}
		if recordCount != sec.RecordCount {
			return fmt.Errorf("snapshot section %s record count mismatch", sec.Section)
		}
	}
	for _, section := range required {
		if !seen[section] {
			return fmt.Errorf("snapshot manifest missing %s", section)
		}
	}
	return nil
}

func validSHA256(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, ch := range value {
		if (ch < '0' || ch > '9') && (ch < 'a' || ch > 'f') {
			return false
		}
	}
	return true
}

func versionAtLeast(current, minimum string) bool {
	parse := func(value string) ([3]int, bool, bool) {
		var out [3]int
		value = strings.SplitN(strings.TrimSpace(value), "+", 2)[0]
		parts := strings.SplitN(value, "-", 2)
		numbers := strings.Split(parts[0], ".")
		if len(numbers) != 3 {
			return out, false, false
		}
		for i := range numbers {
			n, err := strconv.Atoi(numbers[i])
			if err != nil || n < 0 {
				return out, false, false
			}
			out[i] = n
		}
		return out, len(parts) == 2, true
	}
	cur, curPre, curOK := parse(current)
	min, minPre, minOK := parse(minimum)
	if !curOK || !minOK {
		return false
	}
	for i := range cur {
		if cur[i] != min[i] {
			return cur[i] > min[i]
		}
	}
	return !curPre || minPre
}

func (c *Client) doSigned(ctx context.Context, st node.State, secret, method, pathWithQuery string, body []byte) ([]byte, error) {
	origin := strings.TrimRight(st.APIOrigin, "/")
	if err := validateAPIOrigin(origin); err != nil {
		return nil, err
	}
	fullURL := origin + pathWithQuery

	var reader io.Reader
	if len(body) > 0 {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, fullURL, reader)
	if err != nil {
		return nil, err
	}
	if len(body) > 0 {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("X-ORRN-Node-ID", st.InstanceID)
	req.Header.Set("X-ORRN-Edge-Version", c.version)
	tok := middleware.SignORRN(secret, method, pathWithQuery, body, time.Now().UTC())
	req.Header.Set("Authorization", "Bearer "+tok)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusUnauthorized {
		if strings.Contains(string(respBody), "node_revoked") {
			return nil, c.revoke(ctx)
		}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("%s %s failed (%d): %s", method, pathWithQuery, resp.StatusCode, strings.TrimSpace(string(respBody)))
	}
	return respBody, nil
}

func (c *Client) revoke(ctx context.Context) error {
	if err := c.store.MarkRevoked(); err != nil {
		return errors.Join(ErrNodeRevoked, err)
	}
	if err := c.wipeLocalAccess(ctx); err != nil {
		return errors.Join(ErrNodeRevoked, err)
	}
	return ErrNodeRevoked
}

func (c *Client) wipeLocalAccess(ctx context.Context) error {
	var errs []error
	if c.cacheStore != nil {
		if err := c.cacheStore.WipeCompanyCache(ctx); err != nil {
			errs = append(errs, err)
		}
	}
	if c.revocationHandler != nil {
		if err := c.revocationHandler(ctx); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
