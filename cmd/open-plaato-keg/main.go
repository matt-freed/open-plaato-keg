// Command open-plaato-keg runs a local replacement for the discontinued Plaato
// cloud service: it speaks the Blynk protocol to Plaato Keg hardware and
// exposes the results over HTTP, WebSocket and the BarHelper integration.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/matt-freed/open-plaato-keg/internal/api"
	"github.com/matt-freed/open-plaato-keg/internal/barhelper"
	"github.com/matt-freed/open-plaato-keg/internal/config"
	"github.com/matt-freed/open-plaato-keg/internal/events"
	"github.com/matt-freed/open-plaato-keg/internal/keg"
	"github.com/matt-freed/open-plaato-keg/internal/store"
	"github.com/matt-freed/open-plaato-keg/internal/ws"
	"github.com/matt-freed/open-plaato-keg/web"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

// shutdownTimeout bounds how long in-flight HTTP requests may take to finish.
const shutdownTimeout = 10 * time.Second

// pruneInterval is how often old history is discarded.
const pruneInterval = 24 * time.Hour

func main() {
	if err := run(); err != nil {
		slog.Error("open-plaato-keg failed", "error", err)
		os.Exit(1)
	}
}

func run() error {
	setupLogging()

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("configuration: %w", err)
	}

	st, err := store.Open(cfg.DatabaseFilePath)
	if err != nil {
		return err
	}
	defer st.Close()

	slog.Info("starting open-plaato-keg",
		"version", version,
		"database", cfg.DatabaseFilePath,
		"keg_port", cfg.KegListenerPort,
		"http_port", cfg.HTTPListenerPort,
		"barhelper", cfg.BarHelper.Enabled)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	bus := events.NewBus()

	bar := barhelper.New(cfg.BarHelper)
	bar.Start(ctx)
	// The worker only stops when the context is cancelled, so cancel before
	// waiting — otherwise an early return from this function would deadlock.
	defer func() {
		stop()
		bar.Wait()
	}()

	// A nil BarHelper client is still a valid consumer, but passing it as a
	// non-nil interface holding a nil pointer would be confusing, so it is
	// only added when the integration is on.
	var consumers []keg.AmountConsumer
	if bar != nil {
		consumers = append(consumers, bar)
	}

	kegServer := keg.NewServer(st, bus, cfg.IncludeUnknownData, consumers...)
	commander := keg.NewCommander(kegServer.Registry())

	hub := ws.NewHub(st)
	go hub.Run(ctx, bus)

	go prune(ctx, st)

	kegListener, err := net.Listen("tcp", fmt.Sprintf(":%d", cfg.KegListenerPort))
	if err != nil {
		return fmt.Errorf("listen for kegs on port %d: %w", cfg.KegListenerPort, err)
	}

	apiServer := api.NewServer(st, commander, hub, bus, cfg, version, web.Static())
	httpServer := &http.Server{
		Addr:              fmt.Sprintf(":%d", cfg.HTTPListenerPort),
		Handler:           apiServer.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	errc := make(chan error, 2)

	go func() {
		slog.Info("listening for kegs", "port", cfg.KegListenerPort)
		if err := kegServer.Serve(kegListener); err != nil {
			errc <- fmt.Errorf("keg listener: %w", err)
		}
	}()

	go func() {
		slog.Info("listening for HTTP", "port", cfg.HTTPListenerPort)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errc <- fmt.Errorf("http server: %w", err)
		}
	}()

	select {
	case <-ctx.Done():
		slog.Info("shutting down")
	case err := <-errc:
		stop()
		shutdown(httpServer, kegListener, kegServer)
		return err
	}

	shutdown(httpServer, kegListener, kegServer)
	return nil
}

func shutdown(httpServer *http.Server, kegListener net.Listener, kegServer *keg.Server) {
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()

	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		slog.Warn("the HTTP server did not shut down cleanly", "error", err)
	}

	// Closing the listener stops new kegs connecting; Shutdown then closes the
	// live connections so their read loops can exit.
	kegListener.Close()
	kegServer.Shutdown()
}

// prune discards history older than the retention window, once at startup and
// daily thereafter.
func prune(ctx context.Context, st *store.Store) {
	ticker := time.NewTicker(pruneInterval)
	defer ticker.Stop()

	for {
		if removed, err := st.PruneLog(time.Now()); err != nil {
			slog.Error("failed to prune keg history", "error", err)
		} else if removed > 0 {
			slog.Info("pruned old keg history", "rows", removed)
		}

		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func setupLogging() {
	level := slog.LevelInfo
	if os.Getenv("LOG_LEVEL") != "" {
		if err := level.UnmarshalText([]byte(os.Getenv("LOG_LEVEL"))); err != nil {
			level = slog.LevelInfo
		}
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: level})))
}
