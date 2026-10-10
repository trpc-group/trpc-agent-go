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
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/stretchr/testify/require"
	coreagent "trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/graphagent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/graph"
	"trpc.group/trpc-go/trpc-agent-go/internal/agenttoolgraph"
	"trpc.group/trpc-go/trpc-agent-go/internal/flow/processor"
	"trpc.group/trpc-go/trpc-agent-go/internal/state/appender"
	"trpc.group/trpc-go/trpc-agent-go/internal/state/flush"
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
	if id, ok := SubAgentIDFromContext(ctx); ok {
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
	a.lastThread, _ = SubAgentIDFromContext(ctx)
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
	return singleAssistantEvent(`{"answer":"42"}`), nil
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

// threadErrorAgent starts its stream and then reports an error, so the
// subagent's user event is already persisted when the call fails.
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

// threadEmptyOutputAgent finishes successfully without any assistant text.
type threadEmptyOutputAgent struct{ name string }

func (a *threadEmptyOutputAgent) Run(
	_ context.Context,
	_ *coreagent.Invocation,
) (<-chan *event.Event, error) {
	ch := make(chan *event.Event)
	close(ch)
	return ch, nil
}

func (a *threadEmptyOutputAgent) Tools() []tool.Tool { return nil }
func (a *threadEmptyOutputAgent) Info() coreagent.Info {
	return coreagent.Info{Name: a.name, Description: "thread-empty-output-test"}
}
func (a *threadEmptyOutputAgent) SubAgents() []coreagent.Agent        { return nil }
func (a *threadEmptyOutputAgent) FindSubAgent(string) coreagent.Agent { return nil }

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

// threadKeyRecordingAgent records the event filter key and the subagent ID it
// was given.
type threadKeyRecordingAgent struct {
	name string

	mu   sync.Mutex
	keys []string
	ids  []string
}

func (a *threadKeyRecordingAgent) Run(
	ctx context.Context,
	inv *coreagent.Invocation,
) (<-chan *event.Event, error) {
	a.mu.Lock()
	if inv != nil {
		a.keys = append(a.keys, inv.GetEventFilterKey())
	}
	id, _ := SubAgentIDFromContext(ctx)
	a.ids = append(a.ids, id)
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

func (a *threadKeyRecordingAgent) subAgentIDs() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]string(nil), a.ids...)
}

// threadNestingAgent calls a nested AgentTool with its own child context, the
// way an LLM flow does when the wrapped agent has agent tools.
type threadNestingAgent struct {
	name       string
	nested     *Tool
	nestedArgs string

	mu        sync.Mutex
	ownKeys   []string
	idsBefore []string
	idsAfter  []string
	nestedOut any
	callErr   error
}

func (a *threadNestingAgent) Run(
	ctx context.Context,
	inv *coreagent.Invocation,
) (<-chan *event.Event, error) {
	before, _ := SubAgentIDFromContext(ctx)
	a.mu.Lock()
	if inv != nil {
		a.ownKeys = append(a.ownKeys, inv.GetEventFilterKey())
	}
	a.idsBefore = append(a.idsBefore, before)
	a.mu.Unlock()

	args := a.nestedArgs
	if args == "" {
		args = `{"request":"nested"}`
	}
	out, err := a.nested.Call(ctx, []byte(args))
	after, _ := SubAgentIDFromContext(ctx)
	a.mu.Lock()
	a.nestedOut = out
	a.callErr = err
	a.idsAfter = append(a.idsAfter, after)
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

func (a *threadNestingAgent) subAgentIDs() (before, after []string, nestedOut any) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]string(nil), a.idsBefore...),
		append([]string(nil), a.idsAfter...),
		a.nestedOut
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

func requireSubAgentResult(t *testing.T, v any) SubAgentResult {
	t.Helper()
	result, ok := v.(SubAgentResult)
	require.True(t, ok, "expected SubAgentResult, got %T", v)
	return result
}

func subAgentResultJSON(t *testing.T, result SubAgentResult) map[string]any {
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
	_, hasThread := decl.InputSchema.Properties[fieldSubAgentID]
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
// have side effects. Ordinary and resumable NewTool must each apply every
// supplied Option exactly once.
func TestNewTool_ApplyEachOptionOnce(t *testing.T) {
	t.Run("ordinary", func(t *testing.T) {
		calls := 0
		counting := Option(func(*agentToolOptions) { calls++ })
		at := NewTool(
			&threadHistoryAgent{name: "child"},
			counting,
			WithSkipSummarization(true),
		)
		require.Equal(t, 1, calls, "each Option must run exactly once")
		require.False(t, at.thread)
		require.Empty(t, at.threadNamespace)
		require.True(t, at.SkipSummarization())
	})
	t.Run("resumable", func(t *testing.T) {
		calls := 0
		counting := Option(func(*agentToolOptions) { calls++ })
		at := NewTool(
			&threadHistoryAgent{name: "child"},
			counting,
			WithResumableSubAgents(),
			WithSubAgentNamespace("child"),
			WithSkipSummarization(true),
		)
		require.Equal(t, 1, calls, "each Option must run exactly once")
		require.True(t, at.thread)
		require.Equal(t, "child", at.threadNamespace)
		require.True(t, at.SkipSummarization())
	})
}

// A namespace without WithResumableSubAgents is a configuration mistake, such
// as a forgotten option, so it fails loudly instead of being ignored. The
// namespace value does not matter, valid or not.
func TestWithSubAgentNamespace_RequiresResumableSubAgents(t *testing.T) {
	for _, namespace := range []string{"child", "not even valid /", ""} {
		calls := 0
		counting := Option(func(*agentToolOptions) { calls++ })
		msg := requireConfigPanic(t, func() {
			NewTool(
				&mockAgent{name: "plain", description: "plain agent"},
				counting,
				WithSubAgentNamespace(namespace),
			)
		})
		require.Equal(t,
			"Invalid AgentTool configuration: AgentTool[plain]: "+
				"WithSubAgentNamespace requires WithResumableSubAgents",
			msg, "namespace %q", namespace)
		require.Equal(t, 1, calls, "each Option must run exactly once")
	}
}

// --- Declaration -----------------------------------------------------------

func TestNewTool_ResumableSubAgents_DeclarationSchema(t *testing.T) {
	at := NewTool(&mockAgent{name: "math-specialist", description: "Math helper"}, WithResumableSubAgents())
	decl := at.Declaration()
	require.Equal(t, "math-specialist", decl.Name)
	require.Contains(t, decl.Description, fieldSubAgentID)
	require.Equal(t, []string{fieldInput}, decl.InputSchema.Required)
	require.Contains(t, decl.InputSchema.Properties, fieldSubAgentID)
	require.Equal(t, threadIDPattern, decl.InputSchema.Properties[fieldSubAgentID].Pattern)
	input := decl.InputSchema.Properties[fieldInput]
	require.NotNil(t, input)
	require.Equal(t, "object", input.Type)
	require.Contains(t, input.Properties, "request")
	// subagent_id can legitimately be absent, so only status is required.
	require.Equal(t, []string{fieldStatus}, decl.OutputSchema.Required)
	require.Equal(t, "string", decl.OutputSchema.Properties[fieldSubAgentID].Type)
	require.Equal(t, "string", decl.OutputSchema.Properties[fieldOutput].Type)
	require.Equal(t, "string", decl.OutputSchema.Properties[fieldError].Type)
	require.Equal(t, "boolean", decl.OutputSchema.Properties[fieldRetryable].Type)
	require.Equal(t, []any{subAgentStatusCompleted, subAgentStatusFailed},
		decl.OutputSchema.Properties[fieldStatus].Enum)

	// The model-facing field names are the documented contract.
	raw, err := json.Marshal(decl.InputSchema)
	require.NoError(t, err)
	require.Contains(t, string(raw), `"subagent_id"`)
	require.NotContains(t, string(raw), "thread")
}

// --- Core subagent behavior ------------------------------------------------

func TestNewTool_ResumableSubAgents_FirstCallReturnsIDAndPersistsHistory(t *testing.T) {
	child := &threadHistoryAgent{name: "child"}
	at := NewTool(child, WithResumableSubAgents())
	ctx, sess, _ := newThreadParent(t)

	out, err := at.Call(ctx, []byte(`{"input":{"request":"one"}}`))
	require.NoError(t, err)
	result := requireSubAgentResult(t, out)
	require.Equal(t, subAgentStatusCompleted, result.Status)
	require.NoError(t, validateThreadID(result.SubAgentID))
	require.Equal(t, "run1", result.Output)

	keys, ids, inputs := child.snapshot()
	require.Equal(t, []string{threadFilterKey("child", result.SubAgentID)}, keys)
	require.Equal(t, []string{result.SubAgentID}, ids)
	require.Equal(t, []string{`{"request":"one"}`}, inputs)
	require.True(t, sessionHasThreadEvents(sess, keys[0]))
}

func TestNewTool_ResumableSubAgents_ContinuationUsesPriorHistory(t *testing.T) {
	child := &threadHistoryAgent{name: "child"}
	at := NewTool(child, WithResumableSubAgents())
	ctx, _, _ := newThreadParent(t)

	first, err := at.Call(ctx, []byte(`{"input":{"request":"one"}}`))
	require.NoError(t, err)
	id := requireSubAgentResult(t, first).SubAgentID

	second, err := at.Call(ctx, []byte(
		fmt.Sprintf(`{"subagent_id":%q,"input":{"request":"two"}}`, id),
	))
	require.NoError(t, err)
	result := requireSubAgentResult(t, second)
	require.Equal(t, id, result.SubAgentID)
	require.Equal(t, "run1|run2", result.Output)
}

func TestNewTool_ResumableSubAgents_TwoIDsRemainIsolated(t *testing.T) {
	child := &threadHistoryAgent{name: "child"}
	at := NewTool(child, WithResumableSubAgents())
	ctx, _, _ := newThreadParent(t)

	a1, err := at.Call(ctx, []byte(`{"input":{"request":"A"}}`))
	require.NoError(t, err)
	idA := requireSubAgentResult(t, a1).SubAgentID
	b1, err := at.Call(ctx, []byte(`{"input":{"request":"B"}}`))
	require.NoError(t, err)
	idB := requireSubAgentResult(t, b1).SubAgentID
	require.NotEqual(t, idA, idB)

	a2, err := at.Call(ctx, []byte(
		fmt.Sprintf(`{"subagent_id":%q,"input":{"request":"A2"}}`, idA),
	))
	require.NoError(t, err)
	require.Equal(t, "run1|run3", requireSubAgentResult(t, a2).Output)

	b2, err := at.Call(ctx, []byte(
		fmt.Sprintf(`{"subagent_id":%q,"input":{"request":"B2"}}`, idB),
	))
	require.NoError(t, err)
	require.Equal(t, "run2|run4", requireSubAgentResult(t, b2).Output)

	keys, _, _ := child.snapshot()
	require.Equal(t, []string{
		threadFilterKey("child", idA),
		threadFilterKey("child", idB),
		threadFilterKey("child", idA),
		threadFilterKey("child", idB),
	}, keys)
}

func TestNewTool_ResumableSubAgents_UnknownAndInvalidID(t *testing.T) {
	child := &threadFailOnceAgent{name: "child"}
	at := NewTool(child, WithResumableSubAgents())
	ctx, sess, _ := newThreadParent(t)

	unknown, err := at.Call(ctx, []byte(
		`{"subagent_id":"abcd1234","input":{"request":"x"}}`,
	))
	require.NoError(t, err)
	got := requireSubAgentResult(t, unknown)
	require.Equal(t, subAgentStatusFailed, got.Status)
	require.Empty(t, got.SubAgentID, "an unknown ID must not be echoed back")
	require.Contains(t, got.Error, errUnknownSubAgentID)
	require.NotNil(t, got.Retryable)
	require.False(t, *got.Retryable)
	require.Empty(t, sessionFilterKeys(sess), "an unknown ID must not be created")
	require.Empty(t, child.messages(), "the wrapped agent must not run")

	for _, id := range []string{
		"../escape",
		"a/b",
		"a:b",
		"short",
		"has space",
		strings.Repeat("a", threadIDMaxLen+1),
	} {
		raw, marshalErr := json.Marshal(map[string]any{
			"subagent_id": id,
			"input":       map[string]string{"request": "x"},
		})
		require.NoError(t, marshalErr)
		out, callErr := at.Call(ctx, raw)
		require.NoError(t, callErr)
		result := requireSubAgentResult(t, out)
		require.Equal(t, subAgentStatusFailed, result.Status, "id %q", id)
		require.Empty(t, result.SubAgentID, "id %q", id)
		require.NotNil(t, result.Retryable, "id %q", id)
		require.False(t, *result.Retryable, "id %q", id)
		require.NotContains(t, result.Error, errUnknownSubAgentID, "id %q", id)
	}
	require.Empty(t, sessionFilterKeys(sess))
	require.Empty(t, child.messages())
}

// Malformed arguments fail before anything runs and never carry an ID.
func TestNewTool_ResumableSubAgents_MalformedArguments(t *testing.T) {
	child := &threadFailOnceAgent{name: "child"}
	at := NewTool(child, WithResumableSubAgents())
	ctx, sess, _ := newThreadParent(t)

	for _, tc := range []struct {
		args string
		want string
	}{
		{args: ``, want: "arguments are required"},
		{args: `not json`, want: "invalid arguments"},
		{args: `{}`, want: "input is required"},
		{args: `{"subagent_id":"abcd1234"}`, want: "input is required"},
		{args: `{"subagent_id":123,"input":{"request":"x"}}`, want: "invalid subagent_id"},
	} {
		out, err := at.Call(ctx, []byte(tc.args))
		require.NoError(t, err, "args %q", tc.args)
		result := requireSubAgentResult(t, out)
		require.Equal(t, subAgentStatusFailed, result.Status, "args %q", tc.args)
		require.Contains(t, result.Error, tc.want, "args %q", tc.args)
		require.Empty(t, result.SubAgentID, "args %q", tc.args)
		require.NotNil(t, result.Retryable, "args %q", tc.args)
		require.False(t, *result.Retryable, "args %q", tc.args)
	}
	require.Empty(t, sessionFilterKeys(sess))
	require.Empty(t, child.messages())
}

// An omitted, null, or blank subagent_id starts a new subagent.
func TestNewTool_ResumableSubAgents_BlankIDStartsNewSubAgent(t *testing.T) {
	child := &threadHistoryAgent{name: "child"}
	at := NewTool(child, WithResumableSubAgents())
	ctx, _, _ := newThreadParent(t)

	ids := make(map[string]struct{})
	for _, args := range []string{
		`{"input":{"request":"x"}}`,
		`{"subagent_id":null,"input":{"request":"x"}}`,
		`{"subagent_id":"","input":{"request":"x"}}`,
		`{"subagent_id":"   ","input":{"request":"x"}}`,
	} {
		out, err := at.Call(ctx, []byte(args))
		require.NoError(t, err)
		result := requireSubAgentResult(t, out)
		require.Equal(t, subAgentStatusCompleted, result.Status, "args %s", args)
		require.Equal(t, "run"+strconv.Itoa(len(ids)+1), result.Output,
			"args %s must not continue an earlier subagent", args)
		require.NoError(t, validateThreadID(result.SubAgentID))
		ids[result.SubAgentID] = struct{}{}
	}
	require.Len(t, ids, 4)
}

func TestNewTool_ResumableSubAgents_NoParentSession(t *testing.T) {
	at := NewTool(&threadHistoryAgent{name: "child"}, WithResumableSubAgents())
	out, err := at.Call(context.Background(), []byte(`{"input":{"request":"x"}}`))
	require.NoError(t, err)
	result := requireSubAgentResult(t, out)
	require.Equal(t, subAgentStatusFailed, result.Status)
	require.Contains(t, result.Error, "parent session is required")
	require.NotNil(t, result.Retryable)
	require.False(t, *result.Retryable)
	require.Empty(t, result.SubAgentID)
}

func TestNewTool_ResumableSubAgents_CustomInputPassedAsRawChildPayload(t *testing.T) {
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
	at := NewTool(child, WithResumableSubAgents())
	decl := at.Declaration()
	require.Contains(t, decl.InputSchema.Properties[fieldInput].Properties, "query")

	ctx, _, _ := newThreadParent(t)
	out, err := at.Call(ctx, []byte(`{"input":{"query":"hello"}}`))
	require.NoError(t, err)
	result := requireSubAgentResult(t, out)
	require.Equal(t, subAgentStatusCompleted, result.Status)
	require.Equal(t, `{"query":"hello"}`, child.lastMessage)
	require.Equal(t, result.SubAgentID, child.lastThread)
	require.Equal(t, threadFilterKey("custom", result.SubAgentID), child.lastKey)
}

// References in the wrapped agent's schema resolve from the document root, so
// its definitions must move to the root of the wrapper schema.
func TestNewTool_ResumableSubAgents_CustomInputDefinitionsStayResolvable(t *testing.T) {
	child := &threadSchemaAgent{
		name: "custom",
		inputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"item": map[string]any{"$ref": "#/$defs/item"},
			},
			"$defs": map[string]any{
				"item": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"name": map[string]any{"type": "string"},
					},
				},
			},
		},
	}
	at := NewTool(child, WithResumableSubAgents())
	decl := at.Declaration()
	require.Contains(t, decl.InputSchema.Defs, "item")
	input := decl.InputSchema.Properties[fieldInput]
	require.Empty(t, input.Defs)
	require.Equal(t, "#/$defs/item", input.Properties["item"].Ref)

	// The ordinary declaration of the same agent is unchanged.
	plain := NewTool(child).Declaration()
	require.Contains(t, plain.InputSchema.Defs, "item")

	ctx, _, _ := newThreadParent(t)
	out, err := at.Call(ctx, []byte(`{"input":{"item":{"name":"x"}}}`))
	require.NoError(t, err)
	require.Equal(t, subAgentStatusCompleted, requireSubAgentResult(t, out).Status)
	require.Equal(t, `{"item":{"name":"x"}}`, child.lastMessage)
}

func TestNewTool_ResumableSubAgents_CustomInputRootReferencesPreserveValidation(t *testing.T) {
	tests := []struct {
		name    string
		schema  map[string]any
		valid   map[string]any
		invalid map[string]any
	}{
		{
			name: "properties items and additional properties",
			schema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"value": map[string]any{"type": "number"},
					"copy":  map[string]any{"$ref": "#/properties/value"},
					"list": map[string]any{
						"type": "array", "items": map[string]any{"$ref": "#/properties/value"},
					},
					"dict": map[string]any{
						"type": "object", "additionalProperties": map[string]any{"$ref": "#/properties/value"},
					},
				},
			},
			valid:   map[string]any{"value": 1.0, "copy": 2.0, "list": []any{3.0}, "dict": map[string]any{"x": 4.0}},
			invalid: map[string]any{"copy": "not a number"},
		},
		{
			name: "recursive root",
			schema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"value": map[string]any{"type": "string"},
					"child": map[string]any{"$ref": "#"},
				},
			},
			valid:   map[string]any{"child": map[string]any{"value": "nested"}},
			invalid: map[string]any{"child": map[string]any{"value": 1.0}},
		},
		{
			name: "definition referring back to root property",
			schema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"value": map[string]any{"type": "number"},
					"item":  map[string]any{"$ref": "#/$defs/item"},
				},
				"$defs": map[string]any{
					"item": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"copy": map[string]any{"$ref": "#/properties/value"},
						},
					},
				},
			},
			valid:   map[string]any{"item": map[string]any{"copy": 1.0}},
			invalid: map[string]any{"item": map[string]any{"copy": "not a number"}},
		},
		{
			name: "reference member inside enum is ordinary data",
			schema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"literal": map[string]any{
						"enum": []any{map[string]any{"$ref": "#/properties/value"}},
					},
				},
			},
			valid:   map[string]any{"literal": map[string]any{"$ref": "#/properties/value"}},
			invalid: map[string]any{"literal": map[string]any{"$ref": "#/properties/other"}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			child := &threadSchemaAgent{name: "custom", inputSchema: tt.schema}
			plain := NewTool(child).Declaration().InputSchema
			before, err := json.Marshal(plain)
			require.NoError(t, err)
			wrapped := NewTool(child, WithResumableSubAgents()).Declaration().InputSchema
			after, err := json.Marshal(NewTool(child).Declaration().InputSchema)
			require.NoError(t, err)
			require.JSONEq(t, string(before), string(after), "ordinary declaration must remain unchanged")

			compiler := jsonschema.NewCompiler()
			for _, decl := range []struct {
				url     string
				schema  *tool.Schema
				valid   any
				invalid any
			}{
				{"https://example.com/plain.json", plain, tt.valid, tt.invalid},
				{"https://example.com/wrapped.json", wrapped, map[string]any{"input": tt.valid}, map[string]any{"input": tt.invalid}},
			} {
				encoded, err := json.Marshal(decl.schema)
				require.NoError(t, err)
				var doc any
				require.NoError(t, json.Unmarshal(encoded, &doc))
				require.NoError(t, compiler.AddResource(decl.url, doc))
				compiled, err := compiler.Compile(decl.url)
				require.NoError(t, err)
				require.NoError(t, compiled.Validate(decl.valid), decl.url)
				require.Error(t, compiled.Validate(decl.invalid), decl.url)
			}
		})
	}
}

// Output is always text, even when the wrapped agent declares an object output
// schema: the model receives the text as a JSON string, not as an object.
func TestNewTool_ResumableSubAgents_OutputAlwaysStringDespiteObjectSchema(t *testing.T) {
	child := &threadObjectOutputAgent{name: "child"}
	at := NewTool(child, WithResumableSubAgents())
	output := at.Declaration().OutputSchema.Properties[fieldOutput]
	require.NotNil(t, output)
	require.Equal(t, "string", output.Type)

	ctx, _, _ := newThreadParent(t)
	out, err := at.Call(ctx, []byte(`{"input":{"request":"hi"}}`))
	require.NoError(t, err)
	result := requireSubAgentResult(t, out)
	require.Equal(t, subAgentStatusCompleted, result.Status)
	require.Equal(t, `{"answer":"42"}`, result.Output)
	require.Equal(t, `{"answer":"42"}`, subAgentResultJSON(t, result)[fieldOutput])
	require.Equal(t, `{"request":"hi"}`, child.lastMessage)
}

// --- Subagent ID confirmation ----------------------------------------------

// A failure before the child persists anything must not hand back an ID: there
// is no subagent to continue, and the caller simply starts a new one.
func TestNewTool_ResumableSubAgents_PreEventRunErrorReturnsNoSubAgentID(t *testing.T) {
	child := &threadFailOnceAgent{name: "child"}
	at := NewTool(child, WithResumableSubAgents())
	ctx, sess, parent := newThreadParent(t)

	first, err := at.Call(ctx, []byte(`{"input":{"request":"first"}}`))
	require.NoError(t, err)
	failed := requireSubAgentResult(t, first)
	require.Equal(t, subAgentStatusFailed, failed.Status)
	require.Contains(t, failed.Error, "first run failed")
	require.Empty(t, failed.SubAgentID, "an unconfirmed subagent must not be advertised")
	require.Empty(t, sessionFilterKeys(sess), "no event may be persisted")

	// Retrying starts a fresh subagent and the failed request is never replayed.
	second, err := at.Call(ctx, []byte(`{"input":{"request":"retry"}}`))
	require.NoError(t, err)
	ok := requireSubAgentResult(t, second)
	require.Equal(t, subAgentStatusCompleted, ok.Status)
	require.NoError(t, validateThreadID(ok.SubAgentID))
	require.Equal(t, "retry-ok", ok.Output)

	childKey := threadFilterKey("child", ok.SubAgentID)
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

// Once the child stream starts, ensureUserMessageForCall saves the call's
// input, so a later failure still returns a continuable ID.
func TestNewTool_ResumableSubAgents_ErrorAfterStreamStartsKeepsConfirmedSubAgentID(t *testing.T) {
	at := NewTool(&threadErrorAgent{name: "child"}, WithResumableSubAgents())
	ctx, sess, _ := newThreadParent(t)

	out, err := at.Call(ctx, []byte(`{"input":{"request":"boom"}}`))
	require.NoError(t, err)
	result := requireSubAgentResult(t, out)
	require.Equal(t, subAgentStatusFailed, result.Status)
	require.NoError(t, validateThreadID(result.SubAgentID))
	require.Contains(t, result.Error, "child failed")
	// An ordinary child failure is neither known-transient nor known-permanent.
	require.Nil(t, result.Retryable)

	childKey := threadFilterKey("child", result.SubAgentID)
	require.True(t, sessionHasThreadEvents(sess, childKey))

	again, err := at.Call(ctx, []byte(
		fmt.Sprintf(`{"subagent_id":%q,"input":{"request":"retry"}}`, result.SubAgentID),
	))
	require.NoError(t, err)
	retry := requireSubAgentResult(t, again)
	require.Equal(t, result.SubAgentID, retry.SubAgentID,
		"a confirmed subagent stays addressable after a failed call")
	require.Equal(t, subAgentStatusFailed, retry.Status)
}

// A call that fails before it can check the parent Session returns no ID, even
// for a subagent that exists: an ID is only reported once confirmed.
func TestNewTool_ResumableSubAgents_FlushFailureReturnsNoSubAgentID(t *testing.T) {
	child := &threadHistoryAgent{name: "child"}
	at := NewTool(child, WithResumableSubAgents())
	ctx, _, parent := newThreadParent(t)

	out, err := at.Call(ctx, []byte(`{"input":{"request":"one"}}`))
	require.NoError(t, err)
	id := requireSubAgentResult(t, out).SubAgentID
	require.NoError(t, validateThreadID(id))

	// Nobody serves this flush channel, so a canceled call cannot flush.
	flush.Attach(ctx, parent, make(chan *flush.FlushRequest))
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	out, err = at.Call(canceled, []byte(
		fmt.Sprintf(`{"subagent_id":%q,"input":{"request":"two"}}`, id),
	))
	require.NoError(t, err)
	result := requireSubAgentResult(t, out)
	require.Equal(t, subAgentStatusFailed, result.Status)
	require.Contains(t, result.Error, "flush parent invocation session")
	require.Empty(t, result.SubAgentID)
	require.NotNil(t, result.Retryable)
	require.True(t, *result.Retryable)
	_, ids, _ := child.snapshot()
	require.Len(t, ids, 1, "the wrapped agent must not run")
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

// The core design claim is that a subagent is nothing but parent Session events
// under a stable filter key. So a subagent must survive losing every
// in-process object: the Tool, the wrapped agent, the Invocation, and the
// Session value itself. Only the persisted events and the namespace carry over.
func TestNewTool_ResumableSubAgents_ContinuesSubAgentAfterSessionReload(t *testing.T) {
	service := sessioninmemory.NewSessionService()
	key := session.Key{AppName: "app", UserID: "user", SessionID: "session"}
	created, err := service.CreateSession(context.Background(), key, nil)
	require.NoError(t, err)

	firstCtx, _ := newPersistedThreadParent(t, service, created)
	firstAgent := &threadHistoryAgent{name: "child"}
	out, err := NewTool(firstAgent, WithResumableSubAgents()).Call(
		firstCtx, []byte(`{"input":{"request":"one"}}`),
	)
	require.NoError(t, err)
	first := requireSubAgentResult(t, out)
	require.Equal(t, subAgentStatusCompleted, first.Status)
	require.Equal(t, "run1", first.Output)
	threadID := first.SubAgentID
	require.NoError(t, validateThreadID(threadID))

	// Reload the parent session from the service into a fresh value, dropping
	// every object the first call used.
	reloaded, err := service.GetSession(context.Background(), key)
	require.NoError(t, err)
	require.NotNil(t, reloaded)
	require.NotSame(t, created, reloaded, "the reload must not reuse the value")
	childKey := threadFilterKey("child", threadID)
	require.Equal(t, "agenttool:child:thread:"+threadID, childKey,
		"the persisted key layout is a compatibility contract")
	require.True(t, sessionHasThreadEvents(reloaded, childKey),
		"the subagent must be recoverable from persisted events alone")

	secondCtx, _ := newPersistedThreadParent(t, service, reloaded)
	// A fresh Tool and a fresh wrapped agent: same identity, no shared state.
	secondAgent := &threadHistoryAgent{name: "child"}
	out, err = NewTool(secondAgent, WithResumableSubAgents()).Call(secondCtx, []byte(
		fmt.Sprintf(`{"subagent_id":%q,"input":{"request":"two"}}`, threadID),
	))
	require.NoError(t, err)
	second := requireSubAgentResult(t, out)
	require.Equal(t, subAgentStatusCompleted, second.Status)
	require.Equal(t, threadID, second.SubAgentID)
	// The fresh agent's own counter restarts at 1, so "run1|run1" can only come
	// from the reloaded subagent history.
	require.Equal(t, "run1|run1", second.Output)

	keys, ids, inputs := secondAgent.snapshot()
	require.Equal(t, []string{childKey}, keys)
	require.Equal(t, []string{threadID}, ids)
	require.Equal(t, []string{`{"request":"two"}`}, inputs)

	// Both calls are persisted under the same subagent key, and the parent
	// conversation is untouched.
	final, err := service.GetSession(context.Background(), key)
	require.NoError(t, err)
	require.Equal(t,
		[]string{`{"request":"one"}`, `{"request":"two"}`},
		sessionUserContents(final, childKey),
	)
	for _, got := range sessionFilterKeys(final) {
		require.Equal(t, childKey, got,
			"a subagent call must not write outside its history key")
	}

	// An unknown ID still fails after a reload rather than creating a subagent.
	out, err = NewTool(&threadHistoryAgent{name: "child"}, WithResumableSubAgents()).Call(
		secondCtx, []byte(`{"subagent_id":"abcd1234","input":{"request":"x"}}`),
	)
	require.NoError(t, err)
	unknown := requireSubAgentResult(t, out)
	require.Equal(t, subAgentStatusFailed, unknown.Status)
	require.Contains(t, unknown.Error, errUnknownSubAgentID)
	require.Empty(t, unknown.SubAgentID)
}

// --- Nested agent tools ----------------------------------------------------

// A nested ordinary AgentTool keeps its own per-call history key. The
// enclosing subagent ID stays visible to it, but its history never lands in
// the enclosing subagent.
func TestNewTool_ResumableSubAgents_NestedAgentToolKeepsOwnFilterKey(t *testing.T) {
	nestedChild := &threadKeyRecordingAgent{name: "nested-child"}
	outer := &threadNestingAgent{name: "outer", nested: NewTool(nestedChild)}
	at := NewTool(outer, WithResumableSubAgents())
	ctx, sess, _ := newThreadParent(t)

	out, err := at.Call(ctx, []byte(`{"input":{"request":"go"}}`))
	require.NoError(t, err)
	result := requireSubAgentResult(t, out)
	require.Equal(t, subAgentStatusCompleted, result.Status)

	threadKey := threadFilterKey("outer", result.SubAgentID)
	ownKeys, callErr := outer.snapshot()
	require.NoError(t, callErr)
	require.Equal(t, []string{threadKey}, ownKeys,
		"the wrapped agent runs on the subagent history key")

	nestedKeys := nestedChild.filterKeys()
	require.Len(t, nestedKeys, 1)
	require.NotEqual(t, threadKey, nestedKeys[0],
		"a nested ordinary AgentTool must not inherit the subagent history key")
	require.True(t, strings.HasPrefix(nestedKeys[0], "nested-child-"),
		"nested key %q must be the ordinary per-call key", nestedKeys[0])
	require.Equal(t, []string{result.SubAgentID}, nestedChild.subAgentIDs(),
		"the nearest enclosing subagent ID stays visible")

	// The nested sub-agent's events must not land on the subagent key either.
	for _, key := range sessionFilterKeys(sess) {
		if key == threadKey {
			continue
		}
		require.True(t, strings.HasPrefix(key, "nested-child-"),
			"unexpected filter key %q in session", key)
	}
}

// Inside a nested resumable AgentTool the accessor reports the nested
// subagent, and the enclosing ID is visible again once the nested call ends.
func TestNewTool_ResumableSubAgents_NestedResumableToolReportsNearestID(t *testing.T) {
	innerChild := &threadKeyRecordingAgent{name: "inner"}
	outer := &threadNestingAgent{
		name:       "outer",
		nested:     NewTool(innerChild, WithResumableSubAgents()),
		nestedArgs: `{"input":{"request":"nested"}}`,
	}
	at := NewTool(outer, WithResumableSubAgents())
	ctx, _, _ := newThreadParent(t)

	out, err := at.Call(ctx, []byte(`{"input":{"request":"go"}}`))
	require.NoError(t, err)
	outerResult := requireSubAgentResult(t, out)
	require.Equal(t, subAgentStatusCompleted, outerResult.Status)

	before, after, nestedOut := outer.subAgentIDs()
	require.Equal(t, []string{outerResult.SubAgentID}, before)
	require.Equal(t, []string{outerResult.SubAgentID}, after)
	innerResult := requireSubAgentResult(t, nestedOut)
	require.Equal(t, subAgentStatusCompleted, innerResult.Status)
	require.NotEqual(t, outerResult.SubAgentID, innerResult.SubAgentID)
	require.Equal(t, []string{innerResult.SubAgentID}, innerChild.subAgentIDs())
	require.Equal(t,
		[]string{threadFilterKey("inner", innerResult.SubAgentID)},
		innerChild.filterKeys(),
	)
}

func TestSubAgentIDFromContext_OutsideCall(t *testing.T) {
	_, ok := SubAgentIDFromContext(nil)
	require.False(t, ok)
	_, ok = SubAgentIDFromContext(context.Background())
	require.False(t, ok)
	_, ok = SubAgentIDFromContext(contextWithSubAgentID(context.Background(), ""))
	require.False(t, ok)
}

func TestNewTool_ResumableSubAgents_NestedAgentToolKeepsOwnFilterKeyViaGraphRuntime(t *testing.T) {
	nestedChild := &threadKeyRecordingAgent{name: "nested-child"}
	outer := &threadNestingAgent{name: "outer", nested: NewTool(nestedChild)}
	at := NewTool(outer, WithResumableSubAgents())
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
	result := requireSubAgentResult(t, out)
	require.Equal(t, subAgentStatusCompleted, result.Status)

	threadKey := threadFilterKey("outer", result.SubAgentID)
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

func TestNewTool_ResumableSubAgents_GraphRuntimeUsesEnvelopeNotCheckpoint(t *testing.T) {
	child := &threadHistoryAgent{name: "child"}
	at := NewTool(child, WithResumableSubAgents())
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
	result := requireSubAgentResult(t, out)
	require.Equal(t, subAgentStatusCompleted, result.Status)
	require.Equal(t, "run1", result.Output)
	require.NoError(t, validateThreadID(result.SubAgentID))

	keys, ids, inputs := child.snapshot()
	require.Equal(t, []string{threadFilterKey("child", result.SubAgentID)}, keys)
	require.NotContains(t, keys, "graph-should-not-win")
	require.Equal(t, []string{result.SubAgentID}, ids)
	require.Equal(t, []string{`{"request":"graph-input"}`}, inputs)
}

func TestNewTool_ResumableSubAgents_GraphInterruptIsUnsupportedAndNotRetryable(t *testing.T) {
	child := &threadRunErrorAgent{
		name: "child",
		err:  graph.NewInterruptError("needs approval"),
	}
	at := NewTool(child, WithResumableSubAgents())
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
	result := requireSubAgentResult(t, out)
	require.Equal(t, subAgentStatusFailed, result.Status)
	require.Equal(t, errGraphInterrupt, result.Error)
	require.NotNil(t, result.Retryable)
	require.False(t, *result.Retryable, "rerunning cannot clear an interrupt")
	require.Empty(t, result.SubAgentID, "nothing was saved before the interrupt")
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
// still fail the call. Reporting it as completed would tell the model the
// subagent answered when it did not.
func TestNewTool_ResumableSubAgents_GraphAgentEventSignalledInterruptFails(t *testing.T) {
	at := NewTool(newThreadInterruptGraphAgent(t, "graph-child"), WithResumableSubAgents())
	ctx, sess, _ := newThreadParent(t)

	out, err := at.Call(ctx, []byte(`{"input":{"request":"approve"}}`))
	require.NoError(t, err)
	result := requireSubAgentResult(t, out)
	require.Equal(t, subAgentStatusFailed, result.Status)
	require.Equal(t, errGraphInterrupt, result.Error)
	require.NotNil(t, result.Retryable)
	require.False(t, *result.Retryable, "rerunning cannot clear an interrupt")
	require.Empty(t, result.Output)
	// The call's input was saved before the interrupt, so the ID is confirmed.
	require.True(t, sessionHasThreadEvents(sess,
		threadFilterKey("graph-child", result.SubAgentID)))
}

// The same interrupt on the graph runtime entrypoint must not be reported as a
// resumable graph interrupt either: the parent graph has no child checkpoint.
func TestNewTool_ResumableSubAgents_GraphAgentEventSignalledInterruptViaGraphRuntime(t *testing.T) {
	at := NewTool(newThreadInterruptGraphAgent(t, "graph-child"), WithResumableSubAgents())
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
	result := requireSubAgentResult(t, out)
	require.Equal(t, subAgentStatusFailed, result.Status)
	require.Equal(t, errGraphInterrupt, result.Error)
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
// them, even though a subagent call re-enables the events internally.
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

// A caller that turned graph executor events off must still get a failed
// result. The interrupt is signalled only through those events, so a subagent
// call re-enables them for the child run and keeps them out of the shared
// session.
func TestNewTool_ResumableSubAgents_GraphAgentInterruptFailsWhenCallerDisabledExecutorEvents(t *testing.T) {
	at := NewTool(newThreadInterruptGraphAgent(t, "graph-child"), WithResumableSubAgents())
	ctx, sess, parent := newThreadParentRunOptions(
		t, coreagent.WithDisableGraphExecutorEvents(true),
	)
	require.True(t, coreagent.IsGraphExecutorEventsDisabled(parent))

	out, err := at.Call(ctx, []byte(`{"input":{"request":"approve"}}`))
	require.NoError(t, err)
	result := requireSubAgentResult(t, out)
	require.Equal(t, subAgentStatusFailed, result.Status)
	require.Equal(t, errGraphInterrupt, result.Error)
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
	// The subagent's own user message legitimately remains.
	require.Equal(t,
		[]string{`{"request":"approve"}`},
		sessionUserContents(sess, threadFilterKey("graph-child", result.SubAgentID)),
	)
}

// Re-enabling executor events must not change what the shared session ends up
// holding for a subagent call that completes: the graph completion snapshot is
// the record of the child's result, so suppression must not swallow it.
func TestNewTool_ResumableSubAgents_GraphAgentCompletionSnapshotSurvivesEventSuppression(t *testing.T) {
	const answer = "graph-answer"
	run := func(t *testing.T, opts ...coreagent.RunOption) session.StateMap {
		t.Helper()
		at := NewTool(newThreadAnswerGraphAgent(t, "graph-child", answer), WithResumableSubAgents())
		ctx, sess, _ := newThreadParentRunOptions(t, opts...)
		out, err := at.Call(ctx, []byte(`{"input":{"request":"ask"}}`))
		require.NoError(t, err)
		result := requireSubAgentResult(t, out)
		require.Equal(t, subAgentStatusCompleted, result.Status)
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

func TestNewTool_ResumableSubAgents_GraphInterruptOnCallPath(t *testing.T) {
	at := NewTool(&threadRunErrorAgent{
		name: "child",
		err:  fmt.Errorf("wrapped: %w", graph.NewInterruptError("needs approval")),
	}, WithResumableSubAgents())
	ctx, _, _ := newThreadParent(t)

	out, err := at.Call(ctx, []byte(`{"input":{"request":"approve"}}`))
	require.NoError(t, err)
	result := requireSubAgentResult(t, out)
	require.Equal(t, subAgentStatusFailed, result.Status)
	require.Equal(t, errGraphInterrupt, result.Error)
	require.NotNil(t, result.Retryable)
	require.False(t, *result.Retryable)
}

// --- Retryable classification and result serialization ---------------------

func TestNewTool_ResumableSubAgents_RetryableSerialization(t *testing.T) {
	ctx, _, _ := newThreadParent(t)

	t.Run("completed omits retryable and error", func(t *testing.T) {
		at := NewTool(&threadHistoryAgent{name: "child"}, WithResumableSubAgents())
		out, err := at.Call(ctx, []byte(`{"input":{"request":"hi"}}`))
		require.NoError(t, err)
		decoded := subAgentResultJSON(t, requireSubAgentResult(t, out))
		require.Equal(t, subAgentStatusCompleted, decoded[fieldStatus])
		require.NotContains(t, decoded, fieldRetryable)
		require.NotContains(t, decoded, fieldError)
		require.Contains(t, decoded, fieldSubAgentID)
	})

	t.Run("permanent validation failure reports false", func(t *testing.T) {
		at := NewTool(&threadHistoryAgent{name: "child"}, WithResumableSubAgents())
		out, err := at.Call(ctx, []byte(`{"subagent_id":"abcd1234","input":{"request":"x"}}`))
		require.NoError(t, err)
		decoded := subAgentResultJSON(t, requireSubAgentResult(t, out))
		require.Equal(t, false, decoded[fieldRetryable])
		require.NotContains(t, decoded, fieldSubAgentID)
		require.NotContains(t, decoded, fieldOutput)
	})

	t.Run("completed with empty output omits output", func(t *testing.T) {
		at := NewTool(&threadEmptyOutputAgent{name: "child"}, WithResumableSubAgents())
		out, err := at.Call(ctx, []byte(`{"input":{"request":"hi"}}`))
		require.NoError(t, err)
		result := requireSubAgentResult(t, out)
		require.Equal(t, subAgentStatusCompleted, result.Status)
		require.Empty(t, result.Output)
		require.NoError(t, validateThreadID(result.SubAgentID),
			"the saved input alone confirms the subagent")
		decoded := subAgentResultJSON(t, result)
		require.NotContains(t, decoded, fieldOutput)
		require.NotContains(t, decoded, fieldError)
	})

	t.Run("unknown child failure omits retryable", func(t *testing.T) {
		at := NewTool(&threadErrorAgent{name: "child"}, WithResumableSubAgents())
		out, err := at.Call(ctx, []byte(`{"input":{"request":"boom"}}`))
		require.NoError(t, err)
		decoded := subAgentResultJSON(t, requireSubAgentResult(t, out))
		require.Equal(t, subAgentStatusFailed, decoded[fieldStatus])
		require.NotContains(t, decoded, fieldRetryable)
	})

	t.Run("cancellation reports true and omits subagent_id", func(t *testing.T) {
		at := NewTool(&threadRunErrorAgent{
			name: "child",
			err:  fmt.Errorf("child aborted: %w", context.Canceled),
		}, WithResumableSubAgents())
		out, err := at.Call(ctx, []byte(`{"input":{"request":"x"}}`))
		require.NoError(t, err)
		result := requireSubAgentResult(t, out)
		require.Empty(t, result.SubAgentID)
		decoded := subAgentResultJSON(t, result)
		require.Equal(t, true, decoded[fieldRetryable])
		require.NotContains(t, decoded, fieldSubAgentID)
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

func TestNewTool_ResumableSubAgents_NamespaceDefaultsToWrappedAgentName(t *testing.T) {
	at := NewTool(&threadHistoryAgent{name: "child"}, WithResumableSubAgents())
	require.Equal(t, "child", at.threadNamespace)
	require.Equal(t, "agenttool:child:thread:abcd1234",
		threadFilterKey(at.threadNamespace, "abcd1234"))
	require.NotContains(t, threadFilterKey(at.threadNamespace, "abcd1234"), "/")
}

// NewTool ignores WithName, so the namespace must follow the wrapped agent
// rather than any tool-name override.
func TestNewTool_ResumableSubAgents_NamespaceIgnoresWithName(t *testing.T) {
	at := NewTool(&threadHistoryAgent{name: "child"}, WithResumableSubAgents(), WithName("renamed"))
	require.Equal(t, "child", at.threadNamespace)
	require.Equal(t, "child", at.Declaration().Name)
}

func TestNewTool_ResumableSubAgents_ExplicitNamespaceSurvivesAgentRename(t *testing.T) {
	const namespace = "billing-specialist"
	before := NewTool(
		&threadHistoryAgent{name: "old-name"}, WithResumableSubAgents(),
		WithSubAgentNamespace(namespace),
	)
	after := NewTool(
		&threadHistoryAgent{name: "new-name"}, WithResumableSubAgents(),
		WithSubAgentNamespace(namespace),
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
// namespaces, and the subagent of one is unknown to the other.
func TestNewTool_ResumableSubAgents_SameAgentNameDistinctNamespacesStayIsolated(t *testing.T) {
	first := NewTool(
		&threadHistoryAgent{name: "shared"}, WithResumableSubAgents(),
		WithSubAgentNamespace("first"),
	)
	second := NewTool(
		&threadHistoryAgent{name: "shared"}, WithResumableSubAgents(),
		WithSubAgentNamespace("second"),
	)
	ctx, _, _ := newThreadParent(t)

	out, err := first.Call(ctx, []byte(`{"input":{"request":"a"}}`))
	require.NoError(t, err)
	created := requireSubAgentResult(t, out)
	require.Equal(t, subAgentStatusCompleted, created.Status)

	crossed, err := second.Call(ctx, []byte(
		fmt.Sprintf(`{"subagent_id":%q,"input":{"request":"b"}}`, created.SubAgentID),
	))
	require.NoError(t, err)
	result := requireSubAgentResult(t, crossed)
	require.Equal(t, subAgentStatusFailed, result.Status)
	require.Contains(t, result.Error, errUnknownSubAgentID)
	require.Empty(t, result.SubAgentID)
}

func TestNewTool_ResumableSubAgents_SameAgentNameSharedNamespaceSharesSubAgents(t *testing.T) {
	first := NewTool(&threadHistoryAgent{name: "shared"}, WithResumableSubAgents())
	second := NewTool(&threadHistoryAgent{name: "shared"}, WithResumableSubAgents())
	ctx, _, _ := newThreadParent(t)

	out, err := first.Call(ctx, []byte(`{"input":{"request":"a"}}`))
	require.NoError(t, err)
	created := requireSubAgentResult(t, out)

	crossed, err := second.Call(ctx, []byte(
		fmt.Sprintf(`{"subagent_id":%q,"input":{"request":"b"}}`, created.SubAgentID),
	))
	require.NoError(t, err)
	require.Equal(t, subAgentStatusCompleted, requireSubAgentResult(t, crossed).Status)
}

// Namespaces are used exactly as given. Even surrounding spaces are rejected
// rather than trimmed, so two configured values never share one key.
func TestNewTool_ResumableSubAgents_InvalidNamespacePanics(t *testing.T) {
	for _, namespace := range []string{
		"", "  ", " child", "child ", "has space", "a/b", "a:b", "café",
	} {
		requireConfigPanic(t, func() {
			NewTool(
				&threadHistoryAgent{name: "child"}, WithResumableSubAgents(),
				WithSubAgentNamespace(namespace),
			)
		}, "Invalid AgentTool configuration: AgentTool[child]: subagent namespace",
			fmt.Sprintf("%q", namespace))
	}
}

func TestNewTool_ResumableSubAgents_UnusableAgentNamePanicsInsteadOfRewriting(t *testing.T) {
	require.Panics(t, func() {
		NewTool(&threadHistoryAgent{name: "team/child"}, WithResumableSubAgents())
	})
	// The escape hatch is an explicit namespace, never a lossy rewrite.
	require.NotPanics(t, func() {
		NewTool(
			&threadHistoryAgent{name: "team/child"}, WithResumableSubAgents(),
			WithSubAgentNamespace("team_child"),
		)
	})
}

// --- Concurrency -----------------------------------------------------------

// Two calls for one subagent must not run the child concurrently.
func TestNewTool_ResumableSubAgents_SameIDSerialized(t *testing.T) {
	child := &threadGateAgent{name: "child", entered: make(chan struct{}, 4)}
	at := NewTool(child, WithResumableSubAgents())
	ctx, sess, _ := newThreadParent(t)

	// Create the subagent first: an ID is only addressable once persisted.
	out, err := at.Call(ctx, []byte(`{"input":{"request":"one"}}`))
	require.NoError(t, err)
	created := requireSubAgentResult(t, out)
	require.Equal(t, subAgentStatusCompleted, created.Status)
	threadID := created.SubAgentID
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
		`{"subagent_id":%q,"input":{"request":"concurrent"}}`, threadID,
	))
	for i := 0; i < 2; i++ {
		go func() {
			raw, callErr := at.Call(ctx, args)
			results <- outcome{raw: raw, err: callErr}
		}()
	}

	// One call is inside Run; the other must be parked on the subagent lock.
	<-child.entered
	lockKey := threadLockKey(sess, threadID)
	require.Eventually(t, func() bool {
		return threadLockWaiters(at.threadLocks, lockKey) >= 2
	}, eventuallyTimeout, time.Millisecond,
		"second call must queue on the per-subagent lock")

	close(gate)
	for i := 0; i < 2; i++ {
		got := <-results
		require.NoError(t, got.err)
		result := requireSubAgentResult(t, got.raw)
		require.Equal(t, subAgentStatusCompleted, result.Status)
		require.Equal(t, threadID, result.SubAgentID)
	}

	runs, maxActive := child.stats()
	require.Equal(t, 3, runs)
	require.Equal(t, 1, maxActive, "same-subagent calls must not overlap")
	require.Eventually(t, func() bool {
		return threadLockWaiters(at.threadLocks, lockKey) == 0
	}, eventuallyTimeout, time.Millisecond, "lock entry must be released")
}

// A call waiting for another call of the same subagent gives up when its
// context is done. It never runs the wrapped agent, and it still reports the
// subagent ID because that subagent is confirmed.
func TestNewTool_ResumableSubAgents_SameIDWaitCanceled(t *testing.T) {
	child := &threadGateAgent{name: "child", entered: make(chan struct{}, 4)}
	at := NewTool(child, WithResumableSubAgents())
	ctx, sess, _ := newThreadParent(t)

	out, err := at.Call(ctx, []byte(`{"input":{"request":"one"}}`))
	require.NoError(t, err)
	threadID := requireSubAgentResult(t, out).SubAgentID
	require.NoError(t, validateThreadID(threadID))
	<-child.entered

	gate := make(chan struct{})
	child.setGate(gate)
	args := []byte(fmt.Sprintf(
		`{"subagent_id":%q,"input":{"request":"two"}}`, threadID,
	))
	holder := make(chan any, 1)
	go func() {
		raw, _ := at.Call(ctx, args)
		holder <- raw
	}()
	<-child.entered

	waitCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	waiter := make(chan any, 1)
	go func() {
		raw, _ := at.Call(waitCtx, args)
		waiter <- raw
	}()
	lockKey := threadLockKey(sess, threadID)
	require.Eventually(t, func() bool {
		return threadLockWaiters(at.threadLocks, lockKey) >= 2
	}, eventuallyTimeout, time.Millisecond,
		"the second call must queue on the per-subagent lock")
	cancel()

	canceled := requireSubAgentResult(t, <-waiter)
	require.Equal(t, subAgentStatusFailed, canceled.Status)
	require.Contains(t, canceled.Error, context.Canceled.Error())
	require.Equal(t, threadID, canceled.SubAgentID)
	require.NotNil(t, canceled.Retryable)
	require.True(t, *canceled.Retryable)

	close(gate)
	held := requireSubAgentResult(t, <-holder)
	require.Equal(t, subAgentStatusCompleted, held.Status)
	runs, _ := child.stats()
	require.Equal(t, 2, runs, "the canceled call must not run the wrapped agent")
}

// Distinct subagents are independent and may run at the same time.
func TestNewTool_ResumableSubAgents_DistinctIDsRunConcurrently(t *testing.T) {
	child := &threadGateAgent{name: "child", entered: make(chan struct{}, 4)}
	at := NewTool(child, WithResumableSubAgents())
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
		result := requireSubAgentResult(t, got.raw)
		require.Equal(t, subAgentStatusCompleted, result.Status)
		ids[result.SubAgentID] = struct{}{}
	}
	require.Len(t, ids, 2, "each call must create its own subagent")

	_, maxActive := child.stats()
	require.Equal(t, 2, maxActive, "distinct subagents must not serialize")
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

func TestNewTool_ResumableSubAgents_StreamableCallEmitsSingleFinalChunk(t *testing.T) {
	at := NewTool(&threadHistoryAgent{name: "child"}, WithResumableSubAgents(), WithStreamInner(true))
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
	result := requireSubAgentResult(t, final.Result)
	require.Equal(t, subAgentStatusCompleted, result.Status)
	require.Equal(t, "run1", result.Output)
	require.NoError(t, validateThreadID(result.SubAgentID))
}

// --- Option interactions ---------------------------------------------------

func requireConfigPanic(t *testing.T, fn func(), parts ...string) string {
	t.Helper()
	var text string
	func() {
		defer func() {
			recovered := recover()
			require.NotNil(t, recovered, "expected configuration panic")
			text = fmt.Sprint(recovered)
		}()
		fn()
	}()
	for _, part := range parts {
		require.Contains(t, text, part)
	}
	return text
}

func TestNewTool_ResumableSubAgents_RejectsIncompatibleOptions(t *testing.T) {
	child := &threadHistoryAgent{name: "child"}
	keyFunc := PersistentHistoryKeyFunc(func(
		context.Context, *coreagent.Invocation, []byte,
	) string {
		return "k"
	})
	const prefix = "Invalid AgentTool configuration: AgentTool[child]: " +
		"WithResumableSubAgents is incompatible with "
	cases := []struct {
		name     string
		opts     []Option
		conflict string
	}{
		{
			name: "resumable then parent branch",
			opts: []Option{
				WithResumableSubAgents(),
				WithHistoryScope(HistoryScopeParentBranch),
			},
			conflict: "HistoryScopeParentBranch",
		},
		{
			name: "parent branch then resumable",
			opts: []Option{
				WithHistoryScope(HistoryScopeParentBranch),
				WithResumableSubAgents(),
			},
			conflict: "HistoryScopeParentBranch",
		},
		{
			name:     "resumable then persistent history",
			opts:     []Option{WithResumableSubAgents(), WithPersistentHistory()},
			conflict: "WithPersistentHistory",
		},
		{
			name:     "persistent history then resumable",
			opts:     []Option{WithPersistentHistory(), WithResumableSubAgents()},
			conflict: "WithPersistentHistory",
		},
		{
			name: "resumable then persistent key",
			opts: []Option{
				WithResumableSubAgents(),
				WithPersistentHistoryKey("agenttool:child:task"),
			},
			conflict: "WithPersistentHistoryKey",
		},
		{
			name: "persistent key then resumable",
			opts: []Option{
				WithPersistentHistoryKey("agenttool:child:task"),
				WithResumableSubAgents(),
			},
			conflict: "WithPersistentHistoryKey",
		},
		{
			name:     "resumable then key func",
			opts:     []Option{WithResumableSubAgents(), WithPersistentHistoryKeyFunc(keyFunc)},
			conflict: "WithPersistentHistoryKeyFunc",
		},
		{
			name:     "key func then resumable",
			opts:     []Option{WithPersistentHistoryKeyFunc(keyFunc), WithResumableSubAgents()},
			conflict: "WithPersistentHistoryKeyFunc",
		},
		{
			name: "explicit namespace does not hide a conflict",
			opts: []Option{
				WithSubAgentNamespace("child"),
				WithPersistentHistory(),
				WithResumableSubAgents(),
			},
			conflict: "WithPersistentHistory",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			opts := append(
				[]Option{Option(func(*agentToolOptions) { calls++ })},
				tc.opts...,
			)
			msg := requireConfigPanic(t, func() {
				NewTool(child, opts...)
			})
			require.Equal(t, prefix+tc.conflict, msg)
			require.Equal(t, 1, calls, "each Option must run exactly once")
		})
	}
}

func TestNewTool_ResumableSubAgents_PanicsBeforePersistentHistoryNormalization(t *testing.T) {
	original := agentlog.Default
	logger := &dynTestWarnLogger{}
	agentlog.Default = logger
	t.Cleanup(func() {
		agentlog.Default = original
	})

	cases := []struct {
		name string
		opts []Option
		want string
	}{
		{
			name: "persistent history then parent branch then resumable",
			opts: []Option{
				WithPersistentHistory(),
				WithHistoryScope(HistoryScopeParentBranch),
				WithResumableSubAgents(),
			},
			want: "HistoryScopeParentBranch and WithPersistentHistory",
		},
		{
			name: "resumable then parent branch then persistent history",
			opts: []Option{
				WithResumableSubAgents(),
				WithHistoryScope(HistoryScopeParentBranch),
				WithPersistentHistory(),
			},
			want: "HistoryScopeParentBranch and WithPersistentHistory",
		},
		{
			name: "parent branch then persistent key then resumable",
			opts: []Option{
				WithHistoryScope(HistoryScopeParentBranch),
				WithPersistentHistoryKey("agenttool:child:task"),
				WithResumableSubAgents(),
			},
			want: "HistoryScopeParentBranch and WithPersistentHistoryKey",
		},
		{
			name: "resumable then key func then parent branch",
			opts: []Option{
				WithResumableSubAgents(),
				WithPersistentHistoryKeyFunc(func(
					context.Context, *coreagent.Invocation, []byte,
				) string {
					return "k"
				}),
				WithHistoryScope(HistoryScopeParentBranch),
			},
			want: "HistoryScopeParentBranch and WithPersistentHistoryKeyFunc",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			logger.warnfCalls = 0
			msg := requireConfigPanic(t, func() {
				NewTool(&threadHistoryAgent{name: "child"}, tc.opts...)
			}, "Invalid AgentTool configuration: AgentTool[child]: "+
				"WithResumableSubAgents is incompatible with ")
			require.True(t, strings.HasSuffix(msg, tc.want), msg)
			require.Zero(t, logger.warnfCalls, "panic must happen before normalization warns: %s", msg)
		})
	}
}

func TestNewTool_ResumableSubAgents_AllowsExplicitIsolatedHistory(t *testing.T) {
	for _, opts := range [][]Option{
		{WithResumableSubAgents(), WithHistoryScope(HistoryScopeIsolated)},
		{WithHistoryScope(HistoryScopeIsolated), WithResumableSubAgents()},
	} {
		at := NewTool(&threadHistoryAgent{name: "child"}, opts...)
		require.True(t, at.thread)
		require.Equal(t, HistoryScopeIsolated, at.historyScope)
		require.Nil(t, at.persistentHistory)
	}
}

func TestNewTool_WithoutResumableSubAgents_KeepsHistoryOptions(t *testing.T) {
	branched := NewTool(
		&threadHistoryAgent{name: "child"},
		WithHistoryScope(HistoryScopeParentBranch),
	)
	require.False(t, branched.thread)
	require.Empty(t, branched.threadNamespace)
	require.Nil(t, branched.threadLocks)
	require.Equal(t, HistoryScopeParentBranch, branched.historyScope)
	require.Nil(t, branched.persistentHistory)
	_, hasSubAgentID := branched.Declaration().InputSchema.Properties[fieldSubAgentID]
	require.False(t, hasSubAgentID)

	keyed := NewTool(
		&threadHistoryAgent{name: "child"},
		WithPersistentHistoryKey("agenttool:child:task-1"),
	)
	require.False(t, keyed.thread)
	require.NotNil(t, keyed.persistentHistory)
	require.True(t, keyed.persistentHistory.enabled)
	require.Equal(t, "agenttool:child:task-1", keyed.persistentHistory.key)
	_, hasSubAgentID = keyed.Declaration().InputSchema.Properties[fieldSubAgentID]
	require.False(t, hasSubAgentID)
}

func TestNewDynamicTool_RejectsResumableSubAgentOptions(t *testing.T) {
	const (
		resumable = "Invalid Dynamic AgentTool configuration: " +
			"WithResumableSubAgents is not supported by NewDynamicTool"
		namespace = "Invalid Dynamic AgentTool configuration: " +
			"WithSubAgentNamespace is not supported by NewDynamicTool"
	)
	for _, tc := range []struct {
		opts []Option
		want string
	}{
		{opts: []Option{WithResumableSubAgents()}, want: resumable},
		{opts: []Option{WithName("explore"), WithResumableSubAgents()}, want: resumable},
		{opts: []Option{WithResumableSubAgents(), WithName("explore")}, want: resumable},
		{
			opts: []Option{WithResumableSubAgents(), WithSubAgentNamespace("explore")},
			want: resumable,
		},
		{opts: []Option{WithSubAgentNamespace("explore")}, want: namespace},
		{opts: []Option{WithSubAgentNamespace("not valid /")}, want: namespace},
	} {
		calls := 0
		withCount := append(
			[]Option{Option(func(*agentToolOptions) { calls++ })},
			tc.opts...,
		)
		msg := requireConfigPanic(t, func() {
			NewDynamicTool(withCount...)
		})
		require.Equal(t, tc.want, msg)
		require.Equal(t, 1, calls, "each Option must run exactly once")
	}
}
