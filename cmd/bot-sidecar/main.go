package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"go-api/internal/bot"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	timeout := 2 * time.Minute
	if value := os.Getenv("BOT_TIMEOUT"); value != "" {
		duration, err := time.ParseDuration(value)
		if err != nil || duration <= 0 {
			slog.Error("BOT_TIMEOUT must be a positive duration")
			os.Exit(1)
		}
		timeout = duration
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	generator := bot.CLI{CodexPath: os.Getenv("BOT_CODEX_BIN"), ClaudePath: os.Getenv("BOT_CLAUDE_BIN"), Dir: os.Getenv("BOT_WORKDIR")}
	if err := bot.Serve(ctx, os.Stdin, os.Stdout, generator); err != nil {
		slog.Error("sidecar response failed", "error", err)
		os.Exit(1)
	}
}
