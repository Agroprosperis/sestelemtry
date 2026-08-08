package alerts

import (
	"bufio"
	"context"
	"encoding/base64"
	"net"
	"strings"
	"testing"
	"time"
)

func TestNewMailerRejectsBadOptions(t *testing.T) {
	base := SMTPSettings{
		Host: "smtp.example.com",
		Port: 587,
		TLS:  TLSStartTLS,
		From: "alerts@example.com",
	}
	cases := map[string]func(o *SMTPSettings){
		"no host":     func(o *SMTPSettings) { o.Host = "" },
		"bad port":    func(o *SMTPSettings) { o.Port = 0 },
		"unknown tls": func(o *SMTPSettings) { o.TLS = "ssl" },
		"bad from":    func(o *SMTPSettings) { o.From = "not an address" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			settings := base
			mutate(&settings)
			if _, err := NewMailer(settings, ""); err == nil {
				t.Fatal("expected an error, got nil")
			}
		})
	}
}

func TestSendRejectsEmptyRecipients(t *testing.T) {
	m, err := NewMailer(SMTPSettings{
		Host: "smtp.example.com", Port: 587, TLS: TLSStartTLS, From: "alerts@example.com",
	}, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Send(context.Background(), nil, Message{Subject: "s", Body: "b"}); err == nil {
		t.Fatal("expected an error for an empty recipient list")
	}
}

func TestNewMailerAcceptsDisplayName(t *testing.T) {
	m, err := NewMailer(SMTPSettings{
		Host: "smtp.example.com",
		Port: 465,
		TLS:  TLSImplicit,
		From: "СЕС Моніторинг <alerts@example.com>",
	}, "")
	if err != nil {
		t.Fatalf("NewMailer: %v", err)
	}
	// SMTP commands take the bare address; the display name belongs in
	// the header only.
	if m.from != "alerts@example.com" {
		t.Fatalf("envelope sender = %q", m.from)
	}
}

func TestBuildRFC822(t *testing.T) {
	msg := Message{Subject: "СЕС: втрачено звʼязок — Кролевецький елеватор", Body: "рядок один\nрядок два\n"}
	raw := string(BuildRFC822("СЕС Моніторинг <alerts@example.com>", []string{"ops@example.com", "boss@example.com"}, msg, now))

	head, body, found := strings.Cut(raw, "\r\n\r\n")
	if !found {
		t.Fatalf("no header/body separator:\n%s", raw)
	}
	if !strings.Contains(head, "To: <ops@example.com>, <boss@example.com>") {
		t.Fatalf("headers:\n%s", head)
	}
	// A non-ASCII display name and subject must be RFC 2047 encoded.
	if !strings.Contains(head, "From: =?utf-8?q?") || !strings.Contains(head, "<alerts@example.com>") {
		t.Fatalf("headers:\n%s", head)
	}
	if !strings.Contains(head, "Subject: =?utf-8?q?") {
		t.Fatalf("headers:\n%s", head)
	}
	if !strings.Contains(head, "Content-Transfer-Encoding: base64") {
		t.Fatalf("headers:\n%s", head)
	}

	for _, line := range strings.Split(strings.TrimRight(body, "\r\n"), "\r\n") {
		if len(line) > 76 {
			t.Fatalf("base64 line exceeds 76 chars: %d", len(line))
		}
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(body, "\r\n", ""))
	if err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if string(decoded) != msg.Body {
		t.Fatalf("decoded body = %q, want %q", decoded, msg.Body)
	}
}

// TestSendDeliversToServer drives a real SMTP conversation against a
// throwaway listener: the transcript is the part of this package that
// unit tests of the message builder cannot cover.
func TestSendDeliversToServer(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skipf("cannot listen: %v", err)
	}
	defer ln.Close()

	done := make(chan smtpSession, 1)
	go func() { done <- serveSMTP(ln) }()

	host, port, err := net.SplitHostPort(ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	m, err := NewMailer(SMTPSettings{
		Host:    host,
		Port:    atoi(t, port),
		TLS:     TLSNone,
		From:    "alerts@example.com",
		Timeout: Duration(5 * time.Second),
	}, "")
	if err != nil {
		t.Fatal(err)
	}
	to := []string{"ops@example.com"}
	if err := m.Send(context.Background(), to, Message{Subject: "тест", Body: "тіло\n"}); err != nil {
		t.Fatalf("Send: %v", err)
	}

	res := <-done
	if res.err != nil {
		t.Fatalf("server: %v", res.err)
	}
	transcript := strings.Join(res.transcript, "\n")
	for _, want := range []string{"MAIL FROM:<alerts@example.com>", "RCPT TO:<ops@example.com>", "DATA", "QUIT"} {
		if !strings.Contains(transcript, want) {
			t.Fatalf("transcript missing %q:\n%s", want, transcript)
		}
	}
	if !strings.Contains(res.data, "Subject: =?utf-8?q?") {
		t.Fatalf("delivered message:\n%s", res.data)
	}
}

// TestSendSkipsAuthWhenRelayOffersNone covers the internal relay that
// authorizes by source IP: a username left in the settings must not
// turn a working relay into a failed send.
func TestSendSkipsAuthWhenRelayOffersNone(t *testing.T) {
	session, err := sendVia(t, SMTPSettings{Username: "s-elevators@example.com"}, "secret")
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	transcript := strings.Join(session.transcript, "\n")
	if strings.Contains(transcript, "AUTH") {
		t.Fatalf("must not authenticate against a relay without AUTH:\n%s", transcript)
	}
	if !strings.Contains(transcript, "RCPT TO:<ops@example.com>") {
		t.Fatalf("message was not delivered:\n%s", transcript)
	}
}

// TestSendAuthenticatesPlainWithoutTLS is the port 25 internal relay:
// net/smtp would refuse the credentials, we send them because the
// operator chose an unencrypted connection knowingly.
func TestSendAuthenticatesPlainWithoutTLS(t *testing.T) {
	session, err := sendVia(t, SMTPSettings{Username: "user"}, "secret", "AUTH PLAIN LOGIN")
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	transcript := strings.Join(session.transcript, "\n")
	if !strings.Contains(transcript, "AUTH PLAIN |user|secret") {
		t.Fatalf("credentials were not sent:\n%s", transcript)
	}
}

func TestSendFallsBackToLoginMechanism(t *testing.T) {
	session, err := sendVia(t, SMTPSettings{Username: "user"}, "secret", "AUTH LOGIN")
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	transcript := strings.Join(session.transcript, "\n")
	for _, want := range []string{"Username: user", "Password: secret"} {
		if !strings.Contains(transcript, want) {
			t.Fatalf("transcript missing %q:\n%s", want, transcript)
		}
	}
}

func TestSendReportsUnsupportedAuthMechanism(t *testing.T) {
	_, err := sendVia(t, SMTPSettings{Username: "user"}, "secret", "AUTH CRAM-MD5")
	if err == nil {
		t.Fatal("expected an error for a relay we cannot authenticate against")
	}
	if !strings.Contains(err.Error(), "CRAM-MD5") {
		t.Fatalf("error should name the advertised mechanisms: %v", err)
	}
}

// sendVia delivers one message to a throwaway relay that advertises the
// given ESMTP extensions, and reports the conversation it saw.
func sendVia(t *testing.T, settings SMTPSettings, password string, extensions ...string) (smtpSession, error) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skipf("cannot listen: %v", err)
	}
	defer ln.Close()

	done := make(chan smtpSession, 1)
	go func() { done <- serveSMTP(ln, extensions...) }()

	host, port, err := net.SplitHostPort(ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	settings.Host = host
	settings.Port = atoi(t, port)
	settings.TLS = TLSNone
	settings.From = "alerts@example.com"
	settings.Timeout = Duration(5 * time.Second)

	m, err := NewMailer(settings, password)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Send(context.Background(), []string{"ops@example.com"}, Message{Subject: "тест", Body: "тіло\n"}); err != nil {
		return smtpSession{}, err
	}
	session := <-done
	if session.err != nil {
		t.Fatalf("server: %v", session.err)
	}
	return session, nil
}

func TestSendHonoursCancelledContext(t *testing.T) {
	m, err := NewMailer(SMTPSettings{
		Host: "127.0.0.1", Port: 1, TLS: TLSNone,
		From: "alerts@example.com", Timeout: Duration(time.Second),
	}, "")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := m.Send(ctx, []string{"ops@example.com"}, Message{Subject: "s", Body: "b"}); err == nil {
		t.Fatal("expected an error for a cancelled context")
	}
}

type smtpSession struct {
	transcript []string
	data       string
	err        error
}

// serveSMTP accepts one connection and speaks the minimum ESMTP needed
// by net/smtp, returning the commands it saw and the delivered payload.
// extensions are advertised in the EHLO reply, so a test can present a
// relay that wants credentials or one that authorizes by IP.
func serveSMTP(ln net.Listener, extensions ...string) (res smtpSession) {
	conn, err := ln.Accept()
	if err != nil {
		res.err = err
		return res
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))

	r := bufio.NewReader(conn)
	write := func(s string) { _, _ = conn.Write([]byte(s + "\r\n")) }
	write("220 mock ESMTP")

	var body strings.Builder
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			res.err = err
			return res
		}
		cmd := strings.TrimRight(line, "\r\n")
		res.transcript = append(res.transcript, cmd)
		switch {
		case strings.HasPrefix(cmd, "EHLO"), strings.HasPrefix(cmd, "HELO"):
			// The greeting must come first: net/smtp reads extensions
			// from every line of the reply except the opening one.
			if len(extensions) == 0 {
				write("250 mock")
				break
			}
			write("250-mock")
			for _, ext := range extensions[:len(extensions)-1] {
				write("250-" + ext)
			}
			write("250 " + extensions[len(extensions)-1])
		case strings.HasPrefix(cmd, "AUTH LOGIN"):
			for _, prompt := range []string{"Username:", "Password:"} {
				write("334 " + base64.StdEncoding.EncodeToString([]byte(prompt)))
				answer, err := r.ReadString('\n')
				if err != nil {
					res.err = err
					return res
				}
				decoded, err := base64.StdEncoding.DecodeString(strings.TrimRight(answer, "\r\n"))
				if err != nil {
					res.err = err
					return res
				}
				res.transcript = append(res.transcript, prompt+" "+string(decoded))
			}
			write("235 authenticated")
		case strings.HasPrefix(cmd, "AUTH PLAIN "):
			decoded, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(cmd, "AUTH PLAIN "))
			if err != nil {
				res.err = err
				return res
			}
			res.transcript[len(res.transcript)-1] = "AUTH PLAIN " + strings.ReplaceAll(string(decoded), "\x00", "|")
			write("235 authenticated")
		case strings.HasPrefix(cmd, "MAIL FROM"), strings.HasPrefix(cmd, "RCPT TO"):
			write("250 OK")
		case cmd == "DATA":
			write("354 send it")
			for {
				dl, err := r.ReadString('\n')
				if err != nil {
					res.err = err
					return res
				}
				if strings.TrimRight(dl, "\r\n") == "." {
					break
				}
				body.WriteString(dl)
			}
			write("250 queued")
		case cmd == "QUIT":
			write("221 bye")
			res.data = body.String()
			return res
		default:
			write("500 unknown")
		}
	}
}

func atoi(t *testing.T, s string) int {
	t.Helper()
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			t.Fatalf("bad port %q", s)
		}
		n = n*10 + int(r-'0')
	}
	return n
}
