// Package handler is the Vercel serverless entrypoint for the dashboard.
//
// It is intentionally self-contained (no lurien/internal imports): Vercel's
// @vercel/go wraps this file as a package outside the module root, which Go's
// internal-package rule forbids from importing lurien/internal/*. So this reads
// the jobs table directly with pgx and renders the same page as cmd/lurien-web.
package handler

import (
	"context"
	"encoding/json"
	"html/template"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	pool    *pgxpool.Pool
	tmpl    *template.Template
	initErr error
	initOne sync.Once
)

func setup() {
	pool, initErr = pgxpool.New(context.Background(), os.Getenv("LURIEN_DB"))
	if initErr != nil {
		return
	}
	tmpl, initErr = template.New("page").Parse(pageHTML)
}

type jobView struct {
	Company   string   `json:"company"`
	Provider  string   `json:"provider"`
	Title     string   `json:"title"`
	Location  string   `json:"location"`
	URL       string   `json:"url"`
	Seniority string   `json:"seniority"`
	Decision  string   `json:"decision"`
	FirstSeen string   `json:"firstSeen"`
}

// Handler is the Vercel function entrypoint.
func Handler(w http.ResponseWriter, r *http.Request) {
	initOne.Do(setup)
	if initErr != nil {
		http.Error(w, "init: "+initErr.Error(), http.StatusInternalServerError)
		return
	}

	ctx := r.Context()
	rows, err := pool.Query(ctx, `
		SELECT source_id, title, location_raw, url, seniority, decision, first_seen
		FROM jobs WHERE state <> 'closed'
		ORDER BY first_seen DESC LIMIT 2000`)
	if err != nil {
		http.Error(w, "query: "+err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	views := make([]jobView, 0, 512)
	var nMatch, nReview int
	for rows.Next() {
		var sourceID, title, loc, url, sen, dec string
		var firstSeen time.Time
		if err := rows.Scan(&sourceID, &title, &loc, &url, &sen, &dec, &firstSeen); err != nil {
			http.Error(w, "scan: "+err.Error(), http.StatusInternalServerError)
			return
		}
		switch dec {
		case "reject":
			continue
		case "match":
			nMatch++
		default:
			nReview++
		}
		prov, slug, _ := strings.Cut(sourceID, ":")
		views = append(views, jobView{
			Company:   title2(slug),
			Provider:  prov,
			Title:     strings.TrimSpace(title),
			Location:  loc,
			URL:       url,
			Seniority: sen,
			Decision:  dec,
			FirstSeen: firstSeen.Format(time.RFC3339),
		})
	}

	data, _ := json.Marshal(views)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	tmpl.Execute(w, map[string]any{
		"JobsJSON": template.JS(data),
		"Match":    nMatch,
		"Review":   nReview,
		"Updated":  time.Now().Format("2 Jan 2006, 15:04 MST"),
	})
}

func title2(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

const pageHTML = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Lurien — Early-Career Tech Jobs in India</title>
<style>
  :root { --bg:#f7f7f8; --card:#fff; --fg:#18181b; --muted:#71717a; --border:#e4e4e7;
          --accent:#4f46e5; --match:#16a34a; --review:#ca8a04; --chip:#f1f1f3; }
  @media (prefers-color-scheme: dark) {
    :root { --bg:#0b0b0e; --card:#161619; --fg:#f4f4f5; --muted:#a1a1aa; --border:#27272a;
            --accent:#818cf8; --match:#4ade80; --review:#facc15; --chip:#26262b; }
  }
  * { box-sizing: border-box; }
  body { margin:0; background:var(--bg); color:var(--fg);
         font:15px/1.5 -apple-system,BlinkMacSystemFont,"Segoe UI",Roboto,sans-serif; }
  header { padding:28px 20px 12px; max-width:960px; margin:0 auto; }
  h1 { margin:0 0 4px; font-size:22px; letter-spacing:-.02em; }
  .sub { color:var(--muted); font-size:13px; }
  .bar { position:sticky; top:0; background:var(--bg); z-index:5;
         padding:12px 20px; max-width:960px; margin:0 auto; display:flex; gap:8px;
         flex-wrap:wrap; align-items:center; border-bottom:1px solid var(--border); }
  .seg { display:inline-flex; border:1px solid var(--border); border-radius:8px; overflow:hidden; }
  .seg button { background:var(--card); color:var(--fg); border:0; padding:7px 12px;
                font-size:13px; cursor:pointer; }
  .seg button.on { background:var(--accent); color:#fff; }
  input, select { background:var(--card); color:var(--fg); border:1px solid var(--border);
                  border-radius:8px; padding:7px 10px; font-size:13px; }
  input#q { flex:1; min-width:160px; }
  main { max-width:960px; margin:0 auto; padding:8px 20px 60px; }
  .row { display:flex; gap:12px; padding:14px 4px; border-bottom:1px solid var(--border);
         align-items:baseline; }
  .row a.t { font-weight:600; color:var(--fg); text-decoration:none; letter-spacing:-.01em; }
  .row a.t:hover { color:var(--accent); text-decoration:underline; }
  .meta { color:var(--muted); font-size:13px; margin-top:2px; }
  .co { min-width:130px; font-size:13px; color:var(--muted); flex-shrink:0; }
  .co b { color:var(--fg); display:block; font-size:14px; }
  .grow { flex:1; min-width:0; }
  .chip { display:inline-block; font-size:11px; padding:1px 7px; border-radius:999px;
          background:var(--chip); color:var(--muted); margin-right:5px; }
  .chip.m { color:var(--match); } .chip.r { color:var(--review); }
  .prov { text-transform:capitalize; }
  .empty { text-align:center; color:var(--muted); padding:60px 0; }
  .count { color:var(--muted); font-size:13px; margin-left:auto; }
</style>
</head>
<body>
<header>
  <h1>Lurien — early-career tech jobs in India</h1>
  <div class="sub">Discovered directly from company career APIs · {{.Match}} matches · {{.Review}} in review · updated {{.Updated}}</div>
</header>
<div class="bar">
  <div class="seg" id="seg">
    <button data-d="match" class="on">Matches</button>
    <button data-d="review">Review</button>
    <button data-d="all">All</button>
  </div>
  <input id="q" type="search" placeholder="Search title or company…">
  <select id="prov"><option value="">All providers</option></select>
  <span class="count" id="count"></span>
</div>
<main id="list"></main>
<script>
  const JOBS = {{.JobsJSON}};
  let decision = "match", q = "", prov = "";
  const provs = [...new Set(JOBS.map(j => j.provider))].sort();
  const sel = document.getElementById("prov");
  provs.forEach(p => { const o=document.createElement("option"); o.value=p; o.textContent=p; sel.appendChild(o); });
  function ago(iso){ const d=(Date.now()-new Date(iso))/86400000;
    if(d<1) return "today"; if(d<2) return "yesterday"; return Math.floor(d)+"d ago"; }
  function esc(s){ return (s||"").replace(/[&<>"]/g,c=>({"&":"&amp;","<":"&lt;",">":"&gt;",'"':"&quot;"}[c])); }
  function render(){
    const ql = q.toLowerCase();
    const rows = JOBS.filter(j =>
      (decision==="all" || j.decision===decision) &&
      (!prov || j.provider===prov) &&
      (!ql || j.title.toLowerCase().includes(ql) || j.company.toLowerCase().includes(ql)));
    const list = document.getElementById("list");
    document.getElementById("count").textContent = rows.length + " shown";
    if(!rows.length){ list.innerHTML = '<div class="empty">No jobs match your filters.</div>'; return; }
    list.innerHTML = rows.map(j => {
      const badge = j.decision==="match" ? '<span class="chip m">match</span>' : '<span class="chip r">review</span>';
      return '<div class="row">'
        + '<div class="co"><b>'+esc(j.company)+'</b><span class="prov">'+esc(j.provider)+'</span></div>'
        + '<div class="grow">'
        + '<a class="t" href="'+esc(j.url)+'" target="_blank" rel="noopener">'+esc(j.title)+'</a>'
        + '<div class="meta">'+badge+(j.seniority?'<span class="chip">'+esc(j.seniority)+'</span>':'')
        + '📍 '+esc(j.location||"India")+' · '+ago(j.firstSeen)+'</div>'
        + '</div></div>';
    }).join("");
  }
  document.getElementById("seg").addEventListener("click", e => {
    if(e.target.dataset.d){ decision=e.target.dataset.d;
      document.querySelectorAll("#seg button").forEach(b=>b.classList.toggle("on", b===e.target)); render(); }
  });
  document.getElementById("q").addEventListener("input", e => { q=e.target.value; render(); });
  sel.addEventListener("change", e => { prov=e.target.value; render(); });
  render();
</script>
</body>
</html>`

