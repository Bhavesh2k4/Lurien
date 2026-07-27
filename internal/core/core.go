// Package core holds Lurien's domain types. It imports nothing internal and
// contains no I/O — every other package depends inward on this one.
package core

import (
	"encoding/json"
	"time"
)

// Company is a monitored employer.
type Company struct {
	Slug string // stable identifier, e.g. "stripe"
	Name string // display name, e.g. "Stripe"
}

// Source is one polled feed for a company (a provider + its parameters).
// A company may eventually have several sources (Greenhouse + a careers page).
type Source struct {
	ID       string            // "greenhouse:stripe"
	Company  Company           //
	Provider string            // "greenhouse"
	Params   map[string]string // provider-specific, e.g. {"board_token":"stripe"}
	Interval time.Duration     // poll cadence; zero => use engine default
	Enabled  bool              //
	Hints    ClassifyHints     // per-company classification overrides
}

// ClassifyHints carries per-company knowledge the global classifier can't infer.
type ClassifyHints struct {
	// MaxEarlyLevel is the highest numeric title level that still counts as
	// early-career at this company. Default 0 => treated as 1 (only I/1). Set to
	// 2 for employers whose entry level is "Software Engineer II" (e.g. Walmart),
	// 3 where new grads come in at level III, etc.
	MaxEarlyLevel int
}

// RawJob is a posting as returned by a provider, normalized into a common
// shape but NOT yet filtered or classified. Providers produce these and never
// see storage or classification.
type RawJob struct {
	ExternalID  string            // provider-stable id (Greenhouse job id)
	Title       string            //
	LocationRaw string            // free-text, e.g. "IN-Bengaluru"
	URL         string            // absolute apply URL
	UpdatedAt   time.Time         // provider's last-updated timestamp
	FirstSeen   time.Time         // provider's first-published, if given
	Departments []string          // e.g. ["Engineering"]
	Offices     []string          // e.g. ["India"]
	Content     string            // description (HTML)
	Meta        map[string]string // provider extras, e.g. {"Job Level":"IC1"}
	Raw         json.RawMessage   // untouched original payload, for reprocessing
}

// Decision is the terminal outcome of classification.
type Decision string

const (
	DecisionMatch  Decision = "match"  // SWE + early-career + India
	DecisionReview Decision = "review" // ambiguous on some axis; quarantine
	DecisionReject Decision = "reject" // fails at least one axis outright
)

// Classification is the explainable result of running a RawJob through the
// three-axis rule engine. Stored alongside the job so a better classifier can
// be re-run over history.
type Classification struct {
	Decision   Decision
	Function   string   // "tech" | "reject" | "ambiguous"
	Seniority  string   // "early" | "mid" | "senior" | "ambiguous"
	Country    string   // "IN" | ""
	Confidence float64  // 0..1
	Reasons    []string // human-readable evidence, e.g. "swe:title=software engineer"
}

// Notification is an outbound alert queued in the transactional outbox. Job
// fields are denormalized so a dispatcher can deliver without touching the jobs
// table. It never references a provider — delivery is origin-agnostic.
type Notification struct {
	ID            int64
	JobSourceID   string
	JobExternalID string
	EventType     string // "new_match"
	Company       string
	Title         string
	Location      string
	URL           string
	Reasons       []string
	Attempts      int
	NextAttempt   time.Time
	Status        string // pending | sending | sent | failed
}

// JobState is the lifecycle position of a stored posting.
type JobState string

const (
	StateDiscovered JobState = "discovered" // first seen
	StateActive     JobState = "active"     // still present, unchanged
	StateUpdated    JobState = "updated"    // present, content changed
	StateClosed     JobState = "closed"     // gone from the feed
)

// Job is the persisted, classified posting.
type Job struct {
	SourceID    string
	ExternalID  string
	Title       string
	LocationRaw string
	Country     string
	URL         string
	Departments []string
	UpdatedAt   time.Time
	ContentHash string
	State       JobState
	Class       Classification
	FirstSeen   time.Time // when Lurien first saw it
	LastSeen    time.Time // last poll it appeared in
	ClosedAt    time.Time
}
