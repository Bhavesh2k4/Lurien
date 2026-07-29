// Package engine orchestrates a single poll cycle for one source and owns the
// job lifecycle state machine. It depends only on interfaces (provider,
// classifier, store) and never names a concrete provider or channel. On a new
// match it enqueues a notification into the outbox (via the store) rather than
// delivering directly — delivery is a separate dispatcher's job.
package engine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"time"

	"lurien/internal/core"
	"lurien/internal/fetch"
	"lurien/internal/provider"
	"lurien/internal/store"
)

// Classifier is the port the engine needs from package classify.
type Classifier interface {
	Classify(core.RawJob, core.ClassifyHints) core.Classification
}

// Engine runs poll cycles.
type Engine struct {
	hc      fetch.Client
	classic Classifier
	repo    store.Repo
	log     *slog.Logger
	now     func() time.Time
}

// New builds an Engine.
func New(hc fetch.Client, c Classifier, repo store.Repo, log *slog.Logger) *Engine {
	if log == nil {
		log = slog.Default()
	}
	return &Engine{hc: hc, classic: c, repo: repo, log: log, now: time.Now}
}

// Stats summarizes a run.
type Stats struct {
	Fetched    int
	Matched    int
	Review     int
	Rejected   int
	New        int
	Updated    int
	Closed     int
}

// Run executes one poll cycle for src.
func (e *Engine) Run(ctx context.Context, src core.Source) (Stats, error) {
	var st Stats

	p, err := provider.Get(src.Provider)
	if err != nil {
		return st, err
	}
	raws, err := p.Fetch(ctx, src, e.hc)
	if err != nil {
		return st, err
	}
	st.Fetched = len(raws)

	active, err := e.repo.ActiveBySource(ctx, src.ID)
	if err != nil {
		return st, err
	}

	seen := make(map[string]bool, len(raws))
	for _, raw := range raws {
		seen[raw.ExternalID] = true
		class := e.classic.Classify(raw, src.Hints)

		switch class.Decision {
		case core.DecisionMatch:
			st.Matched++
		case core.DecisionReview:
			st.Review++
		default:
			st.Rejected++
			continue // rejects are not persisted in M1 (Postgres will store all)
		}

		if err := e.upsert(ctx, src, raw, class, active, &st); err != nil {
			e.log.Error("upsert failed", "source", src.ID, "job", raw.ExternalID, "err", err)
		}
	}

	// Close anything previously active that vanished from the feed.
	for extID, j := range active {
		if seen[extID] {
			continue
		}
		if err := e.repo.Close(ctx, src.ID, extID); err != nil {
			e.log.Error("close failed", "source", src.ID, "job", extID, "err", err)
			continue
		}
		_ = j
		st.Closed++
	}

	return st, nil
}

// upsert applies the job state machine and fires notifications on new matches.
func (e *Engine) upsert(ctx context.Context, src core.Source, raw core.RawJob, class core.Classification, active map[string]core.Job, st *Stats) error {
	now := e.now()
	hash := contentHash(raw)

	job := core.Job{
		SourceID:    src.ID,
		ExternalID:  raw.ExternalID,
		Title:       raw.Title,
		LocationRaw: raw.LocationRaw,
		Country:     class.Country,
		URL:         raw.URL,
		Departments: raw.Departments,
		UpdatedAt:   raw.UpdatedAt,
		ContentHash: hash,
		Class:       class,
		LastSeen:    now,
	}

	prev, existed := active[raw.ExternalID]
	switch {
	case !existed:
		job.State = core.StateDiscovered
		job.FirstSeen = now
		st.New++
		// New match: persist the job AND enqueue its notification atomically.
		if class.Decision == core.DecisionMatch {
			return e.repo.SaveAndEnqueue(ctx, job, notificationFor(src, job))
		}
		return e.repo.Save(ctx, job)
	case prev.ContentHash != hash:
		job.State = core.StateUpdated
		job.FirstSeen = prev.FirstSeen
		st.Updated++
		return e.repo.Save(ctx, job)
	default:
		job.State = core.StateActive
		job.FirstSeen = prev.FirstSeen
		return e.repo.Save(ctx, job)
	}
}

// notificationFor builds the outbox row for a newly matched job.
func notificationFor(src core.Source, job core.Job) core.Notification {
	company := src.Company.Name
	if company == "" {
		company = src.ID
	}
	return core.Notification{
		JobSourceID:   job.SourceID,
		JobExternalID: job.ExternalID,
		EventType:     "new_match",
		Company:       company,
		Title:         job.Title,
		Location:      job.LocationRaw,
		URL:           job.URL,
		Reasons:       job.Class.Reasons,
	}
}

func contentHash(raw core.RawJob) string {
	h := sha256.New()
	fmt.Fprintf(h, "%s\x00%s\x00%s\x00%s", raw.Title, raw.LocationRaw, raw.URL, raw.Content)
	return hex.EncodeToString(h.Sum(nil))
}
