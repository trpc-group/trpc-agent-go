//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package agent

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/graph"
	"trpc.group/trpc-go/trpc-agent-go/internal/state/flush"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

const (
	fieldThreadID  = "thread_id"
	fieldInput     = "input"
	fieldStatus    = "status"
	fieldOutput    = "output"
	fieldError     = "error"
	fieldRetryable = "retryable"

	// ThreadStatusCompleted is returned when the child agent finished.
	ThreadStatusCompleted = "completed"
	// ThreadStatusFailed is returned when the thread call cannot produce a
	// child result.
	ThreadStatusFailed = "failed"

	threadIDMinLen    = 8
	threadIDMaxLen    = 128
	threadIDRandBytes = 16

	threadNamespacePattern = `^[A-Za-z0-9_-]+$`

	threadKeyPrefix = "agenttool:"
	threadKeyInfix  = ":thread:"

	threadToolDescriptionSuffix = "Omit thread_id to start a new conversation " +
		"branch; pass a previously returned thread_id to append a new message " +
		"to that branch. Unknown or invalid thread IDs fail. Put the wrapped " +
		"agent's arguments in input."

	errNoParentSession = "parent session is required for threaded agent tool calls"
	errUnknownThreadID = "unknown thread_id"
	errGraphInterrupt  = "the wrapped agent requested a graph interrupt, but " +
		"NewThreadTool does not support graph checkpoint or interrupt resume"
)

// threadIDPattern is derived from the length bounds so the schema, the
// validator, and the model-facing error message can never disagree.
var (
	threadIDPattern = fmt.Sprintf(
		`^[A-Za-z0-9_-]{%d,%d}$`, threadIDMinLen, threadIDMaxLen,
	)
	threadIDRegexp        = regexp.MustCompile(threadIDPattern)
	threadNamespaceRegexp = regexp.MustCompile(threadNamespacePattern)
)

type threadIDContextKey struct{}

// ThreadResult is the model-facing envelope returned by NewThreadTool.
type ThreadResult struct {
	// ThreadID is the opaque ID of this conversation branch. Pass it on later
	// calls to append to the same branch. It is empty when a new branch could
	// not be established, which happens when the call failed before the child
	// agent persisted any event.
	ThreadID string `json:"thread_id,omitempty"`
	// Status is ThreadStatusCompleted or ThreadStatusFailed.
	Status string `json:"status"`
	// Output is the wrapped agent's collected assistant text when status is
	// completed.
	Output any `json:"output,omitempty"`
	// Error is the model-facing failure message when status is failed.
	Error string `json:"error,omitempty"`
	// Retryable reports whether the caller may retry this call. It is omitted
	// when the tool cannot tell a transient fault from a permanent one, which
	// is the case for most child agent failures.
	Retryable *bool `json:"retryable,omitempty"`
}

// ThreadIDFromContext returns the thread ID of the nearest enclosing
// NewThreadTool invocation, if the current run was started by one.
//
// The value propagates to everything the wrapped agent runs, including nested
// agents and tools, so it identifies the enclosing branch rather than the
// immediate caller.
//
// Reading this ID does not restore a conversation. Full restoration still
// requires the wrapped agent to consume framework Session history or to map
// this ID to a provider conversation ID. Wrapped agent implementations must be
// concurrency-safe and must not keep conversation-local state across calls:
// different thread IDs may run concurrently.
func ThreadIDFromContext(ctx context.Context) (string, bool) {
	if ctx == nil {
		return "", false
	}
	id, ok := ctx.Value(threadIDContextKey{}).(string)
	if !ok || id == "" {
		return "", false
	}
	return id, true
}

func contextWithThreadID(ctx context.Context, id string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, threadIDContextKey{}, id)
}

// NewThreadTool wraps a fixed agent as an opt-in conversation-branch tool.
// Existing NewTool and NewDynamicTool schemas and runtime behavior are
// unchanged.
//
// The wrapped agent gets multiple independently addressed conversation
// branches inside the parent Session. Each branch is exactly the set of parent
// Session events carrying a stable event-filter key
// (agenttool:<namespace>:thread:<opaque-id>). Calls without thread_id start a
// new branch with a cryptographically strong opaque ID; calls with thread_id
// append a new message to that branch.
//
// NewThreadTool is synchronous call-return. It adds no Controller, message
// queue, lease, background task, executor checkpointing, separate child
// Session, or Graph resume/checkpoint behavior.
//
// Guarantees and limits:
//
//   - Continuity across turns and processes is limited by successful parent
//     Session persistence and by the parent Session's event retention window.
//     That window is shared by the parent conversation and every branch, so a
//     long parent Session can push older branch events out of retrieval.
//   - Filter keys isolate event history, not Session State. Every branch still
//     shares the parent Session's State map; a wrapped agent that stores
//     conversation-local state must namespace its keys by thread ID itself.
//   - Branches have no independent lifecycle: no TTL, list, or delete. They
//     live and die with the parent Session.
//   - Appending is the only turn semantic. There is no resume flag, no
//     replacement of an interrupted turn, and no checkpoint recovery. If a
//     previous turn was interrupted, its partial tail stays in the branch; the
//     framework's orphan tool-call sanitization keeps the next request valid.
//   - Different thread IDs may run concurrently. Same-thread calls are
//     serialized by a per-Tool in-process guard only; there is no cross-node
//     mutual exclusion.
//   - The parent invocation Session is required. NewThreadTool never falls
//     back to an isolated in-memory runner.
//   - output.output is always a string: the child call path collects assistant
//     text and cannot preserve a custom agent OutputSchema type.
//
// Supplied IDs are strictly validated against ^[A-Za-z0-9_-]{8,128}$ so they
// cannot inject filter-key path separators. An unknown thread ID fails instead
// of silently creating a branch. A branch is known only when the parent
// Session holds at least one event under its filter key, so a call that fails
// before the child agent persists anything returns no thread_id at all and the
// caller simply starts a new branch.
//
// CallWithAgentToolGraphRuntime uses this envelope path and does not
// participate in graph checkpoint resume.
//
// A child graph interrupt is reported as a failed envelope with
// retryable=false, whether the wrapped agent returns the interrupt as an error
// or only signals it through a graph executor event. Because the event-signalled
// form is only observable through those events, a thread call always enables
// them for the child run; when the caller disabled them, they are observed
// internally and kept out of the shared Session.
func NewThreadTool(wrapped agent.Agent, opts ...Option) *Tool {
	// Options are applied once and the resolved set is shared with the common
	// fixed-agent constructor, because an application-defined Option may have
	// side effects and must not run twice per constructor call.
	options := applyAgentToolOptions(opts)
	at := newFixedAgentTool(wrapped, options)
	at.thread = true
	// Branch identity tracks the wrapped agent, not the model-facing tool name.
	at.threadNamespace = resolveThreadNamespace(
		wrapped.Info().Name, options.threadNamespace,
	)
	at.threadLocks = newThreadLockSet()
	if at.historyScope == HistoryScopeParentBranch {
		log.Warnf(
			"AgentTool[%s]: HistoryScopeParentBranch is ignored by NewThreadTool; "+
				"threads use isolated filter keys",
			at.name,
		)
		at.historyScope = HistoryScopeIsolated
	}
	if at.persistentHistory != nil {
		log.Warnf(
			"AgentTool[%s]: WithPersistentHistory* is ignored by NewThreadTool; "+
				"threads use per-call thread IDs",
			at.name,
		)
		at.persistentHistory = nil
	}
	at.inputSchema = wrapThreadInputSchema(at.inputSchema)
	// callWithParentInvocation collects child assistant text as a string, so
	// the envelope output field is always a string even when the wrapped
	// agent declares an object OutputSchema.
	at.outputSchema = wrapThreadOutputSchema()
	if !strings.Contains(at.description, fieldThreadID) {
		at.description = strings.TrimSpace(
			at.description + " " + threadToolDescriptionSuffix,
		)
	}
	return at
}

// resolveThreadNamespace validates the thread key namespace at construction
// time. Invalid static configuration panics rather than being rewritten,
// because any lossy rewrite could map two distinct agents onto one namespace
// and silently merge their branches.
func resolveThreadNamespace(agentName string, override *string) string {
	if override != nil {
		namespace := strings.TrimSpace(*override)
		if !threadNamespaceRegexp.MatchString(namespace) {
			panic(fmt.Sprintf(
				"Invalid Thread AgentTool configuration: thread namespace %q "+
					"must match %s",
				namespace, threadNamespacePattern,
			))
		}
		return namespace
	}
	if !threadNamespaceRegexp.MatchString(agentName) {
		panic(fmt.Sprintf(
			"Invalid Thread AgentTool configuration: wrapped agent name %q "+
				"cannot be used as a thread namespace because it does not "+
				"match %s; rename the agent or pass WithThreadNamespace",
			agentName, threadNamespacePattern,
		))
	}
	return agentName
}

func wrapThreadInputSchema(inner *tool.Schema) *tool.Schema {
	if inner == nil {
		inner = &tool.Schema{
			Type:        "object",
			Description: "Input for the agent tool",
			Properties: map[string]*tool.Schema{
				"request": {
					Type:        "string",
					Description: "The request to send to the agent",
				},
			},
			Required: []string{"request"},
		}
	}
	return &tool.Schema{
		Type:        "object",
		Description: "Threaded invocation of the wrapped agent.",
		Properties: map[string]*tool.Schema{
			fieldThreadID: {
				Type: "string",
				Description: "Opaque ID of an existing conversation branch in " +
					"this session. Omit to start a new branch. Must match " +
					threadIDPattern + ". Unknown IDs fail; they are not created.",
				Pattern: threadIDPattern,
			},
			fieldInput: inner,
		},
		Required: []string{fieldInput},
	}
}

func wrapThreadOutputSchema() *tool.Schema {
	return &tool.Schema{
		Type:        "object",
		Description: "Result of a threaded agent-tool call.",
		Properties: map[string]*tool.Schema{
			fieldThreadID: {
				Type: "string",
				Description: "Opaque thread ID to pass on later calls. Present " +
					"once the branch holds at least one persisted event; absent " +
					"when a new branch could not be established.",
			},
			fieldStatus: {
				Type:        "string",
				Description: "completed or failed",
				Enum:        []any{ThreadStatusCompleted, ThreadStatusFailed},
			},
			fieldOutput: {
				Type: "string",
				Description: "The wrapped agent's collected assistant text. " +
					"Always a string: NewThreadTool collects child events as text " +
					"and does not preserve a custom OutputSchema type.",
			},
			fieldError: {
				Type:        "string",
				Description: "Error message when status is failed.",
			},
			fieldRetryable: {
				Type: "boolean",
				Description: "Whether the caller may retry this call. Omitted " +
					"when retryability is unknown.",
			},
		},
		Required: []string{fieldStatus},
	}
}

func (at *Tool) callThread(ctx context.Context, jsonArgs []byte) (any, error) {
	return at.executeThread(ctx, jsonArgs), nil
}

func (at *Tool) streamThread(
	ctx context.Context,
	jsonArgs []byte,
	writer *tool.StreamWriter,
) {
	result := at.executeThread(ctx, jsonArgs)
	_ = writer.Send(tool.StreamChunk{
		Content: tool.FinalResultChunk{Result: result},
	}, nil)
}

func (at *Tool) executeThread(ctx context.Context, jsonArgs []byte) ThreadResult {
	threadID, input, err := parseThreadArgs(jsonArgs)
	if err != nil {
		return threadFailed(threadID, err.Error(), boolPtr(false))
	}
	if threadID != "" {
		if err := validateThreadID(threadID); err != nil {
			return threadFailed(threadID, err.Error(), boolPtr(false))
		}
	}

	parentInv, ok := agent.InvocationFromContext(ctx)
	if !ok || parentInv == nil || parentInv.Session == nil {
		return threadFailed(threadID, errNoParentSession, boolPtr(false))
	}

	if err := flush.Invoke(ctx, parentInv); err != nil {
		return threadFailed(
			threadID,
			fmt.Sprintf("flush parent invocation session: %v", err),
			boolPtr(true),
		)
	}
	parentInv = parentInvocationWithLiveSession(parentInv)
	if parentInv == nil || parentInv.Session == nil {
		return threadFailed(threadID, errNoParentSession, boolPtr(false))
	}

	supplied := threadID != ""
	if !supplied {
		threadID, err = generateThreadID()
		if err != nil {
			return threadFailed("", err.Error(), boolPtr(true))
		}
	}
	childKey := threadFilterKey(at.threadNamespace, threadID)

	if at.threadLocks != nil {
		unlock, lockErr := at.threadLocks.acquire(
			ctx,
			threadLockKey(parentInv.Session, threadID),
		)
		if lockErr != nil {
			return threadFailed(
				suppliedThreadID(supplied, threadID),
				fmt.Sprintf("acquire thread lock: %v", lockErr),
				retryableForError(lockErr),
			)
		}
		defer unlock()
	}

	// Existence is decided under the lock so a concurrent first call for the
	// same ID cannot be observed half-established.
	if supplied && !sessionHasThreadEvents(parentInv.Session, childKey) {
		return threadFailed(threadID, errUnknownThreadID, boolPtr(false))
	}

	runCtx := contextWithThreadID(ctx, threadID)
	message := model.NewUserMessage(string(input))
	output, runErr := at.callWithParentInvocation(
		runCtx, parentInv, message, nil, childKey,
	)

	// A thread ID is only meaningful once the branch holds a persisted event,
	// because that is exactly what a later call looks for. Anything else would
	// hand the model an ID that can never be continued.
	established := sessionHasThreadEvents(parentInv.Session, childKey)
	resultID := threadID
	if !established {
		resultID = ""
	}
	if runErr != nil {
		// Covers both a directly returned interrupt and one that a graph agent
		// only signalled through an executor event (see
		// threadInterruptObserver). Rerunning cannot clear it, so the failure
		// is permanent for this call path.
		if graph.IsInterruptError(runErr) {
			return threadFailed(resultID, errGraphInterrupt, boolPtr(false))
		}
		return threadFailed(resultID, runErr.Error(), retryableForError(runErr))
	}
	return threadCompleted(resultID, output)
}

// suppliedThreadID echoes a caller-supplied ID back on failure so the model can
// correlate the result, while never echoing a generated ID that was not
// established.
func suppliedThreadID(supplied bool, threadID string) string {
	if supplied {
		return threadID
	}
	return ""
}

func parseThreadArgs(jsonArgs []byte) (string, []byte, error) {
	if len(strings.TrimSpace(string(jsonArgs))) == 0 {
		return "", nil, fmt.Errorf("thread tool arguments are required")
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(jsonArgs, &raw); err != nil {
		return "", nil, fmt.Errorf("invalid thread tool arguments: %w", err)
	}
	var threadID string
	if tid, ok := raw[fieldThreadID]; ok && !isJSONNull(tid) {
		if err := json.Unmarshal(tid, &threadID); err != nil {
			return "", nil, fmt.Errorf("invalid thread_id: %w", err)
		}
		threadID = strings.TrimSpace(threadID)
	}
	input, ok := raw[fieldInput]
	if !ok || isJSONNull(input) {
		return threadID, nil, fmt.Errorf("input is required")
	}
	return threadID, []byte(input), nil
}

func isJSONNull(raw json.RawMessage) bool {
	return strings.TrimSpace(string(raw)) == "null"
}

func validateThreadID(id string) error {
	if id == "" {
		return fmt.Errorf("thread_id is empty")
	}
	if strings.ContainsAny(id, "/:\\") || strings.Contains(id, "..") {
		return fmt.Errorf("thread_id contains invalid characters")
	}
	if !threadIDRegexp.MatchString(id) {
		return fmt.Errorf("thread_id must match %s", threadIDPattern)
	}
	return nil
}

func generateThreadID() (string, error) {
	var b [threadIDRandBytes]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("generate thread id: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}

// threadFilterKey builds the stable branch event-filter key. The namespace is
// validated at construction time and the key deliberately contains no "/" so
// the branch never falls under a parent's prefix subtree filter.
func threadFilterKey(namespace, threadID string) string {
	return threadKeyPrefix + namespace + threadKeyInfix + threadID
}

func threadLockKey(sess *session.Session, threadID string) string {
	if sess == nil {
		return threadID
	}
	return sess.AppName + "\x00" + sess.UserID + "\x00" + sess.ID + "\x00" + threadID
}

// sessionHasThreadEvents reports whether the parent session holds at least one
// event for the branch. This is the only existence signal: a branch is exactly
// its persisted events, with no separate marker or registry to keep in sync.
func sessionHasThreadEvents(sess *session.Session, filterKey string) bool {
	if sess == nil || filterKey == "" {
		return false
	}
	sess.EventMu.RLock()
	defer sess.EventMu.RUnlock()
	for i := range sess.Events {
		if sess.Events[i].FilterKey == filterKey {
			return true
		}
	}
	return false
}

// threadInterruptObserver detects a child graph interrupt that is signalled
// through graph executor events instead of a returned error.
//
// A graph agent reports an interrupt by emitting a pregel step event carrying
// the interrupt value and then closing its event channel without an error, so
// the child call returns normally. Without this observer such a run would be
// reported as completed even though the child produced no answer.
//
// The observer is thread-only. It records the interrupt so the envelope can
// report a permanent failure; it never enables graph checkpoint or interrupt
// resume, and it does not change ordinary NewTool graph behavior.
//
// The signal is always available because a thread call enables graph executor
// events for the child run regardless of the caller's own setting; see
// childInvocationOptions.
type threadInterruptObserver struct {
	interrupt *graph.InterruptError
}

// newThreadInterruptObserver returns an observer for a threaded call, or nil
// when this tool is not a thread tool.
func (at *Tool) newThreadInterruptObserver() *threadInterruptObserver {
	if !at.thread {
		return nil
	}
	return &threadInterruptObserver{}
}

func (o *threadInterruptObserver) observe(evt *event.Event) {
	if o == nil || o.interrupt != nil {
		return
	}
	if interrupt, _, ok := pregelStepInterrupt(evt); ok {
		o.interrupt = interrupt
	}
}

// interruptError returns the first observed interrupt as an error that
// graph.IsInterruptError recognizes, or nil when no interrupt was signalled.
// It must be called only after the observed event channel is closed.
func (o *threadInterruptObserver) interruptError() error {
	if o == nil || o.interrupt == nil {
		return nil
	}
	return o.interrupt
}

func boolPtr(v bool) *bool { return &v }

// retryableForError classifies a failure that is not a known framework
// validation error. Cancellation and deadlines are transient, so retrying can
// succeed. Anything else is reported as unknown rather than guessed, because
// the tool cannot distinguish a transient provider fault from a deterministic
// child failure that would loop forever.
func retryableForError(err error) *bool {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) ||
		errors.Is(err, context.DeadlineExceeded) {
		return boolPtr(true)
	}
	return nil
}

func threadCompleted(id string, output any) ThreadResult {
	return ThreadResult{
		ThreadID: id,
		Status:   ThreadStatusCompleted,
		Output:   output,
	}
}

func threadFailed(id, msg string, retryable *bool) ThreadResult {
	return ThreadResult{
		ThreadID:  id,
		Status:    ThreadStatusFailed,
		Error:     msg,
		Retryable: retryable,
	}
}

type threadLockSet struct {
	mu    sync.Mutex
	locks map[string]*threadLock
}

type threadLock struct {
	token chan struct{}
	refs  int
}

func newThreadLockSet() *threadLockSet {
	return &threadLockSet{locks: make(map[string]*threadLock)}
}

func (s *threadLockSet) acquire(ctx context.Context, key string) (func(), error) {
	if ctx == nil {
		ctx = context.Background()
	}
	s.mu.Lock()
	l := s.locks[key]
	if l == nil {
		l = &threadLock{token: make(chan struct{}, 1)}
		l.token <- struct{}{}
		s.locks[key] = l
	}
	l.refs++
	s.mu.Unlock()

	var once sync.Once
	release := func() {
		once.Do(func() {
			l.token <- struct{}{}
			s.mu.Lock()
			l.refs--
			if l.refs == 0 {
				delete(s.locks, key)
			}
			s.mu.Unlock()
		})
	}

	select {
	case <-ctx.Done():
		s.mu.Lock()
		l.refs--
		if l.refs == 0 {
			delete(s.locks, key)
		}
		s.mu.Unlock()
		return nil, ctx.Err()
	case <-l.token:
		return release, nil
	}
}
