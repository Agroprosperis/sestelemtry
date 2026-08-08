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
	"slices"
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

// Mailer sends rendered messages over SMTP. It holds no connection:
// outages are rare, so a fresh connection per email is simpler and
// avoids babysitting a socket that idles for weeks.
//
// Recipients are a per-send argument rather than mailer state, because
// one server delivers to several address lists: each organization can
// replace the default recipients with its own.
type Mailer struct {
	smtp     SMTPSettings
	password string
	from     string
	timeout  time.Duration
}

// NewMailer validates the server settings and resolves the envelope
// sender. The password is passed separately because it is stored apart
// from the rest of the settings and never travels with them.
func NewMailer(settings SMTPSettings, password string) (*Mailer, error) {
	settings.Host = strings.TrimSpace(settings.Host)
	if settings.Host == "" {
		return nil, fmt.Errorf("alerts: smtp host is required")
	}
	if settings.Port < 1 || settings.Port > 65535 {
		return nil, fmt.Errorf("alerts: smtp port out of range: %d", settings.Port)
	}
	switch settings.TLS {
	case TLSStartTLS, TLSImplicit, TLSNone:
	default:
		return nil, fmt.Errorf("alerts: unsupported smtp tls mode %q", settings.TLS)
	}
	timeout := settings.Timeout.Duration()
	if timeout <= 0 {
		timeout = 20 * time.Second
	}
	from, err := envelopeAddress(settings.From)
	if err != nil {
		return nil, err
	}
	return &Mailer{smtp: settings, password: password, from: from, timeout: timeout}, nil
}

// Send delivers one message to the given recipients.
func (m *Mailer) Send(ctx context.Context, to []string, msg Message) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(to) == 0 {
		return fmt.Errorf("alerts: at least one recipient is required")
	}
	addr := net.JoinHostPort(m.smtp.Host, strconv.Itoa(m.smtp.Port))
	dialer := &net.Dialer{Timeout: m.timeout}

	var conn net.Conn
	var err error
	if m.smtp.TLS == TLSImplicit {
		td := &tls.Dialer{NetDialer: dialer, Config: &tls.Config{ServerName: m.smtp.Host}}
		conn, err = td.DialContext(ctx, "tcp", addr)
	} else {
		conn, err = dialer.DialContext(ctx, "tcp", addr)
	}
	if err != nil {
		return fmt.Errorf("alerts: dial %s: %w", addr, err)
	}
	// One deadline for the whole conversation: a relay that accepts the
	// connection and then stalls must not wedge the check loop.
	_ = conn.SetDeadline(time.Now().Add(m.timeout))
	defer conn.Close()

	client, err := smtp.NewClient(conn, m.smtp.Host)
	if err != nil {
		return fmt.Errorf("alerts: smtp handshake: %w", err)
	}
	defer client.Close()

	if m.smtp.TLS == TLSStartTLS {
		if err := client.StartTLS(&tls.Config{ServerName: m.smtp.Host}); err != nil {
			return fmt.Errorf("alerts: starttls: %w", err)
		}
	}
	if m.smtp.Username != "" {
		if err := m.authenticate(client); err != nil {
			return err
		}
	}
	if err := client.Mail(m.from); err != nil {
		return fmt.Errorf("alerts: MAIL FROM %s: %w", m.from, err)
	}
	for _, rcpt := range to {
		parsed, err := envelopeAddress(rcpt)
		if err != nil {
			return err
		}
		if err := client.Rcpt(parsed); err != nil {
			return fmt.Errorf("alerts: RCPT TO %s: %w", parsed, err)
		}
	}
	w, err := client.Data()
	if err != nil {
		return fmt.Errorf("alerts: DATA: %w", err)
	}
	if _, err := w.Write(BuildRFC822(m.smtp.From, to, msg, time.Now())); err != nil {
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

// authenticate logs in, if the relay actually wants credentials.
//
// Two accommodations for the relays these sites run. One: a relay that
// authorizes by source IP advertises no AUTH extension at all, and a
// username left in the settings form must not turn that into a failed
// send. Two: an internal relay on port 25 without TLS is a legitimate
// setup on a private network, but net/smtp refuses to hand PLAIN
// credentials to an unencrypted connection — since the operator chose
// "no encryption" deliberately, we send them rather than report the
// relay as unusable.
func (m *Mailer) authenticate(client *smtp.Client) error {
	supported, mechanisms := client.Extension("AUTH")
	if !supported {
		return nil
	}
	offered := strings.Fields(strings.ToUpper(mechanisms))
	var auth smtp.Auth
	switch {
	case slices.Contains(offered, "PLAIN"):
		if m.smtp.TLS == TLSNone {
			auth = plainAuthNoTLS{username: m.smtp.Username, password: m.password}
		} else {
			auth = smtp.PlainAuth("", m.smtp.Username, m.password, m.smtp.Host)
		}
	case slices.Contains(offered, "LOGIN"):
		auth = &loginAuth{username: m.smtp.Username, password: m.password}
	default:
		return fmt.Errorf("alerts: smtp server offers no supported auth mechanism (advertises %q)", mechanisms)
	}
	if err := client.Auth(auth); err != nil {
		return fmt.Errorf("alerts: smtp auth: %w", err)
	}
	return nil
}

// plainAuthNoTLS is PLAIN without net/smtp's refusal to send
// credentials over an unencrypted link. Used only when the operator
// selected "no encryption" for the mail server.
type plainAuthNoTLS struct {
	username string
	password string
}

func (a plainAuthNoTLS) Start(*smtp.ServerInfo) (string, []byte, error) {
	return "PLAIN", []byte("\x00" + a.username + "\x00" + a.password), nil
}

func (a plainAuthNoTLS) Next(_ []byte, more bool) ([]byte, error) {
	if more {
		return nil, fmt.Errorf("alerts: unexpected server challenge during PLAIN auth")
	}
	return nil, nil
}

// loginAuth implements the non-standard LOGIN mechanism, which Exchange
// and older relays offer where PLAIN is absent. net/smtp has no
// built-in for it.
type loginAuth struct {
	username string
	password string
}

func (a *loginAuth) Start(*smtp.ServerInfo) (string, []byte, error) {
	return "LOGIN", nil, nil
}

func (a *loginAuth) Next(fromServer []byte, more bool) ([]byte, error) {
	if !more {
		return nil, nil
	}
	prompt := strings.ToLower(strings.TrimSpace(strings.TrimSuffix(string(fromServer), ":")))
	switch prompt {
	case "username":
		return []byte(a.username), nil
	case "password":
		return []byte(a.password), nil
	}
	return nil, fmt.Errorf("alerts: unexpected LOGIN challenge %q", fromServer)
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
