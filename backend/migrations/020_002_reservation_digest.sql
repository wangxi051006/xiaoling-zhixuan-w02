-- Existing reservations can still be consumed, released and inspected. A legacy
-- reservation without its original request cannot safely claim an equal replay.
ALTER TABLE stock_reservations ADD COLUMN request_digest bytea
    CHECK (request_digest IS NULL OR octet_length(request_digest) = 32);
