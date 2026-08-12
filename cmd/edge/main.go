// Command edge is the EMS edge controller for the on-site Siemens
// IOT2050: SmartLogger telemetry at 1 s, SQLite black box, shadow
// control (virtual dispatch, no SmartLogger writes) and batch uplink
// to the central sestelemetry API.
//
// MVP-0..2 per ems-spec docs/specs/ems_mvp_edge_shadow_spec.md.
// Writing to registers 40378/40381 is NOT implemented in this build.
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
	flag.Parse()

	if *showVersion {
		fmt.Println(version)
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
