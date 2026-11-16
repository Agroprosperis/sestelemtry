package edge

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// failed reports that the reply lists precondition id as not met.
func failed(r writeReply, id string) bool {
	for _, p := range r.Preconditions {
		if p.ID == id {
			return !p.OK
		}
	}
	return false
}

func TestWritePinSetCheckAndLockout(t *testing.T) {
	path := filepath.Join(t.TempDir(), "write_pin.json")
	g := &pinGate{path: path}
	now := testTS
	if _, err := g.check("246813", now); !errors.Is(err, errPinNotSet) || g.isSet() {
		t.Fatalf("no file: %v, set %v", err, g.isSet())
	}
	if err := SetWritePin(path, "12345", now); err == nil {
		t.Fatal("a 5-character PIN was accepted")
	}
	if err := SetWritePin(path, "246813", now); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o600 {
		t.Fatalf("PIN file mode %v, want 0600", st.Mode().Perm())
	}
	raw, _ := os.ReadFile(path)
	if bytes.Contains(raw, []byte("246813")) {
		t.Fatal("the PIN itself is stored")
	}
	if _, err := g.check("246813", now); err != nil || !g.isSet() {
		t.Fatalf("right PIN: %v", err)
	}
	for i := 1; i < writePinMaxFails; i++ {
		if locked, err := g.check("000000", now); !errors.Is(err, errPinWrong) || locked {
			t.Fatalf("attempt %d: locked=%v err=%v", i, locked, err)
		}
	}
	if locked, err := g.check("000000", now); !errors.Is(err, errPinWrong) || !locked {
		t.Fatalf("fifth wrong PIN: locked=%v err=%v", locked, err)
	}
	if _, err := g.check("246813", now.Add(time.Minute)); err == nil {
		t.Fatal("the right PIN passed during the lockout")
	}
	if _, err := g.check("246813", now.Add(writePinLockFor+time.Second)); err != nil {
		t.Fatalf("after the lockout: %v", err)
	}
	if err := os.WriteFile(path, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := g.check("246813", now.Add(writePinLockFor+2*time.Second)); !errors.Is(err, errPinCorrupt) || g.isSet() {
		t.Fatalf("torn file: %v", err)
	}
}

// The console checks the PIN itself: a wrong one never reaches the
// core loop, a right one arrives marked as matched.
func TestConsoleEnableChecksThePin(t *testing.T) {
	s := writeService(t, zeCfg(t))
	if err := SetWritePin(s.cfg.Control.Write.PinPath, "246813", testTS); err != nil {
		t.Fatal(err)
	}
	post := func(body string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		s.uiWrite(w, httptest.NewRequest(http.MethodPost, "/api/write", strings.NewReader(body)))
		return w
	}
	got := make(chan writeRequest, 1)
	go func() {
		req := <-s.writeRequests
		got <- req
		req.reply <- writeReply{OK: true}
	}()

	w := post(`{"action":"enable","confirm":"ze","pin":"000000"}`)
	if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "невірний PIN") {
		t.Fatalf("wrong PIN: %d %s", w.Code, w.Body.String())
	}
	select {
	case ev := <-s.events:
		if ev.Code != EvWriteDenied || strings.Contains(ev.Message, "000000") {
			t.Fatalf("event = %+v", ev)
		}
	default:
		t.Fatal("no WRITE_ENABLE_DENIED event")
	}
	select {
	case req := <-got:
		t.Fatalf("a wrong PIN reached the core loop: %+v", req)
	default:
	}

	if w := post(`{"action":"enable","confirm":"ze","pin":"246813"}`); w.Code != http.StatusOK {
		t.Fatalf("right PIN: %d %s", w.Code, w.Body.String())
	}
	if req := <-got; req.action != "enable" || !req.pinOK {
		t.Fatalf("core loop got %+v", req)
	}
	if s.gate.Enabled() {
		t.Fatal("the console itself turned the flag on")
	}
}
