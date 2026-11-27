package monitor

import (
	"context"
	"log"
	"sync"
	"time"
)

const jobTimeout = time.Minute

type Job struct {
	monitor   *Monitor
	interval  time.Duration
	mu        sync.Mutex
	running   bool
	started   bool
	stopped   bool
	cancel    context.CancelFunc
	stop      chan struct{}
	waitGroup sync.WaitGroup
}

func NewJob(monitor *Monitor, interval time.Duration) *Job {
	return &Job{monitor: monitor, interval: interval, stop: make(chan struct{})}
}

func (j *Job) Start(ctx context.Context) {
	j.mu.Lock()
	if j.started || j.stopped {
		j.mu.Unlock()
		return
	}
	j.started = true
	j.waitGroup.Add(1)
	j.mu.Unlock()
	go func() {
		defer j.waitGroup.Done()
		j.Run(ctx)
		ticker := time.NewTicker(j.interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-j.stop:
				return
			case <-ticker.C:
				j.Run(ctx)
			}
		}
	}()
}

func (j *Job) Stop() {
	j.mu.Lock()
	if !j.stopped {
		j.stopped = true
		close(j.stop)
	}
	if j.cancel != nil {
		j.cancel()
	}
	j.mu.Unlock()
	j.waitGroup.Wait()
}

func (j *Job) Run(ctx context.Context) {
	j.mu.Lock()
	if j.stopped || j.running {
		j.mu.Unlock()
		return
	}
	j.running = true
	runCtx, cancel := context.WithTimeout(ctx, jobTimeout)
	j.cancel = cancel
	j.mu.Unlock()

	defer func() {
		cancel()
		j.mu.Lock()
		j.running = false
		j.cancel = nil
		j.mu.Unlock()
	}()
	if err := j.monitor.Collect(runCtx); err != nil {
		log.Printf("Resource monitor failed: %v", err)
	}
}
