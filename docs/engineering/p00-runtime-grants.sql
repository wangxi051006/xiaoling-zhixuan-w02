-- Replace @schema@ and @runtime@ with quoted identifiers selected by the operator.
-- Execute as schema owner after the 000 migration. The runtime role is not the owner.
REVOKE CREATE ON SCHEMA @schema@ FROM PUBLIC;
GRANT USAGE ON SCHEMA @schema@ TO @runtime@;
GRANT SELECT ON @schema@.stations TO @runtime@;
GRANT SELECT, INSERT, UPDATE, DELETE ON @schema@.idempotency_keys TO @runtime@;
GRANT INSERT ON @schema@.audit_events TO @runtime@;
GRANT SELECT, INSERT, UPDATE ON @schema@.outbox_jobs TO @runtime@;
GRANT USAGE ON SEQUENCE @schema@.audit_events_id_seq, @schema@.outbox_jobs_id_seq TO @runtime@;
