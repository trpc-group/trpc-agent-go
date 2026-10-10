//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
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
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

const (
	fieldSubAgentID = "subagent_id"
	fieldInput      = "input"
	fieldStatus     = "status"
	fieldOutput     = "output"
	fieldError      = "error"
	fieldRetryable  = "retryable"

	subAgentStatusCompleted = "completed"
	subAgentStatusFailed    = "failed"

	threadIDMinLen    = 8
	threadIDMaxLen    = 128
	threadIDRandBytes = 16

	threadNamespacePattern = `^[A-Za-z0-9_-]+$`

	// Saved subagent history is found by the exact key
	// agenttool:<namespace>:thread:<id>, so this layout is a persistence
	// contract and must not change.
	threadKeyPrefix = "agenttool:"
	threadKeyInfix  = ":thread:"

	subAgentToolDescriptionSuffix = "Omit subagent_id to start a new " +
		"subagent; pass a subagent_id returned by an earlier call to send " +
		"that subagent a follow-up that continues its conversation. Unknown " +
		"or invalid IDs fail. Put the wrapped agent's arguments in input."

	errNoParentSession   = "a parent session is required for resumable subagent calls"
	errUnknownSubAgentID = "unknown subagent_id: this session has no saved " +
		"history for it; omit subagent_id to start a new subagent"
	errGraphInterrupt = "the wrapped agent requested a graph interrupt, but " +
		"resumable subagents do not support graph checkpoint or interrupt resume"
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

type subAgentIDContextKey struct{}

// SubAgentResult is the result of one call to a NewTool configured with
// WithResumableSubAgents. Call returns it, StreamableCall emits it as the
// final result, and the model receives its JSON form.
type SubAgentResult struct {
	// SubAgentID identifies the subagent this call ran. Pass it as
	// subagent_id on a later call to continue that subagent. It is set only
	// when the parent Session holds saved events for the subagent when the
	// call returns, and is empty otherwise: for invalid or unknown IDs, for
	// failures before the call could run, and when a new subagent failed
	// before any of its events were saved.
	SubAgentID string `json:"subagent_id,omitempty"`
	// Status is "completed" when the wrapped agent finished this call and
	// "failed" otherwise. It describes this call only; a subagent whose call
	// completed can still be continued.
	Status string `json:"status"`
	// Output is the assistant text the wrapped agent produced in this call,
	// collected as WithResponseMode configures. It is always text, even when
	// the wrapped agent declares an object output schema, and it may be
	// empty when Status is "completed".
	Output string `json:"output,omitempty"`
	// Error describes the failure when Status is "failed".
	Error string `json:"error,omitempty"`
	// Retryable reports whether retrying the same call may succeed. It is nil
	// when that is unknown, which is the case for most wrapped agent
	// failures. Cancellation and deadlines are retryable, including when the
	// wrapped agent only closes its event stream because the context ended
	// and no normal completion was observed.
	Retryable *bool `json:"retryable,omitempty"`
}

// SubAgentIDFromContext returns the subagent ID of the nearest enclosing call
// to a NewTool configured with WithResumableSubAgents. It returns false when
// ctx is nil or is not inside such a call.
//
// The ID is visible to everything the wrapped agent runs, including nested
// agents and tools. A nested ordinary AgentTool still keeps its own history
// and never writes into the enclosing subagent's history, while a nested
// resumable AgentTool reports its own subagent ID for the duration of its
// call.
//
// Reading the ID restores nothing. The framework restores a subagent only
// through parent Session events, so an agent that keeps conversation state
// elsewhere, such as in a remote service or a provider-side conversation,
// must map this ID to that conversation itself.
func SubAgentIDFromContext(ctx context.Context) (string, bool) {
	if ctx == nil {
		return "", false
	}
	id, ok := ctx.Value(subAgentIDContextKey{}).(string)
	if !ok || id == "" {
		return "", false
	}
	return id, true
}

func contextWithSubAgentID(ctx context.Context, id string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, subAgentIDContextKey{}, id)
}

// resolveSubAgentConfig validates the resumable-subagent options and returns
// the namespace NewTool must use. It returns "" when resumable subagents are
// not enabled and panics on any invalid combination.
//
// NewTool must call it on the resolved option set before newFixedAgentTool.
// That constructor discards an enabled persistent-history option when
// HistoryScopeParentBranch is set and only logs a warning, so checking
// afterward would hide that conflict.
func resolveSubAgentConfig(agentName string, options *agentToolOptions) string {
	if !options.resumableSubAgents {
		if options.subAgentNamespace != nil {
			panic(fmt.Sprintf(
				"Invalid AgentTool configuration: AgentTool[%s]: "+
					"WithSubAgentNamespace requires WithResumableSubAgents",
				agentName,
			))
		}
		return ""
	}
	if conflicts := incompatibleSubAgentOptions(options); len(conflicts) > 0 {
		panic(fmt.Sprintf(
			"Invalid AgentTool configuration: AgentTool[%s]: "+
				"WithResumableSubAgents is incompatible with %s",
			agentName,
			strings.Join(conflicts, " and "),
		))
	}
	return resolveThreadNamespace(agentName, options.subAgentNamespace)
}

func incompatibleSubAgentOptions(options *agentToolOptions) []string {
	var conflicts []string
	if options.historyScope == HistoryScopeParentBranch {
		conflicts = append(conflicts, "HistoryScopeParentBranch")
	}
	if label := persistentHistoryConflict(options.persistentHistory); label != "" {
		conflicts = append(conflicts, label)
	}
	return conflicts
}

func persistentHistoryConflict(cfg *persistentHistoryOptions) string {
	if cfg == nil || !cfg.enabled {
		return ""
	}
	switch {
	case cfg.keyFunc != nil:
		return "WithPersistentHistoryKeyFunc"
	case cfg.key != "":
		return "WithPersistentHistoryKey"
	default:
		return "WithPersistentHistory"
	}
}

// resolveThreadNamespace validates the namespace segment of the history key.
// Invalid static configuration panics rather than being rewritten: any lossy
// rewrite, including trimming spaces, could map two distinct namespaces onto
// one key and silently merge their subagents.
func resolveThreadNamespace(agentName string, override *string) string {
	if override != nil {
		if !threadNamespaceRegexp.MatchString(*override) {
			panic(fmt.Sprintf(
				"Invalid AgentTool configuration: AgentTool[%s]: "+
					"subagent namespace %q must match %s",
				agentName, *override, threadNamespacePattern,
			))
		}
		return *override
	}
	if !threadNamespaceRegexp.MatchString(agentName) {
		panic(fmt.Sprintf(
			"Invalid AgentTool configuration: AgentTool[%s]: the wrapped "+
				"agent name cannot be used as the subagent namespace because "+
				"it does not match %s; rename the agent or pass "+
				"WithSubAgentNamespace",
			agentName, threadNamespacePattern,
		))
	}
	return agentName
}

// configureThreadTool installs the resumable-subagent protocol on a fixed
// Tool already built by newFixedAgentTool, using a namespace returned by
// resolveSubAgentConfig. It does not apply Option values again.
func configureThreadTool(at *Tool, namespace string) {
	at.thread = true
	at.threadNamespace = namespace
	at.threadLocks = newThreadLockSet()
	at.inputSchema = wrapSubAgentInputSchema(at.inputSchema)
	at.outputSchema = subAgentOutputSchema()
	if !strings.Contains(at.description, fieldSubAgentID) {
		at.description = strings.TrimSpace(
			at.description + " " + subAgentToolDescriptionSuffix,
		)
	}
}

func wrapSubAgentInputSchema(inner *tool.Schema) *tool.Schema {
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
	// Local references resolve from the document root. Rebase references to
	// the original root onto input, while keeping references to the hoisted
	// definitions at the envelope root. Work on a copy so an ordinary tool
	// wrapping the same agent keeps its original declaration.
	inner = copySubAgentInputSchema(inner)
	var defs map[string]*tool.Schema
	if len(inner.Defs) > 0 {
		defs = inner.Defs
		inner.Defs = nil
	}
	return &tool.Schema{
		Type:        "object",
		Description: "Call to a resumable subagent of the wrapped agent.",
		Properties: map[string]*tool.Schema{
			fieldSubAgentID: {
				Type: "string",
				Description: "ID returned by an earlier call of this tool in " +
					"this session. Omit it to start a new subagent. Must match " +
					threadIDPattern + ". Unknown IDs fail; they are never created.",
				Pattern: threadIDPattern,
			},
			fieldInput: inner,
		},
		Required: []string{fieldInput},
		Defs:     defs,
	}
}

func copySubAgentInputSchema(inner *tool.Schema) *tool.Schema {
	encoded, err := json.Marshal(inner)
	if err != nil {
		panic(fmt.Sprintf("Invalid AgentTool configuration: subagent input schema: %v", err))
	}
	var doc map[string]any
	if err := json.Unmarshal(encoded, &doc); err != nil {
		panic(fmt.Sprintf("Invalid AgentTool configuration: subagent input schema: %v", err))
	}
	rebaseSubAgentSchemaRefs(doc)
	return convertMapToToolSchema(doc)
}

// rebaseSubAgentSchemaRefs visits schema-valued keywords only. Values such
// as default and enum may contain ordinary data with a "$ref" member and
// must not be rewritten.
func rebaseSubAgentSchemaRefs(schema map[string]any) {
	if ref, ok := schema["$ref"].(string); ok {
		if (ref == "#" || strings.HasPrefix(ref, "#/")) &&
			ref != "#/$defs" && !strings.HasPrefix(ref, "#/$defs/") {
			schema["$ref"] = "#/properties/" + fieldInput + strings.TrimPrefix(ref, "#")
		}
	}
	for _, key := range []string{"properties", "$defs", "definitions", "patternProperties", "dependentSchemas"} {
		children, _ := schema[key].(map[string]any)
		for _, child := range children {
			if nested, ok := child.(map[string]any); ok {
				rebaseSubAgentSchemaRefs(nested)
			}
		}
	}
	for _, key := range []string{"items", "additionalProperties", "contains", "propertyNames", "not", "if", "then", "else", "unevaluatedItems", "unevaluatedProperties"} {
		if nested, ok := schema[key].(map[string]any); ok {
			rebaseSubAgentSchemaRefs(nested)
		}
	}
	for _, key := range []string{"allOf", "anyOf", "oneOf", "prefixItems", "items"} {
		children, _ := schema[key].([]any)
		for _, child := range children {
			if nested, ok := child.(map[string]any); ok {
				rebaseSubAgentSchemaRefs(nested)
			}
		}
	}
}

func subAgentOutputSchema() *tool.Schema {
	return &tool.Schema{
		Type:        "object",
		Description: "Result of one resumable subagent call.",
		Properties: map[string]*tool.Schema{
			fieldSubAgentID: {
				Type: "string",
				Description: "ID to pass as subagent_id on later calls to " +
					"continue this subagent. Present only when this session " +
					"has saved history for it.",
			},
			fieldStatus: {
				Type: "string",
				Description: "completed or failed. Describes this call only; " +
					"a completed subagent can still be continued.",
				Enum: []any{subAgentStatusCompleted, subAgentStatusFailed},
			},
			fieldOutput: {
				Type: "string",
				Description: "Assistant text the wrapped agent produced in " +
					"this call. Always text; omitted when empty.",
			},
			fieldError: {
				Type:        "string",
				Description: "Failure message when status is failed.",
			},
			fieldRetryable: {
				Type: "boolean",
				Description: "Whether retrying the same call may succeed. " +
					"Omitted when unknown.",
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

// executeThread runs one resumable subagent call. Every result carries a
// subagent ID only when confirmedSubAgentID finds saved events for it, so the
// result confirms the ID's current history, without promising future retention.
func (at *Tool) executeThread(ctx context.Context, jsonArgs []byte) SubAgentResult {
	id, input, err := parseSubAgentArgs(jsonArgs)
	if err != nil {
		return subAgentFailed("", err.Error(), boolPtr(false))
	}

	parentInv, ok := agent.InvocationFromContext(ctx)
	if !ok || parentInv == nil || parentInv.Session == nil {
		return subAgentFailed("", errNoParentSession, boolPtr(false))
	}
	if err := flush.Invoke(ctx, parentInv); err != nil {
		return subAgentFailed(
			"",
			fmt.Sprintf("flush parent invocation session: %v", err),
			boolPtr(true),
		)
	}
	parentInv = parentInvocationWithLiveSession(parentInv)
	if parentInv == nil || parentInv.Session == nil {
		return subAgentFailed("", errNoParentSession, boolPtr(false))
	}
	sess := parentInv.Session

	supplied := id != ""
	if !supplied {
		id, err = generateThreadID()
		if err != nil {
			return subAgentFailed("", err.Error(), boolPtr(true))
		}
	}
	childKey := threadFilterKey(at.threadNamespace, id)

	if at.threadLocks != nil {
		unlock, lockErr := at.threadLocks.acquire(ctx, threadLockKey(sess, id))
		if lockErr != nil {
			return subAgentFailed(
				confirmedSubAgentID(sess, childKey, id),
				fmt.Sprintf("wait for the previous call of this subagent: %v", lockErr),
				retryableForError(lockErr),
			)
		}
		defer unlock()
	}

	// Existence is decided under the lock so a concurrent first call for the
	// same ID cannot be observed half-established.
	if supplied && !sessionHasThreadEvents(sess, childKey) {
		return subAgentFailed("", errUnknownSubAgentID, boolPtr(false))
	}

	output, runErr := at.callWithParentInvocation(
		contextWithSubAgentID(ctx, id),
		parentInv,
		model.NewUserMessage(string(input)),
		nil,
		childKey,
	)
	resultID := confirmedSubAgentID(sess, childKey, id)
	if runErr != nil {
		// Covers both a directly returned interrupt and one that a graph agent
		// only signalled through an executor event (see
		// threadInterruptObserver). Rerunning cannot clear it, so the failure
		// is permanent for this call path.
		if graph.IsInterruptError(runErr) {
			return subAgentFailed(resultID, errGraphInterrupt, boolPtr(false))
		}
		return subAgentFailed(resultID, runErr.Error(), retryableForError(runErr))
	}
	return SubAgentResult{
		SubAgentID: resultID,
		Status:     subAgentStatusCompleted,
		Output:     output,
	}
}

// confirmedSubAgentID returns id only when sess holds at least one event under
// the subagent's history key. Those events are exactly what a later call
// checks before it continues the subagent.
func confirmedSubAgentID(sess *session.Session, filterKey, id string) string {
	if !sessionHasThreadEvents(sess, filterKey) {
		return ""
	}
	return id
}

// parseSubAgentArgs splits the tool arguments into the requested subagent ID
// and the raw wrapped-agent input. An omitted, null, or blank subagent_id
// yields an empty ID, which starts a new subagent. The input field must be
// present; its raw JSON, including null, is passed through unchanged.
func parseSubAgentArgs(jsonArgs []byte) (string, []byte, error) {
	if len(strings.TrimSpace(string(jsonArgs))) == 0 {
		return "", nil, fmt.Errorf("arguments are required")
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(jsonArgs, &raw); err != nil {
		return "", nil, fmt.Errorf("invalid arguments: %w", err)
	}
	var id string
	if rawID, ok := raw[fieldSubAgentID]; ok && !isJSONNull(rawID) {
		if err := json.Unmarshal(rawID, &id); err != nil {
			return "", nil, fmt.Errorf("invalid subagent_id: %w", err)
		}
		id = strings.TrimSpace(id)
	}
	input, ok := raw[fieldInput]
	if !ok {
		return "", nil, fmt.Errorf("input is required")
	}
	if id != "" {
		if err := validateThreadID(id); err != nil {
			return "", nil, err
		}
	}
	return id, []byte(input), nil
}

func isJSONNull(raw json.RawMessage) bool {
	return strings.TrimSpace(string(raw)) == "null"
}

func validateThreadID(id string) error {
	if id == "" {
		return fmt.Errorf("subagent_id is empty")
	}
	if strings.ContainsAny(id, "/:\\") || strings.Contains(id, "..") {
		return fmt.Errorf("subagent_id contains invalid characters")
	}
	if !threadIDRegexp.MatchString(id) {
		return fmt.Errorf("subagent_id must match %s", threadIDPattern)
	}
	return nil
}

func generateThreadID() (string, error) {
	var b [threadIDRandBytes]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("generate subagent id: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}

// threadFilterKey builds the stable subagent history key. The namespace is
// validated at construction time and the key deliberately contains no "/" so
// a subagent's history never falls under a parent's prefix or subtree filter.
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
// event under filterKey. This is the only existence signal: a subagent is
// exactly its saved events, with no separate marker or registry to keep in
// sync.
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
// The observer is only used by resumable subagent calls. It records the
// interrupt so the result can report a permanent failure; it never enables
// graph checkpoint or interrupt resume, and it does not change ordinary
// NewTool graph behavior.
//
// The signal is always available because a resumable subagent call enables
// graph executor events for the child run regardless of the caller's own
// setting; see childInvocationOptions.
//
// The same observer records a normal completion. LLMAgent completion is a
// non-partial final response (model.Response.IsFinalResponse); an empty
// assistant choice still counts, and a partial does not. Graph completion is
// the terminal graph.execution snapshot or its visible rewrite, including a
// snapshot with no assistant text. A context cancellation or deadline that
// closes the stream before either of those, and before an earlier error, is
// reported as ctx.Err. An interrupt still takes precedence.
type threadInterruptObserver struct {
	interrupt    *graph.InterruptError
	invocationID string
	completed    bool
}

// newThreadInterruptObserver returns an observer for a resumable subagent
// call, or nil for any other tool.
func (at *Tool) newThreadInterruptObserver(inv *agent.Invocation) *threadInterruptObserver {
	if !at.thread {
		return nil
	}
	return &threadInterruptObserver{invocationID: inv.InvocationID}
}

func (o *threadInterruptObserver) observe(evt *event.Event) {
	if o == nil || evt == nil {
		return
	}
	if o.interrupt == nil {
		if interrupt, _, ok := pregelStepInterrupt(evt); ok {
			o.interrupt = interrupt
		}
	}
	// A nested agent can finish while the wrapped agent is still running.
	// Its completion must not turn cancellation of the outer call into success.
	if !o.completed && evt.InvocationID == o.invocationID && threadEventNormallyCompleted(evt) {
		o.completed = true
	}
}

// threadEventNormallyCompleted reports a terminal success event. Error events
// are not completion: the collector returns them, and they must not hide a
// later cancellation when no successful terminal event was seen.
func threadEventNormallyCompleted(evt *event.Event) bool {
	if evt == nil || evt.Response == nil || evt.Response.Error != nil {
		return false
	}
	if isGraphCompletionSnapshotEvent(evt) {
		return true
	}
	return evt.IsFinalResponse()
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

// canceledWithoutCompletion returns ctx.Err when the child stream ended
// because the context was canceled or its deadline was exceeded and no normal
// completion was observed. The caller must already have preferred collector
// errors and graph interrupts.
func (o *threadInterruptObserver) canceledWithoutCompletion(ctx context.Context) error {
	if o == nil || o.completed || ctx == nil {
		return nil
	}
	err := ctx.Err()
	if err == nil {
		return nil
	}
	return fmt.Errorf("wrapped agent run canceled: %w", err)
}

// threadChildRuntimeState copies parent runtime state for a resumable
// subagent and drops graph checkpoint and interrupt-resume keys.
//
// Invocation.Clone shares RunOptions.RuntimeState with the parent. GraphAgent
// merges that map into the child execution state, treats the presence of
// checkpoint_id (even when empty) as a resume request, and graph.Interrupt
// consumes command and resume values from the same map. Copying the top-level
// map keeps filtering from modifying the parent. Business values retain their
// existing ownership and sharing; this is not deep state isolation. A nil
// result means the child inherits no runtime state.
// RunOptions.Resume is not stored in this map and is left unchanged.
func threadChildRuntimeState(parent map[string]any) map[string]any {
	if len(parent) == 0 {
		return nil
	}
	child := make(map[string]any, len(parent))
	for key, value := range parent {
		if threadParentExecutionStateKey(key) {
			continue
		}
		child[key] = value
	}
	if len(child) == 0 {
		return nil
	}
	return child
}

// threadParentExecutionStateKey reports runtime-state keys that resume a
// parent graph execution. Command covers both *graph.Command and
// *graph.ResumeCommand because both are stored under StateKeyCommand.
func threadParentExecutionStateKey(key string) bool {
	switch key {
	case graph.CfgKeyLineageID,
		graph.CfgKeyCheckpointID,
		graph.CfgKeyCheckpointNS,
		graph.StateKeyCommand,
		graph.ResumeChannel,
		graph.StateKeyResumeMap,
		graph.StateKeyUsedInterrupts,
		graph.StateKeySubgraphInterrupt:
		return true
	default:
		return false
	}
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

func subAgentFailed(id, msg string, retryable *bool) SubAgentResult {
	return SubAgentResult{
		SubAgentID: id,
		Status:     subAgentStatusFailed,
		Error:      msg,
		Retryable:  retryable,
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
