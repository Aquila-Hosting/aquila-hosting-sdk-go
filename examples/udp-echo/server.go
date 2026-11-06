package main

import (
	"context"
	"fmt"
	"log/slog"
	"maps"
	"net"
	"slices"
	"strings"
	"sync"
	"time"

	sdk "github.com/aquila-hosting/aquila-hosting-sdk-go"
)

// lifecycle is the part of the SDK client the server uses.
type lifecycle interface {
	Ready(ctx context.Context) error
	Health(ctx context.Context) error
	Shutdown(ctx context.Context) error
	SetPlayers(ctx context.Context, count, capacity int64) error
	Watch(ctx context.Context, f func(*sdk.GameServer)) error
}

type config struct {
	// idleTimeout ends an allocated session that went quiet.
	idleTimeout time.Duration
	// emptyTimeout ends an allocated session nobody ever sent anything to,
	// such as a server the matchmaker gave up on.
	emptyTimeout time.Duration
	// playerTimeout drops a player who has gone quiet from the count.
	playerTimeout time.Duration
	capacity      int64
}

// server echoes every packet back with what it knows of its match.
type server struct {
	sdk lifecycle
	cfg config
	log *slog.Logger
	now func() time.Time

	mu          sync.Mutex
	state       sdk.State
	meta        map[string]string
	match       *matchInfo
	allocatedAt time.Time
	heard       bool
	lastPacket  time.Time
	// joined maps each player who joined the match to their address, and
	// left holds those who said bye.
	joined   map[string]string
	left     map[string]bool
	players  map[string]time.Time
	reported int64
	shutdown chan struct{}
	once     sync.Once
}

func newServer(lc lifecycle, cfg config, log *slog.Logger, now func() time.Time) *server {
	return &server{
		sdk:      lc,
		cfg:      cfg,
		log:      log,
		now:      now,
		state:    sdk.StateStarting,
		meta:     map[string]string{},
		joined:   map[string]string{},
		left:     map[string]bool{},
		players:  map[string]time.Time{},
		reported: -1,
		shutdown: make(chan struct{}),
	}
}

// onUpdate tracks the server's state and the metadata a matchmaker or the
// game set on it.
func (s *server) onUpdate(gs *sdk.GameServer) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if gs.State != s.state {
		s.log.Info("State changed", "from", s.state, "to", gs.State)
		if gs.State == sdk.StateAllocated {
			// The idle and empty clocks start at allocation, so players get
			// time to connect.
			s.allocatedAt = s.now()
			s.lastPacket = s.allocatedAt
			s.heard = false
		}
	}
	s.state = gs.State

	s.meta = map[string]string{}
	for _, m := range []map[string]string{gs.Labels, gs.Annotations} {
		for k, v := range m {
			if key, ok := strings.CutPrefix(k, sdk.LabelPrefix); ok {
				s.meta[key] = v
			}
		}
	}
	if m, ok := parseMatch(s.meta[matchAnnotation]); ok {
		s.match = m
		// The match is in its own line of the replies, not the metadata.
		delete(s.meta, matchAnnotation)
	}
}

// handle answers one packet. In a match, "join <player>" claims a seat and
// "bye" leaves, and the session ends once everyone who joined has left.
// Without a match "bye" ends it straight away.
func (s *server) handle(from string, msg string) (reply string, bye bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := s.now()
	s.lastPacket = now
	s.heard = true
	s.players[from] = now

	msg = strings.TrimSpace(msg)
	var meta []string
	for _, k := range slices.Sorted(maps.Keys(s.meta)) {
		meta = append(meta, k+"="+s.meta[k])
	}
	prefix := string(s.state)
	if len(meta) > 0 {
		prefix += " " + strings.Join(meta, " ")
	}
	say := func(format string, args ...any) string {
		return fmt.Sprintf("[%s] %s\n", prefix, fmt.Sprintf(format, args...))
	}

	if player, ok := strings.CutPrefix(msg, "join "); ok {
		return say("%s", s.join(strings.TrimSpace(player), from)), false
	}
	if !strings.EqualFold(msg, "bye") {
		return say("%s", msg), false
	}
	if s.match == nil {
		return say("bye, shutting down"), true
	}
	player := s.playerAt(from)
	if player == "" {
		return say("you have not joined this match"), false
	}
	s.left[player] = true
	if playing := len(s.joined) - len(s.left); playing > 0 {
		return say("%s left, %d still playing", player, playing), false
	}
	return say("%s left, everyone is gone, shutting down", player), true
}

// join seats a player of the match. The caller holds s.mu.
func (s *server) join(player, from string) string {
	if s.match == nil {
		return "there is no match to join yet"
	}
	team, ok := s.match.team(player)
	if !ok {
		return player + " is not in this match"
	}
	s.joined[player] = from
	delete(s.left, player)
	return fmt.Sprintf("welcome %s, team %d, %d/%d joined", player, team+1, len(s.joined), s.match.players())
}

// playerAt is the player who joined from addr. The caller holds s.mu.
func (s *server) playerAt(addr string) string {
	for p, a := range s.joined {
		if a == addr {
			return p
		}
	}
	return ""
}

// tick runs once a second: it drops quiet players, reports the count when it
// changed, and ends an allocated session that went idle.
func (s *server) tick(ctx context.Context) {
	s.mu.Lock()
	now := s.now()
	for addr, seen := range s.players {
		if now.Sub(seen) > s.cfg.playerTimeout {
			delete(s.players, addr)
		}
	}
	count := int64(len(s.players))
	report := count != s.reported
	allocated := s.state == sdk.StateAllocated
	empty := allocated && !s.heard && now.Sub(s.allocatedAt) > s.cfg.emptyTimeout
	idle := allocated && now.Sub(s.lastPacket) > s.cfg.idleTimeout
	s.mu.Unlock()

	if report {
		if err := s.sdk.SetPlayers(ctx, count, s.cfg.capacity); err != nil {
			s.log.Warn("Could not report players", "error", err)
		} else {
			s.mu.Lock()
			s.reported = count
			s.mu.Unlock()
		}
	}
	switch {
	case empty:
		s.log.Info("Nobody joined, shutting down", "after", s.cfg.emptyTimeout)
		s.end(ctx)
	case idle:
		s.log.Info("Session idle, shutting down", "after", s.cfg.idleTimeout)
		s.end(ctx)
	}
}

// end calls Shutdown once. The fleet replaces the server.
func (s *server) end(ctx context.Context) {
	s.once.Do(func() {
		if err := s.sdk.Shutdown(ctx); err != nil {
			s.log.Error("Shutdown failed", "error", err)
		}
		close(s.shutdown)
	})
}

// run serves conn until the session ends or ctx is cancelled.
func (s *server) run(ctx context.Context, conn net.PacketConn) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	go s.watch(ctx)
	go s.every(ctx, time.Second, s.tick)
	go s.every(ctx, 2*time.Second, func(ctx context.Context) {
		if err := s.sdk.Health(ctx); err != nil {
			s.log.Warn("Health ping failed", "error", err)
		}
	})

	if err := retry(ctx, s.log, "Ready", s.sdk.Ready); err != nil {
		return err
	}
	s.log.Info("Ready for allocation")

	go func() {
		select {
		case <-ctx.Done():
		case <-s.shutdown:
		}
		_ = conn.Close()
	}()

	buf := make([]byte, 1500)
	for {
		n, addr, err := conn.ReadFrom(buf)
		if err != nil {
			select {
			case <-s.shutdown:
				return nil
			case <-ctx.Done():
				return nil
			default:
				return err
			}
		}
		reply, bye := s.handle(addr.String(), string(buf[:n]))
		if _, err := conn.WriteTo([]byte(reply), addr); err != nil {
			s.log.Warn("Could not reply", "to", addr, "error", err)
		}
		if bye {
			s.end(ctx)
		}
	}
}

// watch follows the server's state, reconnecting when the stream drops.
func (s *server) watch(ctx context.Context) {
	for ctx.Err() == nil {
		if err := s.sdk.Watch(ctx, s.onUpdate); err != nil && ctx.Err() == nil {
			s.log.Warn("Watch ended, reconnecting", "error", err)
		}
		select {
		case <-ctx.Done():
		case <-time.After(time.Second):
		}
	}
}

func (s *server) every(ctx context.Context, d time.Duration, f func(context.Context)) {
	t := time.NewTicker(d)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			f(ctx)
		}
	}
}

// retry calls f until it succeeds, backing off up to 10 seconds. The gateway
// can be briefly unreachable while the operator restarts.
func retry(ctx context.Context, log *slog.Logger, what string, f func(context.Context) error) error {
	wait := 250 * time.Millisecond
	for {
		err := f(ctx)
		if err == nil {
			return nil
		}
		log.Warn(what+" failed, retrying", "error", err, "in", wait)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(wait):
		}
		wait = min(wait*2, 10*time.Second)
	}
}
