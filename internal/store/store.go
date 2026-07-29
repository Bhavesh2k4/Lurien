// Package store defines the persistence port and an in-memory adapter. The
// engine depends only on Repo; a Postgres (sqlc) adapter will implement the
// same interface without any engine change.
package store

import (
	"context"
	"sync"
	"time"

	"lurien/internal/core"
)

// Repo is the persistence port used by the engine.
type Repo interface {
	// ActiveBySource returns non-closed jobs for a source, keyed by ExternalID.
	ActiveBySource(ctx context.Context, sourceID string) (map[string]core.Job, error)
	// Save inserts or updates a job (keyed by SourceID + ExternalID).
	Save(ctx context.Context, job core.Job) error
	// SaveAndEnqueue upserts the job AND enqueues a notification atomically
	// (same transaction), so a crash can never persist one without the other.
	SaveAndEnqueue(ctx context.Context, job core.Job, n core.Notification) error
	// Close marks a job closed.
	Close(ctx context.Context, sourceID, externalID string) error
}

// Outbox is the port the notification dispatcher depends on. Both Memory and
// Postgres implement it; the dispatcher never imports a concrete store.
type Outbox interface {
	// ClaimNotifications leases up to limit due notifications, marking them
	// "sending" with a visibility deadline of now()+timeout.
	ClaimNotifications(ctx context.Context, limit int, timeout time.Duration) ([]core.Notification, error)
	MarkSent(ctx context.Context, id int64) error
	RescheduleNotification(ctx context.Context, id int64, next time.Time, errMsg string) error
	FailNotification(ctx context.Context, id int64, errMsg string) error
}

// Memory is an in-memory Repo + Outbox for development and tests.
type Memory struct {
	mu     sync.RWMutex
	jobs   map[string]core.Job     // key: sourceID + "\x00" + externalID
	outbox map[int64]*core.Notification
	seen   map[string]bool         // idempotency key: source|ext|event
	nextID int64
}

// NewMemory builds an empty in-memory store.
func NewMemory() *Memory {
	return &Memory{
		jobs:   map[string]core.Job{},
		outbox: map[int64]*core.Notification{},
		seen:   map[string]bool{},
	}
}

func key(sourceID, externalID string) string { return sourceID + "\x00" + externalID }

// ActiveBySource implements Repo.
func (m *Memory) ActiveBySource(_ context.Context, sourceID string) (map[string]core.Job, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := map[string]core.Job{}
	for _, j := range m.jobs {
		if j.SourceID == sourceID && j.State != core.StateClosed {
			out[j.ExternalID] = j
		}
	}
	return out, nil
}

// Save implements Repo.
func (m *Memory) Save(_ context.Context, job core.Job) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.jobs[key(job.SourceID, job.ExternalID)] = job
	return nil
}

// Close implements Repo.
func (m *Memory) Close(_ context.Context, sourceID, externalID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	k := key(sourceID, externalID)
	if j, ok := m.jobs[k]; ok {
		j.State = core.StateClosed
		m.jobs[k] = j
	}
	return nil
}

// SaveAndEnqueue implements Repo (atomic here by virtue of the mutex).
func (m *Memory) SaveAndEnqueue(_ context.Context, job core.Job, n core.Notification) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.jobs[key(job.SourceID, job.ExternalID)] = job

	idem := n.JobSourceID + "\x00" + n.JobExternalID + "\x00" + n.EventType
	if m.seen[idem] {
		return nil // idempotent
	}
	m.seen[idem] = true
	m.nextID++
	n.ID = m.nextID
	n.Status = "pending"
	n.NextAttempt = time.Now()
	m.outbox[n.ID] = &n
	return nil
}

// ClaimNotifications implements Outbox.
func (m *Memory) ClaimNotifications(_ context.Context, limit int, timeout time.Duration) ([]core.Notification, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	var out []core.Notification
	for _, n := range m.outbox {
		if len(out) >= limit {
			break
		}
		if (n.Status == "pending" || n.Status == "sending") && !n.NextAttempt.After(now) {
			n.Status = "sending"
			n.Attempts++
			n.NextAttempt = now.Add(timeout)
			out = append(out, *n)
		}
	}
	return out, nil
}

// MarkSent implements Outbox.
func (m *Memory) MarkSent(_ context.Context, id int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if n, ok := m.outbox[id]; ok {
		n.Status = "sent"
	}
	return nil
}

// RescheduleNotification implements Outbox.
func (m *Memory) RescheduleNotification(_ context.Context, id int64, next time.Time, errMsg string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if n, ok := m.outbox[id]; ok {
		n.Status = "pending"
		n.NextAttempt = next
	}
	return nil
}

// FailNotification implements Outbox.
func (m *Memory) FailNotification(_ context.Context, id int64, errMsg string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if n, ok := m.outbox[id]; ok {
		n.Status = "failed"
	}
	return nil
}
