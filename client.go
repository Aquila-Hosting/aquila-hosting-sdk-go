package sdk

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// Client talks to the SDK endpoint of the server it runs in.
type Client struct {
	baseURL   string
	tokenFile string
	http      *http.Client
}

// New reads the endpoint and token path from the environment the operator
// sets up.
func New() (*Client, error) {
	base := os.Getenv(EnvURL)
	if base == "" {
		return nil, fmt.Errorf("%s is not set, so this process is not running as a fleet server", EnvURL)
	}
	return NewWithConfig(base, os.Getenv(EnvTokenFile), nil), nil
}

// NewWithConfig builds a client for a given endpoint, for tests and local
// runs. An empty tokenFile sends no token.
func NewWithConfig(baseURL, tokenFile string, hc *http.Client) *Client {
	if hc == nil {
		hc = &http.Client{Timeout: 10 * time.Second}
	}
	return &Client{baseURL: strings.TrimRight(baseURL, "/"), tokenFile: tokenFile, http: hc}
}

// Ready marks the server ready to be allocated.
func (c *Client) Ready(ctx context.Context) error {
	return c.do(ctx, http.MethodPost, PathReady, nil, nil)
}

// Health reports the server is alive. Call it more often than the fleet's
// health period, or the server is marked Unhealthy and replaced.
func (c *Client) Health(ctx context.Context) error {
	return c.do(ctx, http.MethodPost, PathHealth, nil, nil)
}

// Allocate marks the server allocated, for games that run their own lobby.
func (c *Client) Allocate(ctx context.Context) error {
	return c.do(ctx, http.MethodPost, PathAllocate, nil, nil)
}

// Reserve holds the server back from allocation and scale-down for d.
func (c *Client) Reserve(ctx context.Context, d time.Duration) error {
	return c.do(ctx, http.MethodPost, PathReserve, ReserveRequest{Seconds: int64(d / time.Second)}, nil)
}

// Shutdown ends the server. Its fleet replaces it.
func (c *Client) Shutdown(ctx context.Context) error {
	return c.do(ctx, http.MethodPost, PathShutdown, nil, nil)
}

// SetPlayers reports the player count and capacity.
func (c *Client) SetPlayers(ctx context.Context, count, capacity int64) error {
	return c.do(ctx, http.MethodPut, PathPlayers, Players{Count: count, Capacity: capacity}, nil)
}

// SetLabel sets a label a matchmaker can select on. It is stored with
// LabelPrefix in front of key.
func (c *Client) SetLabel(ctx context.Context, key, value string) error {
	return c.do(ctx, http.MethodPut, PathLabels+url.PathEscape(key), ValueRequest{Value: value}, nil)
}

// SetAnnotation sets an annotation, stored with LabelPrefix in front of key.
func (c *Client) SetAnnotation(ctx context.Context, key, value string) error {
	return c.do(ctx, http.MethodPut, PathAnnotation+url.PathEscape(key), ValueRequest{Value: value}, nil)
}

// GameServer returns the server's current state.
func (c *Client) GameServer(ctx context.Context) (*GameServer, error) {
	var gs GameServer
	if err := c.do(ctx, http.MethodGet, PathGameServer, nil, &gs); err != nil {
		return nil, err
	}
	return &gs, nil
}

// Watch calls f with the server's state now and on every change, until ctx
// ends or the stream breaks. Callers that need it to keep going call it again.
func (c *Client) Watch(ctx context.Context, f func(*GameServer)) error {
	req, err := c.request(ctx, http.MethodGet, PathWatch, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "text/event-stream")

	// The stream outlives any client timeout.
	stream := *c.http
	stream.Timeout = 0
	resp, err := stream.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return readError(resp)
	}

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		data, ok := strings.CutPrefix(scanner.Text(), "data: ")
		if !ok {
			continue
		}
		var gs GameServer
		if err := json.Unmarshal([]byte(data), &gs); err != nil {
			return fmt.Errorf("decoding watch event: %w", err)
		}
		f(&gs)
	}
	if err := scanner.Err(); err != nil && ctx.Err() == nil {
		return err
	}
	return ctx.Err()
}

func (c *Client) do(ctx context.Context, method, path string, in, out any) error {
	var body io.Reader
	if in != nil {
		raw, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(raw)
	}
	req, err := c.request(ctx, method, path, body)
	if err != nil {
		return err
	}
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 300 {
		return readError(resp)
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func (c *Client) request(ctx context.Context, method, path string, body io.Reader) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, body)
	if err != nil {
		return nil, err
	}
	if c.tokenFile != "" {
		token, err := os.ReadFile(c.tokenFile)
		if err != nil {
			return nil, fmt.Errorf("reading SDK token: %w", err)
		}
		req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(string(token)))
	}
	return req, nil
}

func readError(resp *http.Response) error {
	var e Error
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64*1024)).Decode(&e); err != nil || e.Message == "" {
		return fmt.Errorf("SDK call failed with HTTP %d", resp.StatusCode)
	}
	return &e
}

// IsConflict reports whether err is a call the server's state does not allow,
// such as Ready on a server that already shut down.
func IsConflict(err error) bool {
	var e *Error
	return errors.As(err, &e) && e.Code == CodeConflict
}
