package main

import (
	"context"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/nesh/sestelemetry/internal/api"
	"github.com/nesh/sestelemetry/internal/storage"
)

func main() {
	listenAddr := flag.String("listen", ":8080", "HTTP listen address")
	defaultDB := flag.String("database-url", "", "PostgreSQL connection string (fallback if DATABASE_URL is unset)")
	allowOrigin := flag.String("allow-origin", "*", "Allowed CORS origin")
	flag.Parse()

	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	dbURL := strings.TrimSpace(os.Getenv("DATABASE_URL"))
	if dbURL == "" {
		dbURL = strings.TrimSpace(*defaultDB)
	}
	if dbURL == "" {
		log.Error("database_url missing", "hint", "set DATABASE_URL or -database-url")
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	pool, err := storage.Open(ctx, dbURL)
	if err != nil {
		log.Error("db_open", "err", err)
		os.Exit(1)
	}
	defer pool.Close()

	svc := api.NewHandlers(api.NewStore(pool), *allowOrigin)
	server := &http.Server{
		Addr:              *listenAddr,
		Handler:           svc.Router(),
		MaxHeaderBytes:    1 << 20, // 1 MiB
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		log.Info("api_start", "listen", *listenAddr)
		errCh <- server.ListenAndServe()
	}()

	select {
	case <-ctx.Done():
	case err := <-errCh:
		if err != nil && err != http.ErrServerClosed {
			log.Error("listen", "err", err)
			os.Exit(1)
		}
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		log.Error("shutdown", "err", err)
	}
}
