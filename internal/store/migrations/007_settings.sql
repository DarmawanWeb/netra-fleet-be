-- Settings of this installation, one row per key: the installation ID (licenses bound to this
-- server name it), and what later settings need.
CREATE TABLE IF NOT EXISTS settings (
    key        TEXT PRIMARY KEY,
    value      TEXT NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
