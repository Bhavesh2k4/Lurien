// Package amazon implements a provider for Amazon's public jobs API
// (amazon.jobs). It is company-specific (not a shared ATS): Amazon runs its own
// career site. The API filters by country server-side, so this narrows to India
// and paginates that set. Amazon is the largest early-career SDE hirer in India.
package amazon

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"

	"lurien/internal/core"
	"lurien/internal/fetch"
	"lurien/internal/provider"
)

const (
	apiURL      = "https://www.amazon.jobs/en/search.json?base_query=%s&country=IND&result_limit=%d&offset=%d&sort=recent"
	defaultQ    = "software engineer"
	pageLimit   = 100
	maxJobs     = 600
	postedLayout = "January 2, 2006"
)

func init() { provider.Register(Provider{}) }

// Provider is the Amazon adapter.
type Provider struct{}

// Kind implements provider.Provider.
func (Provider) Kind() string { return "amazon" }

type apiResponse struct {
	Hits int      `json:"hits"`
	Jobs []amzJob `json:"jobs"`
}

type amzJob struct {
	IDIcims            string `json:"id_icims"`
	Title              string `json:"title"`
	NormalizedLocation string `json:"normalized_location"`
	Location           string `json:"location"`
	JobPath            string `json:"job_path"`
	PostedDate         string `json:"posted_date"`
	JobCategory        string `json:"job_category"`
	BasicQualifs       string `json:"basic_qualifications"`
	DescriptionShort   string `json:"description_short"`
	IsManager          bool   `json:"is_manager"`
}

// Fetch pages the India-filtered Amazon postings for the configured query.
func (Provider) Fetch(ctx context.Context, src core.Source, hc fetch.Client) ([]core.RawJob, error) {
	q := src.Params["base_query"]
	if q == "" {
		q = defaultQ
	}
	esc := url.QueryEscape(q)

	var out []core.RawJob
	total := -1
	for offset := 0; total < 0 || offset < total; {
		u := fmt.Sprintf(apiURL, esc, pageLimit, offset)
		resp, err := hc.Do(ctx, fetch.Request{URL: u})
		if err != nil {
			return out, fmt.Errorf("amazon: fetch: %w", err)
		}
		var ar apiResponse
		if err := json.Unmarshal(resp.Body, &ar); err != nil {
			return out, fmt.Errorf("amazon: decode: %w", err)
		}
		if total < 0 {
			total = min(ar.Hits, maxJobs)
		}
		if len(ar.Jobs) == 0 {
			break
		}
		for _, j := range ar.Jobs {
			if j.IsManager {
				continue
			}
			out = append(out, mapJob(j))
		}
		offset += len(ar.Jobs)
	}
	return out, nil
}

func mapJob(j amzJob) core.RawJob {
	loc := j.NormalizedLocation
	if loc == "" {
		loc = j.Location
	}
	var posted time.Time
	if t, err := time.Parse(postedLayout, j.PostedDate); err == nil {
		posted = t
	}
	return core.RawJob{
		ExternalID:  j.IDIcims,
		Title:       j.Title,
		LocationRaw: loc,
		URL:         "https://www.amazon.jobs" + j.JobPath,
		UpdatedAt:   posted,
		FirstSeen:   posted,
		Departments: dedup(j.JobCategory),
		Offices:     []string{"India"}, // country=IND filtered server-side
		Content:     strings.TrimSpace(j.BasicQualifs + " " + j.DescriptionShort),
		Meta:        map[string]string{"category": j.JobCategory},
		Raw:         mustRaw(j),
	}
}

func dedup(vals ...string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(vals))
	for _, v := range vals {
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	return out
}

func mustRaw(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	return b
}
