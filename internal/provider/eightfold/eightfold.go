// Package eightfold implements the Eightfold.ai job-board provider.
//
// Eightfold powers many companies' *own* branded career sites (careers.X.com),
// exposing a public positions API at {tenant}.eightfold.ai. It supports a
// server-side location filter, so this provider narrows to India directly and
// paginates only that set.
package eightfold

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

const (
	apiURL  = "https://%s.eightfold.ai/api/apply/v2/jobs?domain=%s&location=India&start=%d&num=%d&sort_by=timestamp"
	pageNum = 100
	maxJobs = 500
)

func init() { provider.Register(Provider{}) }

// Provider is the Eightfold adapter.
type Provider struct{}

// Kind implements provider.Provider.
func (Provider) Kind() string { return "eightfold" }

type apiResponse struct {
	Count     int        `json:"count"`
	Positions []position `json:"positions"`
}

type position struct {
	ID             int64    `json:"id"`
	Name           string   `json:"name"`
	Location       string   `json:"location"`
	Locations      []string `json:"locations"`
	Department     string   `json:"department"`
	BusinessUnit   string   `json:"business_unit"`
	TCreate        int64    `json:"t_create"`
	TUpdate        int64    `json:"t_update"`
	CanonicalURL   string   `json:"canonicalPositionUrl"`
	JobDescription string   `json:"job_description"`
	DisplayJobID   string   `json:"display_job_id"`
}

// Fetch pages the India-filtered positions for a tenant.
func (Provider) Fetch(ctx context.Context, src core.Source, hc fetch.Client) ([]core.RawJob, error) {
	tenant, domain := src.Params["tenant"], src.Params["domain"]
	if tenant == "" || domain == "" {
		return nil, fmt.Errorf("eightfold: need tenant and domain for %s", src.ID)
	}

	var out []core.RawJob
	total := -1
	// Eightfold caps page size server-side (often 10), so advance by the number
	// actually returned rather than a fixed step.
	for start := 0; total < 0 || start < total; {
		url := fmt.Sprintf(apiURL, tenant, domain, start, pageNum)
		resp, err := hc.Do(ctx, fetch.Request{URL: url})
		if err != nil {
			return out, fmt.Errorf("eightfold: fetch %s: %w", tenant, err)
		}
		var ar apiResponse
		if err := json.Unmarshal(resp.Body, &ar); err != nil {
			return out, fmt.Errorf("eightfold: decode %s: %w", tenant, err)
		}
		if total < 0 {
			total = min(ar.Count, maxJobs)
		}
		if len(ar.Positions) == 0 {
			break
		}
		for _, p := range ar.Positions {
			out = append(out, mapPos(p))
		}
		start += len(ar.Positions)
	}
	return out, nil
}

func mapPos(p position) core.RawJob {
	return core.RawJob{
		ExternalID:  strconv.FormatInt(p.ID, 10),
		Title:       p.Name,
		LocationRaw: p.Location,
		URL:         p.CanonicalURL,
		UpdatedAt:   unixSecs(p.TUpdate),
		FirstSeen:   unixSecs(p.TCreate),
		Departments: dedup(p.Department, p.BusinessUnit),
		Offices:     p.Locations,
		Content:     p.JobDescription,
		Meta:        map[string]string{"display_job_id": p.DisplayJobID},
		Raw:         mustRaw(p),
	}
}

func unixSecs(s int64) time.Time {
	if s <= 0 {
		return time.Time{}
	}
	return time.Unix(s, 0)
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
