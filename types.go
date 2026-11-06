// Package sdk is the Go client for the Aquila game server SDK. A fleet server
// uses it to report Ready, send health pings, learn it was allocated, and shut
// down when its match is over.
package sdk

import "time"

// Env vars the operator sets in every container of a fleet server.
const (
	// EnvURL is the SDK endpoint.
	EnvURL = "AQUILA_SDK_URL"
	// EnvTokenFile is the path of the token that proves which server is
	// calling. Kubernetes rotates the file, so it is read on every call.
	EnvTokenFile = "AQUILA_SDK_TOKEN_FILE"
	// EnvGameServer is the name of this server.
	EnvGameServer = "AQUILA_GAMESERVER_NAME"
)

// TokenAudience is the audience of the token the operator mounts.
const TokenAudience = "sdk.aquilahosting.com"

// LabelPrefix is put in front of every label and annotation key set through
// the SDK, so a game can never overwrite the platform's own.
const LabelPrefix = "sdk.aquilahosting.com/"

// Endpoint paths, relative to EnvURL.
const (
	PathReady      = "/v1/ready"
	PathHealth     = "/v1/health"
	PathAllocate   = "/v1/allocate"
	PathReserve    = "/v1/reserve"
	PathShutdown   = "/v1/shutdown"
	PathPlayers    = "/v1/players"
	PathLabels     = "/v1/labels/"
	PathAnnotation = "/v1/annotations/"
	PathGameServer = "/v1/gameserver"
	PathWatch      = "/v1/watch"
)

// State is where a server is in its lifecycle.
type State string

const (
	StateStarting  State = "Starting"
	StateReady     State = "Ready"
	StateAllocated State = "Allocated"
	StateReserved  State = "Reserved"
	StateShutdown  State = "Shutdown"
	StateUnhealthy State = "Unhealthy"
)

// Port is a port players connect to.
type Port struct {
	Name     string `json:"name"`
	Port     int32  `json:"port"`
	Protocol string `json:"protocol"`
}

// Players is the player count and capacity the game last reported.
type Players struct {
	Count    int64 `json:"count"`
	Capacity int64 `json:"capacity"`
}

// GameServer is the server as the SDK sees it.
type GameServer struct {
	Name          string            `json:"name"`
	State         State             `json:"state"`
	Address       string            `json:"address,omitempty"`
	Ports         []Port            `json:"ports,omitempty"`
	Labels        map[string]string `json:"labels,omitempty"`
	Annotations   map[string]string `json:"annotations,omitempty"`
	Players       *Players          `json:"players,omitempty"`
	ReservedUntil *time.Time        `json:"reservedUntil,omitempty"`
}

// ReserveRequest holds a server back from allocation for a while.
type ReserveRequest struct {
	Seconds int64 `json:"seconds"`
}

// ValueRequest sets one label or annotation.
type ValueRequest struct {
	Value string `json:"value"`
}

// Error codes.
const (
	CodeConflict     = "conflict"
	CodeInvalid      = "invalid"
	CodeUnauthorized = "unauthorized"
	CodeForbidden    = "forbidden"
	CodeRateLimited  = "rate_limited"
	CodeInternal     = "internal"
)

// Error is the body of every failed call.
type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *Error) Error() string { return e.Message }
