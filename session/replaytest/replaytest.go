//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package replaytest compares deterministic session and memory replays across
// storage backends.
package replaytest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"sort"
	"strconv"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

// Backend groups the services that participate in one replay.
//
// Session and Memory must use the same logical application and user namespace.
// Track cases require Session to also implement session.TrackService.
type Backend struct {
	// Name identifies the backend in reports.
	Name string
	// Session provides the session behavior exercised by replay cases.
	Session session.Service
	// Memory provides the memory behavior exercised by replay cases.
	Memory memory.Service
	// Unsupported declares intentionally unavailable observable capabilities.
	Unsupported []Unsupported
	// PrivateMetadataPaths lists dot-separated paths in Snapshot.Data that
	// contain backend-owned metadata. Use * for an array element, for example
	// "events.*.extensions.storage_private". These fields are omitted before
	// snapshots are compared.
	PrivateMetadataPaths []string
}

// Unsupported declares a snapshot field that a backend cannot provide. Path
// is relative to Snapshot.Data, for example "tracks" or "memories.search".
// The harness records matching differences as allowed and explains why.
type Unsupported struct {
	// Path identifies the unsupported field relative to Snapshot.Data.
	Path string
	// Reason explains why the difference is allowed.
	Reason string
}

// BackendFactory constructs an optional integration backend from an endpoint
// configured through an environment variable.
type BackendFactory func(context.Context, string) (Backend, error)

// OptionalBackend configures a backend that is enabled only when Environment
// has a non-empty value.
type OptionalBackend struct {
	// Name identifies the backend in skip records and construction errors.
	Name string
	// Environment names the variable containing the backend endpoint.
	Environment string
	// Factory constructs the backend when Environment is set.
	Factory BackendFactory
}

// Environment variables conventionally used for optional integration
// backends. The caller provides the matching BackendFactory so this module does
// not impose connection clients or credentials on lightweight users.
const (
	EnvRedisURL      = "REPLAYTEST_REDIS_URL"
	EnvPostgresDSN   = "REPLAYTEST_POSTGRES_DSN"
	EnvMySQLDSN      = "REPLAYTEST_MYSQL_DSN"
	EnvClickHouseDSN = "REPLAYTEST_CLICKHOUSE_DSN"
)

// Case is one deterministic mutation sequence followed by a snapshot read.
// Implementations should use a case-specific session ID so cases can share a
// backend without affecting one another.
type Case struct {
	// Name identifies the case in reports and is also used as its session ID by
	// the standard case set.
	Name string
	// Replay performs the mutation sequence and captures its observable result.
	Replay func(context.Context, Backend) (Snapshot, error)
}

// CaptureOption configures additional observable data captured from a replay.
type CaptureOption func(*captureOptions)

type captureOptions struct {
	summaryFilterKeys []string
	memoryQueries     []string
}

// WithSummaryFilterKeys captures only the requested summary scopes. Without
// this option, Capture includes all summaries returned by the session service.
func WithSummaryFilterKeys(filterKeys ...string) CaptureOption {
	return func(options *captureOptions) {
		options.summaryFilterKeys = append(options.summaryFilterKeys, filterKeys...)
	}
}

// WithMemorySearchQueries captures retrieval results in the backend-provided
// order for each query. Similarity scores are normalized while result identity
// and ordering remain comparable.
func WithMemorySearchQueries(queries ...string) CaptureOption {
	return func(options *captureOptions) {
		options.memoryQueries = append(options.memoryQueries, queries...)
	}
}

// Snapshot is the normalized observable result of a replay case.
// Data must contain only JSON-compatible values after normalization.
type Snapshot struct {
	// SessionID identifies the session whose data was captured.
	SessionID string `json:"session_id"`
	// Data contains normalized session, state, memory, summary, and track data.
	Data map[string]any `json:"data"`
}

// Difference identifies one observable mismatch between two snapshots.
type Difference struct {
	// Case identifies the replay case that produced the difference.
	Case string `json:"case"`
	// Backend identifies the non-baseline backend.
	Backend string `json:"backend"`
	// SessionID identifies the compared session.
	SessionID string `json:"session_id"`
	// Path identifies the differing field.
	Path string `json:"path"`
	// Baseline contains the baseline JSON value.
	Baseline json.RawMessage `json:"baseline"`
	// Actual contains the compared backend JSON value.
	Actual json.RawMessage `json:"actual"`
	// AllowedDiff reports whether Unsupported permits this difference.
	AllowedDiff bool `json:"allowed_diff"`
	// Reason explains an allowed difference or replay failure.
	Reason string `json:"reason,omitempty"`
}

// Report contains all differences found while replaying cases. An empty
// Differences slice means every backend agreed with the baseline.
type Report struct {
	// Baseline identifies the backend used as the comparison source.
	Baseline string `json:"baseline"`
	// Differences contains every observed mismatch in deterministic order.
	Differences []Difference `json:"differences"`
}

// LoadOptionalBackends builds integrations whose environment variables are
// set. It returns human-readable skip records for unset integrations. If any
// configured backend fails construction or validation, it closes every service
// created during the call before returning the error.
func LoadOptionalBackends(
	ctx context.Context,
	configs ...OptionalBackend,
) ([]Backend, []string, error) {
	backends := make([]Backend, 0, len(configs))
	skipped := make([]string, 0, len(configs))
	for _, config := range configs {
		if config.Name == "" || config.Environment == "" {
			err := errors.New("optional backend name and environment are required")
			return nil, nil, joinBackendCloseError(err, backends)
		}
		endpoint := strings.TrimSpace(os.Getenv(config.Environment))
		if endpoint == "" {
			skipped = append(skipped, config.Name+": "+config.Environment+" is not set")
			continue
		}
		if config.Factory == nil {
			err := fmt.Errorf("optional backend %q factory is required", config.Name)
			return nil, nil, joinBackendCloseError(err, backends)
		}
		backend, err := config.Factory(ctx, endpoint)
		if err != nil {
			cause := fmt.Errorf("create optional backend %q: %w", config.Name, err)
			return nil, nil, joinBackendCloseError(cause, append(backends, backend))
		}
		if backend.Name == "" {
			backend.Name = config.Name
		}
		if err := validateBackend(backend); err != nil {
			return nil, nil, joinBackendCloseError(err, append(backends, backend))
		}
		backends = append(backends, backend)
	}
	return backends, skipped, nil
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
			differences := Compare(replayCase.Name, backend.Name, baseline, actual)
			markAllowedUnsupported(differences, backend.Unsupported)
			report.Differences = append(report.Differences, differences...)
			report.Differences = append(report.Differences, unsupportedDifferences(
				replayCase.Name, backend.Name, baseline.SessionID, backend.Unsupported,
			)...)
		}
	}
	return report, nil
}

// Compare returns field-level differences between normalized snapshots.
func Compare(caseName, backend string, baseline, actual Snapshot) []Difference {
	var differences []Difference
	if baseline.SessionID != actual.SessionID {
		differences = append(differences, Difference{
			Case:      caseName,
			Backend:   backend,
			SessionID: baseline.SessionID,
			Path:      "session_id",
			Baseline:  marshalValue(baseline.SessionID),
			Actual:    marshalValue(actual.SessionID),
		})
	}
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
// and returns a snapshot that can be compared by Compare.
func Capture(
	ctx context.Context,
	backend Backend,
	key session.Key,
	options ...CaptureOption,
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
	sortMemoryEntries(entries)
	captureConfig := captureOptions{}
	for _, option := range options {
		option(&captureConfig)
	}
	summaries := selectSummaries(sess.Summaries, captureConfig.summaryFilterKeys)
	if len(summaries) == 0 {
		var err error
		summaries, err = loadSummaries(ctx, backend.Session, key, captureConfig.summaryFilterKeys)
		if err != nil {
			return Snapshot{}, err
		}
	}
	memorySearch, err := loadMemorySearches(ctx, backend.Memory, key, captureConfig.memoryQueries)
	if err != nil {
		return Snapshot{}, err
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
		"events":        events,
		"state":         state,
		"summaries":     summaries,
		"tracks":        tracks,
		"memories":      captureMemoryEntries(entries),
		"memory_search": memorySearch,
	}, backend.PrivateMetadataPaths)
	if err != nil {
		return Snapshot{}, fmt.Errorf("normalize snapshot: %w", err)
	}
	return Snapshot{SessionID: key.SessionID, Data: data.(map[string]any)}, nil
}

func loadMemorySearches(
	ctx context.Context,
	service memory.Service,
	key session.Key,
	queries []string,
) (map[string][]map[string]any, error) {
	results := make(map[string][]map[string]any, len(queries))
	userKey := memory.UserKey{AppName: key.AppName, UserID: key.UserID}
	for _, query := range queries {
		entries, err := service.SearchMemories(ctx, userKey, query)
		if err != nil {
			return nil, fmt.Errorf("search memories for %q: %w", query, err)
		}
		if entries == nil {
			entries = []*memory.Entry{}
		}
		results[query] = captureMemoryEntries(entries)
	}
	return results, nil
}

func captureMemoryEntries(entries []*memory.Entry) []map[string]any {
	captured := make([]map[string]any, len(entries))
	for index, entry := range entries {
		if entry == nil {
			captured[index] = map[string]any{}
			continue
		}
		captured[index] = map[string]any{
			"id":       entry.ID,
			"app_name": entry.AppName,
			"scope": map[string]string{
				"app_name": entry.AppName,
				"user_id":  entry.UserID,
			},
			"memory":  entry.Memory,
			"user_id": entry.UserID,
			"score":   entry.Score,
		}
	}
	return captured
}

func sortMemoryEntries(entries []*memory.Entry) {
	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i] == nil {
			return false
		}
		if entries[j] == nil {
			return true
		}
		return entries[i].ID < entries[j].ID
	})
}

func loadSummaries(
	ctx context.Context,
	service session.Service,
	key session.Key,
	filterKeys []string,
) (map[string]*session.Summary, error) {
	sessions, err := service.ListSessions(ctx, session.UserKey{
		AppName: key.AppName,
		UserID:  key.UserID,
	})
	if err != nil {
		return nil, fmt.Errorf("list sessions for summaries: %w", err)
	}
	for _, listed := range sessions {
		if listed == nil || listed.ID != key.SessionID {
			continue
		}
		return selectSummaries(listed.Summaries, filterKeys), nil
	}
	return nil, errors.New("session not found while loading summaries")
}

func selectSummaries(
	summaries map[string]*session.Summary,
	filterKeys []string,
) map[string]*session.Summary {
	selected := make(map[string]*session.Summary)
	if len(filterKeys) == 0 {
		for filterKey, summary := range summaries {
			if copied := summary.Clone(); copied != nil {
				selected[filterKey] = copied
			}
		}
		return selected
	}
	for _, filterKey := range filterKeys {
		if copied := summaries[filterKey].Clone(); copied != nil {
			selected[filterKey] = copied
		}
	}
	return selected
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
	for _, unsupported := range backend.Unsupported {
		if strings.TrimSpace(unsupported.Path) == "" || strings.TrimSpace(unsupported.Reason) == "" {
			return fmt.Errorf("backend %q unsupported path and reason are required", backend.Name)
		}
	}
	for _, path := range backend.PrivateMetadataPaths {
		if strings.TrimSpace(path) == "" {
			return fmt.Errorf("backend %q private metadata path is required", backend.Name)
		}
	}
	return nil
}

func markAllowedUnsupported(differences []Difference, unsupported []Unsupported) {
	for index := range differences {
		for _, capability := range unsupported {
			if differences[index].Path == "data."+capability.Path ||
				strings.HasPrefix(differences[index].Path, "data."+capability.Path+".") ||
				strings.HasPrefix(differences[index].Path, "data."+capability.Path+"[") {
				differences[index].AllowedDiff = true
				differences[index].Reason = capability.Reason
				break
			}
		}
	}
}

func unsupportedDifferences(
	caseName, backend, sessionID string,
	unsupported []Unsupported,
) []Difference {
	differences := make([]Difference, 0, len(unsupported))
	for _, capability := range unsupported {
		differences = append(differences, Difference{
			Case:        caseName,
			Backend:     backend,
			SessionID:   sessionID,
			Path:        "data." + capability.Path,
			Baseline:    json.RawMessage(`"supported"`),
			Actual:      json.RawMessage(`"unsupported"`),
			AllowedDiff: true,
			Reason:      capability.Reason,
		})
	}
	return differences
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
// generated IDs, clock values, durations, and backend-private metadata that
// are not part of a replay contract.
func normalize(value any, privateMetadataPaths []string) (any, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var normalized any
	if err := json.Unmarshal(encoded, &normalized); err != nil {
		return nil, err
	}
	return stripVolatile(normalized, nil, privateMetadataPaths), nil
}

func stripVolatile(value any, path, privateMetadataPaths []string) any {
	switch current := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(current))
		for key, child := range current {
			childPath := append(path, key)
			if isPrivateMetadataPath(childPath, privateMetadataPaths) {
				continue
			}
			if isVolatileKey(key, path) {
				if key == "updated_at" && isSummaryRecordPath(path) {
					// Summary recency is part of the contract, but exact wall-clock
					// values differ across backends. Preserve its presence.
					out[key] = "normalized"
				}
				if key == "timestamp" && isTrackEventPath(path) {
					// Track order remains observable through the event slice while
					// wall-clock timestamps are backend-specific.
					out[key] = "normalized"
				}
				continue
			}
			if key == "score" && (containsPath(path, "memory_search") || containsPath(path, "memories")) {
				out[key] = "normalized"
				continue
			}
			if isDurationKey(key, path) {
				out[key] = "normalized"
				continue
			}
			out[key] = stripVolatile(child, childPath, privateMetadataPaths)
		}
		return out
	case []any:
		out := make([]any, len(current))
		for i, child := range current {
			out[i] = stripVolatile(child, append(path, "*"), privateMetadataPaths)
			if containsPath(path, "tracks") && lastPathComponent(path) == "events" {
				if trackEvent, ok := out[i].(map[string]any); ok {
					// JSON decoding represents numeric values as float64. Keep the
					// generated ordinal in the same form so cloned snapshots remain
					// byte-for-byte comparable.
					trackEvent["sequence"] = float64(i)
				}
			}
		}
		return out
	default:
		return value
	}
}

func isPrivateMetadataPath(path, privateMetadataPaths []string) bool {
	for _, candidate := range privateMetadataPaths {
		components := strings.Split(candidate, ".")
		if len(components) != len(path) {
			continue
		}
		matches := true
		for index, component := range components {
			if component != "*" && component != path[index] {
				matches = false
				break
			}
		}
		if matches {
			return true
		}
	}
	return false
}

func isDurationKey(key string, path []string) bool {
	if !containsPath(path, "tracks") {
		return false
	}
	switch key {
	case "duration", "duration_ms", "elapsed", "elapsed_ms", "latency", "latency_ms":
		return true
	default:
		return false
	}
}

func lastPathComponent(path []string) string {
	if len(path) == 0 {
		return ""
	}
	return path[len(path)-1]
}

func containsPath(path []string, target string) bool {
	for _, component := range path {
		if component == target {
			return true
		}
	}
	return false
}

func isVolatileKey(key string, path []string) bool {
	if isEventRecordPath(path) || isEventResponsePath(path) {
		switch key {
		case "id", "timestamp", "created", "created_at":
			return true
		}
	}
	if isSummaryRecordPath(path) && key == "updated_at" {
		return true
	}
	if isSummaryBoundaryPath(path) && key == "cutoff_at" {
		return true
	}
	if isMemoryPayloadPath(path) && key == "last_updated" {
		return true
	}
	return isTrackEventPath(path) && key == "timestamp"
}

func isEventRecordPath(path []string) bool {
	return len(path) == 2 && path[0] == "events" && path[1] == "*"
}

func isEventResponsePath(path []string) bool {
	return len(path) == 3 && path[0] == "events" &&
		path[1] == "*" && path[2] == "response"
}

func isSummaryRecordPath(path []string) bool {
	return len(path) == 2 && path[0] == "summaries"
}

func isSummaryBoundaryPath(path []string) bool {
	return len(path) == 3 && path[0] == "summaries" && path[2] == "boundary"
}

func isMemoryPayloadPath(path []string) bool {
	if len(path) == 3 {
		return path[0] == "memories" && path[1] == "*" && path[2] == "memory"
	}
	return len(path) == 4 && path[0] == "memory_search" &&
		path[2] == "*" && path[3] == "memory"
}

func isTrackEventPath(path []string) bool {
	return len(path) == 4 && path[0] == "tracks" &&
		path[2] == "events" && path[3] == "*"
}

func closeBackends(backends ...Backend) error {
	var closeErr error
	for i := len(backends) - 1; i >= 0; i-- {
		backend := backends[i]
		if backend.Memory != nil {
			closeErr = errors.Join(closeErr, backend.Memory.Close())
		}
		if backend.Session != nil {
			closeErr = errors.Join(closeErr, backend.Session.Close())
		}
	}
	return closeErr
}

func joinBackendCloseError(cause error, backends []Backend) error {
	if closeErr := closeBackends(backends...); closeErr != nil {
		return errors.Join(cause, fmt.Errorf("close optional backends: %w", closeErr))
	}
	return cause
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
			compareValue(sliceItemPath(path, index, leftItem, rightItem), leftItem, rightItem, add)
		}
		return
	}
	add(path, left, right)
}

func sliceItemPath(path string, index int, left, right any) string {
	if strings.HasPrefix(path, "data.memories") || strings.HasPrefix(path, "data.memory_search") {
		if id := snapshotID(left); id != "" {
			return path + "[memory_id=" + id + "]"
		}
		if id := snapshotID(right); id != "" {
			return path + "[memory_id=" + id + "]"
		}
	}
	return path + "[" + strconv.Itoa(index) + "]"
}

func snapshotID(value any) string {
	entry, ok := value.(map[string]any)
	if !ok {
		return ""
	}
	id, _ := entry["id"].(string)
	return id
}
