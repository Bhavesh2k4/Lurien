// Command lurienctl is Lurien's operator CLI.
//
//	lurienctl probe   <provider> <args...>   # does this board exist? how many jobs?
//	lurienctl dryrun  <provider> <args...>   # fetch + classify a board, print matches/review
//	lurienctl testnotify                      # send a sample alert via the configured channel
//
// <args> per provider:
//	greenhouse <board_token> | ashby <board_name> | lever <site>
//	workday <tenant> <host> <site>
//
// dryrun needs no database, so it is the fastest way to validate a provider or
// tune the classifier against a board.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"sort"
	"time"

	"lurien/internal/classify"
	"lurien/internal/config"
	"lurien/internal/core"
	"lurien/internal/fetch"
	"lurien/internal/notify"
	"lurien/internal/provider"
	_ "lurien/internal/provider/amazon"
	_ "lurien/internal/provider/ashby"
	_ "lurien/internal/provider/eightfold"
	_ "lurien/internal/provider/greenhouse"
	_ "lurien/internal/provider/lever"
	_ "lurien/internal/provider/uber"
	_ "lurien/internal/provider/workday"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: lurienctl <probe|dryrun|testnotify> [provider] [token]")
		os.Exit(2)
	}

	// Commands that don't need a board token.
	if os.Args[1] == "testnotify" {
		testNotify()
		return
	}

	if len(os.Args) < 4 {
		fmt.Fprintln(os.Stderr, "usage: lurienctl <probe|dryrun> <provider> <args...>   (greenhouse|ashby|lever <token>; workday <tenant> <host> <site>)")
		os.Exit(2)
	}
	cmd, prov := os.Args[1], os.Args[2]
	args := os.Args[3:]

	p, err := provider.Get(prov)
	if err != nil {
		die(err)
	}
	params, err := paramFor(prov, args)
	if err != nil {
		die(err)
	}
	src := core.Source{
		ID:       prov + ":" + args[0],
		Company:  core.Company{Slug: args[0], Name: args[0]},
		Provider: prov,
		Params:   params,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	hc := fetch.New(fetch.Options{})
	raws, err := p.Fetch(ctx, src, hc)
	if err != nil {
		die(err)
	}

	switch cmd {
	case "probe":
		fmt.Printf("%s/%s: %d jobs\n", prov, args[0], len(raws))
	case "dryrun":
		dryrun(args[0], raws)
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n", cmd)
		os.Exit(2)
	}
}

// paramFor maps a provider's CLI args to its source-config keys.
func paramFor(prov string, args []string) (map[string]string, error) {
	switch prov {
	case "greenhouse":
		return map[string]string{"board_token": args[0]}, nil
	case "ashby":
		return map[string]string{"board_name": args[0]}, nil
	case "lever":
		return map[string]string{"site": args[0]}, nil
	case "workday":
		if len(args) < 3 {
			return nil, fmt.Errorf("workday needs <tenant> <host> <site>")
		}
		return map[string]string{"tenant": args[0], "host": args[1], "site": args[2]}, nil
	case "eightfold":
		if len(args) < 2 {
			return nil, fmt.Errorf("eightfold needs <tenant> <domain>")
		}
		return map[string]string{"tenant": args[0], "domain": args[1]}, nil
	case "amazon":
		return map[string]string{"base_query": args[0]}, nil
	case "uber":
		return map[string]string{}, nil
	default:
		return map[string]string{"token": args[0]}, nil
	}
}

func dryrun(token string, raws []core.RawJob) {
	c := classify.Default()
	var matches, reviews []core.Job

	counts := map[core.Decision]int{}
	for _, raw := range raws {
		cl := c.Classify(raw, core.ClassifyHints{})
		counts[cl.Decision]++
		j := core.Job{Title: raw.Title, LocationRaw: raw.LocationRaw, URL: raw.URL,
			Departments: raw.Departments, Class: cl}
		switch cl.Decision {
		case core.DecisionMatch:
			matches = append(matches, j)
		case core.DecisionReview:
			reviews = append(reviews, j)
		}
	}

	fmt.Printf("\n=== %s ===\n", token)
	fmt.Printf("fetched=%d  match=%d  review=%d  reject=%d\n\n",
		len(raws), counts[core.DecisionMatch], counts[core.DecisionReview], counts[core.DecisionReject])

	print := func(label string, js []core.Job) {
		fmt.Printf("--- %s (%d) ---\n", label, len(js))
		sort.Slice(js, func(i, k int) bool { return js[i].Title < js[k].Title })
		for _, j := range js {
			fmt.Printf("  • %s\n      %s\n      %v\n", j.Title, j.LocationRaw, j.Class.Reasons)
		}
		if len(js) == 0 {
			fmt.Println("  (none)")
		}
		fmt.Println()
	}
	print("MATCH (early-career tech role in India)", matches)
	print("REVIEW (ambiguous, quarantined)", reviews)
}

// testNotify sends one sample notification through the configured channel, to
// verify channel setup (token/chat/url) without waiting for a real match.
func testNotify() {
	_ = config.LoadDotEnv(".env")
	kind := os.Getenv("LURIEN_NOTIFY")
	if kind == "" {
		kind = "log"
	}
	ch, err := buildChannel(kind)
	if err != nil {
		die(err)
	}
	n := core.Notification{
		EventType: "test",
		Company:   "Lurien",
		Title:     "Test notification — early-career SWE",
		Location:  "Bengaluru, India",
		URL:       "https://github.com/lurien",
		Reasons:   []string{"tech:title=software engineer", "early:title=new grad", "india:loc=bengaluru"},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := ch.Send(ctx, n); err != nil {
		die(fmt.Errorf("send via %s: %w", ch.Name(), err))
	}
	fmt.Printf("sent test notification via %q\n", ch.Name())
}

func buildChannel(kind string) (notify.Channel, error) {
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
		return nil, fmt.Errorf("unknown channel %q", kind)
	}
}

func die(err error) {
	fmt.Fprintln(os.Stderr, "error:", err)
	os.Exit(1)
}
