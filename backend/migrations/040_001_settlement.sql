CREATE TABLE settlements (
    order_id text PRIMARY KEY CHECK (length(order_id) BETWEEN 1 AND 128),
    actor_id text NOT NULL CHECK (length(actor_id) BETWEEN 1 AND 128),
    station_id text NOT NULL REFERENCES stations(id),
    method text NOT NULL CHECK (method IN ('COD', 'BALANCE')),
    amount_fen bigint NOT NULL CHECK (amount_fen BETWEEN 0 AND 9007199254740991),
    state text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    CHECK ((method = 'COD' AND state IN ('COD_UNCOLLECTED','COD_COLLECTED','VOID','REFUND_PENDING','REFUNDED'))
        OR (method = 'BALANCE' AND state IN ('BALANCE_PAID','REFUNDED'))),
    UNIQUE (order_id, method, amount_fen),
    UNIQUE (order_id, station_id, actor_id, method, amount_fen)
);

CREATE TABLE cod_receipts (
    order_id text PRIMARY KEY,
    method text NOT NULL DEFAULT 'COD' CHECK (method = 'COD'),
    rider_id text NOT NULL CHECK (length(rider_id) BETWEEN 1 AND 128),
    amount_fen bigint NOT NULL,
    collected_at timestamptz NOT NULL DEFAULT now(),
    FOREIGN KEY (order_id, method, amount_fen) REFERENCES settlements(order_id, method, amount_fen)
);
CREATE INDEX cod_receipts_rider_time_idx ON cod_receipts (rider_id, collected_at, order_id);

CREATE TABLE wallets (
    station_id text NOT NULL REFERENCES stations(id),
    actor_id text NOT NULL CHECK (length(actor_id) BETWEEN 1 AND 128),
    opening_fen bigint NOT NULL DEFAULT 0 CHECK (opening_fen BETWEEN 0 AND 9007199254740991),
    balance_fen bigint NOT NULL DEFAULT 0 CHECK (balance_fen BETWEEN 0 AND 9007199254740991),
    PRIMARY KEY (station_id, actor_id)
);

CREATE TABLE balance_import_batches (
    approval_id text PRIMARY KEY CHECK (approval_id ~ '^[A-Z0-9_]{1,64}$'),
    station_id text NOT NULL REFERENCES stations(id),
    approver_id text NOT NULL CHECK (length(approver_id) BETWEEN 1 AND 128),
    executor_id text NOT NULL CHECK (length(executor_id) BETWEEN 1 AND 128),
    manifest_sha256 bytea NOT NULL UNIQUE CHECK (octet_length(manifest_sha256) = 32),
    total_fen bigint NOT NULL CHECK (total_fen BETWEEN 1 AND 9007199254740991),
    entry_count integer NOT NULL CHECK (entry_count BETWEEN 1 AND 1000),
    recorded_at timestamptz NOT NULL DEFAULT now(),
    CHECK (approver_id <> executor_id),
    UNIQUE (approval_id, station_id)
);

CREATE TABLE wallet_entries (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    station_id text NOT NULL,
    actor_id text NOT NULL,
    order_id text,
    approval_id text,
    external_reference text UNIQUE CHECK (length(external_reference) BETWEEN 1 AND 128),
    kind text NOT NULL CHECK (kind IN ('DEBIT','REFUND','IMPORT')),
    delta_fen bigint NOT NULL CHECK (delta_fen BETWEEN -9007199254740991 AND 9007199254740991),
    amount_fen bigint GENERATED ALWAYS AS (abs(delta_fen)) STORED,
    method text NOT NULL DEFAULT 'BALANCE' CHECK (method = 'BALANCE'),
    recorded_at timestamptz NOT NULL DEFAULT now(),
    CHECK ((kind = 'DEBIT' AND delta_fen <= 0) OR (kind = 'REFUND' AND delta_fen >= 0) OR (kind = 'IMPORT' AND delta_fen > 0)),
    CHECK ((kind = 'IMPORT' AND order_id IS NULL AND approval_id IS NOT NULL AND external_reference IS NOT NULL)
        OR (kind <> 'IMPORT' AND order_id IS NOT NULL AND approval_id IS NULL AND external_reference IS NULL)),
    FOREIGN KEY (station_id, actor_id) REFERENCES wallets(station_id, actor_id),
    FOREIGN KEY (approval_id, station_id) REFERENCES balance_import_batches(approval_id, station_id),
    FOREIGN KEY (order_id, station_id, actor_id, method, amount_fen)
        REFERENCES settlements(order_id, station_id, actor_id, method, amount_fen),
    UNIQUE (order_id, kind)
);
CREATE INDEX wallet_entries_account_idx ON wallet_entries (station_id, actor_id, id);

CREATE FUNCTION reject_settlement_ledger_mutation() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'settlement ledger records are append-only' USING ERRCODE = '42501';
END
$$;
CREATE TRIGGER wallet_entries_append_only BEFORE UPDATE OR DELETE OR TRUNCATE ON wallet_entries
FOR EACH STATEMENT EXECUTE FUNCTION reject_settlement_ledger_mutation();
CREATE TRIGGER balance_import_batches_append_only BEFORE UPDATE OR DELETE OR TRUNCATE ON balance_import_batches
FOR EACH STATEMENT EXECUTE FUNCTION reject_settlement_ledger_mutation();
CREATE TRIGGER cod_receipts_append_only BEFORE UPDATE OR DELETE OR TRUNCATE ON cod_receipts
FOR EACH STATEMENT EXECUTE FUNCTION reject_settlement_ledger_mutation();

CREATE TABLE refunds (
    id text PRIMARY KEY DEFAULT gen_random_uuid()::text,
    order_id text NOT NULL UNIQUE REFERENCES settlements(order_id),
    source text NOT NULL CHECK (source IN ('MANUAL','UNFULFILLED')),
    operator_id text,
    request_reason text,
    requested_at timestamptz NOT NULL DEFAULT now(),
    finance_id text,
    approval_reason text,
    approved_at timestamptz,
    CHECK ((source = 'MANUAL') = (operator_id IS NOT NULL AND request_reason IS NOT NULL)),
    CHECK ((finance_id IS NULL AND approval_reason IS NULL AND approved_at IS NULL)
        OR (source = 'MANUAL' AND finance_id <> operator_id AND finance_id IS NOT NULL
            AND approval_reason IS NOT NULL AND approved_at IS NOT NULL))
);

CREATE TABLE cod_refund_payouts (
    refund_id text PRIMARY KEY REFERENCES refunds(id),
    finance_id text NOT NULL CHECK (length(finance_id) BETWEEN 1 AND 128),
    receipt_reference text NOT NULL UNIQUE CHECK (length(receipt_reference) BETWEEN 1 AND 128),
    recorded_at timestamptz NOT NULL DEFAULT now()
);
CREATE TRIGGER cod_refund_payouts_append_only BEFORE UPDATE OR DELETE OR TRUNCATE ON cod_refund_payouts
FOR EACH STATEMENT EXECUTE FUNCTION reject_settlement_ledger_mutation();

CREATE TABLE cod_batches (
    id text PRIMARY KEY DEFAULT gen_random_uuid()::text,
    station_id text NOT NULL REFERENCES stations(id),
    rider_id text NOT NULL CHECK (length(rider_id) BETWEEN 1 AND 128),
    local_date date NOT NULL,
    creator_id text NOT NULL CHECK (length(creator_id) BETWEEN 1 AND 128),
    expected_fen bigint NOT NULL CHECK (expected_fen BETWEEN 0 AND 9007199254740991),
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (id, creator_id, expected_fen)
);

CREATE TABLE cod_batch_receipts (
    receipt_id text PRIMARY KEY REFERENCES cod_receipts(order_id),
    batch_id text NOT NULL REFERENCES cod_batches(id)
);
CREATE INDEX cod_batch_receipts_batch_idx ON cod_batch_receipts (batch_id, receipt_id);

CREATE TABLE cod_batch_verifications (
    batch_id text PRIMARY KEY,
    creator_id text NOT NULL,
    verifier_id text NOT NULL CHECK (length(verifier_id) BETWEEN 1 AND 128),
    reason_code text NOT NULL CHECK (reason_code ~ '^[A-Z0-9_]{1,64}$'),
    expected_fen bigint NOT NULL,
    actual_fen bigint NOT NULL CHECK (actual_fen BETWEEN 0 AND 9007199254740991),
    difference_fen bigint GENERATED ALWAYS AS (expected_fen - actual_fen) STORED,
    verified_at timestamptz NOT NULL DEFAULT now(),
    CHECK (creator_id <> verifier_id),
    FOREIGN KEY (batch_id, creator_id, expected_fen) REFERENCES cod_batches(id, creator_id, expected_fen)
);

CREATE TRIGGER cod_batches_append_only BEFORE UPDATE OR DELETE OR TRUNCATE ON cod_batches
FOR EACH STATEMENT EXECUTE FUNCTION reject_settlement_ledger_mutation();
CREATE TRIGGER cod_batch_receipts_append_only BEFORE UPDATE OR DELETE OR TRUNCATE ON cod_batch_receipts
FOR EACH STATEMENT EXECUTE FUNCTION reject_settlement_ledger_mutation();
CREATE TRIGGER cod_batch_verifications_append_only BEFORE UPDATE OR DELETE OR TRUNCATE ON cod_batch_verifications
FOR EACH STATEMENT EXECUTE FUNCTION reject_settlement_ledger_mutation();
