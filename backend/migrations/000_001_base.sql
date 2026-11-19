CREATE TABLE stations (
    id text PRIMARY KEY CHECK (id <> ''),
    name text NOT NULL CHECK (name <> ''),
    timezone text NOT NULL DEFAULT 'Asia/Shanghai' CHECK (timezone = 'Asia/Shanghai'),
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX stations_singleton ON stations ((true));

CREATE TABLE idempotency_keys (
    actor_id text NOT NULL CHECK (actor_id <> ''),
    command text NOT NULL CHECK (command <> ''),
    key text NOT NULL CHECK (length(key) BETWEEN 1 AND 128),
    request_digest bytea NOT NULL CHECK (octet_length(request_digest) = 32),
    response_status integer CHECK (response_status BETWEEN 200 AND 299),
    response_body jsonb,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (actor_id, command, key),
    CHECK ((response_status IS NULL) = (response_body IS NULL))
);

CREATE TABLE audit_events (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    station_id text NOT NULL REFERENCES stations(id),
    actor_id text NOT NULL,
    action text NOT NULL,
    object_id text NOT NULL,
    request_id text NOT NULL,
    reason_code text,
    recorded_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX audit_events_station_time_idx ON audit_events (station_id, recorded_at, id);

CREATE FUNCTION reject_audit_mutation() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'audit_events is append-only' USING ERRCODE = '42501';
END
$$;
CREATE TRIGGER audit_events_append_only BEFORE UPDATE OR DELETE OR TRUNCATE ON audit_events
FOR EACH STATEMENT EXECUTE FUNCTION reject_audit_mutation();

CREATE TABLE outbox_jobs (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    station_id text NOT NULL REFERENCES stations(id),
    kind text NOT NULL CHECK (kind <> ''),
    dedupe_key text NOT NULL CHECK (dedupe_key <> ''),
    payload jsonb NOT NULL,
    available_at timestamptz NOT NULL,
    lease_token text,
    lease_until timestamptz,
    attempts integer NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    finished_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (station_id, kind, dedupe_key),
    CHECK ((lease_token IS NULL) = (lease_until IS NULL)),
    CHECK (finished_at IS NULL OR lease_token IS NULL)
);
CREATE INDEX outbox_jobs_ready_idx ON outbox_jobs (available_at, lease_until, id) WHERE finished_at IS NULL;
