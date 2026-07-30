// Package handler is the Vercel serverless entrypoint for the dashboard.
// Vercel builds every api/*.go that exports Handler into a function; a rewrite
// in vercel.json routes all requests here. It reads LURIEN_DB from the Vercel
// environment (the Neon pooled DSN) and renders the same page as cmd/lurien-web.
package handler

import (
	"context"
	"net/http"
	"os"
	"sync"

	"lurien/internal/core"
	"lurien/internal/dashboard"
	"lurien/internal/store"
)

var (
	srv     *dashboard.Server
	initErr error
	initOne sync.Once
)

func setup() {
	pg, err := store.NewPostgres(context.Background(), os.Getenv("LURIEN_DB"))
	if err != nil {
		initErr = err
		return
	}
	srv = dashboard.New(
		func(decision string, n int) ([]core.Job, error) {
			return pg.ListOpenJobs(context.Background(), decision, n)
		},
		nil, // serverless: no companies.yaml on disk; slug fallback for display names
		2000,
	)
}

// Handler is the Vercel function entrypoint.
func Handler(w http.ResponseWriter, r *http.Request) {
	initOne.Do(setup)
	if initErr != nil {
		http.Error(w, "db init: "+initErr.Error(), http.StatusInternalServerError)
		return
	}
	srv.Handler(w, r)
}
