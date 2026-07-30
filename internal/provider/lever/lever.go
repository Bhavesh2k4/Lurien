// Package lever implements the Lever job-board provider.
//
// One site name = one source. The v0 postings API returns a JSON array of all
// postings; this provider maps them all and filters nothing. Mirrors the
// Greenhouse/Ashby adapters; only the JSON shape differs.
package lever

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"lurien/internal/core"
	"lurien/internal/fetch"
	"lurien/internal/provider"
)

const boardURL = "https://api.lever.co/v0/postings/%s?mode=json"

func init() { provider.Register(Provider{}) }

// Provider is the Lever adapter.
type Provider struct{}

// Kind implements provider.Provider.
func (Provider) Kind() string { return "lever" }

type apiJob struct {
	ID         string     `json:"id"`
	Text       string     `json:"text"` // title
	Categories categories `json:"categories"`
	Country    string     `json:"country"`
	CreatedAt  int64      `json:"createdAt"` // epoch millis
	HostedURL  string     `json:"hostedUrl"`
	ApplyURL   string     `json:"applyUrl"`
	DescPlain  string     `json:"descriptionPlain"`
	Workplace  string     `json:"workplaceType"`
}

type categories struct {
	Commitment   string   `json:"commitment"`
	Department   string   `json:"department"`
	Location     string   `json:"location"`
	Team         string   `json:"team"`
	AllLocations []string `json:"allLocations"`
}

// Fetch pulls the site's postings and maps each to a core.RawJob.
func (Provider) Fetch(ctx context.Context, src core.Source, hc fetch.Client) ([]core.RawJob, error) {
	site := src.Params["site"]
	if site == "" {
		return nil, fmt.Errorf("lever: missing site for %s", src.ID)
	}

	resp, err := hc.Do(ctx, fetch.Request{URL: fmt.Sprintf(boardURL, site)})
	if err != nil {
		return nil, fmt.Errorf("lever: fetch %s: %w", site, err)
	}
	if resp.NotModified {
		return nil, nil
	}

	var jobs []apiJob
	if err := json.Unmarshal(resp.Body, &jobs); err != nil {
		return nil, fmt.Errorf("lever: decode %s: %w", site, err)
	}

	out := make([]core.RawJob, 0, len(jobs))
	for _, j := range jobs {
		url := j.HostedURL
		if url == "" {
			url = j.ApplyURL
		}
		var created time.Time
		if j.CreatedAt > 0 {
			created = time.UnixMilli(j.CreatedAt)
		}
		offices := j.Categories.AllLocations
		if len(offices) == 0 && j.Categories.Location != "" {
			offices = []string{j.Categories.Location}
		}
		out = append(out, core.RawJob{
			ExternalID:  j.ID,
			Title:       j.Text,
			LocationRaw: j.Categories.Location,
			URL:         url,
			UpdatedAt:   created,
			FirstSeen:   created,
			Departments: dedup(j.Categories.Department, j.Categories.Team),
			Offices:     offices,
			Content:     j.DescPlain,
			Meta: map[string]string{
				"commitment":    j.Categories.Commitment,
				"workplaceType": j.Workplace,
				"country":       j.Country,
			},
			Raw: mustRaw(j),
		})
	}
	return out, nil
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
