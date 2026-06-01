package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/nesh/sestelemetry/internal/bootstrap"
	"github.com/nesh/sestelemetry/internal/config"
	"github.com/nesh/sestelemetry/internal/decode"
	"github.com/nesh/sestelemetry/internal/modbusclient"
)

func main() {
	configPath := flag.String("config", "config.yaml", "path to YAML config")
	orgID := flag.String("org", "", "organization id to check (default: first organization)")
	deviceIdx := flag.Int("device", 0, "index of the org's modbus device to check (default: 0)")
	delay := flag.Duration("delay", 150*time.Millisecond, "delay between register reads")
	timeout := flag.Duration("timeout", 0, "optional overall timeout, e.g. 2m")
	flag.Parse()

	runtime, err := bootstrap.Load(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "bootstrap error: %v\n", err)
		os.Exit(1)
	}
	cfg := runtime.Config
	resolved := runtime.Resolved

	org, err := pickOrganization(cfg.Organizations, *orgID)
	if err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		os.Exit(1)
	}

	// Resolve the device the same way the collector's runOrg does:
	// Devices() returns the modbus_devices[] list, or a single device
	// wrapping the legacy inline `modbus:` block. Reading org.Modbus
	// directly would be the zero value for modbus_devices orgs.
	devices := org.Devices()
	if *deviceIdx < 0 || *deviceIdx >= len(devices) {
		fmt.Fprintf(os.Stderr, "config: org %q has %d device(s); -device %d is out of range\n", org.ID, len(devices), *deviceIdx)
		os.Exit(1)
	}
	dev := devices[*deviceIdx].Modbus

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if *timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, *timeout)
		defer cancel()
	}

	sess, err := modbusclient.Dial(ctx, modbusclient.DialTarget{
		Host:           dev.Host,
		Port:           dev.Port,
		UnitID:         dev.UnitID,
		ConnectTimeout: dev.ConnectTimeout,
		RequestTimeout: dev.RequestTimeout,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "modbus dial error: %v\n", err)
		os.Exit(1)
	}
	defer func() { _ = sess.Close() }()

	fmt.Printf("Checking org=%s device=%d host=%s:%d map=%s registers=%d\n", org.ID, *deviceIdx, dev.Host, dev.Port, cfg.ModbusRegisterMap, len(resolved))

	okCount := 0
	failCount := 0
	for _, e := range resolved {
		readCtx, cancel := context.WithTimeout(ctx, dev.RequestTimeout)
		var raw []byte
		switch cfg.ModbusRegisterMap {
		case config.MapInput:
			raw, err = sess.ReadInput(readCtx, e.PDUStart, e.WordCount)
		default:
			raw, err = sess.ReadHolding(readCtx, e.PDUStart, e.WordCount)
		}
		cancel()
		if err != nil {
			failCount++
			fmt.Printf("FAIL metric=%s addr=%d type=%s qty=%d err=%v\n", e.MetricKey, e.Address, e.DataType, e.WordCount, err)
			if !sleepBetween(ctx, *delay) {
				break
			}
			continue
		}

		v, derr := decode.Scaled(e.DataType, raw, e.Gain, e.Offset)
		if derr != nil {
			failCount++
			fmt.Printf("FAIL metric=%s addr=%d type=%s qty=%d decode_err=%v\n", e.MetricKey, e.Address, e.DataType, e.WordCount, derr)
			if !sleepBetween(ctx, *delay) {
				break
			}
			continue
		}

		okCount++
		fmt.Printf("OK   metric=%s addr=%d type=%s qty=%d value=%v\n", e.MetricKey, e.Address, e.DataType, e.WordCount, v)
		if !sleepBetween(ctx, *delay) {
			break
		}
	}

	fmt.Printf("Done. ok=%d fail=%d\n", okCount, failCount)
	if errors.Is(ctx.Err(), context.Canceled) {
		os.Exit(130)
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		os.Exit(124)
	}
	if failCount > 0 {
		os.Exit(2)
	}
}

func pickOrganization(orgs []config.Organization, id string) (config.Organization, error) {
	if len(orgs) == 0 {
		return config.Organization{}, fmt.Errorf("config: no organizations found")
	}
	if id == "" {
		return orgs[0], nil
	}
	for _, o := range orgs {
		if o.ID == id {
			return o, nil
		}
	}
	return config.Organization{}, fmt.Errorf("config: organization %q not found", id)
}

func sleepBetween(ctx context.Context, delay time.Duration) bool {
	if delay <= 0 {
		return true
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
