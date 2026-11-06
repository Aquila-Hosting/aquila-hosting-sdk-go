// Command udp-echo-agones is the Agones SDK compatibility fixture. It is
// built against Agones' Go SDK only and runs on an Aquila fleet with the
// Agones SDK protocol. It goes Ready, echoes each packet with its state and
// match, and shuts down and exits on "bye" or when nobody sends anything for
// a minute after allocation.
package main

import (
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"net"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	sdk "agones.dev/agones/pkg/sdk"
	agones "agones.dev/agones/sdks/go"
)

const (
	labelPrefix = "agones.dev/sdk-"
	idleTimeout = time.Minute
)

type state struct {
	mu       sync.Mutex
	state    string
	meta     map[string]string
	lastSeen time.Time
}

func (s *state) update(gs *sdk.GameServer) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if gs.Status.State != s.state && gs.Status.State == "Allocated" {
		s.lastSeen = time.Now()
	}
	s.state = gs.Status.State
	s.meta = map[string]string{}
	for _, m := range []map[string]string{gs.ObjectMeta.Labels, gs.ObjectMeta.Annotations} {
		for k, v := range m {
			if key, ok := strings.CutPrefix(k, labelPrefix); ok {
				s.meta[key] = v
			}
		}
	}
}

func (s *state) reply(msg string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastSeen = time.Now()
	var b strings.Builder
	fmt.Fprintf(&b, "%s\nstate=%s\n", msg, s.state)
	for _, k := range slices.Sorted(maps.Keys(s.meta)) {
		fmt.Fprintf(&b, "%s=%s\n", k, s.meta[k])
	}
	return b.String()
}

func (s *state) idle() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state == "Allocated" && time.Since(s.lastSeen) > idleTimeout
}

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if err := run(log); err != nil {
		log.Error("Server stopped", "error", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
	s, err := agones.NewSDK()
	if err != nil {
		return fmt.Errorf("connecting to the SDK: %w", err)
	}
	conn, err := net.ListenPacket("udp", ":7777")
	if err != nil {
		return err
	}
	defer conn.Close()

	st := &state{}
	if err := s.WatchGameServer(func(gs *sdk.GameServer) {
		st.update(gs)
		log.Info("Server changed", "state", gs.Status.State)
	}); err != nil {
		return fmt.Errorf("watching: %w", err)
	}
	if err := s.Ready(); err != nil {
		return fmt.Errorf("marking Ready: %w", err)
	}

	done := make(chan struct{})
	go func() {
		t := time.NewTicker(2 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-t.C:
				if err := s.Health(); err != nil {
					log.Warn("Health ping failed", "error", err)
				}
				if st.idle() {
					log.Info("Nobody sent anything since allocation, shutting down")
					if err := s.Shutdown(); err != nil {
						log.Warn("Shutdown failed", "error", err)
						continue
					}
					// Ends the read loop below.
					conn.Close()
					return
				}
			}
		}
	}()
	defer close(done)

	buf := make([]byte, 1500)
	for {
		n, from, err := conn.ReadFrom(buf)
		if errors.Is(err, net.ErrClosed) {
			return nil
		}
		if err != nil {
			return err
		}
		msg := strings.TrimSpace(string(buf[:n]))
		if _, err := conn.WriteTo([]byte(st.reply(msg)), from); err != nil {
			log.Warn("Reply failed", "error", err)
		}
		if msg == "bye" {
			log.Info("Got bye, shutting down")
			if err := s.Shutdown(); err != nil {
				return fmt.Errorf("shutting down: %w", err)
			}
			return nil
		}
	}
}
