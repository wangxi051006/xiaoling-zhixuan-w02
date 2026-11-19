-- psql only. Read-only, approved isolated target and restricted evidence destination.
-- Required variables: station_id, local_date (YYYY-MM-DD).
-- Invoke via protected service/pgpass, never put DSN/password in argv/log.
\set ON_ERROR_STOP on
\pset pager off
BEGIN TRANSACTION ISOLATION LEVEL REPEATABLE READ READ ONLY;
SET LOCAL statement_timeout = '30s';
SET LOCAL lock_timeout = '3s';
SET LOCAL TIME ZONE 'Asia/Shanghai';
SELECT current_timestamp AS snapshot_at, current_database() AS database,
       current_schema() AS schema, current_user AS role,
       pg_is_in_recovery() AS in_recovery;
SELECT version, name, encode(digest,'hex') AS sha256, applied_at
FROM schema_migrations ORDER BY version;
SELECT count(*) AS station_matches FROM stations WHERE id=:'station_id';

-- Inventory reserved mismatch must be zero. Physical on-hand needs approved
-- pre-incident baseline + movement deltas + physical count (no invented zero baseline).
SELECT count(*) AS inventory_reserved_mismatches
FROM inventory i JOIN products p ON p.id=i.product_id
LEFT JOIN (SELECT inventory_id,sum(units) AS units FROM stock_reservations
           WHERE state='RESERVED' GROUP BY inventory_id) r ON r.inventory_id=i.id
WHERE p.station_id=:'station_id' AND
 (i.reserved<>coalesce(r.units,0) OR i.reserved<0 OR i.on_hand<i.reserved);
SELECT i.id AS inventory_id,i.on_hand,i.reserved,
       coalesce(sum(m.hand_delta),0)::text AS recorded_hand_delta,
       coalesce(sum(m.reserved_delta),0)::text AS recorded_reserved_delta,
       i.version,count(m.id) AS movement_count
FROM inventory i JOIN products p ON p.id=i.product_id
LEFT JOIN inventory_movements m ON m.inventory_id=i.id
WHERE p.station_id=:'station_id'
GROUP BY i.id ORDER BY i.id;

-- used is a budget counter. Do not release delivered/returned slots manually.
SELECT count(*) AS slot_mismatches
FROM delivery_slots s
LEFT JOIN (SELECT slot_id,count(*) AS n FROM slot_reservations
           WHERE released_at IS NULL GROUP BY slot_id) r ON r.slot_id=s.id
WHERE s.station_id=:'station_id' AND
 (s.used<>coalesce(r.n,0) OR s.used<0 OR s.used>s.capacity);
SELECT s.id,s.starts_at,s.ends_at,s.capacity,s.used,
       count(r.order_id) FILTER (WHERE r.released_at IS NULL) AS unreleased
FROM delivery_slots s LEFT JOIN slot_reservations r ON r.slot_id=s.id
WHERE s.station_id=:'station_id' GROUP BY s.id ORDER BY s.starts_at;

-- Wallet balance identity and debit/refund per-order correspondence.
WITH ledger AS (
 SELECT actor_id,sum(delta_fen) AS net,
 sum(amount_fen) FILTER(WHERE kind='IMPORT') AS imported,
 sum(amount_fen) FILTER(WHERE kind='DEBIT') AS debited,
 sum(amount_fen) FILTER(WHERE kind='REFUND') AS refunded
 FROM wallet_entries WHERE station_id=:'station_id' GROUP BY actor_id
), order_check AS (
 SELECT count(*) AS mismatches FROM settlements s
 LEFT JOIN wallet_entries d ON d.order_id=s.order_id AND d.kind='DEBIT'
 LEFT JOIN wallet_entries r ON r.order_id=s.order_id AND r.kind='REFUND'
 WHERE s.station_id=:'station_id' AND s.method='BALANCE'
 AND (d.id IS NULL OR ((r.id IS NOT NULL)<>(s.state='REFUNDED')))
)
SELECT count(*) AS wallets,
 count(*) FILTER(WHERE w.opening_fen+coalesce(l.net,0)<>w.balance_fen OR w.balance_fen<0) AS wallet_mismatches,
 (SELECT mismatches FROM order_check) AS wallet_order_mismatches,
 coalesce(sum(w.opening_fen),0)::text AS opening_fen,
 coalesce(sum(l.imported),0)::text AS import_fen,
 coalesce(sum(l.debited),0)::text AS debit_fen,
 coalesce(sum(l.refunded),0)::text AS refund_fen,
 coalesce(sum(w.balance_fen),0)::text AS closing_fen
FROM wallets w LEFT JOIN ledger l ON l.actor_id=w.actor_id WHERE w.station_id=:'station_id';

-- COD streams use their own Shanghai business dates, not a single order cohort.
WITH bounds AS (
 SELECT :'local_date'::date::timestamp AT TIME ZONE 'Asia/Shanghai' AS starts_at,
 (:'local_date'::date+1)::timestamp AT TIME ZONE 'Asia/Shanghai' AS ends_at
), receivables AS (
 SELECT sum(amount_fen) AS created,sum(amount_fen) FILTER(WHERE state='VOID') AS voided
 FROM settlements,bounds WHERE station_id=:'station_id' AND method='COD'
 AND created_at>=starts_at AND created_at<ends_at
), receipts AS (
 SELECT sum(r.amount_fen) AS collected,sum(r.amount_fen) FILTER(WHERE m.batch_id IS NULL) AS unbatched
 FROM cod_receipts r JOIN settlements s USING(order_id)
 LEFT JOIN cod_batch_receipts m ON m.receipt_id=r.order_id CROSS JOIN bounds
 WHERE s.station_id=:'station_id' AND r.collected_at>=starts_at AND r.collected_at<ends_at
), batches AS (
 SELECT sum(b.expected_fen) AS expected,sum(b.expected_fen) FILTER(WHERE v.batch_id IS NULL) AS pending,
 sum(v.actual_fen) AS counted,sum(v.difference_fen) AS difference
 FROM cod_batches b LEFT JOIN cod_batch_verifications v ON v.batch_id=b.id
 WHERE b.station_id=:'station_id' AND b.local_date=:'local_date'::date
), payouts AS (
 SELECT sum(s.amount_fen) AS paid FROM cod_refund_payouts p JOIN refunds r ON r.id=p.refund_id
 JOIN settlements s USING(order_id) CROSS JOIN bounds WHERE s.station_id=:'station_id'
 AND p.recorded_at>=starts_at AND p.recorded_at<ends_at
)
SELECT coalesce(created,0)::text AS receivable_fen,coalesce(voided,0)::text AS voided_fen,
 coalesce(collected,0)::text AS collected_fen,coalesce(unbatched,0)::text AS unbatched_fen,
 coalesce(expected,0)::text AS batch_expected_fen,coalesce(pending,0)::text AS pending_count_fen,
 coalesce(counted,0)::text AS counted_fen,coalesce(difference,0)::text AS difference_fen,
 coalesce(paid,0)::text AS payout_fen FROM receivables,receipts,batches,payouts;
SELECT count(*) AS cod_batch_amount_mismatches FROM cod_batches b
LEFT JOIN (SELECT m.batch_id,sum(r.amount_fen) AS total FROM cod_batch_receipts m
 JOIN cod_receipts r ON r.order_id=m.receipt_id GROUP BY m.batch_id) r ON r.batch_id=b.id
WHERE b.station_id=:'station_id' AND b.expected_fen<>coalesce(r.total,0);
SELECT b.id AS batch_id,b.local_date,b.expected_fen,v.actual_fen,v.difference_fen,
       (v.verifier_id IS NOT NULL AND v.creator_id=v.verifier_id) AS same_person
FROM cod_batches b LEFT JOIN cod_batch_verifications v ON v.batch_id=b.id
WHERE b.station_id=:'station_id' AND
 (v.batch_id IS NULL OR v.difference_fen<>0) ORDER BY b.local_date,b.id;

SELECT count(*) AS delivered_cod_receipt_mismatches FROM orders o
LEFT JOIN cod_receipts r ON r.order_id=o.id WHERE o.station_id=:'station_id'
AND o.method='COD' AND ((o.status='DELIVERED')<>(r.order_id IS NOT NULL));
SELECT count(*) AS earnings_state_mismatches FROM delivery_tasks t
JOIN orders o ON o.id=t.order_id LEFT JOIN rider_earnings e ON e.task_id=t.id
WHERE o.station_id=:'station_id' AND
 ((t.state='DELIVERED')<>(e.id IS NOT NULL) OR (e.id IS NOT NULL AND e.amount_fen<>o.fee_fen));
SELECT count(*) AS duplicate_active_cod_accounts FROM (
 SELECT actor_id FROM orders WHERE station_id=:'station_id' AND method='COD'
 AND status NOT IN('DELIVERED','CANCELED','RETURNED') GROUP BY actor_id HAVING count(*)>1) x;
SELECT count(*) AS duplicate_active_riders FROM (
 SELECT rider_id FROM delivery_tasks t JOIN orders o ON o.id=t.order_id
 WHERE o.station_id=:'station_id' AND t.state IN('ASSIGNED','PICKED_UP','RETURNING')
 GROUP BY rider_id HAVING count(*)>1) x;

-- Pending/future jobs are not automatically a failure; compare due age and pre/post sets.
SELECT kind,count(*) AS unfinished,
 count(*) FILTER(WHERE available_at<=current_timestamp) AS due,
 max(extract(epoch FROM current_timestamp-available_at)) FILTER(WHERE available_at<=current_timestamp) AS oldest_due_seconds,
 count(*) FILTER(WHERE lease_until<current_timestamp) AS expired_leases,
 max(attempts) AS max_attempts
FROM outbox_jobs WHERE station_id=:'station_id' AND finished_at IS NULL GROUP BY kind ORDER BY kind;
SELECT id,kind,dedupe_key,available_at,lease_until,attempts,finished_at
FROM outbox_jobs WHERE station_id=:'station_id' AND finished_at IS NULL ORDER BY available_at,id;
SELECT count(*) AS overdue_active_orders FROM orders o JOIN delivery_slots s ON s.id=o.slot_id
WHERE o.station_id=:'station_id' AND o.status NOT IN('DELIVERED','CANCELED','RETURNED')
AND s.ends_at<=current_timestamp;
SELECT count(*) AS due_scheduled_orders FROM orders o JOIN delivery_slots s ON s.id=o.slot_id
WHERE o.station_id=:'station_id' AND o.status='SCHEDULED' AND s.dispatch_at<=current_timestamp;
SELECT count(*) AS audit_rows,min(recorded_at) AS first_at,max(recorded_at) AS last_at
FROM audit_events WHERE station_id=:'station_id';
COMMIT;
