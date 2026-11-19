CREATE TABLE delivery_slots (
    id text PRIMARY KEY CHECK (id <> ''),
    station_id text NOT NULL REFERENCES stations(id),
    starts_at timestamptz NOT NULL,
    ends_at timestamptz NOT NULL CHECK (ends_at = starts_at + interval '15 minutes'),
    dispatch_at timestamptz NOT NULL CHECK (dispatch_at <= starts_at AND dispatch_at >= starts_at - interval '120 minutes'),
    capacity bigint NOT NULL CHECK (capacity BETWEEN 0 AND 9007199254740991),
    used bigint NOT NULL DEFAULT 0 CHECK (used BETWEEN 0 AND capacity),
    accepting boolean NOT NULL DEFAULT true,
    version bigint NOT NULL DEFAULT 1 CHECK (version > 0),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (station_id, starts_at)
);
CREATE INDEX delivery_slots_open_idx ON delivery_slots (station_id, ends_at) WHERE accepting;

CREATE TABLE slot_reservations (
    order_id text PRIMARY KEY CHECK (order_id <> ''),
    slot_id text NOT NULL REFERENCES delivery_slots(id),
    mode text NOT NULL CHECK (mode IN ('ASAP', 'SCHEDULED')),
    reserved_at timestamptz NOT NULL,
    released_at timestamptz CHECK (released_at >= reserved_at)
);
CREATE INDEX slot_reservations_slot_idx ON slot_reservations (slot_id);
