ALTER TABLE stations
    ADD COLUMN merchant_batch_limit integer NOT NULL DEFAULT 15 CHECK (merchant_batch_limit > 0),
    ADD COLUMN rider_active_limit integer NOT NULL DEFAULT 8 CHECK (rider_active_limit > 0),
    ADD COLUMN order_limits_version bigint NOT NULL DEFAULT 1 CHECK (order_limits_version BETWEEN 1 AND 9007199254740991);

ALTER TABLE orders ADD COLUMN merchant_accepted_at timestamptz;

DROP INDEX delivery_tasks_one_active_rider;
CREATE INDEX delivery_tasks_active_rider_idx ON delivery_tasks(rider_id)
    WHERE state IN ('ASSIGNED','PICKED_UP','RETURNING');
