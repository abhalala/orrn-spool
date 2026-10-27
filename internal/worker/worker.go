// Package worker drains accepted jobs to their printers. Each printer is
// driven by at most one goroutine at a time, and every job transition is
// persisted before and after the device write.
package worker

import (
	"context"
	"errors"
	"log"
	"math/rand/v2"
	"sort"
	"sync"
	"time"

	"github.com/orrn/spool/internal/edgeclient"
	"github.com/orrn/spool/internal/jobs"
	"github.com/orrn/spool/internal/printer"
)

const (
	maxBackoff    = 60 * time.Second
	probeInterval = 30 * time.Second
	maxErrorLen   = 512
)

type printerState struct {
	target       printer.Target
	session      *printer.Session
	busy         bool
	failures     int
	probedAt     time.Time
	status       printer.Status
	capabilities *printer.Capabilities
	lastSeen     *time.Time
	lastErr      string
}

type Worker struct {
	jobs            *jobs.Store
	targets         func() []printer.Target
	now             func() time.Time
	recoverEndpoint func(context.Context, printer.Target) (printer.EndpointEvidence, error)
	mu              sync.Mutex
	states          map[string]*printerState
}

func New(jobStore *jobs.Store, targets func() []printer.Target) *Worker {
	return &Worker{
		jobs:            jobStore,
		targets:         targets,
		now:             time.Now,
		recoverEndpoint: printer.RecoverEndpoint,
		states:          map[string]*printerState{},
	}
}

// Run ticks until ctx is cancelled.
func (w *Worker) Run(ctx context.Context, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		w.Tick(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// Tick drains every configured printer once, in parallel, and waits.
func (w *Worker) Tick(ctx context.Context) {
	var wg sync.WaitGroup
	for _, st := range w.sync() {
		wg.Add(1)
		go func(st *printerState) {
			defer wg.Done()
			defer w.release(st)
			w.drain(ctx, st)
		}(st)
	}
	wg.Wait()
}

// sync reconciles sessions with the applied config and claims idle printers.
func (w *Worker) sync() []*printerState {
	w.mu.Lock()
	defer w.mu.Unlock()
	want := map[string]bool{}
	var ready []*printerState
	for _, t := range w.targets() {
		want[t.PrinterID] = true
		st, ok := w.states[t.PrinterID]
		if !ok || st.target.Host != t.Host || st.target.Port != t.Port {
			st = &printerState{target: t, session: printer.NewSession(t.Config())}
			w.states[t.PrinterID] = st
		}
		st.target = t
		if st.busy || t.Paused {
			continue
		}
		st.busy = true
		ready = append(ready, st)
	}
	for id, st := range w.states {
		if !want[id] && !st.busy {
			delete(w.states, id)
		}
	}
	return ready
}

func (w *Worker) release(st *printerState) {
	w.mu.Lock()
	st.busy = false
	w.mu.Unlock()
}

func (w *Worker) drain(ctx context.Context, st *printerState) {
	id := st.target.PrinterID
	printed := false
	for ctx.Err() == nil {
		job, err := w.jobs.ClaimNextJob(ctx, id)
		if err != nil {
			log.Printf("worker: claim for printer %s: %v", id, err)
			return
		}
		if job == nil {
			break
		}
		printed = true
		markedTransmitting := false
		beforeWrite := func() error {
			if markedTransmitting {
				return nil
			}
			if err := w.jobs.MarkTransmitting(ctx, job.ID); err != nil {
				return err
			}
			markedTransmitting = true
			return nil
		}
		res := st.session.TransmitBeforeWrite(ctx, job.TSPL, beforeWrite)
		w.record(st, res.Status, res.Reason)
		if res.Outcome == printer.OutcomeRetryable && res.BytesWritten == 0 && w.recoverAfterDHCP(ctx, st) {
			res = st.session.TransmitBeforeWrite(ctx, job.TSPL, beforeWrite)
			w.record(st, res.Status, res.Reason)
		}
		if res.Cause != nil {
			if errors.Is(res.Cause, jobs.ErrJobStateChanged) {
				continue
			}
			log.Printf("worker: mark transmitting %s: %v", job.ID, res.Cause)
			return
		}
		switch res.Outcome {
		case printer.OutcomeSent:
			err = w.jobs.MarkSent(ctx, job.ID)
			if err == nil {
				err = w.jobs.RecordTestPrintResult(ctx, job.ID, "")
			}
			w.succeeded(st)
		case printer.OutcomeRetryable:
			nextAttemptAt := w.failed(st)
			err = w.jobs.MarkFailedRetryable(ctx, job.ID, res.Reason, nextAttemptAt)
		default:
			err = w.jobs.MarkOutcomeUnknown(ctx, job.ID, res.Reason)
			if err == nil {
				err = w.jobs.RecordTestPrintResult(ctx, job.ID, res.Reason)
			}
			w.failed(st)
		}
		if err != nil {
			log.Printf("worker: record outcome for %s: %v", job.ID, err)
			return
		}
		if res.Outcome != printer.OutcomeSent {
			return
		}
	}
	if !printed && w.now().Sub(st.probedAt) >= probeInterval {
		status, err := st.session.ProbeStatus(ctx)
		reason := ""
		if err != nil {
			reason = err.Error()
		}
		w.record(st, status, reason)
		if status.Online {
			capabilities, discoverErr := printer.Discover(ctx, st.target)
			if discoverErr == nil {
				w.mu.Lock()
				st.capabilities = &capabilities
				w.mu.Unlock()
			}
		}
	}
}

func (w *Worker) recoverAfterDHCP(ctx context.Context, st *printerState) bool {
	w.mu.Lock()
	capabilities := st.capabilities
	target := st.target
	w.mu.Unlock()
	if capabilities == nil || capabilities.MACAddress == "" {
		return false
	}
	target.MACAddress = capabilities.MACAddress
	evidence, err := w.recoverEndpoint(ctx, target)
	if err != nil {
		w.record(st, printer.Status{Online: false, CanPrint: false, State: "offline"}, err.Error())
		return false
	}
	if err := w.jobs.RecordPrinterEndpointVerified(ctx, jobs.PrinterEndpointEvent{
		PrinterID: evidence.PrinterID, Host: evidence.Host, Port: evidence.Port,
		MACAddress: evidence.MACAddress, Model: evidence.Model, VerifiedAt: evidence.VerifiedAt,
	}); err != nil {
		w.record(st, printer.Status{Online: false, CanPrint: false, State: "offline"}, err.Error())
		return false
	}
	w.mu.Lock()
	st.target.Host = evidence.Host
	st.target.Port = evidence.Port
	st.session = printer.NewSession(st.target.Config())
	st.capabilities.Host = evidence.Host
	st.capabilities.Port = evidence.Port
	w.mu.Unlock()
	return true
}

func (w *Worker) record(st *printerState, status printer.Status, reason string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	now := w.now()
	st.probedAt = now
	st.status = status
	if status.Online {
		seen := now.UTC()
		st.lastSeen = &seen
	}
	switch {
	case status.Error != "":
		st.lastErr = status.Error
	case reason != "":
		st.lastErr = reason
	default:
		st.lastErr = ""
	}
}

func (w *Worker) succeeded(st *printerState) {
	w.mu.Lock()
	st.failures = 0
	w.mu.Unlock()
}

func (w *Worker) failed(st *printerState) time.Time {
	w.mu.Lock()
	defer w.mu.Unlock()
	st.failures++
	return w.now().Add(backoff(st.failures))
}

// backoff is 1s doubling to 60s, with ±20% jitter.
func backoff(failures int) time.Duration {
	d := time.Second
	for i := 1; i < failures && d < maxBackoff; i++ {
		d *= 2
	}
	if d > maxBackoff {
		d = maxBackoff
	}
	jitter := time.Duration(rand.Int64N(int64(d)/5*2+1)) - d/5
	return d + jitter
}

// Reports is the printer snapshot sent to ORRN on each poll.
func (w *Worker) Reports() []edgeclient.ReportedPrinter {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := make([]edgeclient.ReportedPrinter, 0, len(w.states))
	for _, st := range w.states {
		r := edgeclient.ReportedPrinter{
			PrinterID: st.target.PrinterID,
			Name:      st.target.Name,
			IPAddress: st.target.Host,
			Port:      st.target.Port,
			Status:    reportStatus(st),
			CanPrint:  !st.target.Paused && st.status.CanPrint,
		}
		if st.lastSeen != nil {
			s := st.lastSeen.Format(time.RFC3339)
			r.LastSeenAt = &s
		}
		if st.lastErr != "" {
			e := st.lastErr
			if len(e) > maxErrorLen {
				e = e[:maxErrorLen]
			}
			r.LastError = &e
		}
		if st.capabilities != nil {
			capabilities := st.capabilities
			r.Capabilities = &edgeclient.ReportedCapabilities{
				MACAddress:    capabilities.MACAddress,
				Host:          capabilities.Host,
				Port:          capabilities.Port,
				Features:      append([]string(nil), capabilities.Features...),
				MediaType:     capabilities.MediaType,
				MediaWidthMM:  capabilities.MediaWidthMM,
				MediaHeightMM: capabilities.MediaHeightMM,
				GapMM:         capabilities.GapMM,
				DPI:           capabilities.DPI,
				SpeedInSec:    capabilities.SpeedInSec,
				Density:       capabilities.Density,
				VerifiedAt:    capabilities.VerifiedAt.Format(time.RFC3339),
			}
			if capabilities.Cutter != nil {
				cutter := *capabilities.Cutter
				r.Capabilities.Cutter = &cutter
			}
		}
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].PrinterID < out[j].PrinterID })
	return out
}

func reportStatus(st *printerState) string {
	switch {
	case st.target.Paused:
		return "paused"
	case st.probedAt.IsZero():
		return "unknown"
	case !st.status.Online:
		return "offline"
	case st.status.CanPrint:
		return "online"
	case st.status.State == "paused":
		return "paused"
	default:
		return "error"
	}
}
