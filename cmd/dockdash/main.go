package main

import (
	"context"
	"errors"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"dockdash/internal/config"
	"dockdash/internal/db"
	"dockdash/internal/server"
	"dockdash/internal/session"
)

func healthcheck() {
	port := 3001
	if raw := os.Getenv("PORT"); raw != "" {
		if value, err := strconv.Atoi(raw); err == nil {
			port = value
		}
	}
	client := &http.Client{Timeout: 3 * time.Second}
	response, err := client.Get("http://127.0.0.1:" + strconv.Itoa(port) + "/api/health/ready")
	if err != nil {
		log.Printf("healthcheck failed: %v", err)
		os.Exit(1)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		log.Printf("healthcheck failed with status %d", response.StatusCode)
		os.Exit(1)
	}
}

func main() {
	if len(os.Args) > 1 && os.Args[1] == "healthcheck" {
		healthcheck()
		return
	}

	cfg, err := config.Load()
	if err != nil {
		log.Fatal(err)
	}

	database, err := db.Open(cfg.DBPath)
	if err != nil {
		log.Fatal(err)
	}
	defer database.Close()

	if err := db.Migrate(database, cfg.MigrationsDir); err != nil {
		log.Fatal(err)
	}

	store, err := session.New(database, cfg.SessionSecret, cfg.SessionMaxAge, cfg.SecureCookies)
	if err != nil {
		log.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	store.StartCleanup(ctx)

	api := server.New(cfg, database, store)
	api.Start(ctx)
	defer api.Shutdown()
	httpServer := &http.Server{
		Addr:              ":" + strconv.Itoa(cfg.Port),
		Handler:           api.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	listener, err := net.Listen("tcp", httpServer.Addr)
	if err != nil {
		log.Fatalf("listen: %v", err)
	}
	errorsCh := make(chan error, 1)
	go func() {
		err := httpServer.Serve(listener)
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			errorsCh <- err
		}
	}()

	api.MarkReady()
	log.Printf("DockDash server running on http://localhost:%d", cfg.Port)

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)
	select {
	case err := <-errorsCh:
		log.Printf("server error: %v", err)
	case <-signals:
	}

	api.MarkNotReady()
	cancel()
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer shutdownCancel()
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		log.Printf("shutdown error: %v", err)
	}
}
