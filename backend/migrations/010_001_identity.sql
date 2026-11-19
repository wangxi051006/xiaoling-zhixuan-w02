CREATE TABLE otp_challenges (
    id text PRIMARY KEY CHECK (id ~ '^[0-9a-f]{32}$'),
    phone text NOT NULL,
    phone_key bytea NOT NULL CHECK (octet_length(phone_key) = 32),
    source_key bytea NOT NULL CHECK (octet_length(source_key) = 32),
    code_digest bytea NOT NULL CHECK (octet_length(code_digest) = 32),
    issued_at timestamptz NOT NULL,
    expires_at timestamptz NOT NULL CHECK (expires_at > issued_at),
    attempts integer NOT NULL DEFAULT 0 CHECK (attempts BETWEEN 0 AND 5),
    sent boolean NOT NULL DEFAULT false,
    consumed boolean NOT NULL DEFAULT false,
    CHECK (NOT consumed OR sent)
);
CREATE INDEX otp_challenge_phone_window ON otp_challenges (phone_key, issued_at DESC);
CREATE INDEX otp_challenge_source_window ON otp_challenges (source_key, issued_at DESC);
CREATE INDEX otp_challenge_retention ON otp_challenges (issued_at);

CREATE TABLE accounts (
    id text PRIMARY KEY,
    station_id text NOT NULL REFERENCES stations(id),
    phone text UNIQUE CHECK (phone ~ '^\+[1-9][0-9]{6,14}$'),
    issuer text,
    subject text,
    enabled boolean NOT NULL DEFAULT true,
    display_name text NOT NULL DEFAULT '',
    UNIQUE (issuer, subject),
    CHECK ((phone IS NOT NULL AND issuer IS NULL AND subject IS NULL) OR
           (phone IS NULL AND issuer IS NOT NULL AND issuer <> '' AND subject IS NOT NULL AND subject <> ''))
);

CREATE TABLE role_bindings (
    account_id text NOT NULL REFERENCES accounts(id),
    role text NOT NULL CHECK (role IN ('CUSTOMER','RIDER','OPERATOR','FINANCE','SECURITY_ADMIN')),
    PRIMARY KEY (account_id, role)
);

CREATE TABLE sessions (
    token_digest bytea PRIMARY KEY CHECK (octet_length(token_digest) = 32),
    account_id text NOT NULL REFERENCES accounts(id),
    entry text NOT NULL CHECK (entry IN ('user','rider','admin')),
    csrf_token text NOT NULL CHECK (length(csrf_token) = 64),
    expires_at timestamptz NOT NULL,
    mfa_at timestamptz,
    UNIQUE (account_id, entry),
    CHECK ((entry = 'user') = (mfa_at IS NULL))
);
CREATE INDEX sessions_expiry_idx ON sessions (expires_at);

CREATE TABLE staff_login_flows (
    state_digest bytea PRIMARY KEY CHECK (octet_length(state_digest) = 32),
    browser_digest bytea NOT NULL CHECK (octet_length(browser_digest) = 32),
    entry text NOT NULL CHECK (entry IN ('rider','admin')),
    nonce text NOT NULL CHECK (length(nonce) = 64),
    verifier text NOT NULL CHECK (length(verifier) BETWEEN 43 AND 128),
    expires_at timestamptz NOT NULL
);
CREATE INDEX staff_login_flows_expiry_idx ON staff_login_flows (expires_at);

CREATE FUNCTION revoke_disabled_account_sessions() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    DELETE FROM sessions WHERE account_id = NEW.id;
    RETURN NEW;
END
$$;
CREATE TRIGGER accounts_revoke_sessions AFTER UPDATE OF enabled ON accounts
FOR EACH ROW WHEN (OLD.enabled AND NOT NEW.enabled) EXECUTE FUNCTION revoke_disabled_account_sessions();

CREATE TABLE service_buildings (
    id text PRIMARY KEY,
    station_id text NOT NULL REFERENCES stations(id),
    name text NOT NULL CHECK (length(btrim(name)) BETWEEN 1 AND 200),
    enabled boolean NOT NULL DEFAULT true
);

CREATE TABLE addresses (
    id text PRIMARY KEY,
    account_id text NOT NULL REFERENCES accounts(id),
    building_id text NOT NULL REFERENCES service_buildings(id),
    room text NOT NULL CHECK (length(btrim(room)) BETWEEN 1 AND 40),
    contact_name text NOT NULL CHECK (length(btrim(contact_name)) BETWEEN 1 AND 60),
    contact_phone text NOT NULL CHECK (contact_phone ~ '^\+[1-9][0-9]{6,14}$'),
    version bigint NOT NULL DEFAULT 1 CHECK (version > 0),
    deleted_at timestamptz
);
CREATE INDEX addresses_owner_idx ON addresses (account_id, id) WHERE deleted_at IS NULL;

CREATE TABLE rider_profiles (
    account_id text PRIMARY KEY REFERENCES accounts(id),
    enabled boolean NOT NULL DEFAULT true,
    available boolean NOT NULL DEFAULT false,
    version bigint NOT NULL DEFAULT 1 CHECK (version > 0),
    CHECK (enabled OR NOT available)
);
