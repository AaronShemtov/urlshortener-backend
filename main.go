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

	"github.com/AaronShemtov/urlshortener-backend/internal/cache"
	"github.com/AaronShemtov/urlshortener-backend/internal/config"
	"github.com/AaronShemtov/urlshortener-backend/internal/handler"
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

	if modeFlag != "" {
		cfg.Mode = modeFlag
		if err := cfg.Validate(); err != nil {
			slog.Error("config validation failed after --mode flag", "error", err)
			os.Exit(1)
		}
	}

	// Never log secrets — ADBPassword stays out of slog attrs.
	slog.Info("starting urlshortener-backend",
		"mode", cfg.Mode,
		"port", cfg.Port,
		"base_url", cfg.BaseURL,
		"adb_base_url", cfg.ADBBaseURL,
		"adb_collection", cfg.ADBCollection,
		"adb_username", cfg.ADBUsername,
	)

	// SodaStore has no network I/O in its constructor — failures show up
	// at first request (and via /readyz before the pod gets traffic).
	store := storage.NewSodaStore(storage.SodaConfig{
		BaseURL:    cfg.ADBBaseURL,
		Collection: cfg.ADBCollection,
		Username:   cfg.ADBUsername,
		Password:   cfg.ADBPassword,
	})
	defer func() {
		if err := store.Close(); err != nil {
			slog.Error("storage close failed", "error", err)
		}
	}()

	// No Redis in MVP — NoopCache makes every Get a miss, every Set a no-op.
	// When Redis is added later, swap this one line for cache.NewRedisCache(...).
	cacheClient := cache.NewNoopCache()
	defer func() { _ = cacheClient.Close() }()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	mux := http.NewServeMux()

	// Health endpoints — always registered, regardless of mode.
	mux.HandleFunc("/healthz", handler.MethodHandler("GET", livenessHandler))
	mux.HandleFunc("/readyz", handler.MethodHandler("GET", readinessHandler(store)))

	// Business endpoints — registered conditionally based on mode.
	// In production: writer pods only POST, reader pods only GET.
	// "all" mode is for local development and integration tests.
	switch cfg.Mode {
	case "writer":
		wh := handler.NewWriterHandler(store, cacheClient, cfg.BaseURL, cfg.ShortCodeLength)
		mux.HandleFunc("/shorten", handler.MethodHandler("POST", wh.Shorten))
		mux.HandleFunc("/createcustom", handler.MethodHandler("POST", wh.CreateCustom))

	case "reader":
		rh := handler.NewReaderHandler(store, cacheClient)
		// register root so reader can extract code from the path
		mux.HandleFunc("/", handler.MethodHandler("GET", rh.Redirect))

	case "all":
		wh := handler.NewWriterHandler(store, cacheClient, cfg.BaseURL, cfg.ShortCodeLength)
		rh := handler.NewReaderHandler(store, cacheClient)
		mux.HandleFunc("/shorten", handler.MethodHandler("POST", wh.Shorten))
		mux.HandleFunc("/createcustom", handler.MethodHandler("POST", wh.CreateCustom))
		mux.HandleFunc("/", handler.MethodHandler("GET", rh.Redirect))
	}

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

func livenessHandler(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

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