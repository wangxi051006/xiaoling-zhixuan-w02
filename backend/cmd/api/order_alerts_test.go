package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"testing"

	"campus.local/xiaoling/internal/fulfillment"
)

func TestOrderAlertsLogStableIDAndRecipientsOnce(t *testing.T) {
	var output bytes.Buffer
	sink := newOrderAlertLog(slog.New(slog.NewJSONHandler(&output, nil)))
	alert := fulfillment.Alert{IdempotencyKey: "fulfillment-outbox:12", Kind: "ORDER_URGE", StationID: "s1", OrderID: "o1", RecipientIDs: []string{"operator1"}}
	for range 2 {
		if err := sink.SendAlert(t.Context(), alert); err != nil {
			t.Fatal(err)
		}
	}
	decoder := json.NewDecoder(&output)
	var event map[string]any
	if err := decoder.Decode(&event); err != nil {
		t.Fatalf("alert was not delivered: %v", err)
	}
	if event["msg"] != "order_operational_alert" || event["event_id"] != alert.IdempotencyKey || event["kind"] != alert.Kind || event["station_id"] != "s1" || event["order_id"] != "o1" {
		t.Fatalf("wrong alert envelope: %v", event)
	}
	recipients, ok := event["recipient_ids"].([]any)
	if !ok || len(recipients) != 1 || recipients[0] != "operator1" {
		t.Fatalf("wrong recipients: %v", event)
	}
	if len(event) != 8 {
		t.Fatalf("unexpected alert fields: %v", event)
	}
	if decoder.Decode(&event) == nil {
		t.Fatal("same stable event ID emitted twice")
	}
}

type failingAlertWriter struct {
	bytes.Buffer
	fail bool
}

func (w *failingAlertWriter) Write(p []byte) (int, error) {
	if w.fail {
		return 0, errors.New("synthetic log write failure")
	}
	return w.Buffer.Write(p)
}

func TestOrderAlertsFailedWriteRemainsRetryableAndCanceledWriteFails(t *testing.T) {
	output := &failingAlertWriter{fail: true}
	sink := newOrderAlertLog(slog.New(slog.NewJSONHandler(output, nil)))
	alert := fulfillment.Alert{IdempotencyKey: "fulfillment-outbox:13", Kind: "OVERDUE_ALERT", StationID: "s", OrderID: "o", RecipientIDs: []string{"op"}}
	if sink.SendAlert(t.Context(), alert) == nil {
		t.Fatal("lost failed alert write")
	}
	output.fail = false
	if err := sink.SendAlert(t.Context(), alert); err != nil {
		t.Fatal(err)
	}
	if output.Len() == 0 {
		t.Fatal("failed write was incorrectly deduplicated")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	alert.IdempotencyKey = "fulfillment-outbox:14"
	if !errors.Is(sink.SendAlert(ctx, alert), context.Canceled) {
		t.Fatal("canceled send must be retryable")
	}
}
