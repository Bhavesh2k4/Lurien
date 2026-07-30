// Package uber implements a provider for Uber's public jobs API
// (uber.com/careers). Company-specific, like the amazon provider. The API is a
// POST that filters by country, so this narrows to India server-side.
package uber

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"lurien/internal/core"
	"lurien/internal/fetch"
	"lurien/internal/provider"
)

const (
	apiURL    = "https://www.uber.com/api/loadSearchJobsResults?localeCode=en"
	pageLimit = 100
	maxJobs   = 500
	browserUA = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120 Safari/537.36"
)

func init() { provider.Register(Provider{}) }

// Provider is the Uber adapter.
type Provider struct{}

// Kind implements provider.Provider.
func (Provider) Kind() string { return "uber" }

type apiResponse struct {
	Data struct {
		TotalResults struct {
			Low int `json:"low"`
		} `json:"totalResults"`
		Results []uberJob `json:"results"`
	} `json:"data"`
}

type uberJob struct {
	ID          int64    `json:"id"`
	Title       string   `json:"title"`
	Department  string   `json:"department"`
	Team        string   `json:"team"`
	Description string   `json:"description"`
	UpdatedDate string   `json:"updatedDate"`
	Level       string   `json:"level"`
	Location    uberLoc  `json:"location"`
}

type uberLoc struct {
	City        string `json:"city"`
	CountryName string `json:"countryName"`
}

// Fetch pages the India-filtered Uber postings.
func (Provider) Fetch(ctx context.Context, src core.Source, hc fetch.Client) ([]core.RawJob, error) {
	var out []core.RawJob
	total := -1
	for page := 0; total < 0 || len(out) < total; page++ {
		body := fmt.Sprintf(`{"limit":%d,"page":%d,"params":{"location":[{"country":"IND"}]}}`, pageLimit, page)
		resp, err := hc.Do(ctx, fetch.Request{
			URL:    apiURL,
			Method: "POST",
			Body:   []byte(body),
			Headers: map[string]string{
				"x-csrf-token": "x",
				"User-Agent":   browserUA,
			},
		})
		if err != nil {
			return out, fmt.Errorf("uber: fetch: %w", err)
		}
		var ar apiResponse
		if err := json.Unmarshal(resp.Body, &ar); err != nil {
			return out, fmt.Errorf("uber: decode: %w", err)
		}
		if total < 0 {
			total = min(ar.Data.TotalResults.Low, maxJobs)
		}
		if len(ar.Data.Results) == 0 {
			break
		}
		for _, j := range ar.Data.Results {
			out = append(out, mapJob(j))
		}
	}
	return out, nil
}

func mapJob(j uberJob) core.RawJob {
	loc := j.Location.City
	if j.Location.CountryName != "" {
		if loc != "" {
			loc += ", "
		}
		loc += j.Location.CountryName
	}
	var updated time.Time
	if t, err := time.Parse(time.RFC3339, j.UpdatedDate); err == nil {
		updated = t
	}
	return core.RawJob{
		ExternalID:  fmt.Sprintf("%d", j.ID),
		Title:       j.Title,
		LocationRaw: loc,
		URL:         fmt.Sprintf("https://www.uber.com/careers/list/%d/", j.ID),
		UpdatedAt:   updated,
		Departments: dedup(j.Department, j.Team),
		Offices:     dedup(j.Location.City, "India"),
		Content:     j.Description,
		Meta:        map[string]string{"level": j.Level},
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
