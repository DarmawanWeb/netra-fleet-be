-- The license uploaded in the dashboard (Settings > License). One row; the license file given to
-- netra-api (NETRA_LICENSE_FILE) is the other source, and the newer of the two applies.
CREATE TABLE IF NOT EXISTS license (
    id          SMALLINT PRIMARY KEY DEFAULT 1 CHECK (id = 1),
    text        TEXT NOT NULL,
    uploaded_by TEXT NOT NULL DEFAULT '',
    uploaded_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
