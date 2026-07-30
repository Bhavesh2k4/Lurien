// Command lurien is the long-running daemon: it loads companies.yaml, then
// schedules poll cycles and runs a notification dispatcher.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"lurien/internal/classify"
	"lurien/internal/config"
	"lurien/internal/core"
	"lurien/internal/engine"
	"lurien/internal/fetch"
	"lurien/internal/notify"
	_ "lurien/internal/provider/amazon"     // register provider
	_ "lurien/internal/provider/ashby"      // register provider
	_ "lurien/internal/provider/eightfold"  // register provider
	_ "lurien/internal/provider/greenhouse" // register provider
	_ "lurien/internal/provider/lever"      // register provider
	_ "lurien/internal/provider/uber"       // register provider
	_ "lurien/internal/provider/workday"    // register provider
	"lurien/internal/scheduler"
	"lurien/internal/store"
)

func main() {
	// Load .env first so flag defaults that read env (e.g. LURIEN_DB) see it.
	_ = config.LoadDotEnv(".env")

	var (
		cfgPath     = flag.String("config", "configs/companies.yaml", "path to companies.yaml")
		concurrency = flag.Int("concurrency", 6, "max simultaneous poll cycles")
		storeKind   = flag.String("store", envOr("LURIEN_STORE", "memory"), "store backend: memory | postgres")
		dbDSN       = flag.String("db", os.Getenv("LURIEN_DB"), "Postgres DSN (or set LURIEN_DB)")
		channelKind = flag.String("notify", envOr("LURIEN_NOTIFY", "log"), "channel: log | telegram | webhook")
		once        = flag.Bool("once", false, "poll every source once, drain the outbox, then exit (for cron)")
	)
	flag.Parse()

	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(log)

	sources, err := config.Load(*cfgPath)
	if err != nil {
		log.Error("config", "err", err)
		os.Exit(1)
	}
	log.Info("loaded config", "sources", len(sources))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	repo, err := newRepo(ctx, *storeKind, *dbDSN)
	if err != nil {
		log.Error("store", "err", err)
		os.Exit(1)
	}
	log.Info("store ready", "backend", *storeKind)

	// The store is also the outbox the dispatcher drains.
	outbox, ok := repo.(notify.Outbox)
	if !ok {
		log.Error("store does not implement outbox")
		os.Exit(1)
	}
	channel, err := newChannel(*channelKind)
	if err != nil {
		log.Error("notify", "err", err)
		os.Exit(1)
	}
	dispatcher := notify.NewDispatcher(outbox, channel, log)

	hc := fetch.New(fetch.Options{})
	cls := classify.Default()
	eng := engine.New(hc, cls, repo, log)

	run := func(ctx context.Context, src core.Source) {
		st, err := eng.Run(ctx, src)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return // graceful shutdown cancelled in-flight work; not an error
			}
			log.Error("poll failed", "source", src.ID, "err", err)
			return
		}
		log.Info("poll",
			"source", src.ID, "fetched", st.Fetched,
			"match", st.Matched, "review", st.Review,
			"new", st.New, "updated", st.Updated, "closed", st.Closed)
	}

	if *once {
		// Cron/serverless mode: poll every source once, drain the outbox, exit.
		log.Info("lurien single pass", "sources", len(sources))
		pollOnce(ctx, sources, run, *concurrency)
		dispatcher.Drain(ctx)
		log.Info("lurien pass complete")
		return
	}

	sched := scheduler.New(sources, run, *concurrency, log)

	log.Info("lurien starting")
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); dispatcher.Run(ctx) }()
	go func() { defer wg.Done(); sched.Run(ctx) }()
	wg.Wait()
	log.Info("lurien stopped")
}

// pollOnce runs the engine over every enabled source exactly once, bounded by
// concurrency.
func pollOnce(ctx context.Context, sources []core.Source, run func(context.Context, core.Source), concurrency int) {
	if concurrency <= 0 {
		concurrency = 6
	}
	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup
	for _, src := range sources {
		if !src.Enabled {
			continue
		}
		wg.Add(1)
		go func(s core.Source) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			run(ctx, s)
		}(src)
	}
	wg.Wait()
}

// newRepo builds the selected store backend.
func newRepo(ctx context.Context, kind, dsn string) (store.Repo, error) {
	switch kind {
	case "memory":
		return store.NewMemory(), nil
	case "postgres":
		if dsn == "" {
			return nil, fmt.Errorf("postgres store requires -db or LURIEN_DB")
		}
		return store.NewPostgres(ctx, dsn)
	default:
		return nil, fmt.Errorf("unknown store %q (want memory|postgres)", kind)
	}
}

// newChannel builds the selected delivery channel from env config.
func newChannel(kind string) (notify.Channel, error) {
	switch kind {
	case "log":
		return notify.LogChannel{Logger: slog.Default()}, nil
	case "telegram":
		token, chat := os.Getenv("TELEGRAM_TOKEN"), os.Getenv("TELEGRAM_CHAT_ID")
		if token == "" || chat == "" {
			return nil, fmt.Errorf("telegram needs TELEGRAM_TOKEN and TELEGRAM_CHAT_ID")
		}
		return notify.TelegramChannel{Token: token, ChatID: chat}, nil
	case "webhook":
		url := os.Getenv("WEBHOOK_URL")
		if url == "" {
			return nil, fmt.Errorf("webhook needs WEBHOOK_URL")
		}
		return notify.WebhookChannel{URL: url}, nil
	default:
		return nil, fmt.Errorf("unknown channel %q (want log|telegram|webhook)", kind)
	}
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
