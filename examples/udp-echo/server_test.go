package main

import (
	"context"
	"io"
	"log/slog"
	"net"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	sdk "github.com/aquila-hosting/aquila-hosting-sdk-go"
)

type fakeSDK struct {
	mu        sync.Mutex
	ready     int
	shutdowns int
	players   []int64
	updates   chan *sdk.GameServer
}

func newFakeSDK() *fakeSDK { return &fakeSDK{updates: make(chan *sdk.GameServer, 4)} }

func (f *fakeSDK) Ready(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ready++
	return nil
}

func (f *fakeSDK) Health(context.Context) error { return nil }

func (f *fakeSDK) Shutdown(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.shutdowns++
	return nil
}

func (f *fakeSDK) SetPlayers(_ context.Context, count, _ int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.players = append(f.players, count)
	return nil
}

func (f *fakeSDK) Watch(ctx context.Context, fn func(*sdk.GameServer)) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case gs := <-f.updates:
			fn(gs)
		}
	}
}

func (f *fakeSDK) shutdownCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.shutdowns
}

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }

func testServer() (*server, *fakeSDK, *clock) {
	f := newFakeSDK()
	c := &clock{t: time.Unix(1_700_000_000, 0)}
	cfg := config{idleTimeout: 2 * time.Minute, emptyTimeout: time.Minute, playerTimeout: 30 * time.Second, capacity: 8}
	return newServer(f, cfg, quiet, c.now), f, c
}

func allocated(labels, annotations map[string]string) *sdk.GameServer {
	return &sdk.GameServer{State: sdk.StateAllocated, Labels: labels, Annotations: annotations}
}

func TestEchoCarriesStateAndMatch(t *testing.T) {
	s, _, _ := testServer()
	s.onUpdate(allocated(
		map[string]string{sdk.LabelPrefix + "map": "dust", "aquilahosting.com/fleet": "arena"},
		map[string]string{sdk.LabelPrefix + "match": "m-1"},
	))

	reply, bye := s.handle("1.2.3.4:5000", "hello\n")

	if bye || reply != "[Allocated map=dust match=m-1] hello\n" {
		t.Errorf("reply %q bye %v", reply, bye)
	}
}

func TestByeEndsTheSession(t *testing.T) {
	s, _, _ := testServer()
	if _, bye := s.handle("1.2.3.4:5000", "BYE"); !bye {
		t.Error("bye did not end the session")
	}
}

func TestIdleAllocatedSessionShutsDownOnce(t *testing.T) {
	s, f, c := testServer()
	ctx := context.Background()

	c.t = c.t.Add(time.Hour)
	s.tick(ctx)
	if f.shutdownCount() != 0 {
		t.Fatal("a Ready server shut down for being idle")
	}

	s.onUpdate(allocated(nil, nil))
	c.t = c.t.Add(time.Minute)
	s.tick(ctx)
	if f.shutdownCount() != 0 {
		t.Fatal("shut down inside the idle timeout")
	}

	c.t = c.t.Add(2 * time.Minute)
	s.tick(ctx)
	s.tick(ctx)
	if got := f.shutdownCount(); got != 1 {
		t.Errorf("Shutdown called %d times, want 1", got)
	}
}

func TestPlayersReportedOnChange(t *testing.T) {
	s, f, c := testServer()
	ctx := context.Background()

	s.tick(ctx)
	s.handle("1.2.3.4:5000", "hi")
	s.handle("5.6.7.8:5000", "hi")
	s.tick(ctx)
	s.tick(ctx)
	c.t = c.t.Add(time.Minute)
	s.tick(ctx)

	if got, want := f.players, []int64{0, 2, 0}; !slices.Equal(got, want) {
		t.Errorf("reported %v, want %v", got, want)
	}
}

const matchJSON = `{"matchId":"m-9","queue":"duel","teams":[{"playerIds":["alice"]},{"playerIds":["bob"]}]}`

func inMatch(s *server) {
	s.onUpdate(allocated(
		map[string]string{sdk.LabelPrefix + "match-id": "m-9"},
		map[string]string{sdk.LabelPrefix + matchAnnotation: matchJSON},
	))
}

func TestMatchSeatsItsPlayers(t *testing.T) {
	s, _, _ := testServer()
	inMatch(s)

	reply, _ := s.handle("1.1.1.1:1", "join alice")
	if reply != "[Allocated match-id=m-9] welcome alice, team 1, 1/2 joined\n" {
		t.Errorf("join reply %q", reply)
	}
	if reply, _ := s.handle("2.2.2.2:2", "join mallory"); !strings.Contains(reply, "mallory is not in this match") {
		t.Errorf("stranger reply %q", reply)
	}
	if reply, _ := s.handle("3.3.3.3:3", "join bob"); !strings.Contains(reply, "team 2, 2/2 joined") {
		t.Errorf("second join reply %q", reply)
	}
}

func TestMatchEndsWhenEveryoneLeft(t *testing.T) {
	s, _, _ := testServer()
	inMatch(s)
	s.handle("1.1.1.1:1", "join alice")
	s.handle("3.3.3.3:3", "join bob")

	if _, bye := s.handle("2.2.2.2:2", "bye"); bye {
		t.Fatal("someone outside the match ended it")
	}
	if reply, bye := s.handle("1.1.1.1:1", "bye"); bye || !strings.Contains(reply, "1 still playing") {
		t.Fatalf("first bye: %q %v", reply, bye)
	}
	if reply, bye := s.handle("3.3.3.3:3", "bye"); !bye || !strings.Contains(reply, "everyone is gone") {
		t.Fatalf("last bye: %q %v", reply, bye)
	}
}

// A plain value under the match key, as the older scripts set, stays in the
// echoed metadata.
func TestPlainMatchValueIsMetadata(t *testing.T) {
	s, _, _ := testServer()
	s.onUpdate(allocated(nil, map[string]string{sdk.LabelPrefix + "match": "m-1"}))

	if reply, _ := s.handle("1.1.1.1:1", "join alice"); !strings.Contains(reply, "match=m-1] there is no match to join yet") {
		t.Errorf("reply %q", reply)
	}
}

func TestNobodyJoinsShutsDown(t *testing.T) {
	s, f, c := testServer()
	ctx := context.Background()
	inMatch(s)

	c.t = c.t.Add(59 * time.Second)
	s.tick(ctx)
	if f.shutdownCount() != 0 {
		t.Fatal("shut down before the empty timeout")
	}
	c.t = c.t.Add(2 * time.Second)
	s.tick(ctx)
	if f.shutdownCount() != 1 {
		t.Fatal("an allocated server nobody joined kept running")
	}
}

func TestEmptyTimeoutSparesAServerSomeoneJoined(t *testing.T) {
	s, f, c := testServer()
	ctx := context.Background()
	inMatch(s)

	c.t = c.t.Add(30 * time.Second)
	s.handle("1.1.1.1:1", "join alice")
	c.t = c.t.Add(time.Minute)
	s.tick(ctx)
	if f.shutdownCount() != 0 {
		t.Fatal("shut down a server with a player in it")
	}
}

// The server comes up, reports Ready, learns it was allocated, echoes, and
// shuts down on bye.
func TestRunOverUDP(t *testing.T) {
	f := newFakeSDK()
	cfg := config{idleTimeout: time.Minute, playerTimeout: 30 * time.Second, capacity: 8}
	s := newServer(f, cfg, quiet, time.Now)

	conn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- s.run(ctx, conn) }()

	f.updates <- allocated(map[string]string{sdk.LabelPrefix + "map": "dust"}, nil)

	client, err := net.Dial("udp", conn.LocalAddr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = client.Close() }()
	buf := make([]byte, 1500)

	// The watch update lands asynchronously, so poll until the reply has it.
	var reply string
	for range 50 {
		if _, err := client.Write([]byte("hello")); err != nil {
			t.Fatal(err)
		}
		_ = client.SetReadDeadline(time.Now().Add(time.Second))
		n, err := client.Read(buf)
		if err != nil {
			t.Fatal(err)
		}
		reply = string(buf[:n])
		if strings.HasPrefix(reply, "[Allocated") {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if reply != "[Allocated map=dust] hello\n" {
		t.Fatalf("reply %q", reply)
	}

	if _, err := client.Write([]byte("bye")); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("run: %v", err)
		}
	case <-ctx.Done():
		t.Fatal("server did not stop after bye")
	}
	if f.ready != 1 || f.shutdownCount() != 1 {
		t.Errorf("Ready %d, Shutdown %d, want 1 each", f.ready, f.shutdownCount())
	}
}
