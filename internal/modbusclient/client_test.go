package modbusclient

import (
	"context"
	"testing"
	"time"
)

func TestDialMockTarget(t *testing.T) {
	sess, err := Dial(context.Background(), DialTarget{
		Host:           "mock",
		Port:           502,
		UnitID:         1,
		ConnectTimeout: time.Second,
		RequestTimeout: time.Second,
	})
	if err != nil {
		t.Fatalf("dial mock returned error: %v", err)
	}
	if sess == nil || !sess.mock {
		t.Fatalf("expected mock session, got %#v", sess)
	}
}

func TestMockSessionReadPayload(t *testing.T) {
	sess := &Session{mock: true}
	got, err := sess.ReadHolding(context.Background(), 10, 2)
	if err != nil {
		t.Fatalf("ReadHolding failed: %v", err)
	}
	if len(got) != 4 {
		t.Fatalf("expected 4 bytes, got %d", len(got))
	}
	if got[0] != 0 || got[1] != 11 || got[2] != 0 || got[3] != 12 {
		t.Fatalf("unexpected payload: %v", got)
	}
}

func TestCloseNilSession(t *testing.T) {
	var sess *Session
	if err := sess.Close(); err != nil {
		t.Fatalf("close on nil session failed: %v", err)
	}
}

func TestIsMockHost(t *testing.T) {
	if !isMockHost(" simulator ") {
		t.Fatal("expected simulator host to be treated as mock")
	}
	if isMockHost("10.0.0.2") {
		t.Fatal("did not expect IP host to be treated as mock")
	}
}
