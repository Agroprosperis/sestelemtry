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
	base := SMTPOptions{
		Host: "smtp.example.com",
		Port: 587,
		TLS:  TLSStartTLS,
		From: "alerts@example.com",
		To:   []string{"ops@example.com"},
	}
	cases := map[string]func(o *SMTPOptions){
		"no host":          func(o *SMTPOptions) { o.Host = "" },
		"bad port":         func(o *SMTPOptions) { o.Port = 0 },
		"unknown tls":      func(o *SMTPOptions) { o.TLS = "ssl" },
		"no recipients":    func(o *SMTPOptions) { o.To = nil },
		"bad from":         func(o *SMTPOptions) { o.From = "not an address" },
		"auth without tls": func(o *SMTPOptions) { o.TLS = TLSNone; o.Username = "user" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			opts := base
			opts.To = append([]string(nil), base.To...)
			mutate(&opts)
			if _, err := NewMailer(opts); err == nil {
				t.Fatal("expected an error, got nil")
			}
		})
	}
}

func TestNewMailerAcceptsDisplayName(t *testing.T) {
	m, err := NewMailer(SMTPOptions{
		Host: "smtp.example.com",
		Port: 465,
		TLS:  TLSImplicit,
		From: "СЕС Моніторинг <alerts@example.com>",
		To:   []string{"ops@example.com"},
	})
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

	type result struct {
		transcript []string
		data       string
		err        error
	}
	done := make(chan result, 1)
	go func() { done <- serveSMTP(ln) }()

	host, port, err := net.SplitHostPort(ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	m, err := NewMailer(SMTPOptions{
		Host:    host,
		Port:    atoi(t, port),
		TLS:     TLSNone,
		From:    "alerts@example.com",
		To:      []string{"ops@example.com"},
		Timeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Send(context.Background(), Message{Subject: "тест", Body: "тіло\n"}); err != nil {
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

func TestSendHonoursCancelledContext(t *testing.T) {
	m, err := NewMailer(SMTPOptions{
		Host: "127.0.0.1", Port: 1, TLS: TLSNone,
		From: "alerts@example.com", To: []string{"ops@example.com"},
		Timeout: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := m.Send(ctx, Message{Subject: "s", Body: "b"}); err == nil {
		t.Fatal("expected an error for a cancelled context")
	}
}

// serveSMTP accepts one connection and speaks the minimum ESMTP needed
// by net/smtp, returning the commands it saw and the delivered payload.
func serveSMTP(ln net.Listener) (res struct {
	transcript []string
	data       string
	err        error
}) {
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
			write("250 mock")
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
