
CREATE TABLE jobs (
    id TEXT PRIMARY KEY NOT NULL,
    target_url TEXT NOT NULL,
    payload BLOB NOT NULL,
    idempotency_key TEXT NOT NULL UNIQUE,

    status TEXT NOT NULL DEFAULT 'pending'
        CHECK (status IN (
            'pending',
            'processing',
            'succeeded',
            'dead_letter'
        )),

    attempts INTEGER NOT NULL DEFAULT 0
        CHECK (attempts >= 0),

    max_attempts INTEGER NOT NULL
        CHECK (max_attempts > 0),

    next_attempt_at INTEGER NOT NULL,
    locked_until INTEGER,

    last_error TEXT NOT NULL DEFAULT '',

    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,

    CHECK (attempts <= max_attempts)
);

CREATE INDEX idx_jobs_pending
ON jobs(next_attempt_at, created_at)
WHERE status = 'pending';

CREATE INDEX idx_jobs_expired_leases
ON jobs(locked_until)
WHERE status = 'processing';
