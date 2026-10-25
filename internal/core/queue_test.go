package core

import (
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/orrn/spool/internal/db"
)

var (
	dbOnce sync.Once
	dbErr  error
)

// newTestQueue shares one database for the whole test binary: db.Init is
// once-per-process, so a per-test temp dir would vanish under later tests.
func newTestQueue(t *testing.T) *Queue {
	t.Helper()
	dbOnce.Do(func() {
		dir, err := os.MkdirTemp("", "spool-core-test")
		if err != nil {
			dbErr = err
			return
		}
		dbErr = db.Init(db.Config{Path: filepath.Join(dir, "spool.db")})
	})
	if dbErr != nil {
		t.Fatal(dbErr)
	}
	return NewQueue(db.GetDB(), nil, nil, nil, nil)
}

// A job the queue just enqueued must be readable by the queue itself. Rows used
// to carry a NULL error_message, which made every GetJob/Dequeue fail to scan
// and left jobs stuck in "pending" forever.
func TestEnqueuedJobCanBeReadBack(t *testing.T) {
	q := newTestQueue(t)
	id, created, err := q.EnqueueOnce(&Job{VariablesJSON: `{"serial":"B-1"}`, Copies: 1, JobKey: "k:1"})
	if err != nil || !created {
		t.Fatalf("enqueue: id=%d created=%v err=%v", id, created, err)
	}
	job, err := q.GetJob(id)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	if job.ErrorMessage != "" || job.Status != JobStatusPending {
		t.Fatalf("unexpected job %+v", job)
	}
	found := false
	for {
		next, err := q.Dequeue()
		if err != nil {
			t.Fatalf("Dequeue: %v", err)
		}
		if next == nil {
			break
		}
		found = found || next.ID == id
	}
	if !found {
		t.Fatalf("Dequeue never returned job %d", id)
	}
}

func TestEnqueueOnceReturnsFirstJobForSameKey(t *testing.T) {
	q := newTestQueue(t)
	first, created, err := q.EnqueueOnce(&Job{Copies: 1, JobKey: "bundle:B-9:label:v1:attempt:1"})
	if err != nil || !created {
		t.Fatal(first, created, err)
	}
	again, created, err := q.EnqueueOnce(&Job{Copies: 1, JobKey: "bundle:B-9:label:v1:attempt:1"})
	if err != nil || created || again != first {
		t.Fatalf("repeat: id=%d created=%v err=%v want id=%d", again, created, err, first)
	}
	other, created, err := q.EnqueueOnce(&Job{Copies: 1, JobKey: "bundle:B-9:label:v1:attempt:2"})
	if err != nil || !created || other == first {
		t.Fatalf("new attempt key must be a new job: id=%d created=%v err=%v", other, created, err)
	}
	// Jobs without a key never collide.
	a, _, _ := q.EnqueueOnce(&Job{Copies: 1})
	b, _, _ := q.EnqueueOnce(&Job{Copies: 1})
	if a == b {
		t.Fatal("keyless jobs must be independent")
	}
}
