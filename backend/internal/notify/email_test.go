package notify

import (
	"context"
	"strings"
	"testing"
)

func TestSMTPSenderRequiresTLSAndDedicatedSender(t *testing.T) {
	base := SMTPConfig{Addr: "smtp.example.test:587", From: "no-reply@mail.example.test"}
	if _, err := NewSMTPSender(base); err != nil {
		t.Fatalf("valid configuration rejected: %v", err)
	}
	bad := []SMTPConfig{
		{Addr: "smtp.example.test:465", From: "no-reply@mail.example.test"},
		{Addr: "smtp.example.test:587", From: ""},
		{Addr: "smtp.example.test:587", From: "no-reply@mail.example.test\r\nBcc: x@y.z"},
		{Addr: "smtp.example.test:587", From: "no-reply@mail.example.test", Username: "user"},
		{Addr: "no-port", From: "no-reply@mail.example.test"},
	}
	for _, cfg := range bad {
		if _, err := NewSMTPSender(cfg); err == nil {
			t.Fatalf("unsafe configuration accepted: %+v", cfg)
		}
	}
	if _, err := NewSMTPSender(SMTPConfig{Addr: "smtp.example.test:465", From: "no-reply@mail.example.test", ImplicitTLS: true}); err != nil {
		t.Fatalf("implicit TLS configuration rejected: %v", err)
	}
}

func TestMessageRejectsHeaderInjection(t *testing.T) {
	for _, m := range []Message{
		{To: "", Subject: "s", Text: "t"},
		{To: "a@b.co", Subject: "", Text: "t"},
		{To: "a@b.co\r\nBcc: c@d.co", Subject: "s", Text: "t"},
		{To: "a@b.co", Subject: "s\r\nBcc: c@d.co", Text: "t"},
		{To: "a@b.co", Subject: strings.Repeat("长", 121), Text: "t"},
	} {
		if err := m.valid(); err == nil {
			t.Fatalf("unsafe message accepted: %+v", m)
		}
	}
}

func TestVerificationMessageCarriesCodeButOrderMessageStaysMinimal(t *testing.T) {
	code := VerificationMessage("no-reply@mail.example.test", "student@example.test", "123456", "REGISTER")
	if !strings.Contains(code.Text, "123456") || !strings.Contains(code.Subject, "邮箱注册") {
		t.Fatalf("verification mail is missing the code or purpose: %+v", code)
	}
	placed := OrderMessage("ORDER_PLACED", "order-1")
	for _, forbidden := range []string{"宿舍", "房号", "联系电话", "PAYMENT", "card"} {
		if strings.Contains(placed.Text, forbidden) {
			t.Fatalf("order mail leaks %q", forbidden)
		}
	}
	if !strings.Contains(placed.Text, "order-1") {
		t.Fatal("order mail should identify the order")
	}
}

func TestOutboxNotificationIsInertWithoutConfiguration(t *testing.T) {
	var disabled *Outbox
	if err := disabled.NotifyOrder(context.Background(), nil, "ORDER_PLACED", "s", "o", "a"); err != nil {
		t.Fatalf("nil outbox should be inert: %v", err)
	}
	inert := &Outbox{}
	if err := inert.NotifyOrder(context.Background(), nil, "ORDER_PLACED", "s", "o", "a"); err != nil {
		t.Fatalf("disabled outbox should be inert: %v", err)
	}
}
