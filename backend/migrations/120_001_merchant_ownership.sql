CREATE TABLE merchants (
    id text PRIMARY KEY CHECK (length(id) BETWEEN 1 AND 128),
    station_id text NOT NULL REFERENCES stations(id),
    name text NOT NULL CHECK (length(btrim(name)) BETWEEN 1 AND 200),
    UNIQUE (id, station_id)
);

ALTER TABLE products ADD COLUMN merchant_id text,
    ADD FOREIGN KEY (merchant_id, station_id) REFERENCES merchants(id, station_id);
ALTER TABLE quotes ADD COLUMN merchant_id text,
    ADD FOREIGN KEY (merchant_id, station_id) REFERENCES merchants(id, station_id),
    ADD UNIQUE (id, merchant_id);
ALTER TABLE orders ADD COLUMN merchant_id text,
    ADD FOREIGN KEY (merchant_id, station_id) REFERENCES merchants(id, station_id),
    ADD FOREIGN KEY (quote_id, merchant_id) REFERENCES quotes(id, merchant_id);

CREATE INDEX products_merchant_idx ON products(merchant_id, id) WHERE merchant_id IS NOT NULL;
CREATE INDEX orders_merchant_idx ON orders(merchant_id, id) WHERE merchant_id IS NOT NULL;

CREATE TABLE merchant_staff (
    account_id text PRIMARY KEY,
    station_id text NOT NULL,
    merchant_id text NOT NULL,
    FOREIGN KEY (account_id, station_id) REFERENCES accounts(id, station_id),
    FOREIGN KEY (merchant_id, station_id) REFERENCES merchants(id, station_id)
);

ALTER TABLE role_bindings DROP CONSTRAINT role_bindings_role_check,
    ADD CHECK (role IN ('CUSTOMER','RIDER','OPERATOR','FINANCE','SECURITY_ADMIN','MERCHANT'));
ALTER TABLE sessions DROP CONSTRAINT sessions_entry_check,
    ADD CHECK (entry IN ('user','rider','admin','merchant'));
ALTER TABLE staff_login_flows DROP CONSTRAINT staff_login_flows_entry_check,
    ADD CHECK (entry IN ('rider','admin','merchant'));
