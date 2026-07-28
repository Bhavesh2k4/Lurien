// Package classify implements Lurien's three-axis rule engine. It is pure: no
// I/O, no provider awareness. A RawJob matches only when it is Software
// Engineering AND early-career AND located in India. Ambiguous jobs are sent
// to review (quarantine) rather than guessed.
package classify

import (
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"lurien/internal/core"
)

// Classifier evaluates RawJobs against a Lexicon.
type Classifier struct {
	lex Lexicon
}

// New builds a Classifier from a Lexicon.
func New(lex Lexicon) *Classifier { return &Classifier{lex: lex} }

// Default builds a Classifier with the built-in lexicon.
func Default() *Classifier { return New(DefaultLexicon()) }

// yoeRe extracts a minimum years-of-experience, e.g. "5+ years", "3-5 years",
// "minimum of 4 years".
var yoeRe = regexp.MustCompile(`(\d{1,2})\s*\+?\s*(?:to|-|–)?\s*(?:\d{1,2})?\s*years?`)

// tagRe strips HTML tags before scanning description text.
var tagRe = regexp.MustCompile(`<[^>]*>`)

// Classify runs all three axes and returns an explainable result. Hints carry
// per-company overrides (e.g. MaxEarlyLevel) the global rules can't infer.
func (c *Classifier) Classify(j core.RawJob, hints core.ClassifyHints) core.Classification {
	titleN := norm(j.Title)
	deptN := norm(strings.Join(j.Departments, " "))
	body := tagRe.ReplaceAllString(j.Content, " ")

	fn, fnReasons := c.function(titleN)
	sr, srReasons := c.seniority(titleN, deptN, body, hints)
	country, locReasons := c.location(j)

	reasons := append(append(fnReasons, srReasons...), locReasons...)

	res := core.Classification{
		Function:  fn,
		Seniority: sr,
		Country:   country,
		Reasons:   reasons,
	}
	res.Decision, res.Confidence = decide(fn, sr, country)
	return res
}

func decide(fn, sr, country string) (core.Decision, float64) {
	// We only resolve India today; anything else is out of scope → reject.
	if country != "IN" {
		return core.DecisionReject, 0.95
	}
	// Definite no on function or seniority.
	if fn == "reject" || sr == "senior" || sr == "mid" {
		return core.DecisionReject, 0.9
	}
	// All three axes positive.
	if fn == "tech" && sr == "early" {
		return core.DecisionMatch, 0.95
	}
	// In India, not hard-rejected, but ambiguous on function or seniority →
	// quarantine for human review instead of guessing.
	return core.DecisionReview, 0.5
}

// function returns "tech", "reject", or "ambiguous" using tiered matching:
// core-tech wins first (so "Software Engineer, Marketing Platform" is tech),
// then non-tech rejects (so "Marketing Manager"/"Sales Engineer" are out),
// then a broad-tech fallback catches the long tail ("Cloud Ops Engineer").
func (c *Classifier) function(titleN string) (string, []string) {
	if m := firstMatch(titleN, c.lex.Function.CoreTech); m != "" {
		return "tech", []string{"tech:title=" + m}
	}
	if m := firstMatch(titleN, c.lex.Function.NonTech); m != "" {
		return "reject", []string{"non-tech:title=" + m}
	}
	if m := firstMatch(titleN, c.lex.Function.BroadTech); m != "" {
		return "tech", []string{"tech:title=" + m}
	}
	return "ambiguous", []string{"function:ambiguous"}
}

// seniority returns "early", "mid", "senior", or "ambiguous". Non-numeric senior
// words dominate; then explicit early markers; then the numeric title level is
// compared against the per-company ceiling; then YoE; then department.
func (c *Classifier) seniority(titleN, deptN, body string, hints core.ClassifyHints) (string, []string) {
	if m := firstMatch(titleN, c.lex.Seniority.RejectTokens); m != "" {
		return "senior", []string{"seniority-reject:title=" + m}
	}
	if m := firstMatch(titleN, c.lex.Seniority.EarlyTokens); m != "" {
		return "early", []string{"early:title=" + m}
	}
	// Numeric title level (e.g. "Software Engineer II" -> 2) vs the company's
	// early ceiling (default 1; overridden by MaxEarlyLevel for employers like
	// Walmart whose entry level is II).
	if lvl := levelOf(titleN); lvl > 0 {
		ceiling := 1
		if hints.MaxEarlyLevel > 0 {
			ceiling = hints.MaxEarlyLevel
		}
		if lvl <= ceiling {
			return "early", []string{"early:level=" + strconv.Itoa(lvl)}
		}
		return "senior", []string{"seniority:level=" + strconv.Itoa(lvl)}
	}
	// Years of experience parsed from the description.
	if min, ok := minYears(body); ok {
		if min > c.lex.Seniority.MaxYears {
			return "mid", []string{"seniority:yoe=" + strconv.Itoa(min)}
		}
		return "early", []string{"early:yoe=" + strconv.Itoa(min)}
	}
	// Weak prior: an explicitly early-career department, only when nothing in the
	// title objected.
	if m := firstMatch(deptN, c.lex.Seniority.EarlyDepartments); m != "" {
		return "early", []string{"early:dept=" + m}
	}
	return "ambiguous", []string{"seniority:ambiguous"}
}

// location returns the ISO country ("IN") or "" if not resolvable to India.
func (c *Classifier) location(j core.RawJob) (string, []string) {
	hay := norm(strings.Join(append([]string{j.LocationRaw}, j.Offices...), " "))
	if m := firstMatch(hay, c.lex.Location.IndiaTerms); m != "" {
		return "IN", []string{"india:loc=" + m}
	}
	return "", []string{"location:not-india"}
}

// levelWords precede a numeric/roman level in a title ("Software Engineer II",
// "SDE 3", "Level 4"). Kept small to avoid reading stray numbers as levels.
var levelWords = map[string]bool{
	"engineer": true, "developer": true, "sde": true, "swe": true,
	"programmer": true, "scientist": true, "level": true, "grade": true,
}

// romanLevel maps standalone roman numerals to their value.
var romanLevel = map[string]int{"i": 1, "ii": 2, "iii": 3, "iv": 4, "v": 5}

// levelOf extracts the highest numeric title level (1..9) from a normalized,
// space-padded title, looking for a level word followed by a digit or roman
// numeral. Returns 0 when no level is present.
func levelOf(titleN string) int {
	toks := strings.Fields(titleN)
	max := 0
	for i := 0; i < len(toks)-1; i++ {
		if !levelWords[toks[i]] {
			continue
		}
		if n := parseLevel(toks[i+1]); n > max {
			max = n
		}
	}
	return max
}

// parseLevel reads a digit (1..9) or roman numeral token as a level, else 0.
func parseLevel(tok string) int {
	if n, err := strconv.Atoi(tok); err == nil && n >= 1 && n <= 9 {
		return n
	}
	return romanLevel[tok]
}

// minYears returns the smallest years-of-experience figure in the text.
func minYears(body string) (int, bool) {
	matches := yoeRe.FindAllStringSubmatch(strings.ToLower(body), -1)
	min, found := 0, false
	for _, m := range matches {
		n, err := strconv.Atoi(m[1])
		if err != nil || n > 40 { // ignore stray numbers like "2024 years"
			continue
		}
		if !found || n < min {
			min, found = n, true
		}
	}
	return min, found
}

// firstMatch returns the first lexicon phrase that appears as a whole-word run
// in the (already normalized, space-padded) haystack.
func firstMatch(paddedHaystack string, phrases []string) string {
	for _, p := range phrases {
		if strings.Contains(paddedHaystack, norm(p)) {
			return p
		}
	}
	return ""
}

// norm lowercases, replaces every non-alphanumeric rune with a space, collapses
// runs, and pads with a single leading/trailing space so phrase matching
// respects word boundaries. norm("Sr. Software Engineer II") ==
// " sr software engineer ii ".
func norm(s string) string {
	var b strings.Builder
	b.Grow(len(s) + 2)
	for _, r := range strings.ToLower(s) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		} else {
			b.WriteByte(' ')
		}
	}
	return " " + strings.Join(strings.Fields(b.String()), " ") + " "
}
