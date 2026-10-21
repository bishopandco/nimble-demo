package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"go-api/internal/api"
	"go-api/internal/bot"
	"go-api/internal/store"
)

func main() {
	if err := run(); err != nil {
		slog.Error("server stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	path := env("DB_PATH", "data/chat.db")
	db, err := store.Open(ctx, path)
	if err != nil {
		return err
	}
	defer db.Close()
	timeout, err := time.ParseDuration(env("BOT_TIMEOUT", "2m"))
	if err != nil || timeout <= 0 {
		return errors.New("BOT_TIMEOUT must be a positive duration")
	}
	provider := env("BOT_PROVIDER", "auto")
	if provider != "auto" && provider != "codex" && provider != "claude" {
		return errors.New("BOT_PROVIDER must be auto, codex, or claude")
	}
	var options []api.Option
	if env("BOT_ENABLED", "true") != "false" {
		options = append(options, api.WithBot(bot.Client{Path: env("BOT_SIDECAR_PATH", "bin/bot-sidecar"), Provider: provider}, timeout))
	}
	handler, err := api.New(ctx, db, slog.Default(), options...)
	if err != nil {
		return err
	}
	defer handler.Close()
	server := &http.Server{
		Addr: env("ADDR", "127.0.0.1:8080"), Handler: handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	done := make(chan error, 1)
	go func() { done <- server.ListenAndServe() }()
	slog.Info("starting chat server", "address", server.Addr, "database", path)
	select {
	case err := <-done:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			_ = server.Close()
			return err
		}
		return nil
	}
}

func env(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
