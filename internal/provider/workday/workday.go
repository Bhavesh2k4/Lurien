// Package workday implements the Workday (CxS) job-board provider.
//
// Workday boards are per-tenant and expose no location or description in the job
// list, and can hold thousands of postings — so this provider filters to India
// server-side. It first discovers the tenant's India location-facet id (facet
// ids are tenant-specific), then paginates only the India-filtered result set.
package workday

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"lurien/internal/core"
	"lurien/internal/fetch"
	"lurien/internal/provider"
)

const (
	pageLimit = 20  // Workday CxS caps page size at 20
	maxJobs   = 600 // safety cap on the India-filtered set per poll
)

func init() { provider.Register(Provider{}) }

// Provider is the Workday adapter.
type Provider struct{}

// Kind implements provider.Provider.
func (Provider) Kind() string { return "workday" }

type cxsRequest struct {
	AppliedFacets map[string][]string `json:"appliedFacets"`
	Limit         int                 `json:"limit"`
	Offset        int                 `json:"offset"`
	SearchText    string              `json:"searchText"`
}

type cxsResponse struct {
	Total       int          `json:"total"`
	JobPostings []jobPosting `json:"jobPostings"`
	Facets      []facet      `json:"facets"`
}

type jobPosting struct {
	Title         string   `json:"title"`
	ExternalPath  string   `json:"externalPath"`
	LocationsText string   `json:"locationsText"`
	PostedOn      string   `json:"postedOn"`
	BulletFields  []string `json:"bulletFields"`
}

type facet struct {
	FacetParameter string       `json:"facetParameter"`
	Values         []facetValue `json:"values"`
}

type facetValue struct {
	Descriptor     string       `json:"descriptor"`
	ID             string       `json:"id"`
	Count          int          `json:"count"`
	FacetParameter string       `json:"facetParameter"`
	Values         []facetValue `json:"values"`
}

// Fetch discovers the India facet, then pages the India-filtered postings.
func (Provider) Fetch(ctx context.Context, src core.Source, hc fetch.Client) ([]core.RawJob, error) {
	tenant, host, site := src.Params["tenant"], src.Params["host"], src.Params["site"]
	if tenant == "" || host == "" || site == "" {
		return nil, fmt.Errorf("workday: need tenant, host, site for %s", src.ID)
	}
	url := fmt.Sprintf("https://%s/wday/cxs/%s/%s/jobs", host, tenant, site)

	// 1. Discovery: unfiltered call to read the facet tree and find India's id.
	first, err := post(ctx, hc, url, cxsRequest{AppliedFacets: map[string][]string{}, Limit: 1})
	if err != nil {
		return nil, fmt.Errorf("workday: discover %s: %w", tenant, err)
	}
	param, indiaIDs := findIndia(first.Facets)
	if len(indiaIDs) == 0 {
		return nil, nil // tenant exposes no India location facet => no India jobs
	}
	applied := map[string][]string{param: indiaIDs}

	// 2. Page the India-filtered set.
	var out []core.RawJob
	total := -1
	for offset := 0; total < 0 || offset < total; offset += pageLimit {
		page, err := post(ctx, hc, url, cxsRequest{AppliedFacets: applied, Limit: pageLimit, Offset: offset})
		if err != nil {
			return out, fmt.Errorf("workday: page %s: %w", tenant, err)
		}
		if total < 0 {
			total = min(page.Total, maxJobs)
		}
		if len(page.JobPostings) == 0 {
			break
		}
		for _, jp := range page.JobPostings {
			if strings.TrimSpace(jp.Title) == "" {
				continue // skip placeholder/empty postings
			}
			out = append(out, mapJob(host, site, jp))
		}
	}
	return out, nil
}

func post(ctx context.Context, hc fetch.Client, url string, body cxsRequest) (*cxsResponse, error) {
	b, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	resp, err := hc.Do(ctx, fetch.Request{URL: url, Method: "POST", Body: b})
	if err != nil {
		return nil, err
	}
	var cr cxsResponse
	if err := json.Unmarshal(resp.Body, &cr); err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}
	return &cr, nil
}

// indiaCities are matched (as substrings) when a tenant exposes no country-level
// "India" facet and we must union city facets instead.
var indiaCities = []string{
	"bengaluru", "bangalore", "hyderabad", "pune", "mumbai", "new delhi",
	"delhi", "gurugram", "gurgaon", "noida", "chennai", "kolkata", "ahmedabad",
}

// findIndia returns the facet parameter and value id(s) that select India jobs.
// It prefers a single country-level "India" facet (highest count). If none
// exists (some tenants only expose city-level location facets), it falls back to
// the union of India-city facet ids under whichever parameter carries the most
// of them.
func findIndia(facets []facet) (param string, ids []string) {
	// Pass 1: country-level "India".
	bestCount := -1
	var country func(vals []facetValue, cur string)
	country = func(vals []facetValue, cur string) {
		for _, v := range vals {
			if strings.EqualFold(strings.TrimSpace(v.Descriptor), "India") && v.ID != "" && v.Count > bestCount {
				bestCount, param, ids = v.Count, cur, []string{v.ID}
			}
			if len(v.Values) > 0 {
				country(v.Values, paramOf(v, cur))
			}
		}
	}
	for _, f := range facets {
		country(f.Values, f.FacetParameter)
	}
	if len(ids) > 0 {
		return param, ids
	}

	// Pass 2: city fallback — collect India-city ids grouped by facet parameter.
	byParam := map[string][]string{}
	var cities func(vals []facetValue, cur string)
	cities = func(vals []facetValue, cur string) {
		for _, v := range vals {
			if v.ID != "" && isIndiaCity(v.Descriptor) {
				byParam[cur] = append(byParam[cur], v.ID)
			}
			if len(v.Values) > 0 {
				cities(v.Values, paramOf(v, cur))
			}
		}
	}
	for _, f := range facets {
		cities(f.Values, f.FacetParameter)
	}
	for p, list := range byParam {
		if len(list) > len(ids) {
			param, ids = p, list
		}
	}
	return param, ids
}

func paramOf(v facetValue, cur string) string {
	if v.FacetParameter != "" {
		return v.FacetParameter
	}
	return cur
}

func isIndiaCity(descriptor string) bool {
	d := strings.ToLower(descriptor)
	for _, c := range indiaCities {
		if strings.Contains(d, c) {
			return true
		}
	}
	return false
}

func mapJob(host, site string, jp jobPosting) core.RawJob {
	ext := jp.ExternalPath
	if len(jp.BulletFields) > 0 && jp.BulletFields[0] != "" {
		ext = jp.BulletFields[0] // stable requisition id
	}
	return core.RawJob{
		ExternalID:  ext,
		Title:       jp.Title,
		LocationRaw: jp.LocationsText,
		URL:         fmt.Sprintf("https://%s/en-US/%s%s", host, site, jp.ExternalPath),
		Offices:     []string{"India"}, // result set is server-side filtered to India
		Meta:        map[string]string{"postedOn": jp.PostedOn},
		Raw:         mustRaw(jp),
	}
}

func mustRaw(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	return b
}
