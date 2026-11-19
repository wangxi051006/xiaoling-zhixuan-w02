package notify

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"campus.local/xiaoling/internal/contract"
	"campus.local/xiaoling/internal/platform"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	maxEmailAttempts = 5
	emailLease       = 60 * time.Second
	emailRetryBase   = 30 * time.Second
)

var (
	ErrEmailJobInvalid = errors.New("invalid email job request")
)

type NoticeKind string

const (
	KindOrderPlaced      = NoticeKind("ORDER_PLACED")
	KindOrderCanceled    = NoticeKind("ORDER_CANCELED")
	KindDeliveryIncident = NoticeKind("DELIVERY_INCIDENT")
)

// Outbox writes email jobs inside the business transaction and delivers them
// afterwards, so a failing mail server can never roll an order back.
type Outbox struct {
	Pool    *pgxpool.Pool
	Clock   contract.Clock
	Sender  Sender
	Enabled bool
}

// NotifyOrder implements the fulfillment notification port. It only records a
// row for the order owner's verified mailbox; delivery happens in RunDue.
func (o *Outbox) NotifyOrder(ctx context.Context, tx pgx.Tx, kind, stationID, orderID, accountID string) error {
	if o == nil || !o.Enabled || o.Pool == nil || o.Clock == nil {
		return nil
	}
	switch NoticeKind(kind) {
	case KindOrderPlaced, KindOrderCanceled, KindDeliveryIncident:
	default:
		return ErrEmailJobInvalid
	}
	if stationID == "" || orderID == "" || accountID == "" {
		return ErrEmailJobInvalid
	}
	var email string
	err := tx.QueryRow(ctx, `SELECT email FROM accounts WHERE id = $1 AND enabled AND email IS NOT NULL AND email_verified_at IS NOT NULL`, accountID).Scan(&email)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	payload, err := json.Marshal(map[string]string{"order_id": orderID})
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO email_outbox
		(station_id, kind, dedupe_key, recipient_account_id, recipient_email, order_id, payload, available_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8) ON CONFLICT (kind, dedupe_key) DO NOTHING`,
		stationID, kind, orderID+":"+accountID, accountID, email, orderID, payload, o.Clock.Now().UTC())
	return err
}

type emailJob struct {
	ID               int64
	Token, StationID string
	Kind, DedupeKey  string
	AccountID, To    string
	Order            string
}

// RunDue claims a bounded batch under short leases, sends outside the order lock
// and records either delivery or a final failure after limited retries.
func (o *Outbox) RunDue(ctx context.Context, limit int) (int, error) {
	if o == nil || !o.Enabled || o.Pool == nil || o.Clock == nil || o.Sender == nil {
		return 0, nil
	}
	if limit < 1 || limit > 100 {
		return 0, ErrEmailJobInvalid
	}
	now := o.Clock.Now().UTC()
	jobs := []emailJob{}
	err := platform.InTransaction(ctx, o.Pool, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT id,station_id,kind,dedupe_key,recipient_account_id,recipient_email,order_id
			FROM email_outbox
			WHERE sent_at IS NULL AND failed_at IS NULL AND available_at <= $1 AND (lease_until IS NULL OR lease_until <= $1)
			ORDER BY available_at,id LIMIT $2 FOR UPDATE SKIP LOCKED`, now, limit)
		if err != nil {
			return err
		}
		jobs, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (emailJob, error) {
			var j emailJob
			e := r.Scan(&j.ID, &j.StationID, &j.Kind, &j.DedupeKey, &j.AccountID, &j.To, &j.Order)
			j.Token = jobToken()
			return j, e
		})
		if err != nil {
			return err
		}
		for _, j := range jobs {
			if _, err := tx.Exec(ctx, `UPDATE email_outbox SET lease_token=$2, lease_until=$3 WHERE id=$1`, j.ID, j.Token, now.Add(emailLease)); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	sent := 0
	var firstErr error
	for _, j := range jobs {
		if err := ctx.Err(); err != nil {
			return sent, err
		}
		message := OrderMessage(j.Kind, j.Order)
		message.To = j.To
		sendErr := o.Sender.Send(ctx, message)
		err := platform.InTransaction(ctx, o.Pool, func(tx pgx.Tx) error {
			var owned bool
			if err := tx.QueryRow(ctx, `SELECT lease_token=$2 AND lease_until>$3 AND sent_at IS NULL AND failed_at IS NULL
				FROM email_outbox WHERE id=$1 FOR UPDATE`, j.ID, j.Token, o.Clock.Now().UTC()).Scan(&owned); err != nil {
				return err
			}
			if !owned {
				return nil
			}
			attempts := 1
			if err := tx.QueryRow(ctx, `UPDATE email_outbox SET attempts = attempts + 1, lease_token = NULL, lease_until = NULL
				WHERE id = $1 RETURNING attempts`, j.ID).Scan(&attempts); err != nil {
				return err
			}
			at := o.Clock.Now().UTC()
			if sendErr == nil {
				_, err := tx.Exec(ctx, `UPDATE email_outbox SET sent_at = $2, last_error = NULL WHERE id = $1`, j.ID, at)
				return err
			}
			if attempts >= maxEmailAttempts {
				_, err := tx.Exec(ctx, `UPDATE email_outbox SET failed_at = $2, last_error = $3 WHERE id = $1`, j.ID, at, shortError(sendErr))
				return err
			}
			_, err := tx.Exec(ctx, `UPDATE email_outbox SET available_at = $2, last_error = $3 WHERE id = $1`, j.ID, at.Add(emailRetryBase*time.Duration(attempts)), shortError(sendErr))
			return err
		})
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		if sendErr == nil {
			sent++
		} else if firstErr == nil {
			firstErr = sendErr
		}
	}
	return sent, firstErr
}

// OrderMessage renders an order notice without address, phone or payment data.
func OrderMessage(kind, orderID string) Message {
	subject, body := "校零智选 · 订单更新", "订单 "+orderID+" 状态已更新，请登录校零智选查看详情。"
	switch kind {
	case "ORDER_PLACED":
		subject, body = "校零智选 · 下单成功", "订单 "+orderID+" 已创建，配送安排以应用内订单状态为准。"
	case "ORDER_CANCELED":
		subject, body = "校零智选 · 订单已取消", "订单 "+orderID+" 已取消，退款或归还进度请登录应用核对。"
	case "DELIVERY_INCIDENT":
		subject, body = "校零智选 · 配送异常提示", "订单 "+orderID+" 出现配送异常，运营人员会跟进处理，请以应用内状态为准。"
	}
	return Message{Subject: subject, Text: body + "\r\n\r\n本邮件只用于账号与订单通知，请勿直接回复。"}
}

func shortError(err error) string {
	message := err.Error()
	if len(message) > 200 {
		message = message[:200]
	}
	return message
}

func jobToken() string {
	var raw [16]byte
	_, _ = rand.Read(raw[:])
	return hex.EncodeToString(raw[:])
}
