package classify

// Lexicon holds the tunable vocabulary for the three classification axes.
// Defaults live here; they can be overridden from configs/classify.yaml so
// tuning never requires a recompile.
type Lexicon struct {
	Function  FunctionLex  `yaml:"function"`
	Seniority SeniorityLex `yaml:"seniority"`
	Location  LocationLex  `yaml:"location"`
}

// FunctionLex decides whether a title is a technical/engineering role.
//
// Matching is tiered and order-sensitive:
//  1. CoreTech  — unambiguous builder/technical phrases; accepted immediately,
//     so "Software Engineer, Marketing Platform" is tech before "marketing"
//     can reject it.
//  2. NonTech   — business functions (sales, marketing, HR, finance, PM…).
//     Because CoreTech already ran, bare words like "sales" are safe here.
//  3. BroadTech — fallback signals ("engineer", "developer") that catch the
//     long tail of technical roles the CoreTech list didn't name.
type FunctionLex struct {
	CoreTech  []string `yaml:"core_tech"`
	NonTech   []string `yaml:"non_tech"`
	BroadTech []string `yaml:"broad_tech"`
}

// SeniorityLex decides early-career vs not. Reject tokens dominate.
type SeniorityLex struct {
	RejectTokens     []string `yaml:"reject_tokens"`     // senior/mid/lead/manager markers
	EarlyTokens      []string `yaml:"early_tokens"`      // new-grad/intern/level-1 markers
	EarlyDepartments []string `yaml:"early_departments"` // weak prior only
	MaxYears         int      `yaml:"max_years"`         // YoE ceiling (inclusive)
}

// LocationLex decides India vs not.
type LocationLex struct {
	IndiaTerms []string `yaml:"india_terms"`
}

// DefaultLexicon returns Lurien's built-in vocabulary. Phrases are matched on
// whole-word boundaries, so "intern" never matches "internal".
func DefaultLexicon() Lexicon {
	return Lexicon{
		Function: FunctionLex{
			CoreTech: []string{
				// software
				"software engineer", "software developer", "software development engineer",
				"sde", "swe", "web developer", "mobile developer", "android developer",
				"ios developer", "backend", "back end", "frontend", "front end",
				"full stack", "fullstack", "programmer", "member of technical staff", "mts",
				// ops / reliability / infra
				"devops", "dev ops", "sre", "site reliability", "platform engineer",
				"infrastructure engineer", "systems engineer", "network engineer",
				"cloud engineer", "reliability engineer", "release engineer", "build engineer",
				"performance engineer",
				// data / ml / ai
				"data engineer", "data scientist", "data analyst", "analytics engineer",
				"machine learning", "ml engineer", "ai engineer", "artificial intelligence",
				"applied scientist", "research engineer", "research scientist",
				"deep learning", "computer vision", "nlp",
				// quality / security / hardware
				"qa engineer", "quality engineer", "test engineer", "sdet",
				"automation engineer", "security engineer", "security researcher",
				"embedded", "firmware", "hardware engineer", "compiler", "kernel", "gpu",
				// solutions / db / devrel
				"solutions engineer", "solutions architect", "solution architect",
				"forward deployed", "developer advocate", "developer relations",
				"database engineer", "database administrator",
			},
			NonTech: []string{
				"sales", "account executive", "account manager", "business development",
				"sales development", "marketing", "advertising", "communications",
				"public relations", "recruiter", "recruiting", "talent acquisition",
				"human resources", "people operations", "finance", "accountant",
				"accounting", "controller", "bookkeeper", "payroll", "treasury", "tax",
				"legal", "counsel", "attorney", "paralegal", "procurement",
				"administrative", "executive assistant", "office manager", "facilities",
				"customer success", "customer support", "customer experience",
				"product manager", "program manager", "project manager",
				"product designer", "designer", "ux researcher", "copywriter",
				"technical writer", "social media", "business analyst", "partnerships",
				"operations associate", "operations manager", "operations analyst",
				"operations specialist", "business operations", "revenue operations",
				"sales operations", "marketing operations",
			},
			BroadTech: []string{
				"engineer", "developer", "scientist", "technical", "programmer",
			},
		},
		Seniority: SeniorityLex{
			// Non-numeric senior markers. Numeric levels (II, 3, "Level 4"…) are
			// handled by levelOf() + the per-company MaxEarlyLevel ceiling, not here.
			RejectTokens: []string{
				"senior", "sr", "staff", "principal", "lead", "manager", "mgr",
				"director", "head", "vp", "vice president", "architect",
				"l4", "l5", "l6", "e4", "e5", "e6", "ic3", "ic4",
			},
			EarlyTokens: []string{
				"new grad", "new graduate", "recent graduate", "university grad",
				"university graduate", "campus", "early career", "early in career",
				"graduate software engineer", "graduate engineer", "graduate",
				"apprentice", "trainee", "fresher", "entry level", "junior", "jr",
				"associate",
				// Internships are in scope now.
				"intern", "internship", "co op", "coop", "working student",
				// Level-1 markers.
				"associate software engineer", "software engineer i", "software engineer 1",
				"sde i", "sde 1", "sde-1", "swe i", "swe 1", "swe-1",
				"software development engineer i", "grade 1",
				// NB: "l3"/"e3" (Google/Meta entry levels) are intentionally NOT
				// early tokens — they falsely match support tiers like "L3 Support
				// Engineer". Add them back via a per-company hint if those employers
				// are onboarded.
			},
			EarlyDepartments: []string{
				"university", "early career", "campus", "new grad", "graduate",
				"university recruiting", "early talent",
			},
			MaxYears: 2,
		},
		Location: LocationLex{
			IndiaTerms: []string{
				"india", "bengaluru", "bangalore", "hyderabad", "pune", "mumbai",
				"new delhi", "delhi", "gurugram", "gurgaon", "noida", "chennai",
				"kolkata", "ahmedabad", "jaipur", "kochi", "coimbatore", "trivandrum",
			},
		},
	}
}
