// Command edge is the EMS edge controller for the on-site Siemens
// IOT2050: SmartLogger telemetry at 1 s, SQLite black box, the dispatch
// engine and batch uplink to the central sestelemetry API.
//
// It writes 40378/40381 (and 0 to 40384/40386) only while the device's
// own write flag is on (ems-spec docs/specs/ems_sl_write_cutover.md).
// The flag is off by default and after every restart, never comes from
// the cloud, and is turned on only by a person on the local console
// with the PIN set on the device (-set-write-pin). The console itself
// takes the login set on the device (-set-console-password).
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/nesh/sestelemetry/internal/edge"
)

// version is stamped at build time via
// -ldflags "-X main.version=v0.1.0".
var version = "dev"

func main() {
	configPath := flag.String("config", "config.edge.yaml", "path to edge YAML config")
	replayPath := flag.String("replay", "", "replay mode: path to a historical telemetry CSV (no Modbus, no uplink)")
	replayOut := flag.String("replay-out", "control_decisions.csv", "replay mode: output CSV for shadow decisions")
	replayManifest := flag.String("replay-manifest", "", "replay mode: optional manifest-lite JSON (plan/preset/limits)")
	showVersion := flag.Bool("version", false, "print version and exit")
	setPin := flag.Bool("set-write-pin", false, "set the PIN that turns SmartLogger writing on (asked twice) and exit")
	setConsolePassword := flag.Bool("set-console-password", false, "set the login of the local web console (password asked twice) and exit")
	flag.Parse()

	if *showVersion {
		fmt.Println(version)
		return
	}
	if *setPin {
		if err := runSetWritePin(*configPath); err != nil {
			fmt.Fprintln(os.Stderr, "PIN не збережено:", err)
			os.Exit(1)
		}
		return
	}
	if *setConsolePassword {
		if err := runSetConsolePassword(*configPath); err != nil {
			fmt.Fprintln(os.Stderr, "Пароль консолі не збережено:", err)
			os.Exit(1)
		}
		return
	}

	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	cfg, err := edge.LoadConfig(*configPath)
	if err != nil {
		log.Error("edge_config", "err", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if *replayPath != "" {
		opts := edge.ReplayOptions{
			InputCSV:     *replayPath,
			OutputCSV:    *replayOut,
			ManifestFile: *replayManifest,
		}
		if err := edge.RunReplay(ctx, cfg, log, opts); err != nil {
			log.Error("edge_replay", "err", err)
			os.Exit(1)
		}
		return
	}

	if err := edge.Run(ctx, cfg, log, version); err != nil {
		log.Error("edge_run", "err", err)
		os.Exit(1)
	}
}
