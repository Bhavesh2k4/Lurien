-- +goose Up
-- +goose StatementBegin
CREATE TABLE jobs (
    source_id    text        NOT NULL,          -- e.g. "greenhouse:stripe"
    external_id  text        NOT NULL,          -- provider-stable id
    title        text        NOT NULL,
    location_raw text        NOT NULL DEFAULT '',
    country      text        NOT NULL DEFAULT '',
    url          text        NOT NULL DEFAULT '',
    departments  text[]      NOT NULL DEFAULT '{}',
    updated_at   timestamptz NOT NULL DEFAULT now(),  -- provider's last-updated
    content_hash text        NOT NULL DEFAULT '',

    -- lifecycle state machine: discovered | active | updated | closed
    state        text        NOT NULL,

    -- classification result (persisted so a better classifier can be re-run)
    decision     text        NOT NULL DEFAULT '',  -- match | review | reject
    function     text        NOT NULL DEFAULT '',  -- tech | reject | ambiguous
    seniority    text        NOT NULL DEFAULT '',  -- early | mid | senior | ambiguous
    confidence   double precision NOT NULL DEFAULT 0,
    reasons      text[]      NOT NULL DEFAULT '{}',

    first_seen   timestamptz NOT NULL DEFAULT now(),  -- when Lurien first saw it
    last_seen    timestamptz NOT NULL DEFAULT now(),  -- last poll it appeared in
    closed_at    timestamptz,

    PRIMARY KEY (source_id, external_id)
);

CREATE INDEX idx_jobs_state ON jobs (state);
CREATE INDEX idx_jobs_source_last_seen ON jobs (source_id, last_seen);
CREATE INDEX idx_jobs_decision ON jobs (decision);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE jobs;
-- +goose StatementEnd
