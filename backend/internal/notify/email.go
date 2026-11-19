// Package notify delivers transactional email outside the order transaction.
package notify

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"mime"
	"net"
	"net/smtp"
	"strings"
	"time"
)

// Message is a plain-text transactional email. Only operational identifiers
// belong here: never a full dorm address, phone number or payment credential.
type Message struct {
	To, Subject, Text string
}

// Sender is the single approved delivery mechanism.
type Sender interface {
	Send(ctx context.Context, m Message) error
}

// SMTPConfig carries the dedicated sender identity. Secrets arrive only from
// controlled server configuration.
type SMTPConfig struct {
	Addr        string
	From        string
	Username    string
	Password    string
	ImplicitTLS bool
}

type SMTPSender struct {
	cfg     SMTPConfig
	timeout time.Duration
}

var ErrSMTPConfig = errors.New("incomplete or unsafe SMTP configuration")

// NewSMTPSender accepts only TLS-protected SMTP with a plain, dedicated sender
// address. Missing configuration must keep the capability switched off upstream.
func NewSMTPSender(cfg SMTPConfig) (*SMTPSender, error) {
	host, port, err := net.SplitHostPort(cfg.Addr)
	if err != nil || host == "" || port == "" || strings.ContainsAny(cfg.Addr, " 	\r\n") {
		return nil, ErrSMTPConfig
	}
	if _, _, err := net.SplitHostPort("x:" + port); err != nil {
		return nil, ErrSMTPConfig
	}
	if len(cfg.From) > 254 || strings.ContainsAny(cfg.From, " 	\r\n<>,;:") || strings.Count(cfg.From, "@") != 1 {
		return nil, ErrSMTPConfig
	}
	if (cfg.Username == "") != (cfg.Password == "") {
		return nil, ErrSMTPConfig
	}
	if strings.ContainsAny(cfg.Username+cfg.Password, "\r\n\x00") {
		return nil, ErrSMTPConfig
	}
	if !cfg.ImplicitTLS && port == "465" {
		return nil, ErrSMTPConfig
	}
	return &SMTPSender{cfg: cfg, timeout: 15 * time.Second}, nil
}

func (s *SMTPSender) dial(ctx context.Context) (net.Conn, error) {
	dialer := &net.Dialer{Timeout: s.timeout}
	if s.cfg.ImplicitTLS {
		tlsDialer := &tls.Dialer{NetDialer: dialer, Config: s.tlsConfig()}
		return tlsDialer.DialContext(ctx, "tcp", s.cfg.Addr)
	}
	return dialer.DialContext(ctx, "tcp", s.cfg.Addr)
}

func (s *SMTPSender) tlsConfig() *tls.Config {
	host, _, _ := net.SplitHostPort(s.cfg.Addr)
	return &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}
}

func (s *SMTPSender) Send(ctx context.Context, m Message) error {
	if err := m.valid(); err != nil {
		return err
	}
	host, _, _ := net.SplitHostPort(s.cfg.Addr)
	conn, err := s.dial(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	deadline := time.Now().Add(s.timeout)
	if ctxDeadline, ok := ctx.Deadline(); ok && ctxDeadline.Before(deadline) {
		deadline = ctxDeadline
	}
	_ = conn.SetDeadline(deadline)
	client, err := smtp.NewClient(conn, host)
	if err != nil {
		return err
	}
	defer client.Close()
	if !s.cfg.ImplicitTLS {
		if ok, _ := client.Extension("STARTTLS"); !ok {
			return errors.New("smtp server does not offer STARTTLS")
		}
		if err := client.StartTLS(s.tlsConfig()); err != nil {
			return err
		}
	}
	if s.cfg.Username != "" {
		if ok, _ := client.Extension("AUTH"); !ok {
			return errors.New("smtp server does not offer authentication")
		}
		if err := client.Auth(smtp.PlainAuth("", s.cfg.Username, s.cfg.Password, host)); err != nil {
			return err
		}
	}
	if err := client.Mail(s.cfg.From); err != nil {
		return err
	}
	if err := client.Rcpt(m.To); err != nil {
		return err
	}
	writer, err := client.Data()
	if err != nil {
		return err
	}
	if _, err := writer.Write(s.render(m)); err != nil {
		return err
	}
	if err := writer.Close(); err != nil {
		return err
	}
	return client.Quit()
}

func (m Message) valid() error {
	if m.To == "" || len(m.To) > 254 || strings.Count(m.To, "@") != 1 {
		return errors.New("invalid recipient address")
	}
	if m.Subject == "" || len([]rune(m.Subject)) > 120 || len(m.Text) > 4096 {
		return errors.New("invalid message content")
	}
	if strings.ContainsAny(m.To+m.Subject, "\r\n\x00") || strings.ContainsAny(m.To, " 	<>,;:") {
		return errors.New("invalid message header value")
	}
	return nil
}

// render builds a UTF-8 plain-text message. Header values were already checked
// for CR/LF, so the encoded-word subject cannot introduce new lines.
func (s *SMTPSender) render(m Message) []byte {
	var b strings.Builder
	b.WriteString("From: ")
	b.WriteString(s.cfg.From)
	b.WriteString("\r\nTo: ")
	b.WriteString(m.To)
	b.WriteString("\r\nSubject: ")
	b.WriteString(mime.BEncoding.Encode("utf-8", m.Subject))
	b.WriteString("\r\nDate: ")
	b.WriteString(time.Now().UTC().Format(time.RFC1123Z))
	b.WriteString("\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=utf-8\r\nContent-Transfer-Encoding: base64\r\n\r\n")
	encoded := base64.StdEncoding.EncodeToString([]byte(m.Text))
	for len(encoded) > 76 {
		b.WriteString(encoded[:76])
		b.WriteString("\r\n")
		encoded = encoded[76:]
	}
	b.WriteString(encoded)
	b.WriteString("\r\n")
	return []byte(b.String())
}

// VerificationGateway adapts the SMTP sender to the identity email port.
type VerificationGateway struct {
	Sender Sender
	From   string
}

func (g *VerificationGateway) SendVerification(ctx context.Context, to, code, purpose string) error {
	if g == nil || g.Sender == nil {
		return ErrSMTPConfig
	}
	return g.Sender.Send(ctx, VerificationMessage(g.From, to, code, purpose))
}

// VerificationMessage renders the one-time code mail. The code is never logged.
func VerificationMessage(from, to, code, purpose string) Message {
	action := map[string]string{"REGISTER": "邮箱注册", "LOGIN": "邮箱登录", "BIND": "绑定邮箱"}[purpose]
	if action == "" {
		action = "邮箱验证"
	}
	return Message{
		To:      to,
		Subject: "校零智选 · " + action + "验证码",
		Text:    fmt.Sprintf("你的%s验证码是 %s，10 分钟内有效且只能使用一次。\r\n如非本人操作，请忽略本邮件。\r\n\r\n校零智选（发件地址仅用于账号与订单通知，请勿直接回复。）", action, code),
	}
}
