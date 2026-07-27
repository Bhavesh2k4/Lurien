// Package provider defines the plugin seam. A provider fetches postings from
// one kind of source and maps them to core.RawJob. It never filters, never
// classifies, and never touches storage. New source types register here.
package provider

import (
	"context"
	"fmt"
	"sort"
	"sync"

	"lurien/internal/core"
	"lurien/internal/fetch"
)

// Provider fetches and normalizes postings for a source. Cross-cutting HTTP
// concerns (retry, rate limit) live in the injected fetch.Client, so
// implementations stay tiny.
type Provider interface {
	Kind() string // "greenhouse"
	Fetch(ctx context.Context, src core.Source, hc fetch.Client) ([]core.RawJob, error)
}

var (
	mu       sync.RWMutex
	registry = map[string]Provider{}
)

// Register adds a provider. Called from a provider package's init().
func Register(p Provider) {
	mu.Lock()
	defer mu.Unlock()
	if _, dup := registry[p.Kind()]; dup {
		panic("provider already registered: " + p.Kind())
	}
	registry[p.Kind()] = p
}

// Get resolves a provider by kind.
func Get(kind string) (Provider, error) {
	mu.RLock()
	defer mu.RUnlock()
	p, ok := registry[kind]
	if !ok {
		return nil, fmt.Errorf("unknown provider %q", kind)
	}
	return p, nil
}

// Kinds lists registered providers (sorted), for diagnostics.
func Kinds() []string {
	mu.RLock()
	defer mu.RUnlock()
	out := make([]string, 0, len(registry))
	for k := range registry {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
