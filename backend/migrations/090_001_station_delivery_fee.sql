ALTER TABLE stations
    ADD COLUMN delivery_fee_fen bigint NOT NULL DEFAULT 0 CHECK (delivery_fee_fen BETWEEN 0 AND 9007199254740991),
    ADD COLUMN delivery_fee_version bigint NOT NULL DEFAULT 1 CHECK (delivery_fee_version BETWEEN 1 AND 9007199254740991);
