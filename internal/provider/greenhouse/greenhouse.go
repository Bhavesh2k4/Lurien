// Package greenhouse implements the Greenhouse job-board provider.
//
// One board token = one source. A single request returns every posting on the
// board (all departments, all locations); this provider maps them all and
// filters nothing — classification happens downstream in the engine.
package greenhouse

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"lurien/internal/core"
	"lurien/internal/fetch"
	"lurien/internal/provider"
)

const boardURL = "https://boards-api.greenhouse.io/v1/boards/%s/jobs?content=true"

func init() { provider.Register(Provider{}) }

// Provider is the Greenhouse adapter.
type Provider struct{}

// Kind implements provider.Provider.
func (Provider) Kind() string { return "greenhouse" }

// apiResponse mirrors the fields Lurien consumes from the board API.
type apiResponse struct {
	Jobs []apiJob `json:"jobs"`
}

type apiJob struct {
	ID             int64      `json:"id"`
	Title          string     `json:"title"`
	UpdatedAt      time.Time  `json:"updated_at"`
	FirstPublished time.Time  `json:"first_published"`
	AbsoluteURL    string     `json:"absolute_url"`
	Content        string     `json:"content"`
	Location       named      `json:"location"`
	Departments    []named    `json:"departments"`
	Offices        []named    `json:"offices"`
	Metadata       []metadata `json:"metadata"`
}

type named struct {
	Name string `json:"name"`
}

type metadata struct {
	Name  string          `json:"name"`
	Value json.RawMessage `json:"value"`
}

// Fetch pulls the board and maps every posting to a core.RawJob.
func (Provider) Fetch(ctx context.Context, src core.Source, hc fetch.Client) ([]core.RawJob, error) {
	token := src.Params["board_token"]
	if token == "" {
		return nil, fmt.Errorf("greenhouse: missing board_token for %s", src.ID)
	}

	resp, err := hc.Do(ctx, fetch.Request{URL: fmt.Sprintf(boardURL, token)})
	if err != nil {
		return nil, fmt.Errorf("greenhouse: fetch %s: %w", token, err)
	}
	if resp.NotModified {
		return nil, nil // board unchanged since last ETag
	}

	var ar apiResponse
	if err := json.Unmarshal(resp.Body, &ar); err != nil {
		return nil, fmt.Errorf("greenhouse: decode %s: %w", token, err)
	}

	out := make([]core.RawJob, 0, len(ar.Jobs))
	for _, j := range ar.Jobs {
		out = append(out, core.RawJob{
			ExternalID:  fmt.Sprintf("%d", j.ID),
			Title:       j.Title,
			LocationRaw: j.Location.Name,
			URL:         j.AbsoluteURL,
			UpdatedAt:   j.UpdatedAt,
			FirstSeen:   j.FirstPublished,
			Departments: names(j.Departments),
			Offices:     names(j.Offices),
			Content:     j.Content,
			Meta:        meta(j.Metadata),
			Raw:         mustRaw(j),
		})
	}
	return out, nil
}

func names(ns []named) []string {
	out := make([]string, 0, len(ns))
	for _, n := range ns {
		if n.Name != "" {
			out = append(out, n.Name)
		}
	}
	return out
}

func meta(ms []metadata) map[string]string {
	if len(ms) == 0 {
		return nil
	}
	out := make(map[string]string, len(ms))
	for _, m := range ms {
		var s string
		if err := json.Unmarshal(m.Value, &s); err == nil {
			out[m.Name] = s
		} else {
			out[m.Name] = string(m.Value)
		}
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
