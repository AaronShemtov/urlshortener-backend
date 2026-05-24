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
)

func main() {
	var modeFlag string
	flag.StringVar(&modeFlag, "mode", "", "Server mode: writer, reader, or all (overrides MODE env)")
	flag.Parse()

	// Structured JSON logging — easy to parse by log aggregators.
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	slog.SetDefault(logger)

	cfg, err := config.Load()
	if err != nil {
		slog.Error("config load failed", "error", err)
		os.Exit(1)
	}

	// CLI flag wins over env var if provided.
	if modeFlag != "" {
		cfg.Mode = modeFlag
	}

	slog.Info("starting urlshortener-backend",
		"mode", cfg.Mode,
		"port", cfg.Port,
		"base_url", cfg.BaseURL,
	)

	// Context that is cancelled on SIGTERM/SIGINT.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	mux := http.NewServeMux()

	// Health endpoints are always registered, regardless of mode —
	// k8s probes must work for every pod.
	mux.HandleFunc("GET /healthz", livenessHandler)
	mux.HandleFunc("GET /readyz", readinessHandler)

	// Business endpoints are added in PR 4. For now, every other path 404s.

	srv := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	// Run server in background; surface fatal errors via channel.
	errCh := make(chan error, 1)
	go func() {
		slog.Info("HTTP server listening", "addr", srv.Addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	// Wait for either shutdown signal or server failure.
	select {
	case <-ctx.Done():
		slog.Info("shutdown signal received, draining")
	case err := <-errCh:
		slog.Error("server failed", "error", err)
		os.Exit(1)
	}

	// Drain in-flight requests with a hard cap to avoid hanging forever.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		slog.Error("graceful shutdown failed", "error", err)
		os.Exit(1)
	}
	slog.Info("server stopped cleanly")
}

// livenessHandler — process is alive. Always 200 unless the process itself
// is broken (in which case nothing answers and k8s restarts the pod).
// MUST NOT check dependencies — that's readiness' job.
func livenessHandler(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

// readinessHandler — pod is ready to serve traffic. In PR 3+ this will check
// storage/cache dependencies. For now (no dependencies) it mirrors liveness.
func readinessHandler(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ready"))
}