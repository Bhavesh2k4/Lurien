-- name: ActiveBySource :many
SELECT * FROM jobs
WHERE source_id = $1 AND state <> 'closed';

-- name: UpsertJob :exec
INSERT INTO jobs (
    source_id, external_id, title, location_raw, country, url, departments,
    updated_at, content_hash, state,
    decision, function, seniority, confidence, reasons,
    first_seen, last_seen, closed_at
) VALUES (
    $1, $2, $3, $4, $5, $6, $7,
    $8, $9, $10,
    $11, $12, $13, $14, $15,
    $16, $17, $18
)
ON CONFLICT (source_id, external_id) DO UPDATE SET
    title        = EXCLUDED.title,
    location_raw = EXCLUDED.location_raw,
    country      = EXCLUDED.country,
    url          = EXCLUDED.url,
    departments  = EXCLUDED.departments,
    updated_at   = EXCLUDED.updated_at,
    content_hash = EXCLUDED.content_hash,
    state        = EXCLUDED.state,
    decision     = EXCLUDED.decision,
    function     = EXCLUDED.function,
    seniority    = EXCLUDED.seniority,
    confidence   = EXCLUDED.confidence,
    reasons      = EXCLUDED.reasons,
    last_seen    = EXCLUDED.last_seen,
    closed_at    = EXCLUDED.closed_at;
    -- first_seen is intentionally NOT updated: it records the original sighting.

-- name: CloseJob :exec
UPDATE jobs
SET state = 'closed', closed_at = now()
WHERE source_id = $1 AND external_id = $2;

-- name: ListOpenJobs :many
-- Powers the dashboard: all non-closed jobs, newest first. Filter by decision
-- with the sqlc.narg (NULL => all decisions).
SELECT * FROM jobs
WHERE state <> 'closed'
  AND (sqlc.narg('decision')::text IS NULL OR decision = sqlc.narg('decision'))
ORDER BY first_seen DESC
LIMIT $1;
