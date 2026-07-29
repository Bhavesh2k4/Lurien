-- name: EnqueueNotification :exec
-- Idempotent: a second enqueue for the same (job, event) is ignored.
INSERT INTO notification_outbox (
    job_source_id, job_external_id, event_type,
    company, title, location, url, reasons
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
ON CONFLICT (job_source_id, job_external_id, event_type) DO NOTHING;

-- name: ClaimNotifications :many
-- Atomically lease a batch of due notifications. Picks up both fresh 'pending'
-- rows and 'sending' rows whose visibility deadline (next_attempt) has passed
-- (crash recovery). SKIP LOCKED lets multiple dispatchers run safely.
UPDATE notification_outbox SET
    status = 'sending',
    attempts = attempts + 1,
    next_attempt = $2            -- visibility deadline: now() + timeout
WHERE id IN (
    SELECT id FROM notification_outbox
    WHERE status IN ('pending', 'sending') AND next_attempt <= now()
    ORDER BY next_attempt
    LIMIT $1
    FOR UPDATE SKIP LOCKED
)
RETURNING *;

-- name: MarkSent :exec
UPDATE notification_outbox
SET status = 'sent', sent_at = now(), last_error = ''
WHERE id = $1;

-- name: RescheduleNotification :exec
UPDATE notification_outbox
SET status = 'pending', next_attempt = $2, last_error = $3
WHERE id = $1;

-- name: FailNotification :exec
UPDATE notification_outbox
SET status = 'failed', last_error = $2
WHERE id = $1;
