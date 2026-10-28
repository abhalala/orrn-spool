package core

import (
	"sync"
	"sync/atomic"
	"testing"
)

type countingPrinterManager struct {
	calls atomic.Int64
	err   error
}

func (p *countingPrinterManager) Print(int64, string, int) error {
	p.calls.Add(1)
	return p.err
}

func (p *countingPrinterManager) GetPrinter(id int64) (*Printer, error) {
	return &Printer{ID: id}, nil
}

func (p *countingPrinterManager) IncrementPrintCount(int64, int) error { return nil }

func TestConcurrentDeliveryPrintsOnlyOnce(t *testing.T) {
	q := newTestQueue(t)
	printer := &countingPrinterManager{}
	q.printerManager = printer
	id, _, err := q.EnqueueOnce(&Job{Copies: 1, TSPLContent: "CLS\r\nPRINT 1,1\r\n"})
	if err != nil {
		t.Fatalf("EnqueueOnce: %v", err)
	}

	start := make(chan struct{})
	var workers sync.WaitGroup
	workers.Add(2)
	for range 2 {
		go func() {
			defer workers.Done()
			<-start
			q.processJob(id)
		}()
	}
	close(start)
	workers.Wait()

	if got := printer.calls.Load(); got != 1 {
		t.Fatalf("device transmissions = %d, want 1", got)
	}
	job, err := q.GetJob(id)
	if err != nil || job.Status != JobStatusCompleted {
		t.Fatalf("job after concurrent delivery: %+v, %v", job, err)
	}
}

func TestRecoveryDoesNotBlindRetryInterruptedJob(t *testing.T) {
	q := newTestQueue(t)
	id, _, err := q.EnqueueOnce(&Job{Copies: 1, TSPLContent: "CLS\r\nPRINT 1,1\r\n"})
	if err != nil {
		t.Fatalf("EnqueueOnce: %v", err)
	}
	q.updateJobStatus(id, JobStatusProcessing, "", nil, nil)

	if err := q.recoverJobs(); err != nil {
		t.Fatalf("recoverJobs: %v", err)
	}
	job, err := q.GetJob(id)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	if job.Status != JobStatusFailed || job.ErrorMessage == "" {
		t.Fatalf("interrupted job must need an explicit retry, got %+v", job)
	}
}

func TestZeroByteFailureUsesConfiguredRetry(t *testing.T) {
	q := newTestQueue(t)
	printer := &countingPrinterManager{err: ErrConnectionFailed}
	q.printerManager = printer
	id, _, err := q.EnqueueOnce(&Job{Copies: 1, MaxRetries: 3, TSPLContent: "CLS\r\nPRINT 1,1\r\n"})
	if err != nil {
		t.Fatalf("EnqueueOnce: %v", err)
	}

	q.processJob(id)
	job, err := q.GetJob(id)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	if job.Status != JobStatusProcessing || job.RetryCount != 1 {
		t.Fatalf("zero-byte failure must use configured retry, got %+v", job)
	}
}

func TestOutcomeUnknownDoesNotRetryAutomatically(t *testing.T) {
	q := newTestQueue(t)
	printer := &countingPrinterManager{err: ErrPrintOutcomeUnknown}
	q.printerManager = printer
	id, _, err := q.EnqueueOnce(&Job{Copies: 1, MaxRetries: 3, TSPLContent: "CLS\r\nPRINT 1,1\r\n"})
	if err != nil {
		t.Fatalf("EnqueueOnce: %v", err)
	}

	q.processJob(id)
	job, err := q.GetJob(id)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	if job.Status != JobStatusFailed || job.RetryCount != 0 {
		t.Fatalf("ambiguous print must fail without automatic retry, got %+v", job)
	}
}
