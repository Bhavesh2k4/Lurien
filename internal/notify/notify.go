// Package notify delivers queued notifications from the transactional outbox.
//
// A Dispatcher leases due notifications, sends each through a Channel, and marks
// the outcome (sent / rescheduled with backoff / failed). Channels consume a
// core.Notification and never learn which provider produced the job — delivery
// is origin-agnostic. Adding a channel (Telegram, Slack, webhook…) touches
// nothing else.
package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"lurien/internal/core"
)

// Outbox is the port the dispatcher needs (implemented by store.Memory and
// store.Postgres). Defined here so notify doesn't depend on the store package.
type Outbox interface {
	ClaimNotifications(ctx context.Context, limit int, timeout time.Duration) ([]core.Notification, error)
	MarkSent(ctx context.Context, id int64) error
	RescheduleNotification(ctx context.Context, id int64, next time.Time, errMsg string) error
	FailNotification(ctx context.Context, id int64, errMsg string) error
}

// Channel delivers a single notification to a destination.
type Channel interface {
	Name() string
	Send(ctx context.Context, n core.Notification) error
}

// Dispatcher drains the outbox on an interval.
type Dispatcher struct {
	ob          Outbox
	ch          Channel
	log         *slog.Logger
	interval    time.Duration
	batch       int
	maxAttempts int
	visibility  time.Duration
}

// NewDispatcher builds a Dispatcher with sensible defaults.
func NewDispatcher(ob Outbox, ch Channel, log *slog.Logger) *Dispatcher {
	if log == nil {
		log = slog.Default()
	}
	return &Dispatcher{
		ob:          ob,
		ch:          ch,
		log:         log,
		interval:    5 * time.Second,
		batch:       20,
		maxAttempts: 6,
		visibility:  2 * time.Minute,
	}
}

// Run drains the outbox until ctx is cancelled.
func (d *Dispatcher) Run(ctx context.Context) {
	d.log.Info("dispatcher started", "channel", d.ch.Name())
	t := time.NewTicker(d.interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			d.tick(ctx)
		}
	}
}

// Drain processes all currently-due notifications and returns. Used by --once
// mode (cron). Stops when a tick claims nothing (rescheduled retries have a
// future next_attempt, so they aren't re-claimed here).
func (d *Dispatcher) Drain(ctx context.Context) {
	for ctx.Err() == nil {
		if d.tick(ctx) == 0 {
			return
		}
	}
}

func (d *Dispatcher) tick(ctx context.Context) int {
	ns, err := d.ob.ClaimNotifications(ctx, d.batch, d.visibility)
	if err != nil {
		d.log.Error("claim failed", "err", err)
		return 0
	}
	for _, n := range ns {
		if err := d.ch.Send(ctx, n); err != nil {
			if n.Attempts >= d.maxAttempts {
				d.log.Error("notification dead-lettered", "id", n.ID, "attempts", n.Attempts, "err", err)
				_ = d.ob.FailNotification(ctx, n.ID, err.Error())
				continue
			}
			next := time.Now().Add(backoff(n.Attempts))
			d.log.Warn("notification retry", "id", n.ID, "attempts", n.Attempts, "next", next, "err", err)
			_ = d.ob.RescheduleNotification(ctx, n.ID, next, err.Error())
			continue
		}
		_ = d.ob.MarkSent(ctx, n.ID)
		d.log.Info("notification sent", "channel", d.ch.Name(), "company", n.Company, "title", n.Title, "url", n.URL)
	}
	return len(ns)
}

// backoff grows exponentially from 10s, capped at 30m.
func backoff(attempt int) time.Duration {
	d := 10 * time.Second * time.Duration(1<<min(attempt, 8))
	if d > 30*time.Minute {
		d = 30 * time.Minute
	}
	return d
}

// ---- Channels ----

// LogChannel prints notifications; the default, and useful in dev/tests.
type LogChannel struct{ Logger *slog.Logger }

// Name implements Channel.
func (LogChannel) Name() string { return "log" }

// Send implements Channel.
func (c LogChannel) Send(_ context.Context, n core.Notification) error {
	log := c.Logger
	if log == nil {
		log = slog.Default()
	}
	log.Info("MATCH",
		"company", n.Company, "title", n.Title, "location", n.Location,
		"url", n.URL, "reasons", strings.Join(n.Reasons, " "))
	return nil
}

// WebhookChannel POSTs the notification as JSON to a URL.
type WebhookChannel struct {
	URL    string
	Client *http.Client
}

// Name implements Channel.
func (WebhookChannel) Name() string { return "webhook" }

// Send implements Channel.
func (c WebhookChannel) Send(ctx context.Context, n core.Notification) error {
	body, _ := json.Marshal(n)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.URL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.client().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("webhook http %d", resp.StatusCode)
	}
	return nil
}

func (c WebhookChannel) client() *http.Client {
	if c.Client != nil {
		return c.Client
	}
	return &http.Client{Timeout: 15 * time.Second}
}

// TelegramChannel sends a formatted message via the Telegram Bot API.
type TelegramChannel struct {
	Token  string
	ChatID string
	Client *http.Client
}

// Name implements Channel.
func (TelegramChannel) Name() string { return "telegram" }

// Send implements Channel.
func (c TelegramChannel) Send(ctx context.Context, n core.Notification) error {
	text := fmt.Sprintf("🎯 New early-career role\n%s — %s\n📍 %s\n%s",
		n.Company, n.Title, n.Location, n.URL)
	payload, _ := json.Marshal(map[string]any{
		"chat_id":                  c.ChatID,
		"text":                     text,
		"disable_web_page_preview": false,
	})
	url := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", c.Token)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	cl := c.Client
	if cl == nil {
		cl = &http.Client{Timeout: 15 * time.Second}
	}
	resp, err := cl.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("telegram http %d", resp.StatusCode)
	}
	return nil
}
