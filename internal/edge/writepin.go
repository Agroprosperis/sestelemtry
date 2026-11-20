package edge

import (
	"errors"
	"fmt"
	"os"
	"sync"
	"time"
)

// The PIN that turns writing on. Only its salted hash lives on the
// device; it is set from the device's own shell (ems-edge
// -set-write-pin), never over the network. Stopping and turning writing
// off need no PIN.
const (
	writePinMinLen   = 6
	writePinMaxFails = 5
	writePinLockFor  = 15 * time.Minute
)

var (
	errPinNotSet  = errors.New("PIN не задано на пристрої")
	errPinWrong   = errors.New("невірний PIN")
	errPinCorrupt = errors.New("файл PIN пошкоджено — задайте PIN заново")
)

// SetWritePin stores the salted hash of pin at path.
func SetWritePin(path, pin string, now time.Time) error {
	if len([]rune(pin)) < writePinMinLen {
		return fmt.Errorf("PIN має бути щонайменше %d символів", writePinMinLen)
	}
	f, err := newSecretFile(pin, now)
	if err != nil {
		return err
	}
	return writeSecretFile(path, f)
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

func (p *pinGate) load() (secretFile, error) {
	if p == nil {
		return secretFile{}, errPinNotSet
	}
	f, err := readSecretFile(p.path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return secretFile{}, errPinNotSet
	case errors.Is(err, errSecretCorrupt):
		return secretFile{}, errPinCorrupt
	case err != nil:
		return secretFile{}, fmt.Errorf("PIN: %w", err)
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
	ok, err := f.matches(pin)
	if errors.Is(err, errSecretCorrupt) {
		return false, errPinCorrupt
	}
	if err != nil {
		return false, err
	}
	if ok {
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
