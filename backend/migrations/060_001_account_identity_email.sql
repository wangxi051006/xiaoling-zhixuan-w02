-- 060_001: explicit account type, verified customer email, email verification challenges.
-- Appended after 050_001; earlier migrations are untouched.

-- Replace the legacy "phone IS NULL means staff" shape with a derived, explicit
-- account type. Deriving it keeps every existing INSERT and upgrade truthful
-- without rewriting callers, while queries stop guessing from phone IS NULL.
DO $$
DECLARE legacy text;
BEGIN
  -- The legacy shape check spans exactly phone, issuer and subject, which the
  -- attribute count identifies without deparsing an expression.
  SELECT c.conname INTO legacy
  FROM pg_constraint c
  JOIN pg_class t ON t.oid = c.conrelid
  JOIN pg_namespace n ON n.oid = t.relnamespace
  WHERE n.nspname = current_schema()
    AND t.relname = 'accounts' AND c.contype = 'c'
    AND array_length(c.conkey, 1) = 3
  ORDER BY c.conname
  LIMIT 1;
  IF legacy IS NOT NULL THEN
    EXECUTE format('ALTER TABLE %I.accounts DROP CONSTRAINT %I', current_schema(), legacy);
  END IF;
END
$$;

ALTER TABLE accounts ADD COLUMN email text;
ALTER TABLE accounts ADD COLUMN email_verified_at timestamptz;

ALTER TABLE accounts ADD COLUMN account_type text
  GENERATED ALWAYS AS (
    CASE WHEN phone IS NOT NULL OR email IS NOT NULL THEN 'CUSTOMER' ELSE 'STAFF' END
  ) STORED;

ALTER TABLE accounts ADD CONSTRAINT accounts_type_values
  CHECK (account_type IN ('CUSTOMER','STAFF'));

-- Canonical form only: trimmed, lower-cased, single @, no whitespace.
-- '+' tags and dots are preserved; nothing is merged automatically.
ALTER TABLE accounts ADD CONSTRAINT accounts_email_normalized
  CHECK (email IS NULL OR (
    email = lower(btrim(email))
    AND length(email) <= 254
    AND email ~ '^[^@[:space:]]{1,64}@[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?([.][a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?)+$'));

ALTER TABLE accounts ADD CONSTRAINT accounts_email_verified_shape
  CHECK ((email IS NULL) = (email_verified_at IS NULL));

-- CUSTOMER: phone and/or verified email, never a staff subject.
-- STAFF: trusted issuer/subject only, never a phone or a customer email.
ALTER TABLE accounts ADD CONSTRAINT accounts_identity_shape
  CHECK (
    (account_type = 'CUSTOMER' AND issuer IS NULL AND subject IS NULL
      AND (phone IS NOT NULL OR email IS NOT NULL))
    OR (account_type = 'STAFF' AND phone IS NULL AND email IS NULL AND email_verified_at IS NULL
      AND issuer IS NOT NULL AND issuer <> '' AND subject IS NOT NULL AND subject <> ''));

CREATE UNIQUE INDEX accounts_email_owner ON accounts (email) WHERE email IS NOT NULL;

CREATE TABLE email_challenges (
    id text PRIMARY KEY CHECK (id ~ '^[0-9a-f]{32}$'),
    purpose text NOT NULL CHECK (purpose IN ('REGISTER','LOGIN','BIND')),
    email text NOT NULL CHECK (email = lower(btrim(email)) AND length(email) <= 254),
    email_key bytea NOT NULL CHECK (octet_length(email_key) = 32),
    source_key bytea NOT NULL CHECK (octet_length(source_key) = 32),
    account_id text REFERENCES accounts(id),
    code_digest bytea NOT NULL CHECK (octet_length(code_digest) = 32),
    issued_at timestamptz NOT NULL,
    expires_at timestamptz NOT NULL CHECK (expires_at > issued_at),
    attempts integer NOT NULL DEFAULT 0 CHECK (attempts BETWEEN 0 AND 5),
    sent boolean NOT NULL DEFAULT false,
    consumed boolean NOT NULL DEFAULT false,
    superseded boolean NOT NULL DEFAULT false,
    CHECK (NOT consumed OR sent),
    CHECK ((purpose = 'BIND') = (account_id IS NOT NULL))
);
-- A resend for the same purpose retires the previous live code before the new
-- row lands, so an old code can never be replayed.
CREATE UNIQUE INDEX email_challenges_active
  ON email_challenges (email_key, purpose) WHERE sent AND NOT consumed AND NOT superseded;
CREATE INDEX email_challenge_email_window ON email_challenges (email_key, issued_at DESC);
CREATE INDEX email_challenge_source_window ON email_challenges (source_key, issued_at DESC);
CREATE INDEX email_challenge_retention ON email_challenges (issued_at);

-- Transactional email outbox. It deliberately carries no foreign key to orders:
-- a legal upgrade prefix that stops before the fulfillment segment must still
-- migrate, and an order id here only ever comes from the owning transaction.
CREATE TABLE email_outbox (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    station_id text NOT NULL REFERENCES stations(id),
    kind text NOT NULL CHECK (kind IN ('ORDER_PLACED','ORDER_CANCELED','DELIVERY_INCIDENT')),
    dedupe_key text NOT NULL CHECK (dedupe_key <> ''),
    recipient_account_id text NOT NULL REFERENCES accounts(id),
    recipient_email text NOT NULL CHECK (length(recipient_email) <= 254),
    order_id text NOT NULL CHECK (order_id <> ''),
    payload jsonb NOT NULL CHECK (jsonb_typeof(payload) = 'object'),
    available_at timestamptz NOT NULL,
    lease_token text,
    lease_until timestamptz,
    attempts integer NOT NULL DEFAULT 0 CHECK (attempts BETWEEN 0 AND 5),
    last_error text CHECK (last_error IS NULL OR length(last_error) <= 200),
    sent_at timestamptz,
    failed_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (kind, dedupe_key),
    CHECK ((lease_token IS NULL) = (lease_until IS NULL)),
    CHECK (NOT (sent_at IS NOT NULL AND failed_at IS NOT NULL)),
    CHECK (failed_at IS NULL OR attempts = 5)
);
CREATE INDEX email_outbox_ready ON email_outbox (available_at, id)
  WHERE sent_at IS NULL AND failed_at IS NULL;
