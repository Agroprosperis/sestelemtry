package edge

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestSetConsolePassword(t *testing.T) {
	path := filepath.Join(t.TempDir(), "console_password.json")
	if err := SetConsolePassword(path, "operator", "short", testTS); err == nil {
		t.Fatal("a 5-character password was accepted")
	}
	if err := SetConsolePassword(path, "op:er", "long enough pass", testTS); err == nil {
		t.Fatal("a login with a colon was accepted")
	}
	if err := SetConsolePassword(path, "", "long enough pass", testTS); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o600 {
		t.Fatalf("password file mode %v, want 0600", st.Mode().Perm())
	}
	raw, _ := os.ReadFile(path)
	if bytes.Contains(raw, []byte("long enough pass")) {
		t.Fatal("the password itself is stored")
	}
	f, err := readSecretFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if f.User != consoleDefaultUser {
		t.Fatalf("user = %q, want the default %q", f.User, consoleDefaultUser)
	}
	if ok, err := f.matches("long enough pass"); err != nil || !ok {
		t.Fatalf("right password: ok=%v err=%v", ok, err)
	}
	if ok, _ := f.matches("wrong password!"); ok {
		t.Fatal("a wrong password matched")
	}
	if err := os.WriteFile(path, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readSecretFile(path); !errors.Is(err, errSecretCorrupt) {
		t.Fatalf("torn file: %v", err)
	}
}
