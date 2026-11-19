CREATE TABLE order_rate_buckets (
 bucket_key text PRIMARY KEY CHECK(length(bucket_key) BETWEEN 1 AND 256),
 window_at timestamptz NOT NULL,
 attempts integer NOT NULL CHECK(attempts BETWEEN 0 AND 30)
);
CREATE TABLE quotes (
 id text PRIMARY KEY DEFAULT gen_random_uuid()::text,
 actor_id text NOT NULL REFERENCES accounts(id),
 station_id text NOT NULL REFERENCES stations(id),
 mode text NOT NULL CHECK(mode IN ('ASAP','SCHEDULED')),
 method text NOT NULL CHECK(method IN ('COD','BALANCE')),
 slot_id text NOT NULL REFERENCES delivery_slots(id),
 address_snapshot jsonb NOT NULL CHECK(jsonb_typeof(address_snapshot)='object'),
 slot_snapshot jsonb NOT NULL CHECK(jsonb_typeof(slot_snapshot)='object'),
 priced_snapshot jsonb NOT NULL CHECK(jsonb_typeof(priced_snapshot)='object'),
 valid_until timestamptz NOT NULL,
 created_at timestamptz NOT NULL,
 CHECK(valid_until=created_at+interval '5 minutes')
);
CREATE TABLE orders (
 id text PRIMARY KEY DEFAULT gen_random_uuid()::text,
 quote_id text NOT NULL UNIQUE REFERENCES quotes(id),
 actor_id text NOT NULL REFERENCES accounts(id),
 station_id text NOT NULL REFERENCES stations(id),
 mode text NOT NULL CHECK(mode IN ('ASAP','SCHEDULED')),
 method text NOT NULL CHECK(method IN ('COD','BALANCE')),
 slot_id text NOT NULL REFERENCES delivery_slots(id) DEFERRABLE INITIALLY DEFERRED,
 address_snapshot jsonb NOT NULL CHECK(jsonb_typeof(address_snapshot)='object'),
 slot_snapshot jsonb NOT NULL CHECK(jsonb_typeof(slot_snapshot)='object'),
 total_fen bigint NOT NULL CHECK(total_fen BETWEEN 0 AND 9007199254740991),
 fee_fen bigint NOT NULL CHECK(fee_fen BETWEEN 0 AND total_fen),
 item_count integer NOT NULL CHECK(item_count BETWEEN 1 AND 999),
 status text NOT NULL CHECK(status IN ('SCHEDULED','PREPARING','READY_FOR_CLAIM','AWAITING_PICKUP','IN_DELIVERY','DELIVERED','CANCELED','RETURNING','RETURNED')),
 stock_status text NOT NULL DEFAULT 'UNSORTED' CHECK(stock_status IN ('UNSORTED','SORTED','RESTOCK_PENDING','RESTOCKED')),
 note text NOT NULL DEFAULT '' CHECK(length(note)<=200),
 overdue boolean NOT NULL DEFAULT false,
 created_at timestamptz NOT NULL,
 last_urge_at timestamptz,
 UNIQUE(id,station_id),
 UNIQUE(id,station_id,actor_id,method,total_fen),
 UNIQUE(id,slot_id,mode),
 CHECK((status <> 'SCHEDULED' OR mode='SCHEDULED')),
 CHECK(status <> 'CANCELED' OR stock_status IN ('RESTOCK_PENDING','RESTOCKED'))
);
CREATE INDEX orders_customer_idx ON orders(station_id,actor_id,id);
CREATE UNIQUE INDEX orders_active_cod_guard ON orders(actor_id) WHERE method='COD' AND status NOT IN ('DELIVERED','CANCELED','RETURNED');
CREATE TABLE order_items (
 order_id text NOT NULL REFERENCES orders(id),
 sku_id text NOT NULL REFERENCES skus(id),
 inventory_id text NOT NULL REFERENCES inventory(id),
 qty integer NOT NULL CHECK(qty BETWEEN 1 AND 999),
 name text NOT NULL CHECK(length(name) BETWEEN 1 AND 200),
 spec text NOT NULL CHECK(length(spec) BETWEEN 1 AND 60),
 units_per_pack integer NOT NULL CHECK(units_per_pack IN (1,3)),
 unit_price_fen bigint NOT NULL CHECK(unit_price_fen BETWEEN 0 AND 9007199254740991),
 line_total_fen bigint NOT NULL CHECK(line_total_fen BETWEEN 0 AND 9007199254740991),
 price_version bigint NOT NULL CHECK(price_version>0),
 promotion_id text NOT NULL,
 discount_bps integer NOT NULL CHECK(discount_bps BETWEEN 0 AND 10000),
 PRIMARY KEY(order_id,sku_id),
 CHECK(line_total_fen::numeric=unit_price_fen::numeric*qty)
);
CREATE TABLE order_events (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 order_id text NOT NULL REFERENCES orders(id),
 actor_id text NOT NULL,
 kind text NOT NULL CHECK(length(kind) BETWEEN 1 AND 64),
 reason_code text CHECK(reason_code ~ '^[A-Z0-9_]{1,64}$'),
 recorded_at timestamptz NOT NULL
);
CREATE TRIGGER order_events_append_only BEFORE UPDATE OR DELETE OR TRUNCATE ON order_events FOR EACH STATEMENT EXECUTE FUNCTION reject_audit_mutation();
CREATE TABLE delivery_tasks (
 id text PRIMARY KEY DEFAULT gen_random_uuid()::text,
 order_id text NOT NULL UNIQUE REFERENCES orders(id),
 rider_id text REFERENCES rider_profiles(account_id),
 state text NOT NULL DEFAULT 'OPEN' CHECK(state IN ('OPEN','ASSIGNED','PICKED_UP','RETURNING','DELIVERED','CLOSED')),
 created_at timestamptz NOT NULL,
 claimed_at timestamptz,
 picked_up_at timestamptz,
 finished_at timestamptz,
 CHECK((state IN ('ASSIGNED','PICKED_UP','RETURNING','DELIVERED')) <= (rider_id IS NOT NULL)),
 UNIQUE(id,rider_id)
);
CREATE UNIQUE INDEX delivery_tasks_one_active_rider ON delivery_tasks(rider_id) WHERE state IN ('ASSIGNED','PICKED_UP','RETURNING');
CREATE TABLE rider_earnings (
 id text PRIMARY KEY DEFAULT gen_random_uuid()::text,
 task_id text NOT NULL UNIQUE,
 rider_id text NOT NULL REFERENCES rider_profiles(account_id),
 amount_fen bigint NOT NULL CHECK(amount_fen BETWEEN 0 AND 9007199254740991),
 recorded_at timestamptz NOT NULL,
 FOREIGN KEY(task_id,rider_id) REFERENCES delivery_tasks(id,rider_id)
);
CREATE TRIGGER rider_earnings_append_only BEFORE UPDATE OR DELETE OR TRUNCATE ON rider_earnings FOR EACH STATEMENT EXECUTE FUNCTION reject_audit_mutation();
CREATE TABLE notifications (
 id text PRIMARY KEY DEFAULT gen_random_uuid()::text,
 station_id text NOT NULL REFERENCES stations(id),
 recipient_id text NOT NULL REFERENCES accounts(id),
 kind text NOT NULL CHECK(length(kind) BETWEEN 1 AND 64),
 task_id text NOT NULL REFERENCES delivery_tasks(id),
 outbox_job_id bigint REFERENCES outbox_jobs(id),
 created_at timestamptz NOT NULL,
 read_at timestamptz
);
CREATE UNIQUE INDEX notifications_job_recipient_unique ON notifications(outbox_job_id,recipient_id);
CREATE INDEX notifications_owner_idx ON notifications(station_id,recipient_id,id);
ALTER TABLE stock_reservations ADD CONSTRAINT stock_reservations_order_fk FOREIGN KEY(order_id) REFERENCES orders(id);
ALTER TABLE slot_reservations ADD CONSTRAINT slot_reservations_order_fk FOREIGN KEY(order_id,slot_id,mode) REFERENCES orders(id,slot_id,mode);
ALTER TABLE settlements ADD CONSTRAINT settlements_order_fk FOREIGN KEY(order_id,station_id,actor_id,method,amount_fen) REFERENCES orders(id,station_id,actor_id,method,total_fen);
ALTER TABLE wallets ADD CONSTRAINT wallets_actor_fk FOREIGN KEY(actor_id) REFERENCES accounts(id);
ALTER TABLE cod_receipts ADD CONSTRAINT cod_receipts_rider_fk FOREIGN KEY(rider_id) REFERENCES rider_profiles(account_id);
ALTER TABLE cod_batches ADD CONSTRAINT cod_batches_rider_fk FOREIGN KEY(rider_id) REFERENCES rider_profiles(account_id);
ALTER TABLE accounts ADD CONSTRAINT accounts_id_station_unique UNIQUE(id,station_id);
ALTER TABLE delivery_slots ADD CONSTRAINT delivery_slots_id_station_unique UNIQUE(id,station_id);
ALTER TABLE quotes ADD CONSTRAINT quotes_identity_unique UNIQUE(id,actor_id,station_id,mode,method,slot_id);
ALTER TABLE quotes ADD CONSTRAINT quotes_actor_station_fk FOREIGN KEY(actor_id,station_id) REFERENCES accounts(id,station_id);
ALTER TABLE quotes ADD CONSTRAINT quotes_slot_station_fk FOREIGN KEY(slot_id,station_id) REFERENCES delivery_slots(id,station_id);
ALTER TABLE orders ADD CONSTRAINT orders_quote_identity_fk FOREIGN KEY(quote_id,actor_id,station_id,mode,method,slot_id) REFERENCES quotes(id,actor_id,station_id,mode,method,slot_id);
ALTER TABLE delivery_tasks ADD CONSTRAINT delivery_tasks_order_rider_unique UNIQUE(order_id,rider_id);
ALTER TABLE cod_receipts ADD CONSTRAINT cod_receipts_task_assignment_fk FOREIGN KEY(order_id,rider_id) REFERENCES delivery_tasks(order_id,rider_id);
ALTER TABLE notifications ADD CONSTRAINT notifications_recipient_station_fk FOREIGN KEY(recipient_id,station_id) REFERENCES accounts(id,station_id);
ALTER TABLE refunds ADD CONSTRAINT refunds_operator_fk FOREIGN KEY(operator_id) REFERENCES accounts(id);
ALTER TABLE refunds ADD CONSTRAINT refunds_finance_fk FOREIGN KEY(finance_id) REFERENCES accounts(id);
ALTER TABLE cod_refund_payouts ADD CONSTRAINT cod_refund_payouts_finance_fk FOREIGN KEY(finance_id) REFERENCES accounts(id);
ALTER TABLE cod_batches ADD CONSTRAINT cod_batches_creator_fk FOREIGN KEY(creator_id) REFERENCES accounts(id);
ALTER TABLE cod_batch_verifications ADD CONSTRAINT cod_batch_verifications_verifier_fk FOREIGN KEY(verifier_id) REFERENCES accounts(id);
ALTER TABLE balance_import_batches ADD CONSTRAINT balance_import_batches_approver_fk FOREIGN KEY(approver_id) REFERENCES accounts(id);
ALTER TABLE balance_import_batches ADD CONSTRAINT balance_import_batches_executor_fk FOREIGN KEY(executor_id) REFERENCES accounts(id);
