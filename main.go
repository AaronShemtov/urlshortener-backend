package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/AaronShemtov/urlshortener-backend/internal/config"
	"github.com/AaronShemtov/urlshortener-backend/internal/storage"
)

func main() {
	var modeFlag string
	flag.StringVar(&modeFlag, "mode", "", "Server mode: writer, reader, or all (overrides MODE env)")
	flag.Parse()

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	slog.SetDefault(logger)

	cfg, err := config.Load()
	if err != nil {
		slog.Error("config load failed", "error", err)
		os.Exit(1)
	}

	// CLI flag wins over env var, but must re-validate to catch bad input.
	if modeFlag != "" {
		cfg.Mode = modeFlag
		if err := cfg.Validate(); err != nil {
			slog.Error("config validation failed after --mode flag", "error", err)
			os.Exit(1)
		}
	}

	slog.Info("starting urlshortener-backend",
		"mode", cfg.Mode,
		"port", cfg.Port,
		"base_url", cfg.BaseURL,
		"nosql_endpoint", cfg.NoSQLEndpoint,
		"nosql_table", cfg.NoSQLTable,
	)

	store, err := storage.NewNoSQLStorage(cfg.NoSQLEndpoint, cfg.NoSQLTable, cfg.OCICompartmentOCID)
	if err != nil {
		slog.Error("storage init failed", "error", err)
		os.Exit(1)
	}
	defer func() {
		if err := store.Close(); err != nil {
			slog.Error("storage close failed", "error", err)
		}
	}()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", livenessHandler)
	mux.HandleFunc("GET /readyz", readinessHandler(store))

	srv := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		slog.Info("HTTP server listening", "addr", srv.Addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	select {
	case <-ctx.Done():
		slog.Info("shutdown signal received, draining")
	case err := <-errCh:
		slog.Error("server failed", "error", err)
		os.Exit(1)
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		slog.Error("graceful shutdown failed", "error", err)
		os.Exit(1)
	}
	slog.Info("server stopped cleanly")
}

// livenessHandler — process is alive. No dependency checks here.
func livenessHandler(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

// readinessHandler verifies storage is reachable before reporting Ready.
// k8s pulls the pod out of Service rotation if this fails.
func readinessHandler(store storage.Storage) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if err := store.Ping(ctx); err != nil {
			slog.Warn("readiness probe: storage unhealthy", "error", err)
			http.Error(w, "storage unhealthy", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ready"))
	}
}
