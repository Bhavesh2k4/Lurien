-- +goose Up
-- +goose StatementBegin
CREATE TABLE notification_outbox (
    id              bigserial   PRIMARY KEY,
    job_source_id   text        NOT NULL,
    job_external_id text        NOT NULL,
    event_type      text        NOT NULL,          -- e.g. "new_match"

    -- denormalized job fields so the dispatcher needs no join to deliver
    company         text        NOT NULL DEFAULT '',
    title           text        NOT NULL DEFAULT '',
    location        text        NOT NULL DEFAULT '',
    url             text        NOT NULL DEFAULT '',
    reasons         text[]      NOT NULL DEFAULT '{}',

    status          text        NOT NULL DEFAULT 'pending', -- pending|sending|sent|failed
    attempts        int         NOT NULL DEFAULT 0,
    next_attempt    timestamptz NOT NULL DEFAULT now(),     -- also the visibility deadline
    last_error      text        NOT NULL DEFAULT '',
    created_at      timestamptz NOT NULL DEFAULT now(),
    sent_at         timestamptz,

    -- one notification per (job, event); makes enqueue idempotent
    UNIQUE (job_source_id, job_external_id, event_type)
);

CREATE INDEX idx_outbox_dispatch ON notification_outbox (status, next_attempt);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE notification_outbox;
-- +goose StatementEnd
