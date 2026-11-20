package edge

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// The login of the on-device console. Like the write PIN, only a salted
// hash lives on the device, set from its own shell (ems-edge
// -set-console-password), never over the network.
const (
	consolePasswordMinLen = 10
	consoleDefaultUser    = "operator"
)

// SetConsolePassword stores user and the salted hash of password at path.
func SetConsolePassword(path, user, password string, now time.Time) error {
	user = strings.TrimSpace(user)
	if user == "" {
		user = consoleDefaultUser
	}
	// Basic credentials are "user:password", split at the first colon.
	if strings.ContainsAny(user, ": \t\r\n") {
		return errors.New("логін не може містити двокрапку чи пробіли")
	}
	if len([]rune(password)) < consolePasswordMinLen {
		return fmt.Errorf("пароль має бути щонайменше %d символів", consolePasswordMinLen)
	}
	f, err := newSecretFile(password, now)
	if err != nil {
		return err
	}
	f.User = user
	return writeSecretFile(path, f)
}
