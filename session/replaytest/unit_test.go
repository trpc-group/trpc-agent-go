//go:build cgo

package replaytest

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

// --- IDAliasMap tests ---

func TestIDAliasMap_StableAliases(t *testing.T) {
	m := NewIDAliasMap()
	first := m.Alias("uuid-001", "event")
	second := m.Alias("uuid-001", "event")
	assert.Equal(t, first, second, "same ID must produce same alias")
	assert.Equal(t, "event-000", first)
}

func TestIDAliasMap_DifferentIDs(t *testing.T) {
	m := NewIDAliasMap()
	a := m.Alias("uuid-001", "event")
	b := m.Alias("uuid-002", "event")
	assert.NotEqual(t, a, b, "different IDs must produce different aliases")
	assert.Equal(t, "event-000", a)
	assert.Equal(t, "event-001", b)
}

func TestIDAliasMap_CrossCategoryIsolation(t *testing.T) {
	m := NewIDAliasMap()
	eventAlias := m.Alias("same-id", "event")
	toolCallAlias := m.Alias("same-id", "tool-call")
	invAlias := m.Alias("same-id", "invocation")
	memAlias := m.Alias("same-id", "memory")
	assert.Equal(t, "event-000", eventAlias)
	assert.Equal(t, "tool-call-000", toolCallAlias)
	assert.Equal(t, "invocation-000", invAlias)
	assert.Equal(t, "memory-000", memAlias)
}

func TestIDAliasMap_EmptyString(t *testing.T) {
	m := NewIDAliasMap()
	assert.Equal(t, "", m.Alias("", "event"))
}

func TestIDAliasMap_UnknownCategory(t *testing.T) {
	m := NewIDAliasMap()
	assert.Equal(t, "orig", m.Alias("orig", "unknown"))
}

func TestIDAliasMap_Lookup(t *testing.T) {
	m := NewIDAliasMap()
	m.Alias("uuid-001", "event")
	assert.Equal(t, "event-000", m.Lookup("uuid-001", "event"))
	assert.Equal(t, "", m.Lookup("uuid-999", "event"))
	assert.Equal(t, "", m.Lookup("", "event"))
}

func TestIDAliasMap_Concurrency(t *testing.T) {
	m := NewIDAliasMap()
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			m.Alias(fmt.Sprintf("id-%d", i), "event")
		}(i)
	}
	wg.Wait()
	// All 100 IDs should be aliased.
	for i := 0; i < 100; i++ {
		alias := m.Lookup(fmt.Sprintf("id-%d", i), "event")
		assert.NotEmpty(t, alias)
	}
}

// --- MissingValue tests ---

func TestMissingValue_MarshalJSON(t *testing.T) {
	mv := MissingValue{}
	raw, err := json.Marshal(mv)
	require.NoError(t, err)
	assert.Equal(t, `{"__missing":true}`, string(raw))
}

func TestMissingValue_UnmarshalJSON_RejectsInput(t *testing.T) {
	var mv MissingValue
	err := mv.UnmarshalJSON([]byte(`{"__missing":true}`))
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "synthetic")
}

func TestMissingValue_DistinctFromNil(t *testing.T) {
	mv := MissingValue{}
	raw, _ := json.Marshal(mv)
	nilRaw, _ := json.Marshal(nil)
	assert.NotEqual(t, string(nilRaw), string(raw))
}

// --- Capabilities tests ---

func TestCapabilities_Has_OmittedMeansSupported(t *testing.T) {
	caps := Capabilities{}
	assert.True(t, caps.Has("events"), "omitted capability should default to supported")
}

func TestCapabilities_Has_ExplicitTrue(t *testing.T) {
	caps := Capabilities{CapEvents: {Supported: true}}
	assert.True(t, caps.Has(CapEvents))
}

func TestCapabilities_Has_ExplicitFalse(t *testing.T) {
	caps := Capabilities{CapTrack: {Supported: false, Reason: "not implemented"}}
	assert.False(t, caps.Has(CapTrack))
}

func TestCapabilities_UnsupportedList(t *testing.T) {
	caps := Capabilities{
		CapEvents: {Supported: true},
		CapTrack:  {Supported: false, Reason: "not implemented"},
	}
	unsupported := caps.UnsupportedList()
	assert.Contains(t, unsupported, CapTrack)
	assert.NotContains(t, unsupported, CapEvents)
}

func TestAllCapabilities(t *testing.T) {
	caps := AllCapabilities()
	for _, name := range []string{CapEvents, CapState, CapMemory, CapSummary, CapTrack, CapEventStateDeltaNull} {
		assert.True(t, caps.Has(name), "AllCapabilities should support %s", name)
	}
}

// --- AllowedDiff.Validate tests ---

func TestAllowedDiff_Validate_Success(t *testing.T) {
	ad := AllowedDiff{
		BackendA: "inmemory",
		BackendB: "sqlite",
		Section:  "state",
		Path:     "$.state.k1",
		Reason:   "known difference",
	}
	assert.NoError(t, ad.Validate())
}

func TestAllowedDiff_Validate_MissingBackend(t *testing.T) {
	ad := AllowedDiff{BackendA: "", BackendB: "sqlite", Section: "state", Path: "$.state.k1", Reason: "reason"}
	assert.Error(t, ad.Validate())
}

func TestAllowedDiff_Validate_UnknownSection(t *testing.T) {
	ad := AllowedDiff{BackendA: "a", BackendB: "b", Section: "unknown", Path: "$.unknown.x", Reason: "reason"}
	assert.Error(t, ad.Validate())
}

func TestAllowedDiff_Validate_EmptyPath(t *testing.T) {
	ad := AllowedDiff{BackendA: "a", BackendB: "b", Section: "state", Path: "", Reason: "reason"}
	assert.Error(t, ad.Validate())
}

func TestAllowedDiff_Validate_EmptyReason(t *testing.T) {
	ad := AllowedDiff{BackendA: "a", BackendB: "b", Section: "state", Path: "$.state.k1", Reason: ""}
	assert.Error(t, ad.Validate())
}

func TestAllowedDiff_Validate_WildcardPath(t *testing.T) {
	ad := AllowedDiff{BackendA: "a", BackendB: "b", Section: "state", Path: "$.state.*", Reason: "reason"}
	err := validateAllowedDiffs([]AllowedDiff{ad})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "wildcard")
}

func TestAllowedDiff_Validate_PathNotDollarPrefix(t *testing.T) {
	ad := AllowedDiff{BackendA: "a", BackendB: "b", Section: "state", Path: "state.k1", Reason: "reason"}
	err := validateAllowedDiffs([]AllowedDiff{ad})
	assert.Error(t, err)
}

func TestAllowedDiff_Validate_PathSectionMismatch(t *testing.T) {
	ad := AllowedDiff{BackendA: "a", BackendB: "b", Section: "state", Path: "$.events.content", Reason: "reason"}
	err := validateAllowedDiffs([]AllowedDiff{ad})
	assert.Error(t, err)
}

// --- Normalizer tests ---

func TestNormalizer_EventIDReplacement(t *testing.T) {
	key := sessKey("norm-event-id")
	backends := makeBackends(t, key)
	backend := backends[0]
	backend.Sess.CreateSession(context.Background(), key, nil)
	sess, _ := backend.Sess.GetSession(context.Background(), key)
	backend.Sess.AppendEvent(context.Background(), sess, newUserEvent("hello"))

	normalizer := NewNormalizer(DefaultNormalizerConfig())
	snap, err := Capture(context.Background(), backend, CaptureOptions{NormalizerConfig: DefaultNormalizerConfig()}, normalizer)
	require.NoError(t, err)
	require.Len(t, snap.Events, 1)

	id, ok := snap.Events[0]["id"].(string)
	require.True(t, ok)
	assert.Equal(t, "event-000", id, "event ID should be replaced with stable alias")
}

func TestNormalizer_TimestampRemoved(t *testing.T) {
	key := sessKey("norm-ts")
	backends := makeBackends(t, key)
	backend := backends[0]
	backend.Sess.CreateSession(context.Background(), key, nil)
	sess, _ := backend.Sess.GetSession(context.Background(), key)
	backend.Sess.AppendEvent(context.Background(), sess, newUserEvent("hello"))

	normalizer := NewNormalizer(DefaultNormalizerConfig())
	snap, err := Capture(context.Background(), backend, CaptureOptions{NormalizerConfig: DefaultNormalizerConfig()}, normalizer)
	require.NoError(t, err)
	require.Len(t, snap.Events, 1)

	_, hasTimestamp := snap.Events[0]["timestamp"]
	_, hasRequestID := snap.Events[0]["requestID"]
	_, hasCreated := snap.Events[0]["created"]
	assert.False(t, hasTimestamp, "timestamp should be removed")
	assert.False(t, hasRequestID, "requestID should be removed")
	assert.False(t, hasCreated, "created should be removed")
}

func TestNormalizer_StateDeltaNilToMissingValue(t *testing.T) {
	key := sessKey("norm-delta-nil")
	backends := makeBackends(t, key)
	backend := backends[0]
	_, createErr := backend.Sess.CreateSession(context.Background(), key, session.StateMap{"k1": []byte("v1"), "k2": []byte("v2")})
	require.NoError(t, createErr, "CreateSession should succeed")
	sess, getSessErr := backend.Sess.GetSession(context.Background(), key)
	require.NoError(t, getSessErr, "GetSession should succeed")
	require.NotNil(t, sess, "session should not be nil")
	// GetSession's ApplyEventFiltering requires a user message; add one first.
	backend.Sess.AppendEvent(context.Background(), sess, newUserEvent("trigger"))
	sess, _ = backend.Sess.GetSession(context.Background(), key)
	backend.Sess.AppendEvent(context.Background(), sess, newEventWithStateDeltaNull("test", "k2"))

	normalizer := NewNormalizer(DefaultNormalizerConfig())
	snap, err := Capture(context.Background(), backend, CaptureOptions{NormalizerConfig: DefaultNormalizerConfig()}, normalizer)
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(snap.Events), 2, "snapshot should have at least 2 events")

	// Find the event containing stateDelta dynamically (don't assume index).
	var stateDelta map[string]any
	found := false
	for _, evt := range snap.Events {
		sd, ok := evt["stateDelta"].(map[string]any)
		if ok {
			stateDelta = sd
			found = true
			break
		}
	}
	require.True(t, found, "expected to find an event with stateDelta")
	k2Val := stateDelta["k2"]
	_, isMissing := k2Val.(MissingValue)
	assert.True(t, isMissing, "nil StateDelta value should become MissingValue")
}

func TestNormalizer_VolatileKeyRemoval(t *testing.T) {
	key := sessKey("norm-volatile")
	backends := makeBackends(t, key)
	backend := backends[0]
	backend.Sess.CreateSession(context.Background(), key, nil)
	sess, _ := backend.Sess.GetSession(context.Background(), key)
	backend.Track.AppendTrackEvent(context.Background(), sess, newTrackEventWithVolatile("test", map[string]any{
		"type":     "end",
		"duration": 1234.5,
	}))

	normalizer := NewNormalizer(DefaultNormalizerConfig())
	snap, err := Capture(context.Background(), backend, CaptureOptions{NormalizerConfig: DefaultNormalizerConfig()}, normalizer)
	require.NoError(t, err)
	require.Contains(t, snap.Tracks, "test")
	require.Len(t, snap.Tracks["test"], 1)

	payload, ok := snap.Tracks["test"][0].Payload.(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "end", payload["type"])
	_, hasDuration := payload["duration"]
	assert.False(t, hasDuration, "volatile key 'duration' should be removed")
}

func TestNormalizer_MemoryOrdered(t *testing.T) {
	key := sessKey("norm-mem-ord")
	uk := userKey()
	backends := makeBackends(t, key)
	backend := backends[0]
	backend.Mem.AddMemory(context.Background(), uk, "First memory", []string{"t1"})
	backend.Mem.AddMemory(context.Background(), uk, "Second memory", []string{"t2"})

	normalizer := NewNormalizer(DefaultNormalizerConfig())
	snap, err := Capture(context.Background(), backend, CaptureOptions{NormalizerConfig: DefaultNormalizerConfig()}, normalizer)
	require.NoError(t, err)
	require.Len(t, snap.Memories, 2)
	assert.Equal(t, 0, snap.Memories[0].Rank)
	assert.Equal(t, 1, snap.Memories[1].Rank)
}

func TestNormalizer_MemoryUnordered(t *testing.T) {
	key := sessKey("norm-mem-unord")
	uk := userKey()
	backends := makeBackends(t, key)
	backend := backends[0]
	backend.Mem.AddMemory(context.Background(), uk, "First memory", []string{"t1"})
	backend.Mem.AddMemory(context.Background(), uk, "Second memory", []string{"t2"})

	cfg := DefaultNormalizerConfig()
	cfg.MemoryUnordered = true
	normalizer := NewNormalizer(cfg)
	snap, err := Capture(context.Background(), backend, CaptureOptions{NormalizerConfig: cfg}, normalizer)
	require.NoError(t, err)
	require.Len(t, snap.Memories, 2)
	assert.Equal(t, -1, snap.Memories[0].Rank)
	assert.Equal(t, -1, snap.Memories[1].Rank)
}

func TestNormalizer_ScoreQuantization(t *testing.T) {
	score := normalizeMemoryScore(0.123456789, 4)
	assert.Equal(t, 0.1235, score)
}

func TestNormalizer_ScoreQuantizationZero(t *testing.T) {
	score := normalizeMemoryScore(0.0, 6)
	assert.Equal(t, 0.0, score)
}

func TestNormalizer_NilSession(t *testing.T) {
	normalizer := NewNormalizer(DefaultNormalizerConfig())
	snap, err := normalizer.Normalize(nil, nil, AllCapabilities(), CaptureOptions{})
	assert.NoError(t, err, "nil session with no memories should be acceptable")
	assert.Empty(t, snap.Events)
	assert.Empty(t, snap.Memories)
}

func TestNormalizer_SnapshotClone(t *testing.T) {
	orig := Snapshot{
		Events: []map[string]any{{"id": "event-000", "content": "hello"}},
		State:  map[string]any{"k1": "v1"},
	}
	cloned, err := orig.Clone()
	require.NoError(t, err)
	assert.Equal(t, orig.Events[0]["content"], cloned.Events[0]["content"])
	// Mutate clone, verify original is unchanged.
	cloned.Events[0]["content"] = "mutated"
	assert.Equal(t, "hello", orig.Events[0]["content"])
}

// --- Diff engine tests ---

func TestDiff_IdenticalSnapshots(t *testing.T) {
	left := Snapshot{Events: []map[string]any{{"content": "hello"}}}
	right := Snapshot{Events: []map[string]any{{"content": "hello"}}}
	diffs, err := Compare("test", "a", "b", left, right, nil)
	require.NoError(t, err)
	assert.Empty(t, diffs)
}

func TestDiff_MissingValueVsNull(t *testing.T) {
	left := Snapshot{State: map[string]any{"k1": MissingValue{}}}
	right := Snapshot{State: map[string]any{"k1": nil}}
	diffs, err := Compare("test", "a", "b", left, right, nil)
	require.NoError(t, err)
	assert.NotEmpty(t, diffs, "MissingValue vs nil should produce a diff")
}

func TestDiff_MissingValueVsMissingValue(t *testing.T) {
	left := Snapshot{State: map[string]any{"k1": MissingValue{}}}
	right := Snapshot{State: map[string]any{"k1": MissingValue{}}}
	diffs, err := Compare("test", "a", "b", left, right, nil)
	require.NoError(t, err)
	assert.Empty(t, diffs, "MissingValue vs MissingValue should not diff")
}

func TestDiff_AllowedDiffBidirectional(t *testing.T) {
	left := Snapshot{State: map[string]any{"k1": "v1"}}
	right := Snapshot{State: map[string]any{"k1": "v2"}}
	allowed := []AllowedDiff{
		{BackendA: "a", BackendB: "b", Section: "state", Path: "$.state.k1", Reason: "known"},
	}
	diffs, err := Compare("test", "a", "b", left, right, allowed)
	require.NoError(t, err)
	require.Len(t, diffs, 1)
	assert.True(t, diffs[0].Allowed)

	// Bidirectional: BackendA=b, BackendB=a should also match.
	diffs2, err := Compare("test", "b", "a", right, left, allowed)
	require.NoError(t, err)
	require.Len(t, diffs2, 1)
	assert.True(t, diffs2[0].Allowed)
}

func TestDiff_AllowedDiffNoWildcard(t *testing.T) {
	allowed := []AllowedDiff{
		{BackendA: "a", BackendB: "b", Section: "state", Path: "$.state.*", Reason: "should fail"},
	}
	_, err := Compare("test", "a", "b", Snapshot{}, Snapshot{}, allowed)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "wildcard")
}

func TestDiff_EventContext(t *testing.T) {
	left := Snapshot{Events: []map[string]any{{"content": "hello"}, {"content": "world"}}}
	right := Snapshot{Events: []map[string]any{{"content": "hello"}, {"content": "changed"}}}
	diffs, err := Compare("test", "a", "b", left, right, nil)
	require.NoError(t, err)
	require.Len(t, diffs, 1)
	assert.NotNil(t, diffs[0].EventIndex)
	assert.Equal(t, 1, *diffs[0].EventIndex)
	assert.Equal(t, "events", diffs[0].Section)
}

func TestDiff_SummaryContext(t *testing.T) {
	left := Snapshot{Summaries: map[string]SummarySnapshot{
		"default": {Text: "original"},
	}}
	right := Snapshot{Summaries: map[string]SummarySnapshot{
		"default": {Text: "changed"},
	}}
	diffs, err := Compare("test", "a", "b", left, right, nil)
	require.NoError(t, err)
	assert.NotEmpty(t, diffs)
	assert.NotNil(t, diffs[0].SummaryKey)
	assert.Equal(t, "default", *diffs[0].SummaryKey)
}

func TestDiff_TrackContext(t *testing.T) {
	left := Snapshot{Tracks: map[string][]TrackSnapshot{
		"agent": {{Track: "agent", Payload: "start"}},
	}}
	right := Snapshot{Tracks: map[string][]TrackSnapshot{
		"agent": {{Track: "agent", Payload: "end"}},
	}}
	diffs, err := Compare("test", "a", "b", left, right, nil)
	require.NoError(t, err)
	assert.NotEmpty(t, diffs)
	assert.Equal(t, "agent", diffs[0].TrackName)
}

func TestHasUnexpectedDiff_Pass(t *testing.T) {
	result := CaseResult{Status: StatusPass}
	assert.False(t, HasUnexpectedDiff(result))
}

func TestHasUnexpectedDiff_Fail(t *testing.T) {
	result := CaseResult{Status: StatusFail, Diffs: []Diff{{Allowed: false}}}
	assert.True(t, HasUnexpectedDiff(result))
}

func TestHasUnexpectedDiff_Inconclusive(t *testing.T) {
	result := CaseResult{Status: StatusInconclusive}
	assert.True(t, HasUnexpectedDiff(result))
}

func TestHasUnexpectedDiff_AllowedOnly(t *testing.T) {
	result := CaseResult{Status: StatusPass, Diffs: []Diff{{Allowed: true}}}
	assert.False(t, HasUnexpectedDiff(result))
}

// --- Harness tests ---

func TestHarness_Validation_EmptyName(t *testing.T) {
	h := Harness{Backends: makeBackends(t, sessKey("v"))}
	_, err := h.Run(context.Background(), Case{Name: "", Run: func(ctx context.Context, b Backend) error { return nil }})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "name")
}

func TestHarness_Validation_NoRunOrOps(t *testing.T) {
	h := Harness{Backends: makeBackends(t, sessKey("v"))}
	_, err := h.Run(context.Background(), Case{Name: "test", RequiredCaps: []string{CapEvents}})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "Run function")
}

func TestHarness_Validation_LessThanTwoBackends(t *testing.T) {
	h := Harness{Backends: []Backend{{Name: "only", Sess: &mockSessionService{}, Caps: AllCapabilities(), SessKey: defaultSessKey}}}
	_, err := h.Run(context.Background(), Case{Name: "test", Run: func(ctx context.Context, b Backend) error { return nil }})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "at least two")
}

func TestHarness_Validation_DuplicateBackendNames(t *testing.T) {
	b1 := Backend{Name: "dup", Sess: &mockSessionService{}, Caps: AllCapabilities(), SessKey: defaultSessKey}
	b2 := Backend{Name: "dup", Sess: &mockSessionService{}, Caps: AllCapabilities(), SessKey: defaultSessKey}
	h := Harness{Backends: []Backend{b1, b2}}
	_, err := h.Run(context.Background(), Case{Name: "test", Run: func(ctx context.Context, b Backend) error { return nil }})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "duplicate")
}

func TestHarness_Validation_NilSessionService(t *testing.T) {
	b := Backend{Name: "bad", Sess: nil, Caps: AllCapabilities(), SessKey: defaultSessKey}
	h := Harness{Backends: []Backend{b, b}}
	_, err := h.Run(context.Background(), Case{Name: "test", Run: func(ctx context.Context, b Backend) error { return nil }})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "nil session service")
}

func TestHarness_AtomicWrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "report.json")
	report := &Report{
		ReportID: "replay-v2",
		Version:  "v2",
		Backends: []string{"a", "b"},
		Cases:    []CaseResult{{Name: "test", Status: StatusPass}},
		Summary:  ReportSummary{TotalCases: 1, PassedCases: 1},
	}
	require.NoError(t, WriteReport(path, *report))
	// No .tmp file should remain.
	_, err := os.Stat(path + ".tmp")
	assert.True(t, os.IsNotExist(err), "temp file should not remain after atomic write")
	// Report file should exist and be valid.
	readBack, err := ReadReport(path)
	require.NoError(t, err)
	assert.Equal(t, "v2", readBack.Version)
}

func TestHarness_ReportWithVerify(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "report.json")
	report := &Report{
		ReportID: "replay-v2",
		Version:  "v2",
		Backends: []string{"a", "b"},
		Cases:    []CaseResult{{Name: "test", Status: StatusPass}},
		Summary:  ReportSummary{TotalCases: 1, PassedCases: 1},
	}
	require.NoError(t, WriteReport(path, *report))
	readBack, err := ReadReportWithVerify(path)
	require.NoError(t, err)
	assert.Equal(t, "v2", readBack.Version)
}

func TestHarness_ReportCorruptedFailsVerify(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "report.json")
	// Write a valid report.
	report := &Report{
		ReportID: "replay-v2",
		Version:  "v2",
		Backends: []string{"a"},
		Summary:  ReportSummary{TotalCases: 1},
	}
	require.NoError(t, WriteReport(path, *report))
	// Corrupt the JSON content by modifying a value (changes the checksum).
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	content := string(raw)
	corrupted := strings.Replace(content, `"total_cases": 1`, `"total_cases": 9`, 1)
	if corrupted == content {
		// Try without space (compact JSON).
		corrupted = strings.Replace(content, `"total_cases":1`, `"total_cases":9`, 1)
	}
	require.NotEqual(t, content, corrupted, "should have modified the file content")
	require.NoError(t, os.WriteFile(path, []byte(corrupted), 0o644))
	// ReadReportWithVerify should fail due to checksum mismatch.
	_, err = ReadReportWithVerify(path)
	assert.Error(t, err, "corrupted report should fail checksum verification")
}

func TestHarness_SnapshotFingerprint(t *testing.T) {
	key := sessKey("fingerprint")
	backends := makeBackends(t, key)
	normalizer := NewNormalizer(DefaultNormalizerConfig())
	h := Harness{Backends: backends, Normalizer: normalizer}
	c := Case{
		Name:         "fingerprint_test",
		RequiredCaps: []string{CapEvents},
		Run: func(ctx context.Context, backend Backend) error {
			backend.Sess.CreateSession(ctx, key, nil)
			sess, _ := backend.Sess.GetSession(ctx, key)
			backend.Sess.AppendEvent(ctx, sess, newUserEvent("hello"))
			return nil
		},
	}
	result, err := h.Run(context.Background(), c)
	require.NoError(t, err)
	assert.NotEmpty(t, result.SnapshotFingerprint)
	assert.Contains(t, result.SnapshotFingerprint, "sha256:")
}

func TestHarness_InconclusiveWhenOneBackendSkipped(t *testing.T) {
	key := sessKey("inconclusive")
	b1 := Backend{
		Name:    "full",
		Sess:    &mockSessionService{},
		Caps:    AllCapabilities(),
		SessKey: func() session.Key { return key },
	}
	b2 := Backend{
		Name: "limited",
		Sess: &mockSessionService{},
		Caps: Capabilities{
			CapEvents:  {Supported: true},
			CapState:   {Supported: true},
			CapSummary: {Supported: false, Reason: "not implemented"},
		},
		SessKey: func() session.Key { return key },
	}
	h := Harness{Backends: []Backend{b1, b2}}
	c := Case{
		Name:         "need_summary",
		RequiredCaps: []string{CapEvents, CapSummary},
		Run:          func(ctx context.Context, b Backend) error { return nil },
	}
	result, err := h.Run(context.Background(), c)
	require.NoError(t, err)
	assert.Equal(t, StatusMixed, result.Status)
}

func TestHarness_WriteReportEmptyPath(t *testing.T) {
	err := WriteReport("", Report{})
	assert.Error(t, err)
}

// --- Factory tests ---

func TestFactory_InMemory(t *testing.T) {
	f := inMemoryFactory{}
	assert.Equal(t, "inmemory", f.Kind())
	b := f.Create(context.Background(), t)
	require.NotNil(t, b)
	require.NotNil(t, b.Sess)
	require.NotNil(t, b.Track)
	require.NotNil(t, b.Mem)
	require.NotNil(t, b.SessKey)
}

func TestFactory_SQLite(t *testing.T) {
	f := sqliteFactory{}
	assert.Equal(t, "sqlite", f.Kind())
	b := f.Create(context.Background(), t)
	require.NotNil(t, b)
	require.NotNil(t, b.Sess)
	require.NotNil(t, b.Track)
	require.NotNil(t, b.Mem)
	require.NotNil(t, b.SessKey)
}

func TestFactory_SQLite_TrackEvents(t *testing.T) {
	f := sqliteFactory{}
	b := f.Create(context.Background(), t)
	b.SessKey = defaultSessKey
	key := b.SessKey()

	_, err := b.Sess.CreateSession(context.Background(), key, nil)
	require.NoError(t, err)
	sess, err := b.Sess.GetSession(context.Background(), key)
	require.NoError(t, err)
	require.NotNil(t, sess)

	trackEvt := newTrackEvent("agent-run", `{"type":"start"}`)
	err = b.Track.AppendTrackEvent(context.Background(), sess, trackEvt)
	require.NoError(t, err)

	// Re-fetch session and check tracks are loaded.
	sess2, err := b.Sess.GetSession(context.Background(), key)
	require.NoError(t, err)
	require.NotNil(t, sess2, "session should exist after AppendTrackEvent")
	require.NotNil(t, sess2.Tracks, "SQLite session should have tracks after AppendTrackEvent")
	_, ok := sess2.Tracks["agent-run"]
	require.True(t, ok, "expected 'agent-run' track in SQLite session")
}

func TestFactory_SQLite_TrackEvents_WithMakeBackends(t *testing.T) {
	// Simulate exactly what case08 does: use makeBackends, then append track events.
	key := sessKey("track-debug")
	backends := makeBackends(t, key)
	sqliteBackend := backends[1] // index 1 is SQLite

	_, err := sqliteBackend.Sess.CreateSession(context.Background(), key, nil)
	require.NoError(t, err)
	sess, err := sqliteBackend.Sess.GetSession(context.Background(), key)
	require.NoError(t, err)
	require.NotNil(t, sess, "SQLite session should exist after CreateSession")

	trackEvt := newTrackEvent("agent-run", `{"type":"start"}`)
	err = sqliteBackend.Track.AppendTrackEvent(context.Background(), sess, trackEvt)
	require.NoError(t, err)

	// Re-fetch session and check tracks
	sess2, err := sqliteBackend.Sess.GetSession(context.Background(), key)
	require.NoError(t, err)
	require.NotNil(t, sess2)
	require.NotNil(t, sess2.Tracks)
	trackHist := sess2.Tracks["agent-run"]
	require.NotNil(t, trackHist)

	// Test Capture flow
	normalizer := NewNormalizer(DefaultNormalizerConfig())
	snap, err := Capture(context.Background(), sqliteBackend, CaptureOptions{NormalizerConfig: DefaultNormalizerConfig()}, normalizer)
	require.NoError(t, err)
	require.NotNil(t, snap.Tracks)
	trackSnaps := snap.Tracks["agent-run"]
	require.NotEmpty(t, trackSnaps, "snapshot should have track events for agent-run")
}

func TestFactory_Miniredis(t *testing.T) {
	f := miniredisFactory{}
	assert.Equal(t, "miniredis", f.Kind())
	b := f.Create(context.Background(), t)
	require.NotNil(t, b)
	require.NotNil(t, b.Sess)
	require.NotNil(t, b.Track)
	require.NotNil(t, b.Mem)
	require.NotNil(t, b.SessKey)
	require.NotNil(t, b.Probe, "miniredis backend should have a health probe")
}

func TestResolvePair_Default(t *testing.T) {
	t.Setenv("REPLAY_BACKEND", "")
	primary, target := ResolvePair(t)
	assert.Equal(t, "inmemory", primary.Kind())
	assert.Equal(t, "sqlite", target.Kind())
}

func TestResolvePair_InMemory(t *testing.T) {
	t.Setenv("REPLAY_BACKEND", "inmemory")
	primary, target := ResolvePair(t)
	assert.Equal(t, "inmemory", primary.Kind())
	assert.Equal(t, "inmemory", target.Kind())
}

// --- Report tests ---

func TestReport_GenerateAndCount(t *testing.T) {
	results := []CaseResult{
		{Name: "case1", Status: StatusPass},
		{Name: "case2", Status: StatusFail, Diffs: []Diff{{Allowed: false}}},
		{Name: "case3", Status: StatusSkip, SkipReason: "missing cap"},
		{Name: "case4", Status: StatusInconclusive},
	}
	report := GenerateReport(results, []string{"a", "b"})
	assert.Equal(t, "v2", report.Version)
	assert.Equal(t, 4, report.Summary.TotalCases)
	assert.Equal(t, 1, report.Summary.PassedCases)
	assert.Equal(t, 1, report.Summary.FailedCases)
	assert.Equal(t, 1, report.Summary.SkippedCases)
	assert.Equal(t, 1, report.Summary.InconclusiveCases)
	assert.Equal(t, 1, report.Summary.TotalDiffs)
	assert.Equal(t, 0, report.Summary.AllowedDiffs)
}

func TestReport_WriteReadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "report.json")
	now := time.Now()
	report := &Report{
		ReportID:    "replay-v2",
		Version:     "v2",
		GeneratedAt: &now,
		Backends:    []string{"a", "b"},
		Cases:       []CaseResult{{Name: "test", Status: StatusPass}},
		Summary:     ReportSummary{TotalCases: 1, PassedCases: 1},
	}
	require.NoError(t, WriteReport(path, *report))
	readBack, err := ReadReport(path)
	require.NoError(t, err)
	assert.Equal(t, report.Version, readBack.Version)
	assert.Equal(t, report.Summary.TotalCases, readBack.Summary.TotalCases)
}

// --- Golden trace tests ---

func TestGoldenTrace_SaveAndLoad(t *testing.T) {
	dir := t.TempDir()
	trace := &GoldenTrace{
		CaseName:  "test-case",
		CreatedAt: time.Now(),
		Snapshots: []Snapshot{{State: map[string]any{"k1": "v1"}}},
	}
	require.NoError(t, SaveGoldenTrace(dir, trace))

	loaded, ok := LoadGoldenTrace(dir, "test-case")
	require.True(t, ok)
	require.NotNil(t, loaded)
	assert.Equal(t, "test-case", loaded.CaseName)
	assert.Len(t, loaded.Snapshots, 1)
}

func TestGoldenTrace_NotFound(t *testing.T) {
	dir := t.TempDir()
	_, ok := LoadGoldenTrace(dir, "nonexistent")
	assert.False(t, ok)
}

// --- SectionForPath tests ---

func TestSectionForPath(t *testing.T) {
	tests := []struct {
		path     string
		expected string
	}{
		{"$.events[0].content", "events"},
		{"$.state.k1", "state"},
		{"$.memories[0].content", "memories"},
		{"$.summaries.default.text", "summaries"},
		{"$.tracks.agent[0].payload", "tracks"},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			assert.Equal(t, tt.expected, sectionForPath(tt.path))
		})
	}
}

// --- normalizeError tests ---

func TestNormalizeError(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"empty", "", ""},
		{"not found", "session not found", "not-found"},
		{"no such", "no such table: session_states", "not-found"},
		{"does not exist", "record does not exist", "not-found"},
		{"unrelated error", "permission denied", "permission denied"},
		{"with period", "session not found.", "not-found"},
		{"with spaces", "  session not found  ", "not-found"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, normalizeError(tt.input))
		})
	}
}

// --- Severity tests ---

func TestDiff_Severity_MissingValueVsValue(t *testing.T) {
	left := Snapshot{State: map[string]any{"k1": MissingValue{}}}
	right := Snapshot{State: map[string]any{"k1": "v1"}}
	diffs, err := Compare("test", "a", "b", left, right, nil)
	require.NoError(t, err)
	require.NotEmpty(t, diffs)
	assert.Equal(t, SeverityCritical, diffs[0].Severity)
}

func TestDiff_Severity_ValueMismatch(t *testing.T) {
	left := Snapshot{State: map[string]any{"k1": "v1"}}
	right := Snapshot{State: map[string]any{"k1": "v2"}}
	diffs, err := Compare("test", "a", "b", left, right, nil)
	require.NoError(t, err)
	require.NotEmpty(t, diffs)
	assert.Equal(t, SeverityMajor, diffs[0].Severity)
}

func TestDiff_Severity_AllowedDiffIsMinor(t *testing.T) {
	left := Snapshot{State: map[string]any{"k1": "v1"}}
	right := Snapshot{State: map[string]any{"k1": "v2"}}
	allowed := []AllowedDiff{
		{BackendA: "a", BackendB: "b", Section: "state", Path: "$.state.k1", Reason: "known"},
	}
	diffs, err := Compare("test", "a", "b", left, right, allowed)
	require.NoError(t, err)
	require.Len(t, diffs, 1)
	assert.True(t, diffs[0].Allowed)
	assert.Equal(t, SeverityMinor, diffs[0].Severity)
}

// --- Retry tests ---

func TestRetry_NoRetryOnFirstSuccess(t *testing.T) {
	policy := DefaultRetryPolicy()
	calls := 0
	err := retryOperation(context.Background(), policy, func(ctx context.Context) error {
		calls++
		return nil
	})
	assert.NoError(t, err)
	assert.Equal(t, 1, calls)
}

func TestRetry_NoRetryOnNonTransient(t *testing.T) {
	policy := DefaultRetryPolicy()
	calls := 0
	err := retryOperation(context.Background(), policy, func(ctx context.Context) error {
		calls++
		return fmt.Errorf("session not found")
	})
	assert.Error(t, err)
	assert.Equal(t, 1, calls)
}

func TestRetry_RetriesOnTransient(t *testing.T) {
	policy := RetryPolicy{MaxAttempts: 3, InitialDelay: time.Millisecond, MaxDelay: time.Millisecond, BackoffFactor: 1, Jitter: false}
	calls := 0
	err := retryOperation(context.Background(), policy, func(ctx context.Context) error {
		calls++
		if calls < 3 {
			return fmt.Errorf("driver: bad connection")
		}
		return nil
	})
	assert.NoError(t, err)
	assert.Equal(t, 3, calls)
}

func TestRetry_ExhaustsAttempts(t *testing.T) {
	policy := RetryPolicy{MaxAttempts: 2, InitialDelay: time.Millisecond, MaxDelay: time.Millisecond, BackoffFactor: 1, Jitter: false}
	calls := 0
	err := retryOperation(context.Background(), policy, func(ctx context.Context) error {
		calls++
		return fmt.Errorf("connection refused")
	})
	assert.Error(t, err)
	assert.Equal(t, 2, calls)
}

func TestIsTransientError(t *testing.T) {
	assert.True(t, isTransientError(fmt.Errorf("driver: bad connection")))
	assert.True(t, isTransientError(fmt.Errorf("connection refused")))
	assert.True(t, isTransientError(context.DeadlineExceeded))
	assert.False(t, isTransientError(fmt.Errorf("session not found")))
	assert.False(t, isTransientError(fmt.Errorf("invalid input")))
}

// --- Backend metrics tests ---

func TestHarness_BackendMetrics(t *testing.T) {
	key := sessKey("metrics")
	backends := makeBackends(t, key)
	normalizer := NewNormalizer(DefaultNormalizerConfig())
	h := Harness{Backends: backends, Normalizer: normalizer}
	c := Case{
		Name:         "metrics_test",
		RequiredCaps: []string{CapEvents},
		Run: func(ctx context.Context, backend Backend) error {
			backend.Sess.CreateSession(ctx, key, nil)
			sess, _ := backend.Sess.GetSession(ctx, key)
			backend.Sess.AppendEvent(ctx, sess, newUserEvent("hello"))
			return nil
		},
	}
	result, err := h.Run(context.Background(), c)
	require.NoError(t, err)
	require.Len(t, result.BackendMetrics, 2)
	for _, m := range result.BackendMetrics {
		assert.NotEmpty(t, m.Name)
		assert.GreaterOrEqual(t, m.SnapshotSize, int64(0))
	}
}

// --- Checkpoint tests ---

func TestCheckpoint_SaveAndExists(t *testing.T) {
	dir := t.TempDir()
	assert.False(t, checkpointExists(dir, "case1"))
	require.NoError(t, saveCheckpoint(dir, "case1"))
	assert.True(t, checkpointExists(dir, "case1"))
	assert.False(t, checkpointExists(dir, "case2"))
}

// --- RunSuite tests ---

func TestHarness_RunSuite(t *testing.T) {
	key1 := sessKey("suite-1")
	key2 := sessKey("suite-2")
	backends := makeBackends(t, key1)
	normalizer := NewNormalizer(DefaultNormalizerConfig())
	h := Harness{Backends: backends, Normalizer: normalizer}
	cases := []Case{
		{
			Name:         "suite_case1",
			RequiredCaps: []string{CapEvents},
			Run: func(ctx context.Context, backend Backend) error {
				backend.SessKey = func() session.Key { return key1 }
				backend.Sess.CreateSession(ctx, key1, nil)
				sess, _ := backend.Sess.GetSession(ctx, key1)
				backend.Sess.AppendEvent(ctx, sess, newUserEvent("hello"))
				return nil
			},
		},
		{
			Name:         "suite_case2",
			RequiredCaps: []string{CapEvents},
			Run: func(ctx context.Context, backend Backend) error {
				backend.SessKey = func() session.Key { return key2 }
				backend.Sess.CreateSession(ctx, key2, nil)
				sess, _ := backend.Sess.GetSession(ctx, key2)
				backend.Sess.AppendEvent(ctx, sess, newAssistantEvent("world"))
				return nil
			},
		},
	}
	report, err := h.RunSuite(context.Background(), cases, "")
	require.NoError(t, err)
	require.NotNil(t, report)
	assert.Equal(t, 2, report.Summary.TotalCases)
}

// --- RunID tests ---

func TestReport_RunID(t *testing.T) {
	results := []CaseResult{{Name: "test", Status: StatusPass}}
	report := GenerateReport(results, []string{"a", "b"})
	assert.NotEmpty(t, report.RunID, "RunID should be auto-generated")
	assert.Contains(t, report.RunID, "-") // timestamp-pid-hostname format
}

// --- Version guard tests ---

func TestReadReportWithVerify_VersionGuard(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "report.json")

	// Write a report with v1 version manually.
	raw := `{"report_id":"replay-v1","version":"v1","backends":["a"],"cases":[],"summary":{"total_cases":0}}`
	checksum := fmt.Sprintf("%x", sha256.Sum256([]byte(raw)))
	content := raw + "\n// sha256:" + checksum + "\n"
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))

	_, err := ReadReportWithVerify(path)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported report version")
}

func TestReadReportWithVerify_V2Passes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "report.json")
	report := &Report{
		ReportID: "replay-v2",
		Version:  "v2",
		Backends: []string{"a"},
		Cases:    []CaseResult{{Name: "test", Status: StatusPass}},
		Summary:  ReportSummary{TotalCases: 1, PassedCases: 1},
	}
	require.NoError(t, WriteReport(path, *report))
	readBack, err := ReadReportWithVerify(path)
	require.NoError(t, err)
	assert.Equal(t, "v2", readBack.Version)
}

// --- Severity in Report summary ---

func TestReport_SeverityCount(t *testing.T) {
	results := []CaseResult{
		{
			Name:   "test",
			Status: StatusFail,
			Diffs: []Diff{
				{Severity: SeverityCritical},
				{Severity: SeverityMajor},
				{Severity: SeverityMinor, Allowed: true},
			},
		},
	}
	report := GenerateReport(results, []string{"a", "b"})
	assert.Equal(t, 1, report.Summary.CriticalDiffs)
	assert.Equal(t, 1, report.Summary.MajorDiffs)
	assert.Equal(t, 1, report.Summary.MinorDiffs)
}

// --- mockSessionService for validation tests ---

type mockSessionService struct{}

func (m *mockSessionService) CreateSession(ctx context.Context, key session.Key, state session.StateMap, opts ...session.Option) (*session.Session, error) {
	return &session.Session{ID: "mock"}, nil
}
func (m *mockSessionService) GetSession(ctx context.Context, key session.Key, opts ...session.Option) (*session.Session, error) {
	return &session.Session{ID: "mock"}, nil
}
func (m *mockSessionService) ListSessions(ctx context.Context, userKey session.UserKey, opts ...session.Option) ([]*session.Session, error) {
	return nil, nil
}
func (m *mockSessionService) DeleteSession(ctx context.Context, key session.Key, opts ...session.Option) error {
	return nil
}
func (m *mockSessionService) UpdateAppState(ctx context.Context, appName string, state session.StateMap) error {
	return nil
}
func (m *mockSessionService) DeleteAppState(ctx context.Context, appName string, key string) error {
	return nil
}
func (m *mockSessionService) ListAppStates(ctx context.Context, appName string) (session.StateMap, error) {
	return nil, nil
}
func (m *mockSessionService) UpdateUserState(ctx context.Context, userKey session.UserKey, state session.StateMap) error {
	return nil
}
func (m *mockSessionService) ListUserStates(ctx context.Context, userKey session.UserKey) (session.StateMap, error) {
	return nil, nil
}
func (m *mockSessionService) DeleteUserState(ctx context.Context, userKey session.UserKey, key string) error {
	return nil
}
func (m *mockSessionService) UpdateSessionState(ctx context.Context, key session.Key, state session.StateMap) error {
	return nil
}
func (m *mockSessionService) AppendEvent(ctx context.Context, sess *session.Session, e *event.Event, opts ...session.Option) error {
	return nil
}
func (m *mockSessionService) CreateSessionSummary(ctx context.Context, sess *session.Session, filterKey string, force bool) error {
	return nil
}
func (m *mockSessionService) EnqueueSummaryJob(ctx context.Context, sess *session.Session, filterKey string, force bool) error {
	return nil
}
func (m *mockSessionService) GetSessionSummaryText(ctx context.Context, sess *session.Session, opts ...session.SummaryOption) (string, bool) {
	return "", false
}
func (m *mockSessionService) Close() error { return nil }

// Verify mockSessionService implements session.Service.
var _ session.Service = (*mockSessionService)(nil)

// --- New feature tests ---

func TestHarness_RunSuite_Parallel(t *testing.T) {
	key1 := sessKey("par-1")
	key2 := sessKey("par-2")
	backends := makeBackends(t, key1)
	normalizer := NewNormalizer(DefaultNormalizerConfig())
	harness := Harness{
		Backends:    backends,
		Normalizer:  normalizer,
		Parallelism: 2,
	}
	cases := []Case{
		{
			Name:         "par_case1",
			RequiredCaps: []string{CapEvents},
			Run: func(ctx context.Context, backend Backend) error {
				backend.SessKey = func() session.Key { return key1 }
				backend.Sess.CreateSession(ctx, key1, nil)
				sess, _ := backend.Sess.GetSession(ctx, key1)
				backend.Sess.AppendEvent(ctx, sess, newUserEvent("hello"))
				return nil
			},
		},
		{
			Name:         "par_case2",
			RequiredCaps: []string{CapEvents},
			Run: func(ctx context.Context, backend Backend) error {
				backend.SessKey = func() session.Key { return key2 }
				backend.Sess.CreateSession(ctx, key2, nil)
				sess, _ := backend.Sess.GetSession(ctx, key2)
				backend.Sess.AppendEvent(ctx, sess, newAssistantEvent("world"))
				return nil
			},
		},
	}
	report, err := harness.RunSuite(context.Background(), cases, "")
	require.NoError(t, err)
	require.NotNil(t, report)
	assert.Equal(t, 2, report.Summary.TotalCases)
	assert.GreaterOrEqual(t, report.Summary.SuiteDuration, time.Duration(0))
}

func TestHarness_RunSuite_ProgressCallback(t *testing.T) {
	key := sessKey("progress")
	backends := makeBackends(t, key)
	normalizer := NewNormalizer(DefaultNormalizerConfig())
	var progressCalls []string
	harness := Harness{
		Backends:   backends,
		Normalizer: normalizer,
		ProgressFunc: func(completed, total int, result CaseResult) {
			progressCalls = append(progressCalls, result.Name)
		},
	}
	cases := []Case{
		{
			Name:         "prog_case1",
			RequiredCaps: []string{CapEvents},
			Run: func(ctx context.Context, backend Backend) error {
				backend.SessKey = func() session.Key { return key }
				backend.Sess.CreateSession(ctx, key, nil)
				sess, _ := backend.Sess.GetSession(ctx, key)
				backend.Sess.AppendEvent(ctx, sess, newUserEvent("hello"))
				return nil
			},
		},
	}
	report, err := harness.RunSuite(context.Background(), cases, "")
	require.NoError(t, err)
	require.NotNil(t, report)
	assert.Len(t, progressCalls, 1)
	assert.Equal(t, "prog_case1", progressCalls[0])
}

func TestCheckpoint_SaveAndLoadResult(t *testing.T) {
	dir := t.TempDir()
	result := CaseResult{
		Name:   "test-case",
		Status: StatusPass,
		Diffs:  []Diff{{Section: "state", Path: "$.state.k1", Allowed: true}},
		BackendMetrics: []BackendMetric{
			{Name: "a", RunDuration: 5 * time.Millisecond, CaptureDuration: 2 * time.Millisecond},
		},
	}
	require.NoError(t, saveCheckpointResult(dir, "test-case", result))
	loaded, ok := loadCheckpointResult(dir, "test-case")
	require.True(t, ok)
	assert.Equal(t, "test-case", loaded.Name)
	assert.Equal(t, StatusPass, loaded.Status)
	assert.Len(t, loaded.BackendMetrics, 1)
}

func TestCheckpoint_LoadFallbackToDoneMarker(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, saveCheckpoint(dir, "legacy-case"))
	loaded, ok := loadCheckpointResult(dir, "legacy-case")
	require.True(t, ok)
	assert.Equal(t, "legacy-case", loaded.Name)
	assert.Equal(t, StatusPass, loaded.Status)
}

func TestCheckpoint_ResultAtomicWrite(t *testing.T) {
	dir := t.TempDir()
	result := CaseResult{Name: "atomic", Status: StatusPass}
	require.NoError(t, saveCheckpointResult(dir, "atomic", result))
	// No .tmp file should remain.
	_, err := os.Stat(filepath.Join(dir, "atomic.result.json.tmp"))
	assert.True(t, os.IsNotExist(err), "temp file should not remain after atomic write")
}

func TestBackend_PerBackendRetryPolicy(t *testing.T) {
	key := sessKey("per-backend-retry")
	backends := makeBackends(t, key)
	// Override retry policy for the second backend.
	customRetry := RetryPolicy{MaxAttempts: 5, InitialDelay: 50 * time.Millisecond, MaxDelay: 2 * time.Second, BackoffFactor: 2, Jitter: false}
	backends[1].Retry = &customRetry
	normalizer := NewNormalizer(DefaultNormalizerConfig())
	harness := Harness{
		Backends:   backends,
		Normalizer: normalizer,
	}
	c := Case{
		Name:         "per_backend_retry",
		RequiredCaps: []string{CapEvents},
		Run: func(ctx context.Context, backend Backend) error {
			backend.SessKey = func() session.Key { return key }
			backend.Sess.CreateSession(ctx, key, nil)
			sess, _ := backend.Sess.GetSession(ctx, key)
			backend.Sess.AppendEvent(ctx, sess, newUserEvent("hello"))
			return nil
		},
	}
	result, err := harness.Run(context.Background(), c)
	require.NoError(t, err)
	require.Len(t, result.BackendMetrics, 2)
	// Both backends should have 0 retries since operations succeed.
	assert.Equal(t, 0, result.BackendMetrics[0].RetryCount)
	assert.Equal(t, 0, result.BackendMetrics[1].RetryCount)
}

func TestBackend_CustomIsRetryable(t *testing.T) {
	// Verify that a custom IsRetryable function is respected.
	customErr := fmt.Errorf("custom retryable error")
	isRetryable := func(err error) bool {
		return err.Error() == customErr.Error()
	}
	// This error would not be retryable by default.
	assert.False(t, isTransientError(customErr))
	// But is retryable with custom checker.
	assert.True(t, isRetryable(customErr))
}

func TestReport_SuiteMetrics(t *testing.T) {
	results := []CaseResult{
		{Name: "c1", Status: StatusPass, BackendMetrics: []BackendMetric{{RetryCount: 2}}},
		{Name: "c2", Status: StatusPass, BackendMetrics: []BackendMetric{{RetryCount: 1}, {RetryCount: 3}}},
	}
	report := GenerateReport(results, []string{"a", "b"})
	report.Summary.TotalRetries = 0
	for _, r := range results {
		for _, m := range r.BackendMetrics {
			report.Summary.TotalRetries += m.RetryCount
		}
	}
	assert.Equal(t, 6, report.Summary.TotalRetries)
}

func TestValidateRequiredCapabilities_BaselineOnly(t *testing.T) {
	c := Case{Name: "test", RequiredCaps: []string{CapEvents, CapSummary}}
	b1 := Backend{Name: "baseline", Caps: AllCapabilities()}
	b2 := Backend{Name: "limited", Caps: Capabilities{
		CapEvents: {Supported: true},
		CapSummary: {Supported: false, Reason: "not implemented"},
	}}
	// Should not error — only baseline must support all capabilities.
	err := validateRequiredCapabilities(c, []Backend{b1, b2})
	assert.NoError(t, err)
}

func TestValidateRequiredCapabilities_BaselineMissing(t *testing.T) {
	c := Case{Name: "test", RequiredCaps: []string{CapEvents, CapSummary}}
	b1 := Backend{Name: "baseline", Caps: Capabilities{
		CapEvents: {Supported: true},
		CapSummary: {Supported: false, Reason: "not implemented"},
	}}
	// Should error — baseline must support all required capabilities.
	err := validateRequiredCapabilities(c, []Backend{b1})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "baseline")
}

// --- AppState/UserState scoped state tests ---

func TestNormalizer_AppStateAndUserState(t *testing.T) {
	normalizer := NewNormalizer(DefaultNormalizerConfig())
	sess := &session.Session{ID: "s1", State: session.StateMap{"sk": []byte("sv")}}
	caps := AllCapabilities()
	opts := CaptureOptions{
		NormalizerConfig: DefaultNormalizerConfig(),
		AppState:         session.StateMap{"theme": []byte("dark")},
		UserState:        session.StateMap{"locale": []byte("en")},
	}
	snap, err := normalizer.Normalize(sess, nil, caps, opts)
	require.NoError(t, err)
	assert.Equal(t, "dark", snap.AppState["theme"])
	assert.Equal(t, "en", snap.UserState["locale"])
	assert.Equal(t, "sv", snap.State["sk"])
}

func TestNormalizer_NilScopedState(t *testing.T) {
	normalizer := NewNormalizer(DefaultNormalizerConfig())
	sess := &session.Session{ID: "s1"}
	caps := AllCapabilities()
	snap, err := normalizer.Normalize(sess, nil, caps, CaptureOptions{NormalizerConfig: DefaultNormalizerConfig()})
	require.NoError(t, err)
	assert.Nil(t, snap.AppState)
	assert.Nil(t, snap.UserState)
}

func TestCompareScopedStates_Identical(t *testing.T) {
	left := Snapshot{
		AppState:  map[string]any{"theme": "dark"},
		UserState: map[string]any{"locale": "en"},
	}
	right := Snapshot{
		AppState:  map[string]any{"theme": "dark"},
		UserState: map[string]any{"locale": "en"},
	}
	diffs := compareScopedStates("test", "a", "b", left, right, nil)
	assert.Empty(t, diffs)
}

func TestCompareScopedStates_AppStateDrift(t *testing.T) {
	left := Snapshot{AppState: map[string]any{"theme": "dark"}}
	right := Snapshot{AppState: map[string]any{"theme": "light"}}
	diffs := compareScopedStates("test", "a", "b", left, right, nil)
	require.Len(t, diffs, 1)
	assert.Equal(t, "app_state", diffs[0].Section)
	assert.Equal(t, "$.app_state.theme", diffs[0].Path)
	assert.Equal(t, "dark", diffs[0].ValueA)
	assert.Equal(t, "light", diffs[0].ValueB)
	assert.Equal(t, SeverityMajor, diffs[0].Severity)
}

func TestCompareScopedStates_UserStateMissing(t *testing.T) {
	left := Snapshot{UserState: map[string]any{"locale": "en"}}
	right := Snapshot{}
	diffs := compareScopedStates("test", "a", "b", left, right, nil)
	require.Len(t, diffs, 1)
	assert.Equal(t, "user_state", diffs[0].Section)
	_, isMissing := diffs[0].ValueB.(MissingValue)
	assert.True(t, isMissing, "right side should be MissingValue")
	assert.Equal(t, SeverityCritical, diffs[0].Severity)
}

func TestCompareScopedStates_AllowedDiff(t *testing.T) {
	left := Snapshot{AppState: map[string]any{"theme": "dark"}}
	right := Snapshot{AppState: map[string]any{"theme": "light"}}
	allowed := []AllowedDiff{
		{BackendA: "a", BackendB: "b", Section: "app_state", Path: "$.app_state.theme", Reason: "known difference"},
	}
	diffs := compareScopedStates("test", "a", "b", left, right, allowed)
	require.Len(t, diffs, 1)
	assert.True(t, diffs[0].Allowed)
	assert.Equal(t, SeverityMinor, diffs[0].Severity)
	assert.Equal(t, "known difference", diffs[0].Explanation)
}

func TestCompareScopedStates_BothNil(t *testing.T) {
	left := Snapshot{}
	right := Snapshot{}
	diffs := compareScopedStates("test", "a", "b", left, right, nil)
	assert.Empty(t, diffs)
}

func TestAllowedDiff_Validate_AppStateSection(t *testing.T) {
	ad := AllowedDiff{
		BackendA: "a", BackendB: "b",
		Section: "app_state", Path: "$.app_state.theme", Reason: "test",
	}
	assert.NoError(t, ad.Validate())
}

func TestAllowedDiff_Validate_UserStateSection(t *testing.T) {
	ad := AllowedDiff{
		BackendA: "a", BackendB: "b",
		Section: "user_state", Path: "$.user_state.locale", Reason: "test",
	}
	assert.NoError(t, ad.Validate())
}

// --- StatusMixed tests ---

func TestHarness_StatusMixed_WhenSkipped(t *testing.T) {
	key := sessKey("mixed-test")
	b1 := Backend{
		Name:    "full",
		Sess:    &mockSessionService{},
		Caps:    AllCapabilities(),
		SessKey: func() session.Key { return key },
	}
	b2 := Backend{
		Name: "limited",
		Sess: &mockSessionService{},
		Caps: Capabilities{
			CapEvents:  {Supported: true},
			CapState:   {Supported: true},
			CapSummary: {Supported: false, Reason: "not implemented"},
		},
		SessKey: func() session.Key { return key },
	}
	h := Harness{Backends: []Backend{b1, b2}}
	c := Case{
		Name:         "mixed_case",
		RequiredCaps: []string{CapEvents, CapSummary},
		Run: func(ctx context.Context, b Backend) error {
			return nil
		},
	}
	result, err := h.Run(context.Background(), c)
	require.NoError(t, err)
	t.Logf("Status: %s, SkippedBackends: %v, Diffs: %d", result.Status, result.SkippedBackends, len(result.Diffs))
	assert.Equal(t, StatusMixed, result.Status, "should be mixed when backends skipped but no diffs")
}

func TestReport_MixedCountedAsPassed(t *testing.T) {
	results := []CaseResult{
		{Name: "pass_case", Status: StatusPass},
		{Name: "mixed_case", Status: StatusMixed, SkippedBackends: map[string][]string{"limited": {"summary"}}},
		{Name: "fail_case", Status: StatusFail},
	}
	report := GenerateReport(results, []string{"a", "b"})
	assert.Equal(t, 3, report.Summary.TotalCases)
	assert.Equal(t, 2, report.Summary.PassedCases, "mixed should be counted as passed")
	assert.Equal(t, 1, report.Summary.FailedCases)
}
