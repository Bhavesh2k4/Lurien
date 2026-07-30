// Package ashby implements the Ashby job-board provider.
//
// One board name = one source. A single request returns every posting; this
// provider maps them all and filters nothing — classification happens
// downstream. Mirrors the Greenhouse adapter; only the JSON shape differs.
package ashby

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"lurien/internal/core"
	"lurien/internal/fetch"
	"lurien/internal/provider"
)

const boardURL = "https://api.ashbyhq.com/posting-api/job-board/%s?includeCompensation=false"

func init() { provider.Register(Provider{}) }

// Provider is the Ashby adapter.
type Provider struct{}

// Kind implements provider.Provider.
func (Provider) Kind() string { return "ashby" }

type apiResponse struct {
	Jobs []apiJob `json:"jobs"`
}

type apiJob struct {
	ID                 string    `json:"id"`
	Title              string    `json:"title"`
	Location           string    `json:"location"`
	SecondaryLocations []secLoc  `json:"secondaryLocations"`
	Department         string    `json:"department"`
	Team               string    `json:"team"`
	EmploymentType     string    `json:"employmentType"`
	IsRemote           bool      `json:"isRemote"`
	PublishedAt        time.Time `json:"publishedAt"`
	JobURL             string    `json:"jobUrl"`
	ApplyURL           string    `json:"applyUrl"`
	DescriptionHTML    string    `json:"descriptionHtml"`
}

type secLoc struct {
	Location string `json:"location"`
}

// Fetch pulls the board and maps every posting to a core.RawJob.
func (Provider) Fetch(ctx context.Context, src core.Source, hc fetch.Client) ([]core.RawJob, error) {
	name := src.Params["board_name"]
	if name == "" {
		return nil, fmt.Errorf("ashby: missing board_name for %s", src.ID)
	}

	resp, err := hc.Do(ctx, fetch.Request{URL: fmt.Sprintf(boardURL, name)})
	if err != nil {
		return nil, fmt.Errorf("ashby: fetch %s: %w", name, err)
	}
	if resp.NotModified {
		return nil, nil
	}

	var ar apiResponse
	if err := json.Unmarshal(resp.Body, &ar); err != nil {
		return nil, fmt.Errorf("ashby: decode %s: %w", name, err)
	}

	out := make([]core.RawJob, 0, len(ar.Jobs))
	for _, j := range ar.Jobs {
		url := j.JobURL
		if url == "" {
			url = j.ApplyURL
		}
		out = append(out, core.RawJob{
			ExternalID:  j.ID,
			Title:       j.Title,
			LocationRaw: j.Location,
			URL:         url,
			UpdatedAt:   j.PublishedAt,
			FirstSeen:   j.PublishedAt,
			Departments: dedup(j.Department, j.Team),
			Offices:     offices(j.SecondaryLocations),
			Content:     j.DescriptionHTML,
			Meta: map[string]string{
				"employmentType": j.EmploymentType,
				"isRemote":       strconv.FormatBool(j.IsRemote),
			},
			Raw: mustRaw(j),
		})
	}
	return out, nil
}

func offices(ls []secLoc) []string {
	out := make([]string, 0, len(ls))
	for _, l := range ls {
		if l.Location != "" {
			out = append(out, l.Location)
		}
	}
	return out
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
