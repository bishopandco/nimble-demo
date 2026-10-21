package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"go-api/internal/monitor"
	"go-api/internal/store"
)

func main() {
	path := flag.String("database", "data/chat.db", "SQLite chat database")
	interval := flag.Duration("interval", time.Second, "Process sample interval")
	parent := flag.Int("parent-pid", 0, "API parent PID; zero for standalone monitoring")
	flag.Parse()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	db, err := store.Open(ctx, *path)
	if err == nil {
		defer db.Close()
		err = monitor.Run(ctx, db, os.Stdout, *interval, *parent)
	}
	if err != nil && ctx.Err() == nil {
		slog.Error("bot monitor stopped", "error", err)
		os.Exit(1)
	}
}
