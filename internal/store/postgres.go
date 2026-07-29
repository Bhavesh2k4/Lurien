package store

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"lurien/internal/core"
	"lurien/internal/store/db"
)

// Postgres is the durable Repo, backed by pgx + sqlc-generated queries. It
// implements the exact same interface as Memory, so the engine is unchanged.
type Postgres struct {
	pool *pgxpool.Pool
	q    *db.Queries
}

// NewPostgres connects to dsn and verifies the connection.
func NewPostgres(ctx context.Context, dsn string) (*Postgres, error) {
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("pgxpool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping: %w", err)
	}
	return &Postgres{pool: pool, q: db.New(pool)}, nil
}

// Shutdown closes the pool. (Named Shutdown, not Close, to avoid clashing with
// the Repo.Close method.)
func (p *Postgres) Shutdown() { p.pool.Close() }

// ActiveBySource implements Repo.
func (p *Postgres) ActiveBySource(ctx context.Context, sourceID string) (map[string]core.Job, error) {
	rows, err := p.q.ActiveBySource(ctx, sourceID)
	if err != nil {
		return nil, err
	}
	out := make(map[string]core.Job, len(rows))
	for _, r := range rows {
		out[r.ExternalID] = fromDB(r)
	}
	return out, nil
}

// Save implements Repo.
func (p *Postgres) Save(ctx context.Context, job core.Job) error {
	return p.q.UpsertJob(ctx, toParams(job))
}

// Close implements Repo (marks a job closed; not a connection close).
func (p *Postgres) Close(ctx context.Context, sourceID, externalID string) error {
	return p.q.CloseJob(ctx, db.CloseJobParams{SourceID: sourceID, ExternalID: externalID})
}

// ListOpenJobs returns non-closed jobs (newest first) for the dashboard.
// decision "" means all; otherwise filter to match/review/reject.
func (p *Postgres) ListOpenJobs(ctx context.Context, decision string, limit int) ([]core.Job, error) {
	arg := db.ListOpenJobsParams{Limit: int32(limit)}
	if decision != "" {
		arg.Decision = pgtype.Text{String: decision, Valid: true}
	}
	rows, err := p.q.ListOpenJobs(ctx, arg)
	if err != nil {
		return nil, err
	}
	out := make([]core.Job, 0, len(rows))
	for _, r := range rows {
		out = append(out, fromDB(r))
	}
	return out, nil
}

// SaveAndEnqueue upserts the job and enqueues the notification in one
// transaction — the heart of the transactional outbox.
func (p *Postgres) SaveAndEnqueue(ctx context.Context, job core.Job, n core.Notification) error {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) // no-op after Commit
	qtx := p.q.WithTx(tx)

	if err := qtx.UpsertJob(ctx, toParams(job)); err != nil {
		return err
	}
	if err := qtx.EnqueueNotification(ctx, db.EnqueueNotificationParams{
		JobSourceID:   n.JobSourceID,
		JobExternalID: n.JobExternalID,
		EventType:     n.EventType,
		Company:       n.Company,
		Title:         n.Title,
		Location:      n.Location,
		Url:           n.URL,
		Reasons:       nonNilSlice(n.Reasons),
	}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// ClaimNotifications implements Outbox.
func (p *Postgres) ClaimNotifications(ctx context.Context, limit int, timeout time.Duration) ([]core.Notification, error) {
	rows, err := p.q.ClaimNotifications(ctx, db.ClaimNotificationsParams{
		Limit:       int32(limit),
		NextAttempt: ts(time.Now().Add(timeout)),
	})
	if err != nil {
		return nil, err
	}
	out := make([]core.Notification, 0, len(rows))
	for _, r := range rows {
		out = append(out, core.Notification{
			ID:            r.ID,
			JobSourceID:   r.JobSourceID,
			JobExternalID: r.JobExternalID,
			EventType:     r.EventType,
			Company:       r.Company,
			Title:         r.Title,
			Location:      r.Location,
			URL:           r.Url,
			Reasons:       r.Reasons,
			Attempts:      int(r.Attempts),
			Status:        r.Status,
		})
	}
	return out, nil
}

// MarkSent implements Outbox.
func (p *Postgres) MarkSent(ctx context.Context, id int64) error {
	return p.q.MarkSent(ctx, id)
}

// RescheduleNotification implements Outbox.
func (p *Postgres) RescheduleNotification(ctx context.Context, id int64, next time.Time, errMsg string) error {
	return p.q.RescheduleNotification(ctx, db.RescheduleNotificationParams{
		ID:          id,
		NextAttempt: ts(next),
		LastError:   errMsg,
	})
}

// FailNotification implements Outbox.
func (p *Postgres) FailNotification(ctx context.Context, id int64, errMsg string) error {
	return p.q.FailNotification(ctx, db.FailNotificationParams{ID: id, LastError: errMsg})
}

func toParams(j core.Job) db.UpsertJobParams {
	return db.UpsertJobParams{
		SourceID:    j.SourceID,
		ExternalID:  j.ExternalID,
		Title:       j.Title,
		LocationRaw: j.LocationRaw,
		Country:     j.Country,
		Url:         j.URL,
		Departments: nonNilSlice(j.Departments),
		UpdatedAt:   ts(j.UpdatedAt),
		ContentHash: j.ContentHash,
		State:       string(j.State),
		Decision:    string(j.Class.Decision),
		Function:    j.Class.Function,
		Seniority:   j.Class.Seniority,
		Confidence:  j.Class.Confidence,
		Reasons:     nonNilSlice(j.Class.Reasons),
		FirstSeen:   ts(j.FirstSeen),
		LastSeen:    ts(j.LastSeen),
		ClosedAt:    tsNull(j.ClosedAt),
	}
}

func fromDB(r db.Job) core.Job {
	return core.Job{
		SourceID:    r.SourceID,
		ExternalID:  r.ExternalID,
		Title:       r.Title,
		LocationRaw: r.LocationRaw,
		Country:     r.Country,
		URL:         r.Url,
		Departments: r.Departments,
		UpdatedAt:   r.UpdatedAt.Time,
		ContentHash: r.ContentHash,
		State:       core.JobState(r.State),
		Class: core.Classification{
			Decision:   core.Decision(r.Decision),
			Function:   r.Function,
			Seniority:  r.Seniority,
			Country:    r.Country,
			Confidence: r.Confidence,
			Reasons:    r.Reasons,
		},
		FirstSeen: r.FirstSeen.Time,
		LastSeen:  r.LastSeen.Time,
		ClosedAt:  r.ClosedAt.Time,
	}
}

// ts builds a non-null timestamptz; NOT NULL columns must never receive NULL,
// so a zero time is replaced with now().
func ts(t time.Time) pgtype.Timestamptz {
	if t.IsZero() {
		t = time.Now()
	}
	return pgtype.Timestamptz{Time: t, Valid: true}
}

// tsNull builds a nullable timestamptz (used for closed_at).
func tsNull(t time.Time) pgtype.Timestamptz {
	if t.IsZero() {
		return pgtype.Timestamptz{Valid: false}
	}
	return pgtype.Timestamptz{Time: t, Valid: true}
}

func nonNilSlice(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
