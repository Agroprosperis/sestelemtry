package main

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/nesh/sestelemetry/internal/edge"
)

// runSetWritePin asks for the PIN twice and stores its hash where the
// service looks for it. It acts at once, without a restart.
func runSetWritePin(configPath string) error {
	path, err := edge.WritePinPath(configPath)
	if err != nil {
		return err
	}
	in := bufio.NewReader(os.Stdin)
	pin, err := readSecret(in, "Новий PIN (щонайменше 6 символів): ")
	if err != nil {
		return err
	}
	again, err := readSecret(in, "Повторіть PIN: ")
	if err != nil {
		return err
	}
	if pin != again {
		return errors.New("PIN не збігаються")
	}
	if err := edge.SetWritePin(path, pin, time.Now()); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "PIN збережено (%s). Діє одразу, перезапуск не потрібен.\n", path)
	return nil
}

// runSetConsolePassword asks for the console login and the password
// twice and stores its hash where the service looks for it. It acts at
// once, without a restart.
func runSetConsolePassword(configPath string) error {
	path, err := edge.ConsolePasswordPath(configPath)
	if err != nil {
		return err
	}
	in := bufio.NewReader(os.Stdin)
	fmt.Fprint(os.Stderr, "Логін (Enter — operator): ")
	user, err := in.ReadString('\n')
	if err != nil && user == "" {
		return err
	}
	pass, err := readSecret(in, "Новий пароль консолі (щонайменше 10 символів): ")
	if err != nil {
		return err
	}
	again, err := readSecret(in, "Повторіть пароль: ")
	if err != nil {
		return err
	}
	if pass != again {
		return errors.New("паролі не збігаються")
	}
	if err := edge.SetConsolePassword(path, strings.TrimSpace(user), pass, time.Now()); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "Пароль консолі збережено (%s). Діє одразу, перезапуск не потрібен.\n", path)
	return nil
}

// readSecret reads one line, with the echo off on a terminal.
func readSecret(in *bufio.Reader, prompt string) (string, error) {
	fmt.Fprint(os.Stderr, prompt)
	if fi, err := os.Stdin.Stat(); err == nil && fi.Mode()&os.ModeCharDevice != 0 {
		if stty("-echo") == nil {
			defer func() {
				_ = stty("echo")
				fmt.Fprintln(os.Stderr)
			}()
		}
	}
	line, err := in.ReadString('\n')
	if err != nil && line == "" {
		return "", err
	}
	return strings.TrimRight(line, "\r\n"), nil
}

func stty(arg string) error {
	cmd := exec.Command("stty", arg)
	cmd.Stdin = os.Stdin
	return cmd.Run()
}
