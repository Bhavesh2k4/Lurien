// Package scheduler decides when each source runs. It is provider-agnostic: it
// dispatches sources to a bounded worker pool and never knows how a source is
// fetched. Today it uses per-source tickers with jitter; the same RunFunc seam
// later swaps in Postgres SKIP LOCKED leasing for distributed operation.
package scheduler

import (
	"context"
	"log/slog"
	"math/rand"
	"sync"
	"time"

	"lurien/internal/core"
)

// RunFunc executes one poll cycle for a source.
type RunFunc func(ctx context.Context, src core.Source)

// Scheduler runs enabled sources on their intervals.
type Scheduler struct {
	sources     []core.Source
	run         RunFunc
	concurrency int
	log         *slog.Logger
}

// New builds a Scheduler. concurrency bounds simultaneous in-flight runs.
func New(sources []core.Source, run RunFunc, concurrency int, log *slog.Logger) *Scheduler {
	if concurrency <= 0 {
		concurrency = 4
	}
	if log == nil {
		log = slog.Default()
	}
	return &Scheduler{sources: sources, run: run, concurrency: concurrency, log: log}
}

// Run blocks until ctx is cancelled, polling each enabled source on its
// interval. A shared semaphore caps global concurrency; jitter avoids a
// thundering herd against a shared host (e.g. all Greenhouse boards).
func (s *Scheduler) Run(ctx context.Context) {
	sem := make(chan struct{}, s.concurrency)
	var wg sync.WaitGroup

	for _, src := range s.sources {
		if !src.Enabled {
			continue
		}
		wg.Add(1)
		go func(src core.Source) {
			defer wg.Done()
			s.loop(ctx, src, sem)
		}(src)
	}
	wg.Wait()
}

func (s *Scheduler) loop(ctx context.Context, src core.Source, sem chan struct{}) {
	// Initial spread so sources don't all fire at t=0.
	if !sleep(ctx, jitter(src.Interval)) {
		return
	}
	for {
		s.dispatch(ctx, src, sem)
		if !sleep(ctx, src.Interval+jitter(src.Interval)) {
			return
		}
	}
}

func (s *Scheduler) dispatch(ctx context.Context, src core.Source, sem chan struct{}) {
	select {
	case sem <- struct{}{}:
	case <-ctx.Done():
		return
	}
	defer func() { <-sem }()
	s.run(ctx, src)
}

// jitter returns a random offset up to 15% of d.
func jitter(d time.Duration) time.Duration {
	if d <= 0 {
		return 0
	}
	return time.Duration(rand.Int63n(int64(d)/6 + 1))
}

// sleep waits d or returns false if ctx is cancelled first.
func sleep(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return true
	case <-ctx.Done():
		return false
	}
}
