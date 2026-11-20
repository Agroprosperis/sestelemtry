package edge

import (
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// Secrets set from the device's own shell (the write PIN, the console
// password) are stored as a salted PBKDF2 hash in a small JSON file.
const (
	secretIter   = 100_000
	secretKeyLen = 32
	secretAlgo   = "pbkdf2-sha256"
)

var errSecretCorrupt = errors.New("secret file is corrupt")

type secretFile struct {
	Algo  string    `json:"algo"`
	Iter  int       `json:"iter"`
	Salt  string    `json:"salt"`
	Hash  string    `json:"hash"`
	User  string    `json:"user,omitempty"`
	SetAt time.Time `json:"set_at"`
}

func hashSecret(secret string, salt []byte, iter int) ([]byte, error) {
	return pbkdf2.Key(sha256.New, secret, salt, iter, secretKeyLen)
}

func newSecretFile(secret string, now time.Time) (secretFile, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return secretFile{}, err
	}
	key, err := hashSecret(secret, salt, secretIter)
	if err != nil {
		return secretFile{}, err
	}
	return secretFile{
		Algo: secretAlgo, Iter: secretIter, SetAt: now.UTC(),
		Salt: base64.StdEncoding.EncodeToString(salt), Hash: base64.StdEncoding.EncodeToString(key),
	}, nil
}

// matches compares secret with the stored hash in constant time.
func (f secretFile) matches(secret string) (bool, error) {
	salt, err1 := base64.StdEncoding.DecodeString(f.Salt)
	want, err2 := base64.StdEncoding.DecodeString(f.Hash)
	if err1 != nil || err2 != nil {
		return false, errSecretCorrupt
	}
	got, err := hashSecret(secret, salt, f.Iter)
	if err != nil {
		return false, err
	}
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}

// readSecretFile loads path; os.ErrNotExist when there is no file,
// errSecretCorrupt when it does not parse.
func readSecretFile(path string) (secretFile, error) {
	if path == "" {
		return secretFile{}, os.ErrNotExist
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return secretFile{}, err
	}
	var f secretFile
	if err := json.Unmarshal(raw, &f); err != nil || f.Algo != secretAlgo || f.Iter <= 0 || f.Salt == "" || f.Hash == "" {
		return secretFile{}, errSecretCorrupt
	}
	return f, nil
}

// writeSecretFile replaces path atomically with f, mode 0600. Run as
// root, the file is handed to the owner of its directory (the service
// user that owns the state directory).
func writeSecretFile(path string, f secretFile) error {
	raw, err := json.Marshal(f)
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+"-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(raw); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if os.Geteuid() == 0 {
		st, err := os.Stat(dir)
		if err != nil {
			return err
		}
		if sys, ok := st.Sys().(*syscall.Stat_t); ok {
			if err := os.Chown(tmp.Name(), int(sys.Uid), int(sys.Gid)); err != nil {
				return fmt.Errorf("chown %s: %w", tmp.Name(), err)
			}
		}
	}
	return os.Rename(tmp.Name(), path)
}
