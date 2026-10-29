package main

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/ozal-project/ozal/internal/admin"
	"github.com/ozal-project/ozal/internal/events"
)

// auditFailureReporter keeps audit failures visible through the independent
// process log without turning request volume into an unbounded log flood. The
// local writer is sticky-failed after an I/O error, so one alert is sufficient.
type auditFailureReporter struct {
	appender    events.AuditAppender
	logger      *slog.Logger
	failure     error
	mu          sync.Mutex
	available   bool
	failures    uint64
	lastFailure time.Time
	once        sync.Once
}

func newAuditFailureReporter(appender events.AuditAppender, logger *slog.Logger, failure error) *auditFailureReporter {
	reporter := &auditFailureReporter{appender: appender, logger: logger, failure: failure, available: appender != nil && failure == nil}
	if failure != nil {
		reporter.failures = 1
		reporter.lastFailure = time.Now().UTC()
	}
	return reporter
}

func (r *auditFailureReporter) Append(ctx context.Context, event events.Event) error {
	err := r.failure
	if r.appender != nil {
		err = r.appender.Append(ctx, event)
	}
	if err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
		r.mu.Lock()
		r.available = false
		r.failures++
		r.lastFailure = time.Now().UTC()
		r.mu.Unlock()
		r.report(err)
	} else if err == nil {
		r.mu.Lock()
		r.available = true
		r.mu.Unlock()
	}
	return err
}

func (r *auditFailureReporter) Status() admin.AuditStatus {
	r.mu.Lock()
	defer r.mu.Unlock()
	status := admin.AuditStatus{Available: r.available, FailureCount: r.failures}
	if !r.lastFailure.IsZero() {
		status.LastFailureAt = r.lastFailure.Format(time.RFC3339Nano)
	}
	return status
}

func (r *auditFailureReporter) report(err error) {
	if err == nil || r.logger == nil {
		return
	}
	r.once.Do(func() { r.logger.Error("durable audit unavailable", "failure_class", "storage") })
}
