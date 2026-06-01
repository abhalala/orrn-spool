package core

type JobManager struct {
	queue      *Queue
	retryLimit int
}

func NewJobManager(queue *Queue, retryLimit int) *JobManager {
	if retryLimit <= 0 {
		retryLimit = 3
	}
	return &JobManager{queue: queue, retryLimit: retryLimit}
}
