package main

import (
	"encoding/json"
	"slices"
)

// matchAnnotation is the key, after the SDK prefix, the managed matchmaker
// writes the match under.
const matchAnnotation = "match"

// matchInfo is the managed matchmaker's match annotation.
type matchInfo struct {
	MatchID string `json:"matchId"`
	Queue   string `json:"queue"`
	Teams   []struct {
		PlayerIDs []string `json:"playerIds"`
	} `json:"teams"`
}

// parseMatch reads the annotation. Anything else under the same key, such as
// a plain id a script set, is not a match.
func parseMatch(raw string) (*matchInfo, bool) {
	var m matchInfo
	if raw == "" || json.Unmarshal([]byte(raw), &m) != nil || m.MatchID == "" {
		return nil, false
	}
	return &m, true
}

func (m *matchInfo) team(player string) (int, bool) {
	for i, t := range m.Teams {
		if slices.Contains(t.PlayerIDs, player) {
			return i, true
		}
	}
	return 0, false
}

func (m *matchInfo) players() int {
	n := 0
	for _, t := range m.Teams {
		n += len(t.PlayerIDs)
	}
	return n
}
