// Command lurien-web serves the dashboard locally (an always-on HTTP server).
// The same rendering runs serverless on Vercel via api/index.go; both use
// internal/dashboard.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"

	"lurien/internal/config"
	"lurien/internal/core"
	"lurien/internal/dashboard"
	"lurien/internal/store"
)

func main() {
	_ = config.LoadDotEnv(".env")
	var (
		addr    = flag.String("addr", ":8080", "listen address")
		cfgPath = flag.String("config", "configs/companies.yaml", "companies.yaml (for display names)")
		dbDSN   = flag.String("db", os.Getenv("LURIEN_DB"), "Postgres DSN (or LURIEN_DB)")
		limit   = flag.Int("limit", 2000, "max jobs to load")
	)
	flag.Parse()

	log := slog.New(slog.NewTextHandler(os.Stdout, nil))
	if *dbDSN == "" {
		log.Error("no db DSN; set -db or LURIEN_DB")
		os.Exit(1)
	}

	pg, err := store.NewPostgres(context.Background(), *dbDSN)
	if err != nil {
		log.Error("connect", "err", err)
		os.Exit(1)
	}
	defer pg.Shutdown()

	srv := dashboard.New(
		func(decision string, n int) ([]core.Job, error) {
			return pg.ListOpenJobs(context.Background(), decision, n)
		},
		companyNames(*cfgPath),
		*limit,
	)

	http.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, "ok") })
	http.HandleFunc("/", srv.Handler)

	log.Info("lurien-web listening", "addr", *addr, "open", "http://localhost"+*addr)
	if err := http.ListenAndServe(*addr, nil); err != nil {
		log.Error("serve", "err", err)
		os.Exit(1)
	}
}

func companyNames(path string) map[string]string {
	m := map[string]string{}
	sources, err := config.Load(path)
	if err != nil {
		return m
	}
	for _, s := range sources {
		m[s.ID] = s.Company.Name
	}
	return m
}
