package core

type Job struct {
	ID          string
	PrinterID   string
	Content     string
	Status      JobStatus
	RetryCount  int
	CreatedAt   int64
	UpdatedAt   int64
}

type JobStatus string

const (
	JobStatusPending    JobStatus = "pending"
	JobStatusProcessing JobStatus = "processing"
	JobStatusCompleted  JobStatus = "completed"
	JobStatusFailed     JobStatus = "failed"
	JobStatusCancelled  JobStatus = "cancelled"
)

type JobManager struct {
	queue      *Queue
	retryLimit int
}
