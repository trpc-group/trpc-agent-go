//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package revision carries private latest-turn checkpoint metadata between the
// runner and session service implementations.
package revision

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

const generationServiceMetaKey = "trpc-agent-go.session.revision-generation"

type latestTurnReplacementSupportReporter interface {
	SupportsLatestTurnReplacement() bool
}

var (
	// ErrLatestTurnReplacementUnsupported is kept as an internal alias for the
	// public optional session capability contract.
	ErrLatestTurnReplacementUnsupported = session.ErrLatestTurnReplacementUnsupported
	// ErrLatestTurnReplacementConflict is kept as an internal alias for the
	// public optional session capability contract.
	ErrLatestTurnReplacementConflict = session.ErrLatestTurnReplacementConflict
	// ErrLatestTurnReplacementUnavailable is kept as an internal alias for the
	// public optional session capability contract.
	ErrLatestTurnReplacementUnavailable = session.ErrLatestTurnReplacementUnavailable
	// ErrStaleGeneration indicates that a write belongs to a session projection
	// which has already been superseded by a replacement.
	ErrStaleGeneration = errors.New("stale session revision generation")
	// ErrStaleProjection indicates that a turn boundary was prepared from a
	// projection that changed before the boundary could be committed.
	ErrStaleProjection = errors.New("stale session revision projection")
)

// LatestTurnReplacementRequest aliases the public backend SPI request while
// the checkpoint state machine remains private.
type LatestTurnReplacementRequest = session.LatestTurnReplacementRequest

// LatestTurnReplacementResult aliases the public backend SPI result while the
// checkpoint state machine remains private.
type LatestTurnReplacementResult = session.LatestTurnReplacementResult

// SupportsLatestTurnReplacement reports whether service implements the
// private backend capability required by Runner latest-turn replacement.
func SupportsLatestTurnReplacement(service session.Service) bool {
	if service == nil {
		return false
	}
	if reporter, ok := service.(latestTurnReplacementSupportReporter); ok {
		return reporter.SupportsLatestTurnReplacement()
	}
	_, ok := service.(session.LatestTurnReplacer)
	return ok
}

// ReplaceLatestTurn invokes the private backend replacement capability and
// verifies its postconditions for Runner.
func ReplaceLatestTurn(
	ctx context.Context,
	service session.Service,
	req LatestTurnReplacementRequest,
) (*LatestTurnReplacementResult, error) {
	if err := ValidateLatestTurnReplacementRequest(req); err != nil {
		return nil, err
	}
	replacer, ok := service.(session.LatestTurnReplacer)
	if !ok || !SupportsLatestTurnReplacement(service) {
		return nil, ErrLatestTurnReplacementUnsupported
	}
	result, err := replacer.ReplaceLatestTurn(ctx, req)
	if err != nil {
		return nil, err
	}
	if result == nil || result.ActiveSession == nil {
		return nil, fmt.Errorf(
			"latest-turn replacement returned no active session",
		)
	}
	activeKey := session.Key{
		AppName:   result.ActiveSession.AppName,
		UserID:    result.ActiveSession.UserID,
		SessionID: result.ActiveSession.ID,
	}
	if activeKey != req.Key {
		return nil, fmt.Errorf(
			"latest-turn replacement returned session key %+v, want %+v",
			activeKey,
			req.Key,
		)
	}
	return result, nil
}

// ValidateLatestTurnReplacementRequest validates backend-independent request
// identity fields.
func ValidateLatestTurnReplacementRequest(req LatestTurnReplacementRequest) error {
	if err := req.Key.CheckSessionKey(); err != nil {
		return err
	}
	if req.ExpectedRequestID == "" {
		return fmt.Errorf("expected request id is required")
	}
	if req.IdempotencyKey == "" {
		return fmt.Errorf("idempotency key is required")
	}
	return nil
}

type turnStartContextKey struct{}
type generationContextKey struct{}
type hazardContextKey struct{}
type runPreparationContextKey struct{}

type runPreparation struct {
	done chan error
	once sync.Once
}

// ContextWithRunPreparation returns a context and one-shot signal for the
// point at which Runner has committed the current turn before agent execution.
func ContextWithRunPreparation(ctx context.Context) (context.Context, <-chan error) {
	if ctx == nil {
		ctx = context.Background()
	}
	preparation := &runPreparation{done: make(chan error, 1)}
	return context.WithValue(ctx, runPreparationContextKey{}, preparation), preparation.done
}

// CompleteRunPreparation signals the current turn's persistence result when
// ctx carries a preparation signal. Repeated calls are ignored.
func CompleteRunPreparation(ctx context.Context, err error) {
	if ctx == nil {
		return
	}
	preparation, _ := ctx.Value(runPreparationContextKey{}).(*runPreparation)
	if preparation == nil {
		return
	}
	preparation.once.Do(func() {
		preparation.done <- err
	})
}

// TurnStart identifies the first canonical event of one top-level runner turn.
type TurnStart struct {
	RequestID    string
	InvocationID string
}

// PersistedCheckpoint is the backend-private durable boundary for the latest
// top-level turn. Snapshot contains the active session immediately before the
// turn began. Terminal reports whether Runner persisted its completion marker;
// a non-terminal checkpoint still identifies an unfinished turn that can be
// replaced after its execution has stopped.
type PersistedCheckpoint struct {
	RequestID    string `json:"requestID"`
	InvocationID string `json:"invocationID"`
	Snapshot     []byte `json:"snapshot"`
	Terminal     bool   `json:"terminal"`
	Hazard       bool   `json:"hazard,omitempty"`
}

// PersistedReplay retains the result needed to answer an idempotent replacement
// retry.
type PersistedReplay struct {
	RequestID  string `json:"requestID"`
	Generation uint64 `json:"generation"`
	Head       uint64 `json:"head"`
}

// PersistedRecord is the backend-private revision metadata for one session.
type PersistedRecord struct {
	Generation uint64                     `json:"generation"`
	Head       uint64                     `json:"head"`
	Checkpoint *PersistedCheckpoint       `json:"checkpoint,omitempty"`
	Replays    map[string]PersistedReplay `json:"replays,omitempty"`
}

// LatestTurnReplacementReplay returns a matching idempotent replacement
// result. A reused key or a replay superseded by later writes returns a
// conflict.
func LatestTurnReplacementReplay(
	record *PersistedRecord,
	expectedRequestID string,
	idempotencyKey string,
) (PersistedReplay, bool, error) {
	if record == nil {
		return PersistedReplay{}, false, nil
	}
	replay, ok := record.Replays[idempotencyKey]
	if !ok {
		return PersistedReplay{}, false, nil
	}
	if replay.RequestID != expectedRequestID ||
		replay.Generation != record.Generation || replay.Head != record.Head {
		return PersistedReplay{}, true, ErrLatestTurnReplacementConflict
	}
	return replay, true, nil
}

// LatestTurnReplacementCheckpoint returns the safe canonical checkpoint for
// the expected latest request. Missing or hazardous checkpoints are
// unavailable, while a different latest request is a conflict.
func LatestTurnReplacementCheckpoint(
	record *PersistedRecord,
	expectedRequestID string,
) (*PersistedCheckpoint, error) {
	if record == nil || record.Checkpoint == nil ||
		record.Checkpoint.Hazard || len(record.Checkpoint.Snapshot) == 0 {
		return nil, ErrLatestTurnReplacementUnavailable
	}
	if record.Checkpoint.RequestID != expectedRequestID {
		return nil, ErrLatestTurnReplacementConflict
	}
	return record.Checkpoint, nil
}

// Write describes revision metadata that must be committed atomically with a
// session write.
type Write struct {
	ExpectedGeneration    uint64
	HasExpectedGeneration bool
	ExpectedHead          uint64
	HasExpectedHead       bool
	Hazard                bool
	Start                 *TurnStart
	Snapshot              []byte
}

// ContextWithTurnStart marks a single persistence call as the start of a turn.
func ContextWithTurnStart(ctx context.Context, start TurnStart) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, turnStartContextKey{}, start)
}

// TurnStartFromContext returns the turn-start marker attached to ctx.
func TurnStartFromContext(ctx context.Context) (TurnStart, bool) {
	if ctx == nil {
		return TurnStart{}, false
	}
	start, ok := ctx.Value(turnStartContextKey{}).(TurnStart)
	return start, ok && start.RequestID != "" && start.InvocationID != ""
}

// SetGeneration attaches a backend revision generation to a session projection.
func SetGeneration(sess *session.Session, generation uint64) {
	if sess == nil {
		return
	}
	if sess.ServiceMeta == nil {
		sess.ServiceMeta = make(map[string]string)
	}
	sess.ServiceMeta[generationServiceMetaKey] = strconv.FormatUint(generation, 10)
}

// Generation returns the backend revision generation attached to sess.
func Generation(sess *session.Session) (uint64, bool) {
	if sess == nil || sess.ServiceMeta == nil {
		return 0, false
	}
	raw, ok := sess.ServiceMeta[generationServiceMetaKey]
	if !ok {
		return 0, false
	}
	generation, err := strconv.ParseUint(raw, 10, 64)
	return generation, err == nil
}

// ContextWithGeneration attaches the expected active revision to runner work
// that does not otherwise carry a session projection.
func ContextWithGeneration(ctx context.Context, generation uint64) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, generationContextKey{}, generation)
}

// GenerationFromContext returns the expected active revision attached to ctx.
func GenerationFromContext(ctx context.Context) (uint64, bool) {
	if ctx == nil {
		return 0, false
	}
	generation, ok := ctx.Value(generationContextKey{}).(uint64)
	return generation, ok
}

// ContextWithHazard marks a framework write that makes the current turn
// ineligible for a single-session replacement.
func ContextWithHazard(ctx context.Context) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, hazardContextKey{}, true)
}

// ExpectedGeneration resolves a write fence from the concrete session first,
// then from the surrounding runner context.
func ExpectedGeneration(ctx context.Context, sess *session.Session) (uint64, bool) {
	if generation, ok := Generation(sess); ok {
		return generation, true
	}
	return GenerationFromContext(ctx)
}

// NewWrite builds the revision metadata for one event persistence call.
func NewWrite(ctx context.Context, sess *session.Session) Write {
	write := Write{}
	write.ExpectedGeneration, write.HasExpectedGeneration = ExpectedGeneration(ctx, sess)
	if ctx != nil {
		write.Hazard, _ = ctx.Value(hazardContextKey{}).(bool)
	}
	if start, ok := TurnStartFromContext(ctx); ok {
		write.Start = &start
	}
	return write
}

// ApplyWrite records a non-event session mutation. It returns whether the
// private record must be persisted.
func ApplyWrite(record *PersistedRecord, write Write) bool {
	if record == nil {
		return false
	}
	record.Head++
	changed := true
	if record.Checkpoint != nil && (!write.HasExpectedGeneration || write.Hazard) {
		record.Checkpoint.Hazard = true
	}
	return changed
}

// ApplyEventWrite records an event mutation and advances the canonical turn
// state machine. persisted reports whether the event is part of the durable
// active event projection; a non-persisted event cannot establish a boundary.
func ApplyEventWrite(
	record *PersistedRecord,
	write Write,
	evt *event.Event,
	persisted bool,
) bool {
	if record == nil {
		return false
	}
	ApplyWrite(record, write)
	validStart := applyTurnStart(record, write, evt, persisted)
	checkpoint := record.Checkpoint
	if checkpoint == nil || evt == nil {
		return true
	}
	if !validStart && checkpoint.Terminal {
		checkpoint.Hazard = true
	}
	if evt.RequestID != checkpoint.RequestID {
		checkpoint.Hazard = true
	}
	if hasNonSessionStateDelta(evt.StateDelta) {
		checkpoint.Hazard = true
	}
	if !evt.IsRunnerCompletion() {
		return true
	}
	if checkpoint.RequestID != evt.RequestID ||
		checkpoint.InvocationID != evt.InvocationID {
		checkpoint.Hazard = true
		return true
	}
	checkpoint.Terminal = true
	return true
}

func applyTurnStart(
	record *PersistedRecord,
	write Write,
	evt *event.Event,
	persisted bool,
) bool {
	valid := write.Start != nil && persisted && evt != nil &&
		write.Start.RequestID == evt.RequestID &&
		write.Start.InvocationID == evt.InvocationID &&
		len(write.Snapshot) > 0
	if !valid {
		return false
	}
	checkpoint := record.Checkpoint
	if checkpoint == nil || checkpoint.Terminal {
		record.Checkpoint = &PersistedCheckpoint{
			RequestID:    write.Start.RequestID,
			InvocationID: write.Start.InvocationID,
			Snapshot:     append([]byte(nil), write.Snapshot...),
		}
		return true
	}
	if checkpoint.RequestID != write.Start.RequestID ||
		checkpoint.InvocationID != write.Start.InvocationID {
		checkpoint.Hazard = true
	}
	return true
}

func hasNonSessionStateDelta(delta map[string][]byte) bool {
	for key := range delta {
		if strings.HasPrefix(key, session.StateAppPrefix) ||
			strings.HasPrefix(key, session.StateUserPrefix) {
			return true
		}
	}
	return false
}

// ApplyTrackWrite records a track mutation and verifies that track activity
// after a checkpoint belongs to the open root request.
func ApplyTrackWrite(
	record *PersistedRecord,
	write Write,
	trackEvent *session.TrackEvent,
) bool {
	if record == nil {
		return false
	}
	ApplyWrite(record, write)
	checkpoint := record.Checkpoint
	if checkpoint == nil {
		return true
	}
	if trackEvent == nil || trackEvent.RequestID == "" ||
		trackEvent.RequestID != checkpoint.RequestID {
		checkpoint.Hazard = true
	}
	return true
}

// Snapshot returns a serialization-safe session snapshot without shared app
// or user state and without service-local routing metadata.
func Snapshot(sess *session.Session) ([]byte, error) {
	if sess == nil {
		return nil, session.ErrNilSession
	}
	cloned := sess.Clone()
	cloned.ServiceMeta = nil
	state := cloned.SnapshotState()
	for key := range state {
		if strings.HasPrefix(key, session.StateAppPrefix) ||
			strings.HasPrefix(key, session.StateUserPrefix) {
			delete(state, key)
		}
	}
	cloned.State = state
	return json.Marshal(cloned)
}

// DecodeSnapshot decodes a private session snapshot.
func DecodeSnapshot(raw []byte) (*session.Session, error) {
	if len(raw) == 0 {
		return nil, session.ErrNilSession
	}
	var sess session.Session
	if err := json.Unmarshal(raw, &sess); err != nil {
		return nil, err
	}
	sess.Hash = session.NewSession(sess.AppName, sess.UserID, sess.ID).Hash
	return &sess, nil
}
