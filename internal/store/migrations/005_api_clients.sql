-- Third-party applications reading data through the integration API (/api/ext/v1), OAuth2 client credentials.
CREATE TABLE IF NOT EXISTS api_clients (
    id           BIGSERIAL PRIMARY KEY,
    client_id    TEXT UNIQUE NOT NULL,
    name         TEXT NOT NULL,
    secret_hash  TEXT NOT NULL,
    scopes       TEXT[] NOT NULL DEFAULT '{}',
    enabled      BOOLEAN NOT NULL DEFAULT TRUE,
    created_by   TEXT NOT NULL DEFAULT '',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_used_at TIMESTAMPTZ
);

CREATE TABLE IF NOT EXISTS api_tokens (
    token_hash TEXT PRIMARY KEY,
    client_pk  BIGINT NOT NULL REFERENCES api_clients(id) ON DELETE CASCADE,
    scopes     TEXT[] NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX IF NOT EXISTS api_tokens_expiry ON api_tokens (expires_at);

-- Change feed for integrations: every insert and update of an anomaly moves updated_at.
ALTER TABLE anomalies ADD COLUMN IF NOT EXISTS updated_at TIMESTAMPTZ;
UPDATE anomalies SET updated_at = GREATEST(created_at, COALESCE(validated_at, created_at), COALESCE(notified_at, created_at))
    WHERE updated_at IS NULL;
ALTER TABLE anomalies ALTER COLUMN updated_at SET DEFAULT clock_timestamp();
ALTER TABLE anomalies ALTER COLUMN updated_at SET NOT NULL;

CREATE OR REPLACE FUNCTION netra_touch_updated_at() RETURNS trigger AS $$
BEGIN
    NEW.updated_at := clock_timestamp();
    RETURN NEW;
END
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS anomalies_touch ON anomalies;
CREATE TRIGGER anomalies_touch BEFORE UPDATE ON anomalies FOR EACH ROW EXECUTE FUNCTION netra_touch_updated_at();
CREATE INDEX IF NOT EXISTS anomalies_updated ON anomalies (updated_at, id);
