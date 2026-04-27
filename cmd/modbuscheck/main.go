package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/nesh/sestelemetry/internal/config"
	"github.com/nesh/sestelemetry/internal/decode"
	"github.com/nesh/sestelemetry/internal/modbusclient"
	"github.com/nesh/sestelemetry/internal/registers"
)

func main() {
	configPath := flag.String("config", "config.yaml", "path to YAML config")
	orgID := flag.String("org", "", "organization id to check (default: first organization)")
	delay := flag.Duration("delay", 150*time.Millisecond, "delay between register reads")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "config error: %v\n", err)
		os.Exit(1)
	}

	org, err := pickOrganization(cfg.Organizations, *orgID)
	if err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		os.Exit(1)
	}

	cat, err := registers.Load(cfg.RegisterCatalog)
	if err != nil {
		fmt.Fprintf(os.Stderr, "register catalog error: %v\n", err)
		os.Exit(1)
	}
	base := cat.Addressing.HoldingAddressBase
	if cfg.RegisterAddressing.HoldingAddressBase != 0 {
		base = cfg.RegisterAddressing.HoldingAddressBase
	}
	resolved, err := cat.Resolve(base)
	if err != nil {
		fmt.Fprintf(os.Stderr, "resolve registers error: %v\n", err)
		os.Exit(1)
	}

	ctx := context.Background()
	sess, err := modbusclient.Dial(ctx, org)
	if err != nil {
		fmt.Fprintf(os.Stderr, "modbus dial error: %v\n", err)
		os.Exit(1)
	}
	defer func() { _ = sess.Close() }()

	fmt.Printf("Checking org=%s host=%s:%d map=%s registers=%d\n", org.ID, org.Modbus.Host, org.Modbus.Port, cfg.ModbusRegisterMap, len(resolved))

	okCount := 0
	failCount := 0
	for _, e := range resolved {
		readCtx, cancel := context.WithTimeout(ctx, org.Modbus.RequestTimeout)
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
			sleepBetween(*delay)
			continue
		}

		v, derr := decode.Scaled(e.DataType, raw, e.Gain, e.Offset)
		if derr != nil {
			failCount++
			fmt.Printf("FAIL metric=%s addr=%d type=%s qty=%d decode_err=%v\n", e.MetricKey, e.Address, e.DataType, e.WordCount, derr)
			sleepBetween(*delay)
			continue
		}

		okCount++
		fmt.Printf("OK   metric=%s addr=%d type=%s qty=%d value=%v\n", e.MetricKey, e.Address, e.DataType, e.WordCount, v)
		sleepBetween(*delay)
	}

	fmt.Printf("Done. ok=%d fail=%d\n", okCount, failCount)
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

func sleepBetween(delay time.Duration) {
	if delay > 0 {
		time.Sleep(delay)
	}
}
