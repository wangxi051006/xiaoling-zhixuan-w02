CREATE TABLE products (
    id text PRIMARY KEY CHECK (length(id) BETWEEN 1 AND 128),
    station_id text NOT NULL REFERENCES stations(id),
    name text NOT NULL CHECK (length(name) BETWEEN 1 AND 200),
    description text NOT NULL DEFAULT '' CHECK (length(description) <= 2000),
    category text NOT NULL DEFAULT 'uncategorized' CHECK (length(category) BETWEEN 1 AND 80),
    on_sale boolean NOT NULL DEFAULT false,
    version bigint NOT NULL DEFAULT 1 CHECK (version BETWEEN 1 AND 9007199254740991),
    promotion_id text,
    promotion_starts_at timestamptz,
    promotion_ends_at timestamptz,
    promotion_tiers jsonb,
    CHECK ((promotion_id IS NULL AND promotion_starts_at IS NULL AND promotion_ends_at IS NULL AND promotion_tiers IS NULL)
        OR (promotion_id IS NOT NULL AND promotion_starts_at IS NOT NULL AND promotion_ends_at IS NOT NULL
          AND promotion_ends_at > promotion_starts_at AND promotion_tiers IS NOT NULL AND jsonb_typeof(promotion_tiers) = 'array'))
);
CREATE INDEX products_station_id_idx ON products(station_id, id);

CREATE TABLE inventory (
    id text PRIMARY KEY CHECK (length(id) BETWEEN 1 AND 128),
    product_id text NOT NULL UNIQUE REFERENCES products(id),
    on_hand bigint NOT NULL DEFAULT 0 CHECK (on_hand BETWEEN 0 AND 9007199254740991),
    reserved bigint NOT NULL DEFAULT 0 CHECK (reserved BETWEEN 0 AND on_hand),
    reorder_point bigint NOT NULL DEFAULT 0 CHECK (reorder_point BETWEEN 0 AND 9007199254740991),
    version bigint NOT NULL DEFAULT 1 CHECK (version BETWEEN 1 AND 9007199254740991)
);

CREATE TABLE skus (
    id text PRIMARY KEY CHECK (length(id) BETWEEN 1 AND 128),
    product_id text NOT NULL REFERENCES products(id),
    label text NOT NULL CHECK (length(label) BETWEEN 1 AND 60),
    units_per_pack integer NOT NULL CHECK (units_per_pack IN (1,3)),
    unit_price_fen bigint NOT NULL CHECK (unit_price_fen BETWEEN 0 AND 9007199254740991),
    active boolean NOT NULL DEFAULT true
);
CREATE INDEX skus_product_id_idx ON skus(product_id, id);

CREATE TABLE stock_reservations (
    order_id text NOT NULL CHECK (length(order_id) BETWEEN 1 AND 128),
    inventory_id text NOT NULL REFERENCES inventory(id),
    units integer NOT NULL CHECK (units > 0),
    state text NOT NULL DEFAULT 'RESERVED' CHECK (state IN ('RESERVED','CONSUMED','RELEASED','RETURNED','CANCELED_RESTOCKED')),
    final_units integer CHECK (final_units BETWEEN 0 AND units),
    PRIMARY KEY (order_id, inventory_id),
    CHECK ((state IN ('RETURNED','CANCELED_RESTOCKED')) = (final_units IS NOT NULL))
);

CREATE TABLE inventory_movements (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    inventory_id text NOT NULL REFERENCES inventory(id),
    order_id text,
    kind text NOT NULL CHECK (kind IN ('RESERVED','CONSUMED','RELEASED','RETURNED','CANCELED_RESTOCKED','ADJUSTED')),
    hand_delta integer NOT NULL,
    reserved_delta integer NOT NULL,
    inventory_version bigint NOT NULL CHECK (inventory_version > 0),
    actor_id text,
    reason_code text,
    recorded_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (inventory_id, inventory_version),
    UNIQUE (order_id, inventory_id, kind),
    FOREIGN KEY (order_id, inventory_id) REFERENCES stock_reservations(order_id, inventory_id),
    CHECK ((kind = 'ADJUSTED') = (order_id IS NULL)),
    CHECK (kind <> 'ADJUSTED' OR (actor_id IS NOT NULL AND reason_code IS NOT NULL AND hand_delta <> 0 AND reserved_delta = 0))
);
CREATE FUNCTION reject_inventory_movement_mutation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'inventory_movements is append-only' USING ERRCODE = '42501';
END
$$;
CREATE TRIGGER inventory_movements_append_only BEFORE UPDATE OR DELETE OR TRUNCATE ON inventory_movements
FOR EACH STATEMENT EXECUTE FUNCTION reject_inventory_movement_mutation();
