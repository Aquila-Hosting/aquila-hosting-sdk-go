// Command udp-echo is the smallest fleet server: it echoes UDP packets back
// with its state and match metadata, reports Ready and health through the
// SDK, counts players, and shuts down when a session ends. Its fleet then
// replaces it.
//
// Allocated by the managed matchmaker, it reads the match annotation:
// players send "join <player id>" to take their seat and "bye" to leave, and
// the session ends once everyone who joined has left. Allocated directly,
// "bye" ends it straight away.
//
// Environment:
//
//	PORT           UDP port to listen on (default 7777)
//	IDLE_TIMEOUT   how long an allocated session may go quiet (default 2m)
//	EMPTY_TIMEOUT  how long an allocated session waits for anyone (default 1m)
//	CAPACITY       players reported as capacity (default 8)
package main

import (
	"context"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	sdk "github.com/aquila-hosting/aquila-hosting-sdk-go"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if err := run(log); err != nil {
		log.Error("Server stopped", "error", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
	defer stop()

	client, err := sdk.New()
	if err != nil {
		return err
	}

	idle, err := time.ParseDuration(env("IDLE_TIMEOUT", "2m"))
	if err != nil {
		return err
	}
	empty, err := time.ParseDuration(env("EMPTY_TIMEOUT", "1m"))
	if err != nil {
		return err
	}
	capacity, err := strconv.ParseInt(env("CAPACITY", "8"), 10, 64)
	if err != nil {
		return err
	}

	addr := ":" + env("PORT", "7777")
	conn, err := net.ListenPacket("udp", addr)
	if err != nil {
		return err
	}
	log.Info("Listening", "addr", addr, "gameServer", os.Getenv(sdk.EnvGameServer))

	cfg := config{idleTimeout: idle, emptyTimeout: empty, playerTimeout: 30 * time.Second, capacity: capacity}
	return newServer(client, cfg, log, time.Now).run(ctx, conn)
}

func env(name, def string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return def
}
