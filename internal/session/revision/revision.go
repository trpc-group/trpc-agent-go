//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package revision carries private rewind metadata between the runner and
// session service implementations.
package revision

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"math"
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
const rewindHeadFenceServiceMetaKey = "trpc-agent-go.session.rewind-head-fence"

const stableProjectionReadAttempts = 3

const maxPersistedReplays = 64

const maxPendingErrorKeys = 1024

var (
	// ErrRewindUnsupported is the public unsupported rewind error.
	ErrRewindUnsupported = session.ErrRewindUnsupported
	// ErrRewindConflict is the public rewind conflict error.
	ErrRewindConflict = session.ErrRewindConflict
	// ErrRewindUnavailable is the public unavailable rewind error.
	ErrRewindUnavailable = session.ErrRewindUnavailable
	// ErrStaleGeneration indicates that a write belongs to a session projection
	// which has already been superseded by a replacement.
	ErrStaleGeneration = errors.New("stale session revision generation")
	// ErrStaleProjection indicates that a turn boundary was prepared from a
	// projection that changed before the boundary could be committed.
	ErrStaleProjection           = errors.New("stale session revision projection")
	errRewindEventNotPersistable = errors.New(
		"first event after session rewind must be persistable",
	)
)

// RewindRequest aliases the public request for backend-private helpers.
type RewindRequest = session.RewindRequest

// StorageRewindResult describes the authoritative projection returned by a
// backend-private rewind transition.
type StorageRewindResult struct {
	ActiveSession *session.Session
	Applied       bool
}

// TrackEventsEqual compares persisted Track event semantics while ignoring
// time.Location representation differences introduced by serialization.
func TrackEventsEqual(a, b session.TrackEvent) bool {
	return a.Track == b.Track &&
		JSONEqual(a.Payload, b.Payload) &&
		a.Timestamp.Equal(b.Timestamp)
}

// JSONEqual compares JSON values independent of insignificant encoding
// differences such as whitespace and object key order.
func JSONEqual(a, b []byte) bool {
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

// Rewind invokes the public backend capability and verifies its postconditions
// for framework callers.
func Rewind(
	ctx context.Context,
	service session.Service,
	req session.RewindRequest,
) (*session.RewindResult, error) {
	if err := ValidateRewindRequest(req); err != nil {
		return nil, err
	}
	rewinder, ok := service.(session.RewindService)
	if !ok {
		return nil, ErrRewindUnsupported
	}
	result, err := rewinder.Rewind(ctx, req)
	if err != nil {
		return nil, err
	}
	if result == nil || result.Session == nil {
		return nil, fmt.Errorf("session rewind returned no active session")
	}
	activeKey := session.Key{
		AppName:   result.Session.AppName,
		UserID:    result.Session.UserID,
		SessionID: result.Session.ID,
	}
	if activeKey != req.Key {
		return nil, fmt.Errorf(
			"session rewind returned key %+v, want %+v",
			activeKey,
			req.Key,
		)
	}
	return result, nil
}

// ValidateRewindRequest validates backend-independent request identity fields.
func ValidateRewindRequest(req session.RewindRequest) error {
	if err := req.Key.CheckSessionKey(); err != nil {
		return err
	}
	if req.TargetRequestID == "" {
		return fmt.Errorf("target request id is required")
	}
	if req.ExpectedHeadRequestID == "" {
		return fmt.Errorf("expected head request id is required")
	}
	if req.IdempotencyKey == "" {
		return fmt.Errorf("idempotency key is required")
	}
	return nil
}

type turnStartContextKey struct{}
type generationContextKey struct{}
type hazardContextKey struct{}
type requestIDContextKey struct{}
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
	// RestoreState overlays state consumed while routing the turn onto the
	// pre-turn checkpoint without restoring it to the active projection.
	RestoreState session.StateMap
}

// PersistedCheckpoint is the backend-private durable boundary for the latest
// top-level turn. Boundary compactly identifies the active projection and
// captures the mutable session fields immediately before the turn began.
// Terminal reports whether Runner persisted its completion marker; a
// non-terminal checkpoint still identifies an unfinished turn that can be
// replaced after its execution has stopped.
type PersistedCheckpoint struct {
	RequestID          string `json:"requestID"`
	InvocationID       string `json:"invocationID"`
	PriorHeadRequestID string `json:"priorHeadRequestID,omitempty"`
	Boundary           []byte `json:"boundary"`
	Terminal           bool   `json:"terminal"`
	Hazard             bool   `json:"hazard,omitempty"`
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
	Count        uint64     `json:"count"`
	Digest       []byte     `json:"digest"`
	MaxTimestamp *time.Time `json:"maxTimestamp,omitempty"`
}

const persistedProjectionVersion = 2

// PersistedProjection is the rolling event and track prefix retained in a
// backend-private revision record. A nil projection requires one authoritative
// bootstrap read before another turn boundary can be captured.
type PersistedProjection struct {
	Version int                               `json:"version"`
	Events  persistedPrefix                   `json:"events"`
	Tracks  map[session.Track]persistedPrefix `json:"tracks,omitempty"`
}

// PersistedReplay retains the result needed to answer an idempotent rewind
// retry.
type PersistedReplay struct {
	TargetRequestID       string `json:"targetRequestID"`
	ExpectedHeadRequestID string `json:"expectedHeadRequestID"`
	Generation            uint64 `json:"generation"`
	Head                  uint64 `json:"head"`
}

// PersistedRecord is the backend-private revision metadata for one session.
type PersistedRecord struct {
	Generation    uint64                     `json:"generation"`
	Head          uint64                     `json:"head"`
	HeadRequestID string                     `json:"headRequestID,omitempty"`
	Checkpoint    *PersistedCheckpoint       `json:"checkpoint,omitempty"`
	Replays       map[string]PersistedReplay `json:"replays,omitempty"`
	Projection    *PersistedProjection       `json:"projection,omitempty"`

	// incompatibleVersion is set only while decoding a newer sidecar format.
	// It keeps base reads available without allowing an older writer to replace
	// metadata it cannot understand.
	incompatibleVersion int
}

// AttachRecord attaches the persisted generation and whether revision
// metadata is active to a loaded session projection.
func AttachRecord(sess *session.Session, record *PersistedRecord) {
	if sess == nil || record == nil {
		return
	}
	SetGeneration(sess, record.Generation)
	if record.Generation == 0 && record.Head == 0 && record.HeadRequestID == "" &&
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

// RecordRewindReplay retains a bounded window of rewind identities. Entries
// outside the window no longer participate in reuse detection; callers should
// retry ambiguous transitions promptly.
func RecordRewindReplay(
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

// RewindReplay returns a matching idempotent rewind result. A reused key or a
// replay superseded by later writes returns a conflict.
func RewindReplay(
	record *PersistedRecord,
	targetRequestID string,
	expectedHeadRequestID string,
	idempotencyKey string,
) (PersistedReplay, bool, error) {
	if record == nil {
		return PersistedReplay{}, false, nil
	}
	replay, ok := record.Replays[idempotencyKey]
	if !ok {
		return PersistedReplay{}, false, nil
	}
	if replay.TargetRequestID != targetRequestID ||
		replay.ExpectedHeadRequestID != expectedHeadRequestID ||
		replay.Generation != record.Generation || replay.Head != record.Head {
		return PersistedReplay{}, true, ErrRewindConflict
	}
	return replay, true, nil
}

// RewindCheckpoint returns the safe canonical checkpoint for a rewind. Missing
// or hazardous checkpoints are unavailable, while a different active head is
// a conflict. The current storage format retains only the latest boundary, so
// an older target is unavailable even when the expected head matches.
func RewindCheckpoint(
	record *PersistedRecord,
	targetRequestID string,
	expectedHeadRequestID string,
) (*PersistedCheckpoint, error) {
	if record == nil {
		return nil, ErrRewindUnavailable
	}
	activeHeadRequestID := record.HeadRequestID
	if activeHeadRequestID == "" && record.Checkpoint != nil {
		// A record written before explicit head identity can still identify its
		// latest canonical turn from the retained checkpoint.
		activeHeadRequestID = record.Checkpoint.RequestID
	}
	if activeHeadRequestID != "" && activeHeadRequestID != expectedHeadRequestID {
		return nil, ErrRewindConflict
	}
	if record.Checkpoint == nil {
		// A successful rewind changes the active head even when its restored
		// pre-turn boundary predates persisted request identity.
		if len(record.Replays) > 0 {
			return nil, ErrRewindConflict
		}
		return nil, ErrRewindUnavailable
	}
	if record.Checkpoint.Hazard || len(record.Checkpoint.Boundary) == 0 {
		return nil, ErrRewindUnavailable
	}
	if record.Checkpoint.RequestID != targetRequestID {
		return nil, ErrRewindUnavailable
	}
	return record.Checkpoint, nil
}

// Write describes revision metadata that must be committed atomically with a
// session write.
type Write struct {
	ExpectedGeneration      uint64
	HasExpectedGeneration   bool
	ExpectedHead            uint64
	HasExpectedHead         bool
	Hazard                  bool
	RequestID               string
	Start                   *TurnStart
	Boundary                []byte
	BoundaryRequiresSummary bool
	Projection              *PersistedProjection
}

// ContextWithRequestID associates framework-owned writes with the active
// Runner request without adding request identity to public persistence types.
func ContextWithRequestID(ctx context.Context, requestID string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, requestIDContextKey{}, requestID)
}

// ContextWithTurnStart marks a single persistence call as the start of a turn.
func ContextWithTurnStart(ctx context.Context, start TurnStart) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	start.RestoreState = cloneState(start.RestoreState)
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

// AttachRewindFence marks a Rewind result with the revision identity that the
// caller's first replacement write must still observe. The marker is private
// framework metadata; ordinary session reads intentionally do not carry it.
func AttachRewindFence(sess *session.Session, record *PersistedRecord) {
	if sess == nil || record == nil {
		return
	}
	SetGeneration(sess, record.Generation)
	if sess.ServiceMeta == nil {
		sess.ServiceMeta = make(map[string]string)
	}
	sess.ServiceMeta[rewindHeadFenceServiceMetaKey] = strconv.FormatUint(
		record.Head, 10,
	)
}

// RewindHeadFence returns the first-write head fence on a Rewind result.
func RewindHeadFence(sess *session.Session) (uint64, bool) {
	if sess == nil || sess.ServiceMeta == nil {
		return 0, false
	}
	raw, ok := sess.ServiceMeta[rewindHeadFenceServiceMetaKey]
	if !ok {
		return 0, false
	}
	head, err := strconv.ParseUint(raw, 10, 64)
	return head, err == nil
}

// ClearRewindHeadFence removes the one-shot first-write fence after the
// corresponding write has committed successfully.
func ClearRewindHeadFence(sess *session.Session) {
	if sess == nil || sess.ServiceMeta == nil {
		return
	}
	delete(sess.ServiceMeta, rewindHeadFenceServiceMetaKey)
}

// CompleteWrite consumes a successful first-write fence and returns err.
func CompleteWrite(sess *session.Session, write Write, err error) error {
	if err == nil && write.HasExpectedHead {
		ClearRewindHeadFence(sess)
	}
	return err
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
		write.RequestID, _ = ctx.Value(requestIDContextKey{}).(string)
	}
	if start, ok := TurnStartFromContext(ctx); ok {
		write.Start = &start
		write.ExpectedHead, write.HasExpectedHead = RewindHeadFence(sess)
		if sess != nil {
			sess.EventMu.RLock()
			for i := range sess.Events {
				if sess.Events[i].RequestID == start.RequestID {
					write.Hazard = true
					break
				}
			}
			sess.EventMu.RUnlock()
		}
	}
	return write
}

// NewEventWrite builds revision metadata for an event persistence call. The
// first event submitted through a Rewind result carries its one-shot head
// fence even when the caller is outside Runner and has no turn-start marker.
func NewEventWrite(
	ctx context.Context,
	sess *session.Session,
	evt *event.Event,
) (Write, error) {
	write := NewWrite(ctx, sess)
	if !write.HasExpectedHead {
		write.ExpectedHead, write.HasExpectedHead = RewindHeadFence(sess)
	}
	if write.HasExpectedHead && (evt == nil || evt.Response == nil ||
		evt.IsPartial || !evt.IsValidContent()) {
		return write, errRewindEventNotPersistable
	}
	return write, nil
}

// CheckWrite verifies the optimistic revision identity carried by a write.
// Backends must call it inside the same lock or transaction as the mutation.
func CheckWrite(record *PersistedRecord, write Write) error {
	var generation, head uint64
	if record != nil {
		generation = record.Generation
		head = record.Head
	}
	if write.HasExpectedGeneration && generation != write.ExpectedGeneration {
		return ErrStaleGeneration
	}
	if write.HasExpectedHead && head != write.ExpectedHead {
		return ErrRewindConflict
	}
	return nil
}

// ApplyWrite records a non-event session mutation. It returns whether the
// private record must be persisted.
func ApplyWrite(record *PersistedRecord, write Write) bool {
	return applyWrite(record, write, true)
}

func applyWrite(record *PersistedRecord, write Write, requireRequestID bool) bool {
	if record == nil {
		return false
	}
	record.Head++
	changed := true
	if record.Checkpoint != nil && (!write.HasExpectedGeneration || write.Hazard ||
		(requireRequestID && (write.RequestID == "" ||
			write.RequestID != record.Checkpoint.RequestID))) {
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
	applyWrite(record, write, false)
	validStart := applyTurnStart(record, write, evt, persisted)
	if persisted && evt != nil && evt.RequestID != "" {
		record.HeadRequestID = evt.RequestID
	}
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
			RequestID:          write.Start.RequestID,
			InvocationID:       write.Start.InvocationID,
			PriorHeadRequestID: record.HeadRequestID,
			Boundary:           append([]byte(nil), write.Boundary...),
			Hazard: write.Hazard || record.HeadRequestID != "" &&
				record.HeadRequestID == write.Start.RequestID,
		}
		return true
	}
	if checkpoint.RequestID != write.Start.RequestID ||
		checkpoint.InvocationID != write.Start.InvocationID {
		checkpoint.Hazard = true
	}
	return true
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
	applyWrite(record, write, false)
	checkpoint := record.Checkpoint
	if checkpoint == nil {
		return true
	}
	if trackEvent == nil || write.RequestID == "" ||
		write.RequestID != checkpoint.RequestID {
		checkpoint.Hazard = true
	}
	return true
}

const persistedBoundaryVersion = 2

// ProjectionInitialized reports whether record carries a rolling projection
// that this binary can extend.
func ProjectionInitialized(record *PersistedRecord) bool {
	return record != nil && validateProjection(record.Projection) == nil
}

// InitializeProjection bootstraps record's rolling projection from an
// authoritative complete session projection.
func InitializeProjection(
	record *PersistedRecord,
	sess *session.Session,
) error {
	if record == nil {
		return fmt.Errorf("initialize nil session revision projection")
	}
	projection, err := projectionFromSession(sess)
	if err != nil {
		return err
	}
	record.Projection = projection
	return nil
}

// CloneProjection returns a deep copy of projection.
func CloneProjection(projection *PersistedProjection) *PersistedProjection {
	if projection == nil {
		return nil
	}
	cloned := &PersistedProjection{
		Version: projection.Version,
		Events:  clonePrefix(projection.Events),
	}
	if len(projection.Tracks) > 0 {
		cloned.Tracks = make(
			map[session.Track]persistedPrefix,
			len(projection.Tracks),
		)
		for track, prefix := range projection.Tracks {
			cloned.Tracks[track] = clonePrefix(prefix)
		}
	}
	return cloned
}

// AppendProjectionEvent extends an initialized rolling event prefix. A nil
// projection is left untouched so legacy records can bootstrap atomically at
// the next turn start.
func AppendProjectionEvent(
	record *PersistedRecord,
	evt *event.Event,
) error {
	if record == nil || record.Projection == nil {
		return nil
	}
	if record.Projection.Version != persistedProjectionVersion {
		return ErrRewindUnavailable
	}
	if evt == nil {
		return fmt.Errorf("append nil event revision projection")
	}
	return appendTimestampedProjectionValue(
		record, "events", &record.Projection.Events, evt.Timestamp, evt,
	)
}

// AppendProjectionTrack extends an initialized rolling track prefix. A nil
// projection is left untouched so legacy records can bootstrap atomically at
// the next turn start.
func AppendProjectionTrack(
	record *PersistedRecord,
	trackEvent *session.TrackEvent,
) error {
	if record == nil || record.Projection == nil {
		return nil
	}
	if record.Projection.Version != persistedProjectionVersion {
		return ErrRewindUnavailable
	}
	if trackEvent == nil {
		return fmt.Errorf("append nil track revision projection")
	}
	if record.Projection.Tracks == nil {
		record.Projection.Tracks = make(
			map[session.Track]persistedPrefix,
		)
	}
	prefix, ok := record.Projection.Tracks[trackEvent.Track]
	if !ok {
		prefix.Digest = projectionSeed(trackDomain(trackEvent.Track))
	}
	if err := appendTimestampedProjectionValue(
		record,
		trackDomain(trackEvent.Track),
		&prefix,
		trackEvent.Timestamp,
		trackEvent,
	); err != nil {
		return err
	}
	if record.Projection == nil {
		return nil
	}
	record.Projection.Tracks[trackEvent.Track] = prefix
	return nil
}

// ResetProjectionFromBoundary restores the rolling projection described by a
// successfully verified boundary.
func ResetProjectionFromBoundary(
	record *PersistedRecord,
	raw []byte,
) error {
	if record == nil {
		return fmt.Errorf("reset nil session revision projection")
	}
	var boundary persistedBoundary
	if err := json.Unmarshal(raw, &boundary); err != nil {
		return fmt.Errorf("decode session boundary projection: %w", err)
	}
	if boundary.Version != persistedBoundaryVersion {
		record.Projection = nil
		return nil
	}
	record.Projection = &PersistedProjection{
		Version: persistedProjectionVersion,
		Events:  clonePrefix(boundary.Events),
	}
	if len(boundary.Tracks) > 0 {
		record.Projection.Tracks = make(
			map[session.Track]persistedPrefix,
			len(boundary.Tracks),
		)
		for track, prefix := range boundary.Tracks {
			record.Projection.Tracks[track] = clonePrefix(prefix)
		}
	}
	return validateProjection(record.Projection)
}

// InvalidateProjection requires the next turn start to bootstrap from the
// authoritative projection. Callers must advance the record head and hazard
// state in the same atomic mutation when invalidation accompanies data loss.
func InvalidateProjection(record *PersistedRecord) {
	if record != nil {
		record.Projection = nil
	}
}

// NewBoundary returns a compact, serialization-safe description of sess.
// App- and user-scoped state and service-local metadata are excluded.
func NewBoundary(sess *session.Session) ([]byte, error) {
	projection, err := projectionFromSession(sess)
	if err != nil {
		return nil, err
	}
	return NewBoundaryFromProjection(sess, projection, nil)
}

// NewBoundaryFromProjection captures mutable session fields while reusing an
// initialized rolling event and track prefix.
func NewBoundaryFromProjection(
	sess *session.Session,
	projection *PersistedProjection,
	restoreState session.StateMap,
) ([]byte, error) {
	if sess == nil {
		return nil, session.ErrNilSession
	}
	if err := validateProjection(projection); err != nil {
		return nil, ErrRewindUnavailable
	}
	state := sess.SnapshotState()
	for key, value := range restoreState {
		if value == nil {
			delete(state, key)
			continue
		}
		state[key] = append([]byte(nil), value...)
	}
	for key := range state {
		if strings.HasPrefix(key, session.StateAppPrefix) ||
			strings.HasPrefix(key, session.StateUserPrefix) {
			delete(state, key)
		}
	}
	boundary := persistedBoundary{
		Version:   persistedBoundaryVersion,
		AppName:   sess.AppName,
		UserID:    sess.UserID,
		SessionID: sess.ID,
		State:     state,
		Summaries: cloneSummaries(sess.Summaries),
		Events:    clonePrefix(projection.Events),
		CreatedAt: sess.CreatedAt,
		UpdatedAt: sess.UpdatedAt,
	}
	if len(projection.Tracks) > 0 {
		boundary.Tracks = make(
			map[session.Track]persistedPrefix,
			len(projection.Tracks),
		)
		for track, prefix := range projection.Tracks {
			boundary.Tracks[track] = clonePrefix(prefix)
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
		return nil, ErrRewindUnavailable
	}
	var boundary persistedBoundary
	if err := json.Unmarshal(raw, &boundary); err != nil {
		return nil, fmt.Errorf("decode session boundary: %w", err)
	}
	if boundary.Version != persistedBoundaryVersion ||
		boundary.AppName != current.AppName ||
		boundary.UserID != current.UserID ||
		boundary.SessionID != current.ID {
		return nil, ErrRewindUnavailable
	}
	if boundary.Events.Count >= uint64(len(current.Events)) {
		return nil, ErrRewindUnavailable
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
					trackDomain(track), []session.TrackEvent(nil), prefix,
				); err != nil {
					return nil, ErrRewindUnavailable
				}
				restored.Tracks[track] = &session.TrackEvents{Track: track}
				continue
			}
			if prefix.Count > uint64(len(history.Events)) {
				return nil, ErrRewindUnavailable
			}
			if err := verifyProjectionPrefix(
				trackDomain(track), history.Events, prefix,
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
		return ErrRewindUnavailable
	}
	digest, err := projectionDigest(domain, values[:prefix.Count])
	if err != nil {
		return err
	}
	if !bytes.Equal(digest, prefix.Digest) {
		return ErrRewindUnavailable
	}
	return nil
}

func projectionDigest[T any](domain string, values []T) ([]byte, error) {
	prefix := persistedPrefix{Digest: projectionSeed(domain)}
	for i := range values {
		if err := appendProjectionValue(domain, &prefix, values[i]); err != nil {
			return nil, err
		}
	}
	return prefix.Digest, nil
}

func appendTimestampedProjectionValue[T any](
	record *PersistedRecord,
	domain string,
	prefix *persistedPrefix,
	timestamp time.Time,
	value T,
) error {
	if prefix.Count > 0 && prefix.MaxTimestamp != nil &&
		timestamp.Before(*prefix.MaxTimestamp) {
		// The authoritative projection is ordered by timestamp in at least one
		// backend. A backdated row is not a suffix, so the rolling digest cannot
		// be extended safely. Keep replacement fail-closed and let the next turn
		// rebuild from the authoritative projection.
		if record.Checkpoint != nil {
			record.Checkpoint.Hazard = true
		}
		record.Projection = nil
		return nil
	}
	if err := appendProjectionValue(domain, prefix, value); err != nil {
		return err
	}
	if prefix.Count == 1 || prefix.MaxTimestamp == nil ||
		timestamp.After(*prefix.MaxTimestamp) {
		maxTimestamp := timestamp
		prefix.MaxTimestamp = &maxTimestamp
	}
	return nil
}

func projectionFromSession(
	sess *session.Session,
) (*PersistedProjection, error) {
	if sess == nil {
		return nil, session.ErrNilSession
	}
	cloned := sess.Clone()
	eventDigest, err := projectionDigest("events", cloned.Events)
	if err != nil {
		return nil, err
	}
	projection := &PersistedProjection{
		Version: persistedProjectionVersion,
		Events: persistedPrefix{
			Count:        uint64(len(cloned.Events)),
			Digest:       eventDigest,
			MaxTimestamp: maxEventTimestamp(cloned.Events),
		},
	}
	if len(cloned.Tracks) == 0 {
		return projection, nil
	}
	projection.Tracks = make(
		map[session.Track]persistedPrefix,
		len(cloned.Tracks),
	)
	for track, history := range cloned.Tracks {
		var events []session.TrackEvent
		if history != nil {
			events = history.Events
		}
		digest, err := projectionDigest(trackDomain(track), events)
		if err != nil {
			return nil, err
		}
		projection.Tracks[track] = persistedPrefix{
			Count:        uint64(len(events)),
			Digest:       digest,
			MaxTimestamp: maxTrackEventTimestamp(events),
		}
	}
	return projection, nil
}

func appendProjectionValue[T any](
	domain string,
	prefix *persistedPrefix,
	value T,
) error {
	if prefix == nil || len(prefix.Digest) != sha256.Size {
		return ErrRewindUnavailable
	}
	if prefix.Count == math.MaxUint64 {
		return ErrRewindUnavailable
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("encode projection prefix: %w", err)
	}
	h := sha256.New()
	if _, err := h.Write([]byte("trpc-agent-go:projection:item:v1")); err != nil {
		return err
	}
	writeLengthPrefixed(h, []byte(domain))
	if _, err := h.Write(prefix.Digest); err != nil {
		return err
	}
	writeLengthPrefixed(h, raw)
	prefix.Count++
	prefix.Digest = h.Sum(nil)
	return nil
}

func projectionSeed(domain string) []byte {
	h := sha256.New()
	_, _ = h.Write([]byte("trpc-agent-go:projection:seed:v1"))
	writeLengthPrefixed(h, []byte(domain))
	return h.Sum(nil)
}

func writeLengthPrefixed(h interface{ Write([]byte) (int, error) }, value []byte) {
	var length [8]byte
	binary.BigEndian.PutUint64(length[:], uint64(len(value)))
	_, _ = h.Write(length[:])
	_, _ = h.Write(value)
}

func trackDomain(track session.Track) string {
	return "track:" + string(track)
}

func clonePrefix(prefix persistedPrefix) persistedPrefix {
	return persistedPrefix{
		Count:        prefix.Count,
		Digest:       append([]byte(nil), prefix.Digest...),
		MaxTimestamp: cloneTimestamp(prefix.MaxTimestamp),
	}
}

func cloneTimestamp(timestamp *time.Time) *time.Time {
	if timestamp == nil {
		return nil
	}
	cloned := *timestamp
	return &cloned
}

func maxEventTimestamp(events []event.Event) *time.Time {
	if len(events) == 0 {
		return nil
	}
	var max time.Time
	for i := range events {
		if i == 0 || events[i].Timestamp.After(max) {
			max = events[i].Timestamp
		}
	}
	return &max
}

func maxTrackEventTimestamp(events []session.TrackEvent) *time.Time {
	if len(events) == 0 {
		return nil
	}
	var max time.Time
	for i := range events {
		if i == 0 || events[i].Timestamp.After(max) {
			max = events[i].Timestamp
		}
	}
	return &max
}

func validateProjection(projection *PersistedProjection) error {
	if projection == nil || projection.Version != persistedProjectionVersion ||
		len(projection.Events.Digest) != sha256.Size ||
		(projection.Events.Count > 0 && projection.Events.MaxTimestamp == nil) {
		return ErrRewindUnavailable
	}
	for _, prefix := range projection.Tracks {
		if len(prefix.Digest) != sha256.Size ||
			(prefix.Count > 0 && prefix.MaxTimestamp == nil) {
			return ErrRewindUnavailable
		}
	}
	return nil
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
