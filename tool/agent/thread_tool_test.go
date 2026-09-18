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
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	coreagent "trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/graphagent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/graph"
	"trpc.group/trpc-go/trpc-agent-go/internal/agenttoolgraph"
	"trpc.group/trpc-go/trpc-agent-go/internal/flow/processor"
	"trpc.group/trpc-go/trpc-agent-go/internal/state/appender"
	agentlog "trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	sessioninmemory "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// eventuallyTimeout bounds the few tests that must observe a goroutine parking
// on the per-thread lock. Nothing in the production path polls.
const eventuallyTimeout = 5 * time.Second

// threadLockWaiters reports how many callers currently hold or wait on the
// per-thread lock entry for key. Only tests need this view, so it is not part
// of the production lock set.
func threadLockWaiters(locks *threadLockSet, key string) int {
	locks.mu.Lock()
	defer locks.mu.Unlock()
	if l := locks.locks[key]; l != nil {
		return l.refs
	}
	return 0
}

func singleAssistantEvent(content string) <-chan *event.Event {
	ch := make(chan *event.Event, 1)
	ch <- &event.Event{
		Response: &model.Response{
			Done: true,
			Choices: []model.Choice{{
				Index:   0,
				Message: model.NewAssistantMessage(content),
			}},
		},
	}
	close(ch)
	return ch
}

type threadHistoryAgent struct {
	name string

	mu         sync.Mutex
	call       int
	seenKeys   []string
	seenIDs    []string
	seenInputs []string
}

func (a *threadHistoryAgent) Run(
	ctx context.Context,
	inv *coreagent.Invocation,
) (<-chan *event.Event, error) {
	a.mu.Lock()
	a.call++
	call := a.call
	if inv != nil {
		a.seenKeys = append(a.seenKeys, inv.GetEventFilterKey())
		a.seenInputs = append(a.seenInputs, inv.Message.Content)
	}
	if id, ok := ThreadIDFromContext(ctx); ok {
		a.seenIDs = append(a.seenIDs, id)
	} else {
		a.seenIDs = append(a.seenIDs, "")
	}
	fk := ""
	if inv != nil {
		fk = inv.GetEventFilterKey()
	}
	var prev []string
	if inv != nil && inv.Session != nil {
		inv.Session.EventMu.RLock()
		for _, evt := range inv.Session.Events {
			if evt.FilterKey != fk || evt.Response == nil || len(evt.Response.Choices) == 0 {
				continue
			}
			msg := evt.Response.Choices[0].Message
			if msg.Role == model.RoleAssistant && msg.Content != "" {
				prev = append(prev, msg.Content)
			}
		}
		inv.Session.EventMu.RUnlock()
	}
	a.mu.Unlock()

	content := fmt.Sprintf("run%d", call)
	if len(prev) > 0 {
		content = strings.Join(prev, "|") + "|" + content
	}
	return singleAssistantEvent(content), nil
}

func (a *threadHistoryAgent) Tools() []tool.Tool { return nil }
func (a *threadHistoryAgent) Info() coreagent.Info {
	return coreagent.Info{Name: a.name, Description: "thread-history-test"}
}
func (a *threadHistoryAgent) SubAgents() []coreagent.Agent        { return nil }
func (a *threadHistoryAgent) FindSubAgent(string) coreagent.Agent { return nil }

func (a *threadHistoryAgent) snapshot() (keys, ids, inputs []string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]string(nil), a.seenKeys...),
		append([]string(nil), a.seenIDs...),
		append([]string(nil), a.seenInputs...)
}

type threadSchemaAgent struct {
	name        string
	inputSchema map[string]any
	lastMessage string
	lastThread  string
	lastKey     string
}

func (a *threadSchemaAgent) Run(
	ctx context.Context,
	inv *coreagent.Invocation,
) (<-chan *event.Event, error) {
	if inv != nil {
		a.lastMessage = inv.Message.Content
		a.lastKey = inv.GetEventFilterKey()
	}
	a.lastThread, _ = ThreadIDFromContext(ctx)
	return singleAssistantEvent("schema-ok"), nil
}

func (a *threadSchemaAgent) Tools() []tool.Tool { return nil }
func (a *threadSchemaAgent) Info() coreagent.Info {
	return coreagent.Info{
		Name:        a.name,
		Description: "thread-schema-test",
		InputSchema: a.inputSchema,
	}
}
func (a *threadSchemaAgent) SubAgents() []coreagent.Agent        { return nil }
func (a *threadSchemaAgent) FindSubAgent(string) coreagent.Agent { return nil }

type threadObjectOutputAgent struct {
	name        string
	lastMessage string
}

func (a *threadObjectOutputAgent) Run(
	_ context.Context,
	inv *coreagent.Invocation,
) (<-chan *event.Event, error) {
	if inv != nil {
		a.lastMessage = inv.Message.Content
	}
	return singleAssistantEvent("object-child-text"), nil
}

func (a *threadObjectOutputAgent) Tools() []tool.Tool { return nil }
func (a *threadObjectOutputAgent) Info() coreagent.Info {
	return coreagent.Info{
		Name:        a.name,
		Description: "thread-object-output-test",
		OutputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"answer": map[string]any{"type": "string"},
			},
		},
	}
}
func (a *threadObjectOutputAgent) SubAgents() []coreagent.Agent        { return nil }
func (a *threadObjectOutputAgent) FindSubAgent(string) coreagent.Agent { return nil }

// threadFailOnceAgent fails its first Run before returning an event channel, so
// nothing is persisted for that attempt.
type threadFailOnceAgent struct {
	name string

	mu          sync.Mutex
	runMessages []string
}

func (a *threadFailOnceAgent) Run(
	_ context.Context,
	inv *coreagent.Invocation,
) (<-chan *event.Event, error) {
	msg := ""
	if inv != nil {
		msg = inv.Message.Content
	}
	a.mu.Lock()
	a.runMessages = append(a.runMessages, msg)
	n := len(a.runMessages)
	a.mu.Unlock()
	if n == 1 {
		return nil, fmt.Errorf("first run failed")
	}
	return singleAssistantEvent("retry-ok"), nil
}

func (a *threadFailOnceAgent) Tools() []tool.Tool { return nil }
func (a *threadFailOnceAgent) Info() coreagent.Info {
	return coreagent.Info{Name: a.name, Description: "thread-fail-once-test"}
}
func (a *threadFailOnceAgent) SubAgents() []coreagent.Agent        { return nil }
func (a *threadFailOnceAgent) FindSubAgent(string) coreagent.Agent { return nil }

func (a *threadFailOnceAgent) messages() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]string(nil), a.runMessages...)
}

// threadErrorAgent starts its stream and then reports an error, so the branch
// user event is already persisted when the call fails.
type threadErrorAgent struct{ name string }

func (a *threadErrorAgent) Run(
	_ context.Context,
	_ *coreagent.Invocation,
) (<-chan *event.Event, error) {
	ch := make(chan *event.Event, 1)
	ch <- &event.Event{
		Response: &model.Response{
			Error: &model.ResponseError{Message: "child failed"},
		},
	}
	close(ch)
	return ch, nil
}

func (a *threadErrorAgent) Tools() []tool.Tool { return nil }
func (a *threadErrorAgent) Info() coreagent.Info {
	return coreagent.Info{Name: a.name, Description: "thread-error-test"}
}
func (a *threadErrorAgent) SubAgents() []coreagent.Agent        { return nil }
func (a *threadErrorAgent) FindSubAgent(string) coreagent.Agent { return nil }

// threadRunErrorAgent fails before any event with a caller-supplied error.
type threadRunErrorAgent struct {
	name string
	err  error
}

func (a *threadRunErrorAgent) Run(
	_ context.Context,
	_ *coreagent.Invocation,
) (<-chan *event.Event, error) {
	return nil, a.err
}

func (a *threadRunErrorAgent) Tools() []tool.Tool { return nil }
func (a *threadRunErrorAgent) Info() coreagent.Info {
	return coreagent.Info{Name: a.name, Description: "thread-run-error-test"}
}
func (a *threadRunErrorAgent) SubAgents() []coreagent.Agent        { return nil }
func (a *threadRunErrorAgent) FindSubAgent(string) coreagent.Agent { return nil }

// threadGateAgent records concurrent Run overlap and can park inside Run.
type threadGateAgent struct {
	name    string
	entered chan struct{}

	mu        sync.Mutex
	gate      chan struct{}
	runs      int
	active    int
	maxActive int
}

func (a *threadGateAgent) Run(
	_ context.Context,
	_ *coreagent.Invocation,
) (<-chan *event.Event, error) {
	a.mu.Lock()
	a.runs++
	a.active++
	if a.active > a.maxActive {
		a.maxActive = a.active
	}
	gate := a.gate
	a.mu.Unlock()

	select {
	case a.entered <- struct{}{}:
	default:
	}
	if gate != nil {
		<-gate
	}

	a.mu.Lock()
	a.active--
	a.mu.Unlock()
	return singleAssistantEvent("gate-ok"), nil
}

func (a *threadGateAgent) Tools() []tool.Tool { return nil }
func (a *threadGateAgent) Info() coreagent.Info {
	return coreagent.Info{Name: a.name, Description: "thread-gate-test"}
}
func (a *threadGateAgent) SubAgents() []coreagent.Agent        { return nil }
func (a *threadGateAgent) FindSubAgent(string) coreagent.Agent { return nil }

func (a *threadGateAgent) setGate(gate chan struct{}) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.gate = gate
}

func (a *threadGateAgent) stats() (runs, maxActive int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.runs, a.maxActive
}

// threadKeyRecordingAgent records the event filter key it was given.
type threadKeyRecordingAgent struct {
	name string

	mu   sync.Mutex
	keys []string
}

func (a *threadKeyRecordingAgent) Run(
	_ context.Context,
	inv *coreagent.Invocation,
) (<-chan *event.Event, error) {
	a.mu.Lock()
	if inv != nil {
		a.keys = append(a.keys, inv.GetEventFilterKey())
	}
	a.mu.Unlock()
	return singleAssistantEvent("nested-ok"), nil
}

func (a *threadKeyRecordingAgent) Tools() []tool.Tool { return nil }
func (a *threadKeyRecordingAgent) Info() coreagent.Info {
	return coreagent.Info{Name: a.name, Description: "thread-nested-test"}
}
func (a *threadKeyRecordingAgent) SubAgents() []coreagent.Agent        { return nil }
func (a *threadKeyRecordingAgent) FindSubAgent(string) coreagent.Agent { return nil }

func (a *threadKeyRecordingAgent) filterKeys() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]string(nil), a.keys...)
}

// threadNestingAgent calls an ordinary NewTool AgentTool with its own child
// context, the way an LLM flow does when the wrapped agent has agent tools.
type threadNestingAgent struct {
	name   string
	nested *Tool

	mu      sync.Mutex
	ownKeys []string
	callErr error
}

func (a *threadNestingAgent) Run(
	ctx context.Context,
	inv *coreagent.Invocation,
) (<-chan *event.Event, error) {
	a.mu.Lock()
	if inv != nil {
		a.ownKeys = append(a.ownKeys, inv.GetEventFilterKey())
	}
	a.mu.Unlock()

	_, err := a.nested.Call(ctx, []byte(`{"request":"nested"}`))
	a.mu.Lock()
	a.callErr = err
	a.mu.Unlock()
	return singleAssistantEvent("outer-ok"), nil
}

func (a *threadNestingAgent) Tools() []tool.Tool { return nil }
func (a *threadNestingAgent) Info() coreagent.Info {
	return coreagent.Info{Name: a.name, Description: "thread-nesting-test"}
}
func (a *threadNestingAgent) SubAgents() []coreagent.Agent        { return nil }
func (a *threadNestingAgent) FindSubAgent(string) coreagent.Agent { return nil }

func (a *threadNestingAgent) snapshot() (ownKeys []string, callErr error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]string(nil), a.ownKeys...), a.callErr
}

func newThreadParent(t *testing.T) (context.Context, *session.Session, *coreagent.Invocation) {
	t.Helper()
	sess := session.NewSession("app", "user", "session")
	parent := coreagent.NewInvocation(
		coreagent.WithInvocationSession(sess),
		coreagent.WithInvocationEventFilterKey("parent"),
	)
	return coreagent.NewInvocationContext(context.Background(), parent), sess, parent
}

// newThreadParentRunOptions builds a parent invocation carrying explicit run
// options, so a test can act as a caller that turned graph executor events off.
func newThreadParentRunOptions(
	t *testing.T,
	opts ...coreagent.RunOption,
) (context.Context, *session.Session, *coreagent.Invocation) {
	t.Helper()
	sess := session.NewSession("app", "user", "session")
	parent := coreagent.NewInvocation(
		coreagent.WithInvocationSession(sess),
		coreagent.WithInvocationEventFilterKey("parent"),
		coreagent.WithInvocationRunOptions(coreagent.NewRunOptions(opts...)),
	)
	return coreagent.NewInvocationContext(context.Background(), parent), sess, parent
}

func requireThreadResult(t *testing.T, v any) ThreadResult {
	t.Helper()
	result, ok := v.(ThreadResult)
	require.True(t, ok, "expected ThreadResult, got %T", v)
	return result
}

func threadResultJSON(t *testing.T, result ThreadResult) map[string]any {
	t.Helper()
	raw, err := json.Marshal(result)
	require.NoError(t, err)
	var decoded map[string]any
	require.NoError(t, json.Unmarshal(raw, &decoded))
	return decoded
}

func sessionFilterKeys(sess *session.Session) []string {
	if sess == nil {
		return nil
	}
	sess.EventMu.RLock()
	defer sess.EventMu.RUnlock()
	keys := make([]string, 0, len(sess.Events))
	for i := range sess.Events {
		keys = append(keys, sess.Events[i].FilterKey)
	}
	return keys
}

func sessionEventObjects(sess *session.Session) []string {
	if sess == nil {
		return nil
	}
	sess.EventMu.RLock()
	defer sess.EventMu.RUnlock()
	objects := make([]string, 0, len(sess.Events))
	for i := range sess.Events {
		if sess.Events[i].Response == nil {
			continue
		}
		objects = append(objects, sess.Events[i].Response.Object)
	}
	return objects
}

func sessionUserContents(sess *session.Session, filterKey string) []string {
	if sess == nil {
		return nil
	}
	sess.EventMu.RLock()
	defer sess.EventMu.RUnlock()
	var users []string
	for i := range sess.Events {
		evt := sess.Events[i]
		if evt.FilterKey != filterKey || !evt.IsUserMessage() ||
			evt.Response == nil || len(evt.Response.Choices) == 0 {
			continue
		}
		if content := evt.Response.Choices[0].Message.Content; content != "" {
			users = append(users, content)
		}
	}
	return users
}

// --- NewTool / NewDynamicTool compatibility --------------------------------

func TestNewTool_UnchangedWithoutThreadEnvelope(t *testing.T) {
	at := NewTool(&mockAgent{name: "plain", description: "plain agent"})
	require.False(t, at.thread)
	require.Empty(t, at.threadNamespace)
	require.Nil(t, at.threadLocks)
	decl := at.Declaration()
	require.NotNil(t, decl.InputSchema)
	_, hasThread := decl.InputSchema.Properties[fieldThreadID]
	require.False(t, hasThread)
	_, hasInput := decl.InputSchema.Properties[fieldInput]
	require.False(t, hasInput)
	require.Contains(t, decl.InputSchema.Properties, "request")
	require.Equal(t, "string", decl.OutputSchema.Type)

	out, err := at.Call(context.Background(), []byte(`{"request":"hi"}`))
	require.NoError(t, err)
	_, ok := out.(string)
	require.True(t, ok, "NewTool must keep returning a string")
}

// Option is an exported function type, so an application-defined Option may
// have side effects. Every fixed-agent constructor must apply each Option
// exactly once.
func TestFixedAgentToolConstructors_ApplyEachOptionOnce(t *testing.T) {
	for _, tc := range []struct {
		name    string
		newTool func(coreagent.Agent, ...Option) *Tool
	}{
		{name: "NewTool", newTool: NewTool},
		{name: "NewThreadTool", newTool: NewThreadTool},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			counting := Option(func(opts *agentToolOptions) {
				calls++
			})
			at := tc.newTool(
				&threadHistoryAgent{name: "child"},
				counting,
				WithThreadNamespace("child"),
				WithSkipSummarization(true),
			)
			require.Equal(t, 1, calls, "each Option must run exactly once")
			require.True(t, at.SkipSummarization(),
				"the resolved options must still take effect")
		})
	}
}

// WithThreadNamespace is thread-only: the other constructors must ignore it,
// including its validation.
func TestWithThreadNamespace_IgnoredByNewToolAndNewDynamicTool(t *testing.T) {
	at := NewTool(
		&mockAgent{name: "plain", description: "plain agent"},
		WithThreadNamespace("not even valid /"),
	)
	require.False(t, at.thread)
	require.Empty(t, at.threadNamespace)
	require.Equal(t, "plain", at.Declaration().Name)

	dyn := NewDynamicTool(WithThreadNamespace("also ignored /"))
	require.Empty(t, dyn.threadNamespace)
	require.Equal(t, DefaultDynamicToolName, dyn.Declaration().Name)
}

// --- Declaration -----------------------------------------------------------

func TestNewThreadTool_DeclarationSchema(t *testing.T) {
	at := NewThreadTool(&mockAgent{name: "math-specialist", description: "Math helper"})
	decl := at.Declaration()
	require.Equal(t, "math-specialist", decl.Name)
	require.Contains(t, decl.Description, fieldThreadID)
	require.Equal(t, []string{fieldInput}, decl.InputSchema.Required)
	require.Contains(t, decl.InputSchema.Properties, fieldThreadID)
	require.Equal(t, threadIDPattern, decl.InputSchema.Properties[fieldThreadID].Pattern)
	input := decl.InputSchema.Properties[fieldInput]
	require.NotNil(t, input)
	require.Equal(t, "object", input.Type)
	require.Contains(t, input.Properties, "request")
	// thread_id can legitimately be absent, so only status is required.
	require.Equal(t, []string{fieldStatus}, decl.OutputSchema.Required)
	require.Contains(t, decl.OutputSchema.Properties, fieldThreadID)
	require.Contains(t, decl.OutputSchema.Properties, fieldOutput)
	require.Contains(t, decl.OutputSchema.Properties, fieldError)
	require.Contains(t, decl.OutputSchema.Properties, fieldRetryable)
	require.Equal(t, []any{ThreadStatusCompleted, ThreadStatusFailed},
		decl.OutputSchema.Properties[fieldStatus].Enum)
}

// --- Core branch behavior --------------------------------------------------

func TestNewThreadTool_FirstCallReturnsIDAndPersistsHistory(t *testing.T) {
	child := &threadHistoryAgent{name: "child"}
	at := NewThreadTool(child)
	ctx, sess, _ := newThreadParent(t)

	out, err := at.Call(ctx, []byte(`{"input":{"request":"one"}}`))
	require.NoError(t, err)
	result := requireThreadResult(t, out)
	require.Equal(t, ThreadStatusCompleted, result.Status)
	require.NoError(t, validateThreadID(result.ThreadID))
	require.Equal(t, "run1", result.Output)

	keys, ids, inputs := child.snapshot()
	require.Equal(t, []string{threadFilterKey("child", result.ThreadID)}, keys)
	require.Equal(t, []string{result.ThreadID}, ids)
	require.Equal(t, []string{`{"request":"one"}`}, inputs)
	require.True(t, sessionHasThreadEvents(sess, keys[0]))
}

func TestNewThreadTool_ContinuationUsesPriorHistory(t *testing.T) {
	child := &threadHistoryAgent{name: "child"}
	at := NewThreadTool(child)
	ctx, _, _ := newThreadParent(t)

	first, err := at.Call(ctx, []byte(`{"input":{"request":"one"}}`))
	require.NoError(t, err)
	id := requireThreadResult(t, first).ThreadID

	second, err := at.Call(ctx, []byte(
		fmt.Sprintf(`{"thread_id":%q,"input":{"request":"two"}}`, id),
	))
	require.NoError(t, err)
	result := requireThreadResult(t, second)
	require.Equal(t, id, result.ThreadID)
	require.Equal(t, "run1|run2", result.Output)
}

func TestNewThreadTool_TwoIDsRemainIsolated(t *testing.T) {
	child := &threadHistoryAgent{name: "child"}
	at := NewThreadTool(child)
	ctx, _, _ := newThreadParent(t)

	a1, err := at.Call(ctx, []byte(`{"input":{"request":"A"}}`))
	require.NoError(t, err)
	idA := requireThreadResult(t, a1).ThreadID
	b1, err := at.Call(ctx, []byte(`{"input":{"request":"B"}}`))
	require.NoError(t, err)
	idB := requireThreadResult(t, b1).ThreadID
	require.NotEqual(t, idA, idB)

	a2, err := at.Call(ctx, []byte(
		fmt.Sprintf(`{"thread_id":%q,"input":{"request":"A2"}}`, idA),
	))
	require.NoError(t, err)
	require.Equal(t, "run1|run3", requireThreadResult(t, a2).Output)

	b2, err := at.Call(ctx, []byte(
		fmt.Sprintf(`{"thread_id":%q,"input":{"request":"B2"}}`, idB),
	))
	require.NoError(t, err)
	require.Equal(t, "run2|run4", requireThreadResult(t, b2).Output)

	keys, _, _ := child.snapshot()
	require.Equal(t, []string{
		threadFilterKey("child", idA),
		threadFilterKey("child", idB),
		threadFilterKey("child", idA),
		threadFilterKey("child", idB),
	}, keys)
}

func TestNewThreadTool_UnknownAndInvalidID(t *testing.T) {
	at := NewThreadTool(&threadHistoryAgent{name: "child"})
	ctx, _, _ := newThreadParent(t)

	unknown, err := at.Call(ctx, []byte(
		`{"thread_id":"abcd1234","input":{"request":"x"}}`,
	))
	require.NoError(t, err)
	got := requireThreadResult(t, unknown)
	require.Equal(t, ThreadStatusFailed, got.Status)
	require.Equal(t, "abcd1234", got.ThreadID)
	require.Contains(t, got.Error, errUnknownThreadID)
	require.NotNil(t, got.Retryable)
	require.False(t, *got.Retryable)

	for _, id := range []string{
		"../escape",
		"a/b",
		"a:b",
		"short",
		"has space",
		strings.Repeat("a", threadIDMaxLen+1),
	} {
		raw, marshalErr := json.Marshal(map[string]any{
			"thread_id": id,
			"input":     map[string]string{"request": "x"},
		})
		require.NoError(t, marshalErr)
		out, callErr := at.Call(ctx, raw)
		require.NoError(t, callErr)
		result := requireThreadResult(t, out)
		require.Equal(t, ThreadStatusFailed, result.Status, "id %q", id)
		require.NotNil(t, result.Retryable, "id %q", id)
		require.False(t, *result.Retryable, "id %q", id)
		require.NotContains(t, result.Error, errUnknownThreadID, "id %q", id)
	}
}

func TestNewThreadTool_NoParentSession(t *testing.T) {
	at := NewThreadTool(&threadHistoryAgent{name: "child"})
	out, err := at.Call(context.Background(), []byte(`{"input":{"request":"x"}}`))
	require.NoError(t, err)
	result := requireThreadResult(t, out)
	require.Equal(t, ThreadStatusFailed, result.Status)
	require.Contains(t, result.Error, "parent session is required")
	require.NotNil(t, result.Retryable)
	require.False(t, *result.Retryable)
	require.Empty(t, result.ThreadID)
}

func TestNewThreadTool_CustomInputPassedAsRawChildPayload(t *testing.T) {
	child := &threadSchemaAgent{
		name: "custom",
		inputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query": map[string]any{"type": "string"},
			},
			"required": []any{"query"},
		},
	}
	at := NewThreadTool(child)
	decl := at.Declaration()
	require.Contains(t, decl.InputSchema.Properties[fieldInput].Properties, "query")

	ctx, _, _ := newThreadParent(t)
	out, err := at.Call(ctx, []byte(`{"input":{"query":"hello"}}`))
	require.NoError(t, err)
	result := requireThreadResult(t, out)
	require.Equal(t, ThreadStatusCompleted, result.Status)
	require.Equal(t, `{"query":"hello"}`, child.lastMessage)
	require.Equal(t, result.ThreadID, child.lastThread)
	require.Equal(t, threadFilterKey("custom", result.ThreadID), child.lastKey)
}

func TestNewThreadTool_OutputAlwaysStringDespiteObjectSchema(t *testing.T) {
	child := &threadObjectOutputAgent{name: "child"}
	at := NewThreadTool(child)
	output := at.Declaration().OutputSchema.Properties[fieldOutput]
	require.NotNil(t, output)
	require.Equal(t, "string", output.Type)

	ctx, _, _ := newThreadParent(t)
	out, err := at.Call(ctx, []byte(`{"input":{"request":"hi"}}`))
	require.NoError(t, err)
	result := requireThreadResult(t, out)
	require.Equal(t, ThreadStatusCompleted, result.Status)
	_, isString := result.Output.(string)
	require.True(t, isString, "output must be string, got %T", result.Output)
	require.Equal(t, "object-child-text", result.Output)
	require.Equal(t, `{"request":"hi"}`, child.lastMessage)
}

// --- Thread establishment --------------------------------------------------

// A failure before the child persists anything must not hand back an ID: there
// is no branch to continue, and the caller simply starts a new one.
func TestNewThreadTool_PreEventRunErrorReturnsNoThreadID(t *testing.T) {
	child := &threadFailOnceAgent{name: "child"}
	at := NewThreadTool(child)
	ctx, sess, parent := newThreadParent(t)

	first, err := at.Call(ctx, []byte(`{"input":{"request":"first"}}`))
	require.NoError(t, err)
	failed := requireThreadResult(t, first)
	require.Equal(t, ThreadStatusFailed, failed.Status)
	require.Contains(t, failed.Error, "first run failed")
	require.Empty(t, failed.ThreadID, "an unestablished branch must not be advertised")
	require.Empty(t, sessionFilterKeys(sess), "no event may be persisted")

	// Retrying starts a fresh branch and the failed request is never replayed.
	second, err := at.Call(ctx, []byte(`{"input":{"request":"retry"}}`))
	require.NoError(t, err)
	ok := requireThreadResult(t, second)
	require.Equal(t, ThreadStatusCompleted, ok.Status)
	require.NoError(t, validateThreadID(ok.ThreadID))
	require.Equal(t, "retry-ok", ok.Output)

	childKey := threadFilterKey("child", ok.ThreadID)
	require.Equal(t, []string{`{"request":"retry"}`}, sessionUserContents(sess, childKey))
	require.Equal(t, []string{`{"request":"first"}`, `{"request":"retry"}`}, child.messages())

	childInv := parent.Clone(coreagent.WithInvocationEventFilterKey(childKey))
	req := &model.Request{}
	processor.NewContentRequestProcessor().ProcessRequest(
		context.Background(), childInv, req, nil,
	)
	var rendered strings.Builder
	for _, msg := range req.Messages {
		rendered.WriteString(msg.Role.String())
		rendered.WriteString(":")
		rendered.WriteString(msg.Content)
		rendered.WriteString("\n")
	}
	out := rendered.String()
	require.NotContains(t, out, `{"request":"first"}`)
	require.Contains(t, out, `{"request":"retry"}`)
	require.Equal(t, 1, strings.Count(out, `{"request":"retry"}`))
}

// Once the child stream starts, ensureUserMessageForCall establishes the
// branch, so a later failure still returns a continuable ID.
func TestNewThreadTool_ErrorAfterStreamStartsKeepsEstablishedThreadID(t *testing.T) {
	at := NewThreadTool(&threadErrorAgent{name: "child"})
	ctx, sess, _ := newThreadParent(t)

	out, err := at.Call(ctx, []byte(`{"input":{"request":"boom"}}`))
	require.NoError(t, err)
	result := requireThreadResult(t, out)
	require.Equal(t, ThreadStatusFailed, result.Status)
	require.NoError(t, validateThreadID(result.ThreadID))
	require.Contains(t, result.Error, "child failed")
	// An ordinary child failure is neither known-transient nor known-permanent.
	require.Nil(t, result.Retryable)

	childKey := threadFilterKey("child", result.ThreadID)
	require.True(t, sessionHasThreadEvents(sess, childKey))

	again, err := at.Call(ctx, []byte(
		fmt.Sprintf(`{"thread_id":%q,"input":{"request":"retry"}}`, result.ThreadID),
	))
	require.NoError(t, err)
	retry := requireThreadResult(t, again)
	require.Equal(t, result.ThreadID, retry.ThreadID,
		"an established branch stays addressable after a failed turn")
	require.Equal(t, ThreadStatusFailed, retry.Status)
}

// --- Persistence and reload ------------------------------------------------

// newPersistedThreadParent builds a parent invocation whose events are written
// through a session service, the way the runner wires an AgentTool call.
func newPersistedThreadParent(
	t *testing.T,
	service *sessioninmemory.SessionService,
	sess *session.Session,
) (context.Context, *coreagent.Invocation) {
	t.Helper()
	parent := coreagent.NewInvocation(
		coreagent.WithInvocationSession(sess),
		coreagent.WithInvocationEventFilterKey("parent"),
	)
	appender.Attach(parent, func(ctx context.Context, evt *event.Event) error {
		return service.AppendEvent(ctx, sess, evt)
	})
	return coreagent.NewInvocationContext(context.Background(), parent), parent
}

// The core design claim is that a branch is nothing but parent Session events
// under a stable filter key. So a thread must survive losing every in-process
// object: the Tool, the wrapped agent, the Invocation, and the Session value
// itself. Only the persisted events and the agent identity carry over.
func TestNewThreadTool_ContinuesBranchAfterSessionReload(t *testing.T) {
	service := sessioninmemory.NewSessionService()
	key := session.Key{AppName: "app", UserID: "user", SessionID: "session"}
	created, err := service.CreateSession(context.Background(), key, nil)
	require.NoError(t, err)

	firstCtx, _ := newPersistedThreadParent(t, service, created)
	firstAgent := &threadHistoryAgent{name: "child"}
	out, err := NewThreadTool(firstAgent).Call(
		firstCtx, []byte(`{"input":{"request":"one"}}`),
	)
	require.NoError(t, err)
	first := requireThreadResult(t, out)
	require.Equal(t, ThreadStatusCompleted, first.Status)
	require.Equal(t, "run1", first.Output)
	threadID := first.ThreadID
	require.NoError(t, validateThreadID(threadID))

	// Reload the parent session from the service into a fresh value, dropping
	// every object the first call used.
	reloaded, err := service.GetSession(context.Background(), key)
	require.NoError(t, err)
	require.NotNil(t, reloaded)
	require.NotSame(t, created, reloaded, "the reload must not reuse the value")
	childKey := threadFilterKey("child", threadID)
	require.True(t, sessionHasThreadEvents(reloaded, childKey),
		"the branch must be recoverable from persisted events alone")

	secondCtx, _ := newPersistedThreadParent(t, service, reloaded)
	// A fresh Tool and a fresh wrapped agent: same identity, no shared state.
	secondAgent := &threadHistoryAgent{name: "child"}
	out, err = NewThreadTool(secondAgent).Call(secondCtx, []byte(
		fmt.Sprintf(`{"thread_id":%q,"input":{"request":"two"}}`, threadID),
	))
	require.NoError(t, err)
	second := requireThreadResult(t, out)
	require.Equal(t, ThreadStatusCompleted, second.Status)
	require.Equal(t, threadID, second.ThreadID)
	// The fresh agent's own counter restarts at 1, so "run1|run1" can only come
	// from the reloaded branch history.
	require.Equal(t, "run1|run1", second.Output)

	keys, ids, inputs := secondAgent.snapshot()
	require.Equal(t, []string{childKey}, keys)
	require.Equal(t, []string{threadID}, ids)
	require.Equal(t, []string{`{"request":"two"}`}, inputs)

	// Both turns are persisted under the same branch key, and the parent
	// conversation is untouched.
	final, err := service.GetSession(context.Background(), key)
	require.NoError(t, err)
	require.Equal(t,
		[]string{`{"request":"one"}`, `{"request":"two"}`},
		sessionUserContents(final, childKey),
	)
	for _, got := range sessionFilterKeys(final) {
		require.Equal(t, childKey, got,
			"a thread call must not write outside its branch key")
	}

	// An unknown ID still fails after a reload rather than creating a branch.
	out, err = NewThreadTool(&threadHistoryAgent{name: "child"}).Call(
		secondCtx, []byte(`{"thread_id":"abcd1234","input":{"request":"x"}}`),
	)
	require.NoError(t, err)
	unknown := requireThreadResult(t, out)
	require.Equal(t, ThreadStatusFailed, unknown.Status)
	require.Contains(t, unknown.Error, errUnknownThreadID)
}

// --- P0: nested ordinary AgentTool keeps its own filter key ----------------

func TestNewThreadTool_NestedAgentToolKeepsOwnFilterKey(t *testing.T) {
	nestedChild := &threadKeyRecordingAgent{name: "nested-child"}
	outer := &threadNestingAgent{name: "outer", nested: NewTool(nestedChild)}
	at := NewThreadTool(outer)
	ctx, sess, _ := newThreadParent(t)

	out, err := at.Call(ctx, []byte(`{"input":{"request":"go"}}`))
	require.NoError(t, err)
	result := requireThreadResult(t, out)
	require.Equal(t, ThreadStatusCompleted, result.Status)

	threadKey := threadFilterKey("outer", result.ThreadID)
	ownKeys, callErr := outer.snapshot()
	require.NoError(t, callErr)
	require.Equal(t, []string{threadKey}, ownKeys,
		"the wrapped agent runs on the thread branch key")

	nestedKeys := nestedChild.filterKeys()
	require.Len(t, nestedKeys, 1)
	require.NotEqual(t, threadKey, nestedKeys[0],
		"a nested ordinary AgentTool must not inherit the thread branch key")
	require.True(t, strings.HasPrefix(nestedKeys[0], "nested-child-"),
		"nested key %q must be the ordinary per-call key", nestedKeys[0])

	// The nested sub-agent's events must not land on the thread branch either.
	for _, key := range sessionFilterKeys(sess) {
		if key == threadKey {
			continue
		}
		require.True(t, strings.HasPrefix(key, "nested-child-"),
			"unexpected filter key %q in session", key)
	}
}

func TestNewThreadTool_NestedAgentToolKeepsOwnFilterKeyViaGraphRuntime(t *testing.T) {
	nestedChild := &threadKeyRecordingAgent{name: "nested-child"}
	outer := &threadNestingAgent{name: "outer", nested: NewTool(nestedChild)}
	at := NewThreadTool(outer)
	ctx, _, parent := newThreadParent(t)

	out, err := at.CallWithAgentToolGraphRuntime(
		ctx,
		[]byte(`{"input":{"request":"go"}}`),
		agenttoolgraph.RuntimeContext{
			ParentInvocation: parent,
			State:            map[string]any{},
			ParentNodeID:     "tools",
			ToolCallID:       "call-1",
			ToolCallKey:      "0:outer:call-1",
			ChildFilterKey:   "graph-should-not-win",
		},
	)
	require.NoError(t, err)
	result := requireThreadResult(t, out)
	require.Equal(t, ThreadStatusCompleted, result.Status)

	threadKey := threadFilterKey("outer", result.ThreadID)
	ownKeys, callErr := outer.snapshot()
	require.NoError(t, callErr)
	require.Equal(t, []string{threadKey}, ownKeys)

	nestedKeys := nestedChild.filterKeys()
	require.Len(t, nestedKeys, 1)
	require.NotEqual(t, threadKey, nestedKeys[0])
	require.NotEqual(t, "graph-should-not-win", nestedKeys[0])
	require.True(t, strings.HasPrefix(nestedKeys[0], "nested-child-"))
}

// --- Graph runtime ---------------------------------------------------------

func TestNewThreadTool_GraphRuntimeUsesEnvelopeNotCheckpoint(t *testing.T) {
	child := &threadHistoryAgent{name: "child"}
	at := NewThreadTool(child)
	ctx, _, parent := newThreadParent(t)

	out, err := at.CallWithAgentToolGraphRuntime(
		ctx,
		[]byte(`{"input":{"request":"graph-input"}}`),
		agenttoolgraph.RuntimeContext{
			ParentInvocation: parent,
			State: map[string]any{
				graph.CfgKeyCheckpointID: "ckpt-should-not-clear-message",
			},
			ParentNodeID:   "tools",
			ToolCallID:     "call-1",
			ToolCallKey:    "0:child:call-1",
			ChildFilterKey: "graph-should-not-win",
		},
	)
	require.NoError(t, err)
	result := requireThreadResult(t, out)
	require.Equal(t, ThreadStatusCompleted, result.Status)
	require.Equal(t, "run1", result.Output)
	require.NoError(t, validateThreadID(result.ThreadID))

	keys, ids, inputs := child.snapshot()
	require.Equal(t, []string{threadFilterKey("child", result.ThreadID)}, keys)
	require.NotContains(t, keys, "graph-should-not-win")
	require.Equal(t, []string{result.ThreadID}, ids)
	require.Equal(t, []string{`{"request":"graph-input"}`}, inputs)
}

func TestNewThreadTool_GraphInterruptIsUnsupportedAndNotRetryable(t *testing.T) {
	child := &threadRunErrorAgent{
		name: "child",
		err:  graph.NewInterruptError("needs approval"),
	}
	at := NewThreadTool(child)
	ctx, _, parent := newThreadParent(t)

	out, err := at.CallWithAgentToolGraphRuntime(
		ctx,
		[]byte(`{"input":{"request":"approve"}}`),
		agenttoolgraph.RuntimeContext{
			ParentInvocation: parent,
			State:            map[string]any{},
			ParentNodeID:     "tools",
			ToolCallID:       "call-1",
			ToolCallKey:      "0:child:call-1",
		},
	)
	// The interrupt must not propagate as a graph interrupt error: the parent
	// graph has no child checkpoint to resume.
	require.NoError(t, err)
	result := requireThreadResult(t, out)
	require.Equal(t, ThreadStatusFailed, result.Status)
	require.Contains(t, result.Error, "does not support graph checkpoint")
	require.NotNil(t, result.Retryable)
	require.False(t, *result.Retryable, "rerunning cannot clear an interrupt")
}

// newThreadInterruptGraphAgent builds a real GraphAgent whose only node
// interrupts. Such an agent returns no error from Run: the executor emits a
// pregel interrupt event and closes the channel, which is exactly the case a
// directly returned graph.InterruptError cannot cover.
func newThreadInterruptGraphAgent(t *testing.T, name string) coreagent.Agent {
	t.Helper()
	sg := graph.NewStateGraph(graph.MessagesStateSchema())
	sg.AddNode("ask", func(ctx context.Context, state graph.State) (any, error) {
		approval, err := graph.Interrupt(ctx, state, "approval", "approve?")
		if err != nil {
			return nil, err
		}
		text, _ := approval.(string)
		return graph.State{graph.StateKeyLastResponse: "approved:" + text}, nil
	})
	compiled := sg.SetEntryPoint("ask").SetFinishPoint("ask").MustCompile()
	ga, err := graphagent.New(name, compiled)
	require.NoError(t, err)
	return ga
}

// An interrupt that a real GraphAgent only signals through executor events must
// still fail the envelope. Reporting it as completed would tell the model the
// branch answered when it did not.
func TestNewThreadTool_GraphAgentEventSignalledInterruptFails(t *testing.T) {
	at := NewThreadTool(newThreadInterruptGraphAgent(t, "graph-child"))
	ctx, _, _ := newThreadParent(t)

	out, err := at.Call(ctx, []byte(`{"input":{"request":"approve"}}`))
	require.NoError(t, err)
	result := requireThreadResult(t, out)
	require.Equal(t, ThreadStatusFailed, result.Status)
	require.Contains(t, result.Error, "does not support graph checkpoint")
	require.NotNil(t, result.Retryable)
	require.False(t, *result.Retryable, "rerunning cannot clear an interrupt")
	require.Empty(t, result.Output)
}

// The same interrupt on the graph runtime entrypoint must not be reported as a
// resumable graph interrupt either: the parent graph has no child checkpoint.
func TestNewThreadTool_GraphAgentEventSignalledInterruptViaGraphRuntime(t *testing.T) {
	at := NewThreadTool(newThreadInterruptGraphAgent(t, "graph-child"))
	ctx, _, parent := newThreadParent(t)

	out, err := at.CallWithAgentToolGraphRuntime(
		ctx,
		[]byte(`{"input":{"request":"approve"}}`),
		agenttoolgraph.RuntimeContext{
			ParentInvocation: parent,
			State:            map[string]any{},
			ParentNodeID:     "tools",
			ToolCallID:       "call-1",
			ToolCallKey:      "0:graph-child:call-1",
		},
	)
	require.NoError(t, err)
	result := requireThreadResult(t, out)
	require.Equal(t, ThreadStatusFailed, result.Status)
	require.Contains(t, result.Error, "does not support graph checkpoint")
	require.NotNil(t, result.Retryable)
	require.False(t, *result.Retryable)
}

// newThreadAnswerGraphAgent builds a real GraphAgent that completes normally.
func newThreadAnswerGraphAgent(t *testing.T, name, answer string) coreagent.Agent {
	t.Helper()
	sg := graph.NewStateGraph(graph.MessagesStateSchema())
	sg.AddNode("answer", func(ctx context.Context, state graph.State) (any, error) {
		return graph.State{graph.StateKeyLastResponse: answer}, nil
	})
	compiled := sg.SetEntryPoint("answer").SetFinishPoint("answer").MustCompile()
	ga, err := graphagent.New(name, compiled)
	require.NoError(t, err)
	return ga
}

// graphExecutorMetadataStateKeys are the session state keys carried only by
// graph.* executor events. A caller that turned those events off must never see
// them, even though a thread call re-enables the events internally.
var graphExecutorMetadataStateKeys = []string{
	graph.MetadataKeyPregel,
	graph.MetadataKeyNode,
	graph.MetadataKeyNodeEmitter,
	graph.MetadataKeyChannel,
	graph.MetadataKeyState,
}

func decodeJSONString(t *testing.T, raw []byte) string {
	t.Helper()
	var decoded string
	require.NoError(t, json.Unmarshal(raw, &decoded))
	return decoded
}

// requireNoGraphExecutorState asserts that no graph.* executor event merged its
// metadata into the shared session state.
func requireNoGraphExecutorState(t *testing.T, state session.StateMap) {
	t.Helper()
	keys := make([]string, 0, len(state))
	for key := range state {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range graphExecutorMetadataStateKeys {
		require.NotContains(t, keys, key,
			"forced executor events must not reach the shared session state")
	}
}

// A caller that turned graph executor events off must still get a fail-fast
// envelope. The interrupt is signalled only through those events, so a thread
// call re-enables them for the child run and keeps them out of the shared
// session.
func TestNewThreadTool_GraphAgentInterruptFailsWhenCallerDisabledExecutorEvents(t *testing.T) {
	at := NewThreadTool(newThreadInterruptGraphAgent(t, "graph-child"))
	ctx, sess, parent := newThreadParentRunOptions(
		t, coreagent.WithDisableGraphExecutorEvents(true),
	)
	require.True(t, coreagent.IsGraphExecutorEventsDisabled(parent))

	out, err := at.Call(ctx, []byte(`{"input":{"request":"approve"}}`))
	require.NoError(t, err)
	result := requireThreadResult(t, out)
	require.Equal(t, ThreadStatusFailed, result.Status)
	require.Contains(t, result.Error, "does not support graph checkpoint")
	require.NotNil(t, result.Retryable)
	require.False(t, *result.Retryable, "rerunning cannot clear an interrupt")
	require.Empty(t, result.Output)

	// The re-enabled events are internal to this call: they must leave no
	// trace in the shared session, as events or as merged state.
	for _, object := range sessionEventObjects(sess) {
		require.False(t, strings.HasPrefix(object, "graph."),
			"graph event %q must not be retained in the session", object)
	}
	requireNoGraphExecutorState(t, sess.SnapshotState())
	require.True(t, coreagent.IsGraphExecutorEventsDisabled(parent),
		"the caller's own run options must not be rewritten")
	// The branch's own user message legitimately remains.
	require.Equal(t,
		[]string{`{"request":"approve"}`},
		sessionUserContents(sess, threadFilterKey("graph-child", result.ThreadID)),
	)
}

// Re-enabling executor events must not change what the shared session ends up
// holding for a branch that completes: the graph completion snapshot is the
// branch's record of the child's result, so suppression must not swallow it.
func TestNewThreadTool_GraphAgentCompletionSnapshotSurvivesEventSuppression(t *testing.T) {
	const answer = "graph-answer"
	run := func(t *testing.T, opts ...coreagent.RunOption) session.StateMap {
		t.Helper()
		at := NewThreadTool(newThreadAnswerGraphAgent(t, "graph-child", answer))
		ctx, sess, _ := newThreadParentRunOptions(t, opts...)
		out, err := at.Call(ctx, []byte(`{"input":{"request":"ask"}}`))
		require.NoError(t, err)
		result := requireThreadResult(t, out)
		require.Equal(t, ThreadStatusCompleted, result.Status)
		require.Equal(t, answer, result.Output)
		return sess.SnapshotState()
	}

	// A caller that leaves executor events on already accepts them in the
	// shared session. That run is the baseline for what the completion
	// snapshot has to contribute.
	enabled := run(t)
	require.Contains(t, enabled, graph.MetadataKeyPregel)
	require.Contains(t, enabled, graph.MetadataKeyCompletion)
	require.Equal(t, answer,
		decodeJSONString(t, enabled[graph.StateKeyLastResponse]))

	disabled := run(t, coreagent.WithDisableGraphExecutorEvents(true))
	requireNoGraphExecutorState(t, disabled)
	require.Contains(t, disabled, graph.MetadataKeyCompletion,
		"the completion snapshot must survive executor-event suppression")
	require.Equal(t, answer,
		decodeJSONString(t, disabled[graph.StateKeyLastResponse]))
}

// An ordinary NewTool wrapping the same agent keeps its existing behavior: the
// thread-only observer must not turn a plain graph run into a tool error.
func TestNewTool_GraphAgentEventSignalledInterruptUnchanged(t *testing.T) {
	at := NewTool(newThreadInterruptGraphAgent(t, "graph-child"))
	ctx, _, _ := newThreadParent(t)

	out, err := at.Call(ctx, []byte(`{"request":"approve"}`))
	require.NoError(t, err)
	text, ok := out.(string)
	require.True(t, ok, "NewTool must keep returning a string, got %T", out)
	require.Empty(t, text)
}

func TestNewThreadTool_GraphInterruptOnCallPath(t *testing.T) {
	at := NewThreadTool(&threadRunErrorAgent{
		name: "child",
		err:  fmt.Errorf("wrapped: %w", graph.NewInterruptError("needs approval")),
	})
	ctx, _, _ := newThreadParent(t)

	out, err := at.Call(ctx, []byte(`{"input":{"request":"approve"}}`))
	require.NoError(t, err)
	result := requireThreadResult(t, out)
	require.Equal(t, ThreadStatusFailed, result.Status)
	require.Contains(t, result.Error, "does not support graph checkpoint")
	require.NotNil(t, result.Retryable)
	require.False(t, *result.Retryable)
}

// --- Retryable classification and envelope serialization -------------------

func TestNewThreadTool_RetryableSerialization(t *testing.T) {
	ctx, _, _ := newThreadParent(t)

	t.Run("completed omits retryable and error", func(t *testing.T) {
		at := NewThreadTool(&threadHistoryAgent{name: "child"})
		out, err := at.Call(ctx, []byte(`{"input":{"request":"hi"}}`))
		require.NoError(t, err)
		decoded := threadResultJSON(t, requireThreadResult(t, out))
		require.Equal(t, ThreadStatusCompleted, decoded[fieldStatus])
		require.NotContains(t, decoded, fieldRetryable)
		require.NotContains(t, decoded, fieldError)
		require.Contains(t, decoded, fieldThreadID)
	})

	t.Run("permanent validation failure reports false", func(t *testing.T) {
		at := NewThreadTool(&threadHistoryAgent{name: "child"})
		out, err := at.Call(ctx, []byte(`{"thread_id":"abcd1234","input":{"request":"x"}}`))
		require.NoError(t, err)
		decoded := threadResultJSON(t, requireThreadResult(t, out))
		require.Equal(t, false, decoded[fieldRetryable])
	})

	t.Run("unknown child failure omits retryable", func(t *testing.T) {
		at := NewThreadTool(&threadErrorAgent{name: "child"})
		out, err := at.Call(ctx, []byte(`{"input":{"request":"boom"}}`))
		require.NoError(t, err)
		decoded := threadResultJSON(t, requireThreadResult(t, out))
		require.Equal(t, ThreadStatusFailed, decoded[fieldStatus])
		require.NotContains(t, decoded, fieldRetryable)
	})

	t.Run("cancellation reports true and omits thread_id", func(t *testing.T) {
		at := NewThreadTool(&threadRunErrorAgent{
			name: "child",
			err:  fmt.Errorf("child aborted: %w", context.Canceled),
		})
		out, err := at.Call(ctx, []byte(`{"input":{"request":"x"}}`))
		require.NoError(t, err)
		result := requireThreadResult(t, out)
		require.Empty(t, result.ThreadID)
		decoded := threadResultJSON(t, result)
		require.Equal(t, true, decoded[fieldRetryable])
		require.NotContains(t, decoded, fieldThreadID)
	})
}

func TestRetryableForError(t *testing.T) {
	require.Nil(t, retryableForError(nil))
	require.Nil(t, retryableForError(fmt.Errorf("boom")))

	deadline := retryableForError(fmt.Errorf("x: %w", context.DeadlineExceeded))
	require.NotNil(t, deadline)
	require.True(t, *deadline)

	canceled := retryableForError(fmt.Errorf("x: %w", context.Canceled))
	require.NotNil(t, canceled)
	require.True(t, *canceled)
}

// --- Namespace -------------------------------------------------------------

func TestNewThreadTool_NamespaceDefaultsToWrappedAgentName(t *testing.T) {
	at := NewThreadTool(&threadHistoryAgent{name: "child"})
	require.Equal(t, "child", at.threadNamespace)
	require.Equal(t, "agenttool:child:thread:abcd1234",
		threadFilterKey(at.threadNamespace, "abcd1234"))
	require.NotContains(t, threadFilterKey(at.threadNamespace, "abcd1234"), "/")
}

// NewTool ignores WithName, so branch identity must follow the wrapped agent
// rather than any tool-name override.
func TestNewThreadTool_NamespaceIgnoresWithName(t *testing.T) {
	at := NewThreadTool(&threadHistoryAgent{name: "child"}, WithName("renamed"))
	require.Equal(t, "child", at.threadNamespace)
	require.Equal(t, "child", at.Declaration().Name)
}

func TestNewThreadTool_ExplicitNamespaceSurvivesAgentRename(t *testing.T) {
	const namespace = "billing-specialist"
	before := NewThreadTool(
		&threadHistoryAgent{name: "old-name"},
		WithThreadNamespace(namespace),
	)
	after := NewThreadTool(
		&threadHistoryAgent{name: "new-name"},
		WithThreadNamespace(namespace),
	)
	require.Equal(t, namespace, before.threadNamespace)
	require.Equal(t, namespace, after.threadNamespace)
	require.Equal(t,
		threadFilterKey(before.threadNamespace, "abcd1234"),
		threadFilterKey(after.threadNamespace, "abcd1234"),
	)
	// The model-facing tool name still tracks the agent.
	require.Equal(t, "old-name", before.Declaration().Name)
	require.Equal(t, "new-name", after.Declaration().Name)
}

// Two tools whose agents share a name stay isolated when given distinct
// namespaces, and the thread of one is unknown to the other.
func TestNewThreadTool_SameAgentNameDistinctNamespacesStayIsolated(t *testing.T) {
	first := NewThreadTool(
		&threadHistoryAgent{name: "shared"},
		WithThreadNamespace("first"),
	)
	second := NewThreadTool(
		&threadHistoryAgent{name: "shared"},
		WithThreadNamespace("second"),
	)
	ctx, _, _ := newThreadParent(t)

	out, err := first.Call(ctx, []byte(`{"input":{"request":"a"}}`))
	require.NoError(t, err)
	created := requireThreadResult(t, out)
	require.Equal(t, ThreadStatusCompleted, created.Status)

	crossed, err := second.Call(ctx, []byte(
		fmt.Sprintf(`{"thread_id":%q,"input":{"request":"b"}}`, created.ThreadID),
	))
	require.NoError(t, err)
	result := requireThreadResult(t, crossed)
	require.Equal(t, ThreadStatusFailed, result.Status)
	require.Contains(t, result.Error, errUnknownThreadID)
}

func TestNewThreadTool_SameAgentNameSharedNamespaceSharesBranches(t *testing.T) {
	first := NewThreadTool(&threadHistoryAgent{name: "shared"})
	second := NewThreadTool(&threadHistoryAgent{name: "shared"})
	ctx, _, _ := newThreadParent(t)

	out, err := first.Call(ctx, []byte(`{"input":{"request":"a"}}`))
	require.NoError(t, err)
	created := requireThreadResult(t, out)

	crossed, err := second.Call(ctx, []byte(
		fmt.Sprintf(`{"thread_id":%q,"input":{"request":"b"}}`, created.ThreadID),
	))
	require.NoError(t, err)
	require.Equal(t, ThreadStatusCompleted, requireThreadResult(t, crossed).Status)
}

func TestNewThreadTool_InvalidNamespacePanics(t *testing.T) {
	for _, namespace := range []string{"", "  ", "has space", "a/b", "a:b", "café"} {
		require.Panics(t, func() {
			NewThreadTool(
				&threadHistoryAgent{name: "child"},
				WithThreadNamespace(namespace),
			)
		}, "namespace %q must be rejected", namespace)
	}
}

func TestNewThreadTool_UnusableAgentNamePanicsInsteadOfRewriting(t *testing.T) {
	require.Panics(t, func() {
		NewThreadTool(&threadHistoryAgent{name: "team/child"})
	})
	// The escape hatch is an explicit namespace, never a lossy rewrite.
	require.NotPanics(t, func() {
		NewThreadTool(
			&threadHistoryAgent{name: "team/child"},
			WithThreadNamespace("team_child"),
		)
	})
}

// --- Concurrency -----------------------------------------------------------

// Two calls on one established branch must not run the child concurrently.
func TestNewThreadTool_SameIDSerialized(t *testing.T) {
	child := &threadGateAgent{name: "child", entered: make(chan struct{}, 4)}
	at := NewThreadTool(child)
	ctx, sess, _ := newThreadParent(t)

	// Establish the branch first: an ID is only addressable once persisted.
	out, err := at.Call(ctx, []byte(`{"input":{"request":"one"}}`))
	require.NoError(t, err)
	created := requireThreadResult(t, out)
	require.Equal(t, ThreadStatusCompleted, created.Status)
	threadID := created.ThreadID
	require.NoError(t, validateThreadID(threadID))
	<-child.entered

	gate := make(chan struct{})
	child.setGate(gate)

	type outcome struct {
		raw any
		err error
	}
	results := make(chan outcome, 2)
	args := []byte(fmt.Sprintf(
		`{"thread_id":%q,"input":{"request":"concurrent"}}`, threadID,
	))
	for i := 0; i < 2; i++ {
		go func() {
			raw, callErr := at.Call(ctx, args)
			results <- outcome{raw: raw, err: callErr}
		}()
	}

	// One call is inside Run; the other must be parked on the thread lock.
	<-child.entered
	lockKey := threadLockKey(sess, threadID)
	require.Eventually(t, func() bool {
		return threadLockWaiters(at.threadLocks, lockKey) >= 2
	}, eventuallyTimeout, time.Millisecond,
		"second call must queue on the per-thread lock")

	close(gate)
	for i := 0; i < 2; i++ {
		got := <-results
		require.NoError(t, got.err)
		result := requireThreadResult(t, got.raw)
		require.Equal(t, ThreadStatusCompleted, result.Status)
		require.Equal(t, threadID, result.ThreadID)
	}

	runs, maxActive := child.stats()
	require.Equal(t, 3, runs)
	require.Equal(t, 1, maxActive, "same-thread calls must not overlap")
	require.Eventually(t, func() bool {
		return threadLockWaiters(at.threadLocks, lockKey) == 0
	}, eventuallyTimeout, time.Millisecond, "lock entry must be released")
}

// Distinct branches are independent and may run at the same time.
func TestNewThreadTool_DistinctIDsRunConcurrently(t *testing.T) {
	child := &threadGateAgent{name: "child", entered: make(chan struct{}, 4)}
	at := NewThreadTool(child)
	ctx, _, _ := newThreadParent(t)

	gate := make(chan struct{})
	child.setGate(gate)

	type outcome struct {
		raw any
		err error
	}
	results := make(chan outcome, 2)
	for i := 0; i < 2; i++ {
		go func() {
			raw, callErr := at.Call(ctx, []byte(`{"input":{"request":"x"}}`))
			results <- outcome{raw: raw, err: callErr}
		}()
	}

	<-child.entered
	<-child.entered
	close(gate)

	ids := make(map[string]struct{}, 2)
	for i := 0; i < 2; i++ {
		got := <-results
		require.NoError(t, got.err)
		result := requireThreadResult(t, got.raw)
		require.Equal(t, ThreadStatusCompleted, result.Status)
		ids[result.ThreadID] = struct{}{}
	}
	require.Len(t, ids, 2, "each call must create its own branch")

	_, maxActive := child.stats()
	require.Equal(t, 2, maxActive, "distinct branches must not serialize")
}

func TestThreadLockSet_CancellationReleasesEntry(t *testing.T) {
	locks := newThreadLockSet()
	unlock, err := locks.acquire(context.Background(), "k")
	require.NoError(t, err)

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	release, err := locks.acquire(canceled, "k")
	require.ErrorIs(t, err, context.Canceled)
	require.Nil(t, release)
	require.Equal(t, 1, threadLockWaiters(locks, "k"))

	unlock()
	require.Equal(t, 0, threadLockWaiters(locks, "k"))
}

// --- Streaming -------------------------------------------------------------

func TestNewThreadTool_StreamableCallEmitsSingleFinalChunk(t *testing.T) {
	at := NewThreadTool(&threadHistoryAgent{name: "child"}, WithStreamInner(true))
	ctx, _, _ := newThreadParent(t)

	reader, err := at.StreamableCall(
		tool.WithFinalResultChunks(ctx),
		[]byte(`{"input":{"request":"hi"}}`),
	)
	require.NoError(t, err)
	t.Cleanup(func() { reader.Close() })

	var chunks []tool.StreamChunk
	for {
		chunk, recvErr := reader.Recv()
		if recvErr == io.EOF {
			break
		}
		require.NoError(t, recvErr)
		chunks = append(chunks, chunk)
	}
	require.Len(t, chunks, 1)
	final, ok := chunks[0].Content.(tool.FinalResultChunk)
	require.True(t, ok, "got %T", chunks[0].Content)
	result := requireThreadResult(t, final.Result)
	require.Equal(t, ThreadStatusCompleted, result.Status)
	require.Equal(t, "run1", result.Output)
	require.NoError(t, validateThreadID(result.ThreadID))
}

// --- Option interactions ---------------------------------------------------

func TestNewThreadTool_IgnoresParentBranchAndPersistentHistory(t *testing.T) {
	original := agentlog.Default
	logger := &dynTestWarnLogger{}
	agentlog.Default = logger
	t.Cleanup(func() {
		agentlog.Default = original
	})

	at := NewThreadTool(
		&threadHistoryAgent{name: "child"},
		WithHistoryScope(HistoryScopeParentBranch),
		WithPersistentHistory(),
	)
	require.GreaterOrEqual(t, logger.warnfCalls, 1)
	require.Equal(t, HistoryScopeIsolated, at.historyScope)
	require.Nil(t, at.persistentHistory)
}
