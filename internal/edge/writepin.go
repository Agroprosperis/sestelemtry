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
	"sync"
	"syscall"
	"time"
)

// The PIN that turns writing on. Only its salted hash lives on the
// device; it is set from the device's own shell (ems-edge
// -set-write-pin), never over the network. Stopping and turning writing
// off need no PIN.
const (
	writePinMinLen   = 6
	writePinIter     = 100_000
	writePinKeyLen   = 32
	writePinMaxFails = 5
	writePinLockFor  = 15 * time.Minute
	writePinAlgo     = "pbkdf2-sha256"
)

var (
	errPinNotSet  = errors.New("PIN не задано на пристрої")
	errPinWrong   = errors.New("невірний PIN")
	errPinCorrupt = errors.New("файл PIN пошкоджено — задайте PIN заново")
)

type pinFile struct {
	Algo  string    `json:"algo"`
	Iter  int       `json:"iter"`
	Salt  string    `json:"salt"`
	Hash  string    `json:"hash"`
	SetAt time.Time `json:"set_at"`
}

func hashPin(pin string, salt []byte, iter int) ([]byte, error) {
	return pbkdf2.Key(sha256.New, pin, salt, iter, writePinKeyLen)
}

// SetWritePin stores the salted hash of pin at path. The file is
// readable by the owner of its directory only: run as root, it is
// handed to the service user that owns the state directory.
func SetWritePin(path, pin string, now time.Time) error {
	if len([]rune(pin)) < writePinMinLen {
		return fmt.Errorf("PIN має бути щонайменше %d символів", writePinMinLen)
	}
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return err
	}
	key, err := hashPin(pin, salt, writePinIter)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(pinFile{
		Algo: writePinAlgo, Iter: writePinIter, SetAt: now.UTC(),
		Salt: base64.StdEncoding.EncodeToString(salt), Hash: base64.StdEncoding.EncodeToString(key),
	})
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".write_pin-*")
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
				return err
			}
		}
	}
	return os.Rename(tmp.Name(), path)
}

// pinGate checks enable attempts against the stored hash and locks out
// after repeated failures. Attempts are serialized, so parallel
// requests cannot outrun the counter.
type pinGate struct {
	path  string
	mu    sync.Mutex
	fails int
	until time.Time
}

func (p *pinGate) load() (pinFile, error) {
	if p == nil || p.path == "" {
		return pinFile{}, errPinNotSet
	}
	raw, err := os.ReadFile(p.path)
	if os.IsNotExist(err) {
		return pinFile{}, errPinNotSet
	}
	if err != nil {
		return pinFile{}, fmt.Errorf("PIN: %w", err)
	}
	var f pinFile
	if err := json.Unmarshal(raw, &f); err != nil || f.Algo != writePinAlgo || f.Iter <= 0 || f.Salt == "" || f.Hash == "" {
		return pinFile{}, errPinCorrupt
	}
	return f, nil
}

// isSet reports a readable PIN on the device.
func (p *pinGate) isSet() bool {
	_, err := p.load()
	return err == nil
}

// check verifies pin; locked reports the attempt that started a lockout.
func (p *pinGate) check(pin string, now time.Time) (locked bool, err error) {
	if p == nil {
		return false, errPinNotSet
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if now.Before(p.until) {
		return false, fmt.Errorf("забагато невірних PIN — спробуйте через %s", p.until.Sub(now).Round(time.Minute))
	}
	f, err := p.load()
	if err != nil {
		return false, err
	}
	salt, err1 := base64.StdEncoding.DecodeString(f.Salt)
	want, err2 := base64.StdEncoding.DecodeString(f.Hash)
	if err1 != nil || err2 != nil {
		return false, errPinCorrupt
	}
	got, err := hashPin(pin, salt, f.Iter)
	if err != nil {
		return false, err
	}
	if subtle.ConstantTimeCompare(got, want) == 1 {
		p.fails = 0
		return false, nil
	}
	p.fails++
	if p.fails >= writePinMaxFails {
		p.fails = 0
		p.until = now.Add(writePinLockFor)
		return true, fmt.Errorf("%w — введення заблоковано на %s", errPinWrong, writePinLockFor)
	}
	return false, fmt.Errorf("%w (лишилось спроб: %d)", errPinWrong, writePinMaxFails-p.fails)
}
