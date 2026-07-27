//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package replaytest compares deterministic session and memory replays across
// storage backends.
package replaytest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strconv"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

// Backend groups the services that participate in one replay.
//
// Session and Memory must use the same logical application and user namespace.
// Track cases require Session to also implement session.TrackService.
type Backend struct {
	Name    string
	Session session.Service
	Memory  memory.Service
}

// Case is one deterministic mutation sequence followed by a snapshot read.
// Implementations should use a case-specific session ID so cases can share a
// backend without affecting one another.
type Case struct {
	Name   string
	Replay func(context.Context, Backend) (Snapshot, error)
}

// Snapshot is the normalized observable result of a replay case.
// Data must contain only JSON-compatible values after normalization.
type Snapshot struct {
	SessionID string         `json:"session_id"`
	Data      map[string]any `json:"data"`
}

// Difference identifies one observable mismatch between two snapshots.
type Difference struct {
	Case        string          `json:"case"`
	Backend     string          `json:"backend"`
	SessionID   string          `json:"session_id"`
	Path        string          `json:"path"`
	Baseline    json.RawMessage `json:"baseline"`
	Actual      json.RawMessage `json:"actual"`
	AllowedDiff bool            `json:"allowed_diff"`
	Reason      string          `json:"reason,omitempty"`
}

// Report contains all differences found while replaying cases. An empty
// Differences slice means every backend agreed with the baseline.
type Report struct {
	Baseline    string       `json:"baseline"`
	Differences []Difference `json:"differences"`
}

// HasDisallowedDifferences reports whether a report contains a failing
// comparison result.
func (r Report) HasDisallowedDifferences() bool {
	for _, diff := range r.Differences {
		if !diff.AllowedDiff {
			return true
		}
	}
	return false
}

// JSON returns an indented, portable report suitable for CI artifacts.
func (r Report) JSON() ([]byte, error) {
	return json.MarshalIndent(r, "", "  ")
}

// Run replays each case on every backend and compares each result with the
// first backend. Replay failures are represented as report differences so a
// single run can show all affected backends and cases.
func Run(ctx context.Context, backends []Backend, cases []Case) (Report, error) {
	if len(backends) < 2 {
		return Report{}, errors.New("replay requires at least two backends")
	}
	if err := validateBackends(backends); err != nil {
		return Report{}, err
	}

	report := Report{Baseline: backends[0].Name, Differences: []Difference{}}
	for _, replayCase := range cases {
		if replayCase.Name == "" || replayCase.Replay == nil {
			return Report{}, errors.New("replay case name and function are required")
		}
		baseline, err := replayCase.Replay(ctx, backends[0])
		if err != nil {
			report.Differences = append(report.Differences, executionDifference(
				replayCase.Name, backends[0].Name, "", err,
			))
			continue
		}
		for _, backend := range backends[1:] {
			actual, replayErr := replayCase.Replay(ctx, backend)
			if replayErr != nil {
				report.Differences = append(report.Differences, executionDifference(
					replayCase.Name, backend.Name, baseline.SessionID, replayErr,
				))
				continue
			}
			report.Differences = append(report.Differences, Compare(
				replayCase.Name, backend.Name, baseline, actual,
			)...)
		}
	}
	return report, nil
}

// Compare returns field-level differences between normalized snapshots.
func Compare(caseName, backend string, baseline, actual Snapshot) []Difference {
	var differences []Difference
	compareValue(
		"data", baseline.Data, actual.Data,
		func(path string, left, right any) {
			differences = append(differences, Difference{
				Case:      caseName,
				Backend:   backend,
				SessionID: baseline.SessionID,
				Path:      path,
				Baseline:  marshalValue(left),
				Actual:    marshalValue(right),
			})
		},
	)
	return differences
}

// Capture reads a session and its memories, removes volatile backend values,
// and returns a snapshot that can be compared by Compare. summaryFilterKeys
// identifies the summary scopes that the replay case expects to observe.
func Capture(
	ctx context.Context,
	backend Backend,
	key session.Key,
	summaryFilterKeys ...string,
) (Snapshot, error) {
	if err := validateBackend(backend); err != nil {
		return Snapshot{}, err
	}
	sess, err := backend.Session.GetSession(ctx, key)
	if err != nil {
		return Snapshot{}, fmt.Errorf("get session: %w", err)
	}
	if sess == nil {
		return Snapshot{}, errors.New("session not found")
	}
	// Services may update a live session asynchronously. Snapshot from a clone so
	// a concurrent append cannot mix fields from two logical reads.
	sess = sess.Clone()
	entries, err := backend.Memory.ReadMemories(ctx, memory.UserKey{
		AppName: key.AppName,
		UserID:  key.UserID,
	}, 0)
	if err != nil {
		return Snapshot{}, fmt.Errorf("read memories: %w", err)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].ID < entries[j].ID })
	summaries := make(map[string]string, len(summaryFilterKeys))
	for _, filterKey := range summaryFilterKeys {
		if text, ok := backend.Session.GetSessionSummaryText(
			ctx, sess, session.WithSummaryFilterKey(filterKey),
		); ok {
			summaries[filterKey] = text
		}
	}
	events := sess.GetEvents()
	if events == nil {
		events = []event.Event{}
	}
	state := sess.SnapshotState()
	if state == nil {
		state = session.StateMap{}
	}
	tracks := sess.Tracks
	if tracks == nil {
		tracks = map[session.Track]*session.TrackEvents{}
	}
	if entries == nil {
		entries = []*memory.Entry{}
	}
	data, err := normalize(map[string]any{
		"events":    events,
		"state":     state,
		"summaries": summaries,
		"tracks":    tracks,
		"memories":  entries,
	})
	if err != nil {
		return Snapshot{}, fmt.Errorf("normalize snapshot: %w", err)
	}
	return Snapshot{SessionID: key.SessionID, Data: data.(map[string]any)}, nil
}

func validateBackends(backends []Backend) error {
	for _, backend := range backends {
		if err := validateBackend(backend); err != nil {
			return err
		}
	}
	return nil
}

func validateBackend(backend Backend) error {
	if backend.Name == "" {
		return errors.New("backend name is required")
	}
	if backend.Session == nil {
		return fmt.Errorf("backend %q session service is required", backend.Name)
	}
	if backend.Memory == nil {
		return fmt.Errorf("backend %q memory service is required", backend.Name)
	}
	return nil
}

func executionDifference(caseName, backend, sessionID string, err error) Difference {
	return Difference{
		Case:      caseName,
		Backend:   backend,
		SessionID: sessionID,
		Path:      "replay",
		Actual:    marshalValue(err.Error()),
		Reason:    "backend replay failed",
	}
}

func marshalValue(value any) json.RawMessage {
	encoded, err := json.Marshal(value)
	if err != nil {
		return json.RawMessage(strconv.Quote(fmt.Sprint(value)))
	}
	return encoded
}

// normalize uses JSON semantics to make map ordering irrelevant and removes
// generated IDs and clock values that are not part of a replay contract.
func normalize(value any) (any, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var normalized any
	if err := json.Unmarshal(encoded, &normalized); err != nil {
		return nil, err
	}
	return stripVolatile(normalized, nil), nil
}

func stripVolatile(value any, path []string) any {
	switch current := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(current))
		for key, child := range current {
			if isVolatileKey(key, path) {
				continue
			}
			out[key] = stripVolatile(child, append(path, key))
		}
		return out
	case []any:
		out := make([]any, len(current))
		for i, child := range current {
			out[i] = stripVolatile(child, path)
		}
		return out
	default:
		return value
	}
}

func isVolatileKey(key string, path []string) bool {
	switch key {
	case "timestamp", "created", "created_at", "updated_at", "last_updated", "cutoff_at":
		return true
	case "id":
		for _, component := range path {
			if component == "events" || component == "response" {
				return true
			}
		}
	}
	return false
}

func compareValue(path string, left, right any, add func(string, any, any)) {
	if reflect.DeepEqual(left, right) {
		return
	}
	leftMap, leftIsMap := left.(map[string]any)
	rightMap, rightIsMap := right.(map[string]any)
	if leftIsMap && rightIsMap {
		keys := make(map[string]struct{}, len(leftMap)+len(rightMap))
		for key := range leftMap {
			keys[key] = struct{}{}
		}
		for key := range rightMap {
			keys[key] = struct{}{}
		}
		ordered := make([]string, 0, len(keys))
		for key := range keys {
			ordered = append(ordered, key)
		}
		sort.Strings(ordered)
		for _, key := range ordered {
			compareValue(path+"."+key, leftMap[key], rightMap[key], add)
		}
		return
	}
	leftSlice, leftIsSlice := left.([]any)
	rightSlice, rightIsSlice := right.([]any)
	if leftIsSlice && rightIsSlice {
		limit := len(leftSlice)
		if len(rightSlice) > limit {
			limit = len(rightSlice)
		}
		for index := 0; index < limit; index++ {
			var leftItem, rightItem any
			if index < len(leftSlice) {
				leftItem = leftSlice[index]
			}
			if index < len(rightSlice) {
				rightItem = rightSlice[index]
			}
			compareValue(path+"["+strconv.Itoa(index)+"]", leftItem, rightItem, add)
		}
		return
	}
	add(path, left, right)
}
