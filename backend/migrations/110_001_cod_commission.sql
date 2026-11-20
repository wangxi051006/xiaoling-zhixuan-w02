ALTER TABLE stations ADD COLUMN cod_commission_bps integer NOT NULL DEFAULT 1000
    CHECK (cod_commission_bps BETWEEN 0 AND 10000);

ALTER TABLE orders ADD COLUMN cod_commission_bps integer NOT NULL DEFAULT 1000
    CHECK (cod_commission_bps BETWEEN 0 AND 10000);

ALTER TABLE rider_earnings ADD COLUMN commission_fen bigint
    CHECK (commission_fen BETWEEN 0 AND amount_fen);

CREATE TABLE cod_merchant_payouts (
    order_id text PRIMARY KEY REFERENCES cod_receipts(order_id),
    finance_id text NOT NULL REFERENCES accounts(id),
    amount_fen bigint NOT NULL CHECK (amount_fen BETWEEN 1 AND 9007199254740991),
    receipt_reference text NOT NULL CHECK (length(receipt_reference) BETWEEN 1 AND 128),
    recorded_at timestamptz NOT NULL DEFAULT now()
);

CREATE TRIGGER cod_merchant_payouts_append_only
    BEFORE UPDATE OR DELETE OR TRUNCATE ON cod_merchant_payouts
    FOR EACH STATEMENT EXECUTE FUNCTION reject_settlement_ledger_mutation();
