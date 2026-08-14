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
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

const generationServiceMetaKey = "trpc-agent-go.session.revision-generation"
const activeRevisionServiceMetaKey = "trpc-agent-go.session.revision-active"

const stableProjectionReadAttempts = 3

const maxPersistedReplays = 64

const maxPendingErrorKeys = 1024

type latestTurnReplacementSupportReporter interface {
	SupportsLatestTurnReplacement() bool
}

var (
	// ErrLatestTurnReplacementUnsupported indicates that a session service
	// cannot replace the latest persisted turn.
	ErrLatestTurnReplacementUnsupported = errors.New(
		"latest-turn replacement is unsupported",
	)
	// ErrLatestTurnReplacementConflict indicates that the active latest turn
	// no longer matches the turn observed by the caller.
	ErrLatestTurnReplacementConflict = errors.New(
		"latest-turn replacement conflict",
	)
	// ErrLatestTurnReplacementUnavailable indicates that the latest turn cannot
	// be replaced without risking an inconsistent session projection.
	ErrLatestTurnReplacementUnavailable = errors.New(
		"latest-turn replacement is unavailable",
	)
	// ErrStaleGeneration indicates that a write belongs to a session projection
	// which has already been superseded by a replacement.
	ErrStaleGeneration = errors.New("stale session revision generation")
	// ErrStaleProjection indicates that a turn boundary was prepared from a
	// projection that changed before the boundary could be committed.
	ErrStaleProjection = errors.New("stale session revision projection")
)

// LatestTurnReplacementRequest describes the private storage transition
// requested by Runner.
type LatestTurnReplacementRequest struct {
	Key               session.Key
	ExpectedRequestID string
	IdempotencyKey    string
}

// LatestTurnReplacementResult describes the authoritative active projection
// after a private storage transition.
type LatestTurnReplacementResult struct {
	ActiveSession *session.Session
	Applied       bool
}

type latestTurnReplacer interface {
	ReplaceLatestTurn(
		context.Context,
		LatestTurnReplacementRequest,
	) (*LatestTurnReplacementResult, error)
}

// TrackEventsEqual compares persisted Track event semantics while ignoring
// time.Location representation differences introduced by serialization.
func TrackEventsEqual(a, b session.TrackEvent) bool {
	return a.Track == b.Track &&
		a.RequestID == b.RequestID &&
		rawJSONEqual(a.Payload, b.Payload) &&
		a.Timestamp.Equal(b.Timestamp)
}

func rawJSONEqual(a, b []byte) bool {
	if bytes.Equal(a, b) {
		return true
	}
	var decodedA, decodedB any
	if json.Unmarshal(a, &decodedA) != nil || json.Unmarshal(b, &decodedB) != nil {
		return false
	}
	return reflect.DeepEqual(decodedA, decodedB)
}

// LoadStableProjection reads a projection bracketed by its replacement
// generation. A generation change means the projection may contain data from
// both sides of a replacement, so the read is retried instead of attaching a
// newer generation to stale data.
func LoadStableProjection(
	ctx context.Context,
	readGeneration func(context.Context) (uint64, error),
	readProjection func(context.Context) (*session.Session, error),
) (*session.Session, error) {
	for attempt := 0; attempt < stableProjectionReadAttempts; attempt++ {
		before, err := readGeneration(ctx)
		if err != nil {
			return nil, err
		}
		projection, err := readProjection(ctx)
		if err != nil || projection == nil {
			return projection, err
		}
		after, err := readGeneration(ctx)
		if err != nil {
			return nil, err
		}
		if before == after {
			SetGeneration(projection, before)
			return projection, nil
		}
	}
	return nil, fmt.Errorf(
		"read stable session projection: %w",
		ErrStaleProjection,
	)
}

// LoadStableListedProjection prevents a generation read taken after a list
// projection from blessing stale data. Generation zero can be attached to the
// listed projection safely: a later first replacement will fence it. Once a
// session has been replaced, the projection is reread with generation
// bracketing before it is returned for further use.
func LoadStableListedProjection(
	ctx context.Context,
	listed *session.Session,
	metadataOnly bool,
	readGeneration func(context.Context) (uint64, error),
	readProjection func(context.Context) (*session.Session, error),
) (*session.Session, error) {
	if listed == nil {
		return nil, nil
	}
	generation, err := readGeneration(ctx)
	if err != nil {
		return nil, err
	}
	return LoadStableListedProjectionAtGeneration(
		ctx, listed, metadataOnly, generation, readGeneration, readProjection,
	)
}

// LoadStableListedProjectionAtGeneration validates a listed projection using
// a generation obtained by a caller's batched read.
func LoadStableListedProjectionAtGeneration(
	ctx context.Context,
	listed *session.Session,
	metadataOnly bool,
	generation uint64,
	readGeneration func(context.Context) (uint64, error),
	readProjection func(context.Context) (*session.Session, error),
) (*session.Session, error) {
	if listed == nil {
		return nil, nil
	}
	if generation == 0 {
		SetGeneration(listed, 0)
		return listed, nil
	}
	projection, err := LoadStableProjection(ctx, readGeneration, readProjection)
	if err != nil || projection == nil || !metadataOnly {
		return projection, err
	}
	projection.Events = nil
	projection.Tracks = nil
	projection.Summaries = nil
	return projection, nil
}

// SupportsLatestTurnReplacement reports whether service implements the
// private backend capability required by Runner latest-turn replacement.
func SupportsLatestTurnReplacement(service session.Service) bool {
	if service == nil {
		return false
	}
	if reporter, ok := service.(latestTurnReplacementSupportReporter); ok {
		return reporter.SupportsLatestTurnReplacement()
	}
	_, ok := service.(latestTurnReplacer)
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
	replacer, ok := service.(latestTurnReplacer)
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
// top-level turn. Boundary compactly identifies the active projection and
// captures the mutable session fields immediately before the turn began.
// Terminal reports whether Runner persisted its completion marker; a
// non-terminal checkpoint still identifies an unfinished turn that can be
// replaced after its execution has stopped.
type PersistedCheckpoint struct {
	RequestID    string `json:"requestID"`
	InvocationID string `json:"invocationID"`
	Boundary     []byte `json:"boundary"`
	Terminal     bool   `json:"terminal"`
	Hazard       bool   `json:"hazard,omitempty"`
}

type persistedBoundary struct {
	Version   int                               `json:"version"`
	AppName   string                            `json:"appName"`
	UserID    string                            `json:"userID"`
	SessionID string                            `json:"sessionID"`
	State     session.StateMap                  `json:"state"`
	Summaries map[string]*session.Summary       `json:"summaries,omitempty"`
	Events    persistedPrefix                   `json:"events"`
	Tracks    map[session.Track]persistedPrefix `json:"tracks,omitempty"`
	CreatedAt time.Time                         `json:"createdAt"`
	UpdatedAt time.Time                         `json:"updatedAt"`
}

type persistedPrefix struct {
	Count  uint64 `json:"count"`
	Digest []byte `json:"digest"`
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

// AttachRecord attaches the persisted generation and whether revision
// metadata is active to a loaded session projection.
func AttachRecord(sess *session.Session, record *PersistedRecord) {
	if sess == nil || record == nil {
		return
	}
	SetGeneration(sess, record.Generation)
	if record.Generation == 0 && record.Head == 0 &&
		record.Checkpoint == nil && len(record.Replays) == 0 {
		return
	}
	if sess.ServiceMeta == nil {
		sess.ServiceMeta = make(map[string]string)
	}
	sess.ServiceMeta[activeRevisionServiceMetaKey] = "true"
}

// RecordActive reports whether a loaded session carries non-empty persisted
// revision metadata.
func RecordActive(sess *session.Session) bool {
	return sess != nil && sess.ServiceMeta != nil &&
		sess.ServiceMeta[activeRevisionServiceMetaKey] == "true"
}

// RecordLatestTurnReplacementReplay retains a bounded window of replacement
// identities. Entries outside the window no longer participate in reuse
// detection; callers should retry ambiguous transitions promptly.
func RecordLatestTurnReplacementReplay(
	record *PersistedRecord,
	idempotencyKey string,
	replay PersistedReplay,
) {
	if record == nil || idempotencyKey == "" {
		return
	}
	if record.Replays == nil {
		record.Replays = make(map[string]PersistedReplay)
	}
	record.Replays[idempotencyKey] = replay
	if len(record.Replays) <= maxPersistedReplays {
		return
	}
	var oldestKey string
	var oldest PersistedReplay
	for key, candidate := range record.Replays {
		if oldestKey == "" || replayPrecedes(candidate, key, oldest, oldestKey) {
			oldestKey = key
			oldest = candidate
		}
	}
	delete(record.Replays, oldestKey)
}

func replayPrecedes(a PersistedReplay, aKey string, b PersistedReplay, bKey string) bool {
	if a.Generation != b.Generation {
		return a.Generation < b.Generation
	}
	if a.Head != b.Head {
		return a.Head < b.Head
	}
	return aKey < bKey
}

// PendingErrors accumulates asynchronous persistence failures by session.
// It is intended to be owned by one persistence worker and is not safe for
// concurrent use.
type PendingErrors struct {
	byKey    map[session.Key]error
	overflow error
}

// Add retains the first representative error for the next barrier belonging
// to key. Once the bounded key set is full, a worker-wide poison error keeps
// later barriers from incorrectly reporting successful persistence.
func (p *PendingErrors) Add(key session.Key, err error) {
	if err == nil {
		return
	}
	if p.byKey == nil {
		p.byKey = make(map[session.Key]error)
	}
	if _, ok := p.byKey[key]; ok {
		return
	}
	if len(p.byKey) >= maxPendingErrorKeys {
		if p.overflow == nil {
			p.overflow = errors.New(
				"asynchronous persistence error retention capacity exceeded",
			)
		}
		return
	}
	p.byKey[key] = err
}

// Deliver sends the failure retained for key to an unbuffered barrier. A keyed
// error is cleared only after the waiter has received it. Cancellation before
// delivery leaves the error available to a later barrier. A worker-wide
// overflow poison is intentionally retained for the worker lifetime because
// the individual failed keys were not retained.
func (p *PendingErrors) Deliver(
	ctx context.Context,
	key session.Key,
	done chan<- error,
) {
	if ctx == nil {
		ctx = context.Background()
	}
	err, keyed := p.byKey[key]
	if err == nil {
		err = p.overflow
	}
	select {
	case done <- err:
		if keyed {
			delete(p.byKey, key)
		}
	case <-ctx.Done():
	}
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
		record.Checkpoint.Hazard || len(record.Checkpoint.Boundary) == 0 {
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
	Boundary              []byte
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
		len(write.Boundary) > 0
	if !valid {
		return false
	}
	checkpoint := record.Checkpoint
	if checkpoint == nil || checkpoint.Terminal {
		record.Checkpoint = &PersistedCheckpoint{
			RequestID:    write.Start.RequestID,
			InvocationID: write.Start.InvocationID,
			Boundary:     append([]byte(nil), write.Boundary...),
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

const persistedBoundaryVersion = 1

// NewBoundary returns a compact, serialization-safe description of sess.
// App- and user-scoped state and service-local metadata are excluded.
func NewBoundary(sess *session.Session) ([]byte, error) {
	if sess == nil {
		return nil, session.ErrNilSession
	}
	cloned := sess.Clone()
	state := cloned.SnapshotState()
	for key := range state {
		if strings.HasPrefix(key, session.StateAppPrefix) ||
			strings.HasPrefix(key, session.StateUserPrefix) {
			delete(state, key)
		}
	}
	eventDigest, err := projectionDigest("events", cloned.Events)
	if err != nil {
		return nil, err
	}
	boundary := persistedBoundary{
		Version:   persistedBoundaryVersion,
		AppName:   cloned.AppName,
		UserID:    cloned.UserID,
		SessionID: cloned.ID,
		State:     state,
		Summaries: cloned.Summaries,
		Events: persistedPrefix{
			Count:  uint64(len(cloned.Events)),
			Digest: eventDigest,
		},
		CreatedAt: cloned.CreatedAt,
		UpdatedAt: cloned.UpdatedAt,
	}
	if len(cloned.Tracks) > 0 {
		boundary.Tracks = make(
			map[session.Track]persistedPrefix,
			len(cloned.Tracks),
		)
		for track, history := range cloned.Tracks {
			var events []session.TrackEvent
			if history != nil {
				events = history.Events
			}
			digest, err := projectionDigest("track:"+string(track), events)
			if err != nil {
				return nil, err
			}
			boundary.Tracks[track] = persistedPrefix{
				Count:  uint64(len(events)),
				Digest: digest,
			}
		}
	}
	raw, err := json.Marshal(boundary)
	if err != nil {
		return nil, fmt.Errorf("encode session boundary: %w", err)
	}
	return raw, nil
}

// RestoreBoundary verifies that current still contains the projection captured
// by boundary, then returns the restored pre-turn session. Prefix mismatches
// fail closed rather than reconstructing data which has expired or changed.
func RestoreBoundary(
	current *session.Session,
	raw []byte,
) (*session.Session, error) {
	if current == nil {
		return nil, session.ErrNilSession
	}
	if len(raw) == 0 {
		return nil, ErrLatestTurnReplacementUnavailable
	}
	var boundary persistedBoundary
	if err := json.Unmarshal(raw, &boundary); err != nil {
		return nil, fmt.Errorf("decode session boundary: %w", err)
	}
	if boundary.Version != persistedBoundaryVersion ||
		boundary.AppName != current.AppName ||
		boundary.UserID != current.UserID ||
		boundary.SessionID != current.ID {
		return nil, ErrLatestTurnReplacementUnavailable
	}
	if boundary.Events.Count >= uint64(len(current.Events)) {
		return nil, ErrLatestTurnReplacementUnavailable
	}
	if err := verifyProjectionPrefix(
		"events", current.Events, boundary.Events,
	); err != nil {
		return nil, err
	}
	restored := current.Clone()
	restored.Events = restored.Events[:boundary.Events.Count]
	restored.Tracks = nil
	if len(boundary.Tracks) > 0 {
		restored.Tracks = make(
			map[session.Track]*session.TrackEvents,
			len(boundary.Tracks),
		)
		for track, prefix := range boundary.Tracks {
			history := current.Tracks[track]
			if history == nil {
				if err := verifyProjectionPrefix(
					"track:"+string(track), []session.TrackEvent(nil), prefix,
				); err != nil {
					return nil, ErrLatestTurnReplacementUnavailable
				}
				restored.Tracks[track] = &session.TrackEvents{Track: track}
				continue
			}
			if prefix.Count > uint64(len(history.Events)) {
				return nil, ErrLatestTurnReplacementUnavailable
			}
			if err := verifyProjectionPrefix(
				"track:"+string(track), history.Events, prefix,
			); err != nil {
				return nil, err
			}
			events := append(
				[]session.TrackEvent(nil),
				history.Events[:prefix.Count]...,
			)
			restored.Tracks[track] = &session.TrackEvents{
				Track:  track,
				Events: events,
			}
		}
	}
	restored.State = cloneState(boundary.State)
	restored.Summaries = cloneSummaries(boundary.Summaries)
	restored.CreatedAt = boundary.CreatedAt
	restored.UpdatedAt = boundary.UpdatedAt
	restored.ServiceMeta = nil
	return normalizeSession(restored)
}

func verifyProjectionPrefix[T any](
	domain string,
	values []T,
	prefix persistedPrefix,
) error {
	if prefix.Count > uint64(len(values)) || len(prefix.Digest) != sha256.Size {
		return ErrLatestTurnReplacementUnavailable
	}
	digest, err := projectionDigest(domain, values[:prefix.Count])
	if err != nil {
		return err
	}
	if !bytes.Equal(digest, prefix.Digest) {
		return ErrLatestTurnReplacementUnavailable
	}
	return nil
}

func projectionDigest[T any](domain string, values []T) ([]byte, error) {
	h := sha256.New()
	if _, err := h.Write([]byte(domain)); err != nil {
		return nil, err
	}
	var length [8]byte
	for i := range values {
		raw, err := json.Marshal(values[i])
		if err != nil {
			return nil, fmt.Errorf("encode projection prefix: %w", err)
		}
		binary.BigEndian.PutUint64(length[:], uint64(len(raw)))
		if _, err := h.Write(length[:]); err != nil {
			return nil, err
		}
		if _, err := h.Write(raw); err != nil {
			return nil, err
		}
	}
	return h.Sum(nil), nil
}

func cloneState(state session.StateMap) session.StateMap {
	if state == nil {
		return nil
	}
	cloned := make(session.StateMap, len(state))
	for key, value := range state {
		cloned[key] = append([]byte(nil), value...)
	}
	return cloned
}

func cloneSummaries(
	summaries map[string]*session.Summary,
) map[string]*session.Summary {
	if summaries == nil {
		return nil
	}
	cloned := make(map[string]*session.Summary, len(summaries))
	for key, summary := range summaries {
		if summary != nil {
			cloned[key] = summary.Clone()
		}
	}
	return cloned
}

func normalizeSession(sess *session.Session) (*session.Session, error) {
	raw, err := json.Marshal(sess)
	if err != nil {
		return nil, err
	}
	var normalized session.Session
	if err := json.Unmarshal(raw, &normalized); err != nil {
		return nil, err
	}
	normalized.Hash = session.NewSession(
		normalized.AppName,
		normalized.UserID,
		normalized.ID,
	).Hash
	return &normalized, nil
}
