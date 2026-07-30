// Package dashboard renders the read-only jobs view. It is shared by the local
// server (cmd/lurien-web) and the Vercel serverless function (api/index.go).
package dashboard

import (
	"encoding/json"
	"html/template"
	"net/http"
	"sort"
	"strings"
	"time"

	"lurien/internal/core"
)

// Server renders the dashboard from a job source.
type Server struct {
	list  func(decision string, limit int) ([]core.Job, error)
	names map[string]string
	tmpl  *template.Template
	limit int
}

// New builds a Server. list fetches open jobs; names maps source IDs to display
// names (may be nil, in which case the slug is used).
func New(list func(decision string, limit int) ([]core.Job, error), names map[string]string, limit int) *Server {
	if limit <= 0 {
		limit = 2000
	}
	return &Server{
		list:  list,
		names: names,
		tmpl:  template.Must(template.New("page").Parse(pageHTML)),
		limit: limit,
	}
}

type jobView struct {
	Company   string   `json:"company"`
	Provider  string   `json:"provider"`
	Title     string   `json:"title"`
	Location  string   `json:"location"`
	URL       string   `json:"url"`
	Seniority string   `json:"seniority"`
	Decision  string   `json:"decision"`
	Reasons   []string `json:"reasons"`
	FirstSeen string   `json:"firstSeen"`
}

// Handler serves the dashboard HTML.
func (s *Server) Handler(w http.ResponseWriter, r *http.Request) {
	jobs, err := s.list("", s.limit) // all decisions; filtered client-side
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	views := make([]jobView, 0, len(jobs))
	var nMatch, nReview int
	for _, j := range jobs {
		switch j.Class.Decision {
		case core.DecisionReject:
			continue
		case core.DecisionMatch:
			nMatch++
		default:
			nReview++
		}
		views = append(views, s.toView(j))
	}
	sort.Slice(views, func(i, k int) bool { return views[i].FirstSeen > views[k].FirstSeen })
	data, _ := json.Marshal(views)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	s.tmpl.Execute(w, map[string]any{
		"JobsJSON": template.JS(data),
		"Match":    nMatch,
		"Review":   nReview,
		"Updated":  time.Now().Format("2 Jan 2006, 15:04 MST"),
	})
}

func (s *Server) toView(j core.Job) jobView {
	prov, slug, _ := strings.Cut(j.SourceID, ":")
	company := s.names[j.SourceID]
	if company == "" {
		company = title(slug)
	}
	return jobView{
		Company:   company,
		Provider:  prov,
		Title:     strings.TrimSpace(j.Title),
		Location:  j.LocationRaw,
		URL:       j.URL,
		Seniority: j.Class.Seniority,
		Decision:  string(j.Class.Decision),
		Reasons:   j.Class.Reasons,
		FirstSeen: j.FirstSeen.Format(time.RFC3339),
	}
}

func title(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
