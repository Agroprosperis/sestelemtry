package alerts

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"mime"
	"net"
	"net/mail"
	"net/smtp"
	"strconv"
	"strings"
	"time"
)

// TLS modes accepted by SMTPOptions.TLS. They mirror the
// `alerts.smtp.tls` config values.
const (
	TLSStartTLS = "starttls"
	TLSImplicit = "implicit"
	TLSNone     = "none"
)

// SMTPOptions describes the mail server. The daemon maps the config
// block onto this struct so the package stays independent of the YAML
// schema (same split as internal/weather and internal/oree).
type SMTPOptions struct {
	Host     string
	Port     int
	TLS      string
	Username string
	Password string
	From     string
	To       []string
	Timeout  time.Duration
}

// Mailer sends rendered messages over SMTP. It holds no connection:
// outages are rare, so a fresh connection per email is simpler and
// avoids babysitting a socket that idles for weeks.
type Mailer struct {
	opts SMTPOptions
	from string
}

// NewMailer validates the options and resolves the envelope sender.
func NewMailer(opts SMTPOptions) (*Mailer, error) {
	opts.Host = strings.TrimSpace(opts.Host)
	if opts.Host == "" {
		return nil, fmt.Errorf("alerts: smtp host is required")
	}
	if opts.Port < 1 || opts.Port > 65535 {
		return nil, fmt.Errorf("alerts: smtp port out of range: %d", opts.Port)
	}
	switch opts.TLS {
	case TLSStartTLS, TLSImplicit, TLSNone:
	default:
		return nil, fmt.Errorf("alerts: unsupported smtp tls mode %q", opts.TLS)
	}
	if len(opts.To) == 0 {
		return nil, fmt.Errorf("alerts: at least one recipient is required")
	}
	// net/smtp refuses PLAIN auth over an unencrypted link, and failing
	// here with a clear message beats surfacing that deep inside a send.
	if opts.Username != "" && opts.TLS == TLSNone {
		return nil, fmt.Errorf("alerts: smtp username requires tls %q or %q", TLSStartTLS, TLSImplicit)
	}
	if opts.Timeout <= 0 {
		opts.Timeout = 20 * time.Second
	}
	from, err := envelopeAddress(opts.From)
	if err != nil {
		return nil, err
	}
	return &Mailer{opts: opts, from: from}, nil
}

// Recipients returns the configured destination addresses.
func (m *Mailer) Recipients() []string {
	return append([]string(nil), m.opts.To...)
}

// Send delivers one message to every configured recipient.
func (m *Mailer) Send(ctx context.Context, msg Message) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	addr := net.JoinHostPort(m.opts.Host, strconv.Itoa(m.opts.Port))
	dialer := &net.Dialer{Timeout: m.opts.Timeout}

	var conn net.Conn
	var err error
	if m.opts.TLS == TLSImplicit {
		td := &tls.Dialer{NetDialer: dialer, Config: &tls.Config{ServerName: m.opts.Host}}
		conn, err = td.DialContext(ctx, "tcp", addr)
	} else {
		conn, err = dialer.DialContext(ctx, "tcp", addr)
	}
	if err != nil {
		return fmt.Errorf("alerts: dial %s: %w", addr, err)
	}
	// One deadline for the whole conversation: a relay that accepts the
	// connection and then stalls must not wedge the check loop.
	_ = conn.SetDeadline(time.Now().Add(m.opts.Timeout))
	defer conn.Close()

	client, err := smtp.NewClient(conn, m.opts.Host)
	if err != nil {
		return fmt.Errorf("alerts: smtp handshake: %w", err)
	}
	defer client.Close()

	if m.opts.TLS == TLSStartTLS {
		if err := client.StartTLS(&tls.Config{ServerName: m.opts.Host}); err != nil {
			return fmt.Errorf("alerts: starttls: %w", err)
		}
	}
	if m.opts.Username != "" {
		auth := smtp.PlainAuth("", m.opts.Username, m.opts.Password, m.opts.Host)
		if err := client.Auth(auth); err != nil {
			return fmt.Errorf("alerts: smtp auth: %w", err)
		}
	}
	if err := client.Mail(m.from); err != nil {
		return fmt.Errorf("alerts: MAIL FROM %s: %w", m.from, err)
	}
	for _, rcpt := range m.opts.To {
		to, err := envelopeAddress(rcpt)
		if err != nil {
			return err
		}
		if err := client.Rcpt(to); err != nil {
			return fmt.Errorf("alerts: RCPT TO %s: %w", to, err)
		}
	}
	w, err := client.Data()
	if err != nil {
		return fmt.Errorf("alerts: DATA: %w", err)
	}
	if _, err := w.Write(BuildRFC822(m.opts.From, m.opts.To, msg, time.Now())); err != nil {
		w.Close()
		return fmt.Errorf("alerts: write body: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("alerts: close body: %w", err)
	}
	if err := client.Quit(); err != nil {
		return fmt.Errorf("alerts: QUIT: %w", err)
	}
	return nil
}

// BuildRFC822 renders the wire form of the email.
//
// The body is base64-encoded rather than sent as raw 8-bit UTF-8: the
// text is Ukrainian, so every line is multi-byte, and base64 sidesteps
// both the 998-octet line limit and relays that still advertise no
// 8BITMIME.
func BuildRFC822(from string, to []string, msg Message, now time.Time) []byte {
	recipients := make([]string, 0, len(to))
	for _, addr := range to {
		recipients = append(recipients, headerAddress(addr))
	}

	var b strings.Builder
	fmt.Fprintf(&b, "From: %s\r\n", headerAddress(from))
	fmt.Fprintf(&b, "To: %s\r\n", strings.Join(recipients, ", "))
	fmt.Fprintf(&b, "Subject: %s\r\n", mime.QEncoding.Encode("utf-8", msg.Subject))
	fmt.Fprintf(&b, "Date: %s\r\n", now.Format(time.RFC1123Z))
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: text/plain; charset=\"utf-8\"\r\n")
	b.WriteString("Content-Transfer-Encoding: base64\r\n")
	b.WriteString("\r\n")

	encoded := base64.StdEncoding.EncodeToString([]byte(msg.Body))
	const lineLen = 76
	for len(encoded) > lineLen {
		b.WriteString(encoded[:lineLen])
		b.WriteString("\r\n")
		encoded = encoded[lineLen:]
	}
	if encoded != "" {
		b.WriteString(encoded)
		b.WriteString("\r\n")
	}
	return []byte(b.String())
}

// headerAddress renders an address for a From/To header, Q-encoding a
// non-ASCII display name ("СЕС Моніторинг <a@b>") as RFC 2047 requires.
// Unparseable values pass through untouched — a malformed header is a
// better outcome than dropping the alert.
func headerAddress(value string) string {
	value = strings.TrimSpace(value)
	parsed, err := mail.ParseAddress(value)
	if err != nil {
		return value
	}
	return parsed.String()
}

// envelopeAddress extracts the bare address from a header value that may
// carry a display name ("СЕС Моніторинг <alerts@example.com>"). SMTP
// commands only accept the address itself.
func envelopeAddress(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("alerts: empty email address")
	}
	parsed, err := mail.ParseAddress(value)
	if err != nil {
		return "", fmt.Errorf("alerts: invalid email address %q: %w", value, err)
	}
	return parsed.Address, nil
}
