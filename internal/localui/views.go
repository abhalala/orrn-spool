package localui

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/orrn/spool/internal/cache"
	"github.com/orrn/spool/internal/edgeclient"
	"github.com/orrn/spool/internal/jobs"
	"github.com/orrn/spool/internal/printer"
)

var printerReporterMu sync.RWMutex

// SetPrinterReporter connects the live worker's redacted printer snapshots to
// the local UI. It is optional so account setup and tests can run without a
// worker; unreported printers are shown as "unknown" rather than "online".
func (s *Server) SetPrinterReporter(reporter func() []edgeclient.ReportedPrinter) {
	printerReporterMu.Lock()
	defer printerReporterMu.Unlock()
	s.printerReporter = reporter
}

type jobView struct {
	ID            string        `json:"id"`
	JobKey        string        `json:"jobKey"`
	PayloadHash   string        `json:"payloadHash"`
	PrinterID     string        `json:"printerId"`
	Copies        int           `json:"copies"`
	State         jobs.JobState `json:"state"`
	Attempts      int           `json:"attempts"`
	LastError     string        `json:"lastError,omitempty"`
	SourceJobID   string        `json:"sourceJobId,omitempty"`
	ReprintByUser string        `json:"reprintByUser,omitempty"`
	CreatedAt     string        `json:"createdAt"`
	UpdatedAt     string        `json:"updatedAt"`
	CanReprint    bool          `json:"canReprint"`
}

func publicJob(job jobs.Job) jobView {
	return jobView{
		ID:            job.ID,
		JobKey:        job.JobKey,
		PayloadHash:   job.PayloadHash,
		PrinterID:     job.PrinterID,
		Copies:        job.Copies,
		State:         job.State,
		Attempts:      job.Attempts,
		LastError:     boundedError(job.LastError),
		SourceJobID:   job.SourceJobID,
		ReprintByUser: job.ReprintByUser,
		CreatedAt:     job.CreatedAt,
		UpdatedAt:     job.UpdatedAt,
		CanReprint:    isReprintable(job.State),
	}
}

func (s *Server) publicJobs(ctx context.Context, items []jobs.Job) []jobView {
	config, _ := s.appliedConfig(ctx)
	destinations := make(map[string]bool, len(config.Printers))
	for _, target := range config.Printers {
		destinations[target.PrinterID] = true
	}
	out := make([]jobView, 0, len(items))
	for _, item := range items {
		view := publicJob(item)
		view.CanReprint = view.CanReprint && strings.TrimSpace(item.TSPL) != "" && destinations[item.PrinterID]
		out = append(out, view)
	}
	return out
}

func isReprintable(state jobs.JobState) bool {
	switch state {
	case jobs.StateSent, jobs.StateOutcomeUnknown, jobs.StateFailedFinal, jobs.StateCancelled:
		return true
	default:
		return false
	}
}

func boundedError(value string) string {
	const max = 500
	if len(value) > max {
		return value[:max]
	}
	return value
}

type cachedRecordView struct {
	Section  string          `json:"section"`
	EntityID string          `json:"entityId"`
	SortKey  string          `json:"sortKey"`
	DTO      json.RawMessage `json:"dto"`
}

func publicCacheRecords(records []cache.Record) []cachedRecordView {
	out := make([]cachedRecordView, 0, len(records))
	for _, record := range records {
		out = append(out, cachedRecordView{
			Section:  record.Section,
			EntityID: record.EntityID,
			SortKey:  record.SortKey,
			DTO:      redactJSON(record.DTO),
		})
	}
	return out
}

func redactJSON(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return json.RawMessage(`{}`)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return json.RawMessage(`{"redacted":"invalid cached DTO"}`)
	}
	encoded, err := json.Marshal(redactValue(value))
	if err != nil {
		return json.RawMessage(`{"redacted":"unavailable cached DTO"}`)
	}
	return encoded
}

func redactValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, item := range typed {
			if sensitiveCacheKey(key) {
				continue
			}
			out[key] = redactValue(item)
		}
		return out
	case []any:
		out := make([]any, len(typed))
		for index, item := range typed {
			out[index] = redactValue(item)
		}
		return out
	default:
		return value
	}
}

func sensitiveCacheKey(key string) bool {
	normalized := strings.ToLower(key)
	normalized = strings.NewReplacer("_", "", "-", "", " ", "", ".", "").Replace(normalized)
	for _, fragment := range []string{"labelvariable", "templatevariable", "password", "secret", "token", "credential", "authorization", "price", "cost"} {
		if strings.Contains(normalized, fragment) {
			return true
		}
	}
	if strings.HasPrefix(normalized, "rate") || strings.HasSuffix(normalized, "rate") || strings.Contains(normalized, "ratepaise") || strings.Contains(normalized, "rateper") {
		return true
	}
	switch normalized {
	case "variables", "variabledata", "variablevalues", "labelvars", "labeldata", "printdata",
		"tspl", "tsplcommands", "payload", "labelpayload", "printpayload",
		"cookie", "privatekey", "accesskey", "accesskeyid", "apikey",
		"amount", "total", "totalamount", "subtotal", "tax", "taxamount", "grandtotal",
		"discount", "rate", "mrp", "nettotal", "grosstotal":
		return true
	default:
		return false
	}
}

type appliedConfigView struct {
	ConfigVersion int              `json:"configVersion"`
	UpdatedAt     string           `json:"updatedAt,omitempty"`
	Printers      []printer.Target `json:"printers"`
}

func (s *Server) appliedConfig(ctx context.Context) (appliedConfigView, error) {
	var raw, updatedAt string
	var version int
	err := s.db.QueryRowContext(ctx, `
		SELECT applied_config_version, config_json, updated_at
		FROM edge_config_state WHERE id = 1
	`).Scan(&version, &raw, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return appliedConfigView{Printers: []printer.Target{}}, nil
	}
	if err != nil {
		return appliedConfigView{}, err
	}
	var stored struct {
		ConfigVersion int              `json:"configVersion"`
		Printers      []printer.Target `json:"printers"`
	}
	if err := json.Unmarshal([]byte(raw), &stored); err != nil {
		return appliedConfigView{}, errors.New("stored printer configuration is invalid")
	}
	if stored.ConfigVersion != 0 {
		version = stored.ConfigVersion
	}
	if stored.Printers == nil {
		stored.Printers = []printer.Target{}
	}
	sort.Slice(stored.Printers, func(i, j int) bool {
		return stored.Printers[i].PrinterID < stored.Printers[j].PrinterID
	})
	return appliedConfigView{ConfigVersion: version, UpdatedAt: updatedAt, Printers: stored.Printers}, nil
}

type printerView struct {
	PrinterID  string  `json:"printerId"`
	Name       string  `json:"name"`
	Status     string  `json:"status"`
	CanPrint   bool    `json:"canPrint"`
	LastSeenAt *string `json:"lastSeenAt,omitempty"`
	LastError  *string `json:"lastError,omitempty"`
}

func (s *Server) printerViews(ctx context.Context) ([]printerView, int, error) {
	config, err := s.appliedConfig(ctx)
	if err != nil {
		return nil, 0, err
	}
	reports := map[string]edgeclient.ReportedPrinter{}
	printerReporterMu.RLock()
	reporter := s.printerReporter
	printerReporterMu.RUnlock()
	if reporter != nil {
		for _, report := range reporter() {
			reports[report.PrinterID] = report
		}
	}
	out := make([]printerView, 0, len(config.Printers))
	for _, target := range config.Printers {
		view := printerView{PrinterID: target.PrinterID, Name: target.Name, Status: "unknown"}
		if report, ok := reports[target.PrinterID]; ok {
			view.Status = report.Status
			view.CanPrint = report.CanPrint
			view.LastSeenAt = report.LastSeenAt
			if report.LastError != nil {
				message := boundedError(*report.LastError)
				view.LastError = &message
			}
		}
		out = append(out, view)
	}
	return out, config.ConfigVersion, nil
}

func (s *Server) createLocalTest(ctx context.Context, printerID string) (*jobs.Job, error) {
	if s.jobStore == nil {
		return nil, errors.New("job store unavailable")
	}
	config, err := s.appliedConfig(ctx)
	if err != nil {
		return nil, err
	}
	var selected *printer.Target
	for index := range config.Printers {
		if config.Printers[index].PrinterID == printerID {
			selected = &config.Printers[index]
			break
		}
	}
	if selected == nil {
		return nil, fmt.Errorf("printer %q is not in this node's config", printerID)
	}
	commandID := "local-test-" + randomHex(16)
	tspl := localTestLabel(*selected, time.Now().UTC())
	digest := sha256.Sum256([]byte(tspl))
	resolution, err := s.jobStore.ResolvePrintCommand(ctx, jobs.PrintCommandInput{
		CommandID:   commandID,
		JobKey:      "local-test:" + commandID,
		PayloadHash: hex.EncodeToString(digest[:]),
		PrinterID:   printerID,
		TSPL:        tspl,
		Copies:      1,
	})
	if err != nil {
		return nil, err
	}
	if resolution.Outcome != "accepted" || resolution.AgentJobID == "" {
		return nil, errors.New("local printer test was rejected")
	}
	return s.jobStore.GetJob(ctx, resolution.AgentJobID)
}

func localTestLabel(target printer.Target, now time.Time) string {
	if target.LabelWidthMM <= 0 {
		target.LabelWidthMM = 100
	}
	if target.LabelHeightMM <= 0 {
		target.LabelHeightMM = 75
	}
	if target.GapMM <= 0 {
		target.GapMM = 3
	}
	if target.DPI <= 0 {
		target.DPI = 203
	}
	dotsPerMM := float64(target.DPI) / 25.4
	centerX := int(target.LabelWidthMM*dotsPerMM) / 2
	centerY := int(target.LabelHeightMM*dotsPerMM) / 2
	name := strings.ReplaceAll(target.Name, "\"", "'")
	return fmt.Sprintf(
		"SIZE %.2f mm, %.2f mm\r\nGAP %.2f mm, 0 mm\r\nDIRECTION 0\r\nCLS\r\n"+
			"TEXT %d,%d,\"3\",0,2,2,\"TEST LABEL\"\r\n"+
			"TEXT %d,%d,\"3\",0,1,1,\"Printer: %s\"\r\n"+
			"TEXT %d,%d,\"2\",0,1,1,\"%s\"\r\nPRINT 1,1\r\n",
		target.LabelWidthMM, target.LabelHeightMM, target.GapMM,
		centerX-80, centerY-60,
		centerX-100, centerY, name,
		centerX-100, centerY+30, now.Format("2006-01-02 15:04 UTC"),
	)
}

func (s *Server) cacheFreshness(ctx context.Context) ([]map[string]any, error) {
	sections := make([]map[string]any, 0, 4)
	for _, name := range []string{"stock", "orders", "dies", "print_status"} {
		item := map[string]any{"section": name, "cachedAsOf": "", "recordCount": 0, "banner": "No cached snapshot"}
		if s.cacheStore != nil {
			meta, found, err := s.cacheStore.GetSectionMeta(ctx, name)
			if err != nil {
				return nil, err
			}
			if found {
				item["cachedAsOf"] = meta.GeneratedAt
				item["recordCount"] = meta.RecordCount
				item["snapshotId"] = meta.SnapshotID
			}
		}
		sections = append(sections, item)
	}
	return sections, nil
}
