package classify

import (
	"testing"

	"lurien/internal/core"
)

func TestClassify(t *testing.T) {
	c := Default()

	cases := []struct {
		name string
		job  core.RawJob
		want core.Decision
	}{
		// --- genuine matches ---
		{"newgrad swe india", core.RawJob{
			Title: "Software Engineer, New Grad", LocationRaw: "Bengaluru, India"}, core.DecisionMatch},
		{"sde1 india", core.RawJob{
			Title: "SDE I", LocationRaw: "Hyderabad, India"}, core.DecisionMatch},
		{"associate swe india", core.RawJob{
			Title: "Associate Software Engineer", LocationRaw: "Pune"}, core.DecisionMatch},
		{"swe yoe within cap", core.RawJob{
			Title: "Software Engineer, Payments", LocationRaw: "Bengaluru",
			Content: "<p>1-2 years of experience required</p>"}, core.DecisionMatch},
		{"university dept marker", core.RawJob{
			Title: "Software Engineer", LocationRaw: "Bengaluru",
			Departments: []string{"University Recruiting"}}, core.DecisionMatch},

		// --- broadened function: any early-career technical role matches ---
		{"devops newgrad india", core.RawJob{
			Title: "DevOps Engineer, New Grad", LocationRaw: "Bengaluru"}, core.DecisionMatch},
		{"data scientist junior india", core.RawJob{
			Title: "Junior Data Scientist", LocationRaw: "Hyderabad"}, core.DecisionMatch},
		{"ai engineer intern india", core.RawJob{
			Title: "AI Engineer Intern", LocationRaw: "Pune"}, core.DecisionMatch},
		{"solutions engineer entry india", core.RawJob{
			Title: "Associate Solutions Engineer", LocationRaw: "Bangalore"}, core.DecisionMatch},
		{"swe intern now matches", core.RawJob{
			Title: "Software Engineer, Intern", LocationRaw: "Bengaluru"}, core.DecisionMatch},

		// --- non-tech still excluded even when entry-level ---
		{"sales newgrad india", core.RawJob{
			Title: "Sales Development Representative, New Grad", LocationRaw: "Bengaluru"}, core.DecisionReject},
		{"marketing associate india", core.RawJob{
			Title: "Associate Marketing Manager", LocationRaw: "Bengaluru"}, core.DecisionReject},
		{"sales engineer india", core.RawJob{
			Title: "Associate Sales Engineer", LocationRaw: "Bengaluru"}, core.DecisionReject},

		// --- seniority rejects (title dominates) ---
		{"senior swe india", core.RawJob{
			Title: "Senior Software Engineer (Money)", LocationRaw: "Bengaluru, India"}, core.DecisionReject},
		{"swe II india", core.RawJob{
			Title: "Software Engineer II", LocationRaw: "Bengaluru"}, core.DecisionReject},
		{"numeric level 3 india", core.RawJob{ // MongoDB style
			Title: "Software Engineer 3", LocationRaw: "Gurugram",
			Content: "2 years"}, core.DecisionReject},
		{"numeric level 1 matches", core.RawJob{
			Title: "Software Engineer 1", LocationRaw: "Gurugram"}, core.DecisionMatch},
		{"eng manager india", core.RawJob{
			Title: "Engineering Manager – Backend", LocationRaw: "Bengaluru"}, core.DecisionReject},
		{"yoe over cap", core.RawJob{
			Title: "Software Engineer", LocationRaw: "Bengaluru",
			Content: "<p>Minimum 5 years of experience</p>"}, core.DecisionReject},

		// --- function rejects ---
		{"newgrad PM india", core.RawJob{
			Title: "Associate Product Manager, New Grad", LocationRaw: "Bengaluru"}, core.DecisionReject},
		{"support engineer india", core.RawJob{
			Title: "Senior Designated Support Engineer", LocationRaw: "Bengaluru"}, core.DecisionReject},

		// --- location rejects ---
		{"newgrad swe US", core.RawJob{
			Title: "Software Engineer, New Grad", LocationRaw: "Bellevue, Washington"}, core.DecisionReject},

		// --- the substring trap: "intern" must not match "internal" ---
		{"internal auditor not intern", core.RawJob{
			Title: "Internal Audit Lead", LocationRaw: "Bengaluru"}, core.DecisionReject},

		// --- ambiguous → review (quarantine, not a match) ---
		{"bare swe india no level", core.RawJob{
			Title: "Software Engineer", LocationRaw: "Bengaluru, India"}, core.DecisionReview},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := c.Classify(tc.job, core.ClassifyHints{})
			if got.Decision != tc.want {
				t.Fatalf("got %s want %s\n  reasons: %v", got.Decision, tc.want, got.Reasons)
			}
		})
	}
}

// TestLevelOverride covers per-company numeric-level semantics (e.g. Walmart,
// whose entry level is "Software Engineer II").
func TestLevelOverride(t *testing.T) {
	c := Default()
	cases := []struct {
		name  string
		title string
		hints core.ClassifyHints
		want  core.Decision
	}{
		{"SE2 rejected by default", "Software Engineer II", core.ClassifyHints{}, core.DecisionReject},
		{"SE2 early when ceiling=2", "Software Engineer II", core.ClassifyHints{MaxEarlyLevel: 2}, core.DecisionMatch},
		{"SE3 rejected when ceiling=2", "Software Engineer 3", core.ClassifyHints{MaxEarlyLevel: 2}, core.DecisionReject},
		{"SE3 early when ceiling=3", "Software Engineer III", core.ClassifyHints{MaxEarlyLevel: 3}, core.DecisionMatch},
		{"senior still rejected at ceiling=3", "Senior Software Engineer", core.ClassifyHints{MaxEarlyLevel: 3}, core.DecisionReject},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := c.Classify(core.RawJob{Title: tc.title, LocationRaw: "Bengaluru"}, tc.hints)
			if got.Decision != tc.want {
				t.Fatalf("got %s want %s\n  reasons: %v", got.Decision, tc.want, got.Reasons)
			}
		})
	}
}
