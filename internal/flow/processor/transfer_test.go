//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package processor

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	itransfer "trpc.group/trpc-go/trpc-agent-go/internal/transfer"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

const (
	testControllerRejectErr = "blocked"
	testNodeTimeout         = 10 * time.Second
)

// mockAgent minimal implementation for transfer tests.
type mockAgent struct {
	name             string
	emit             bool
	gotEndInvocation bool
	gotTraceNodeID   string
	gotSurfaceRoot   string
	gotMessage       model.Message
	invoked          bool
}

func (m *mockAgent) Info() agent.Info                { return agent.Info{Name: m.name} }
func (m *mockAgent) SubAgents() []agent.Agent        { return nil }
func (m *mockAgent) FindSubAgent(string) agent.Agent { return nil }
func (m *mockAgent) Tools() []tool.Tool              { return nil }
func (m *mockAgent) Run(ctx context.Context, inv *agent.Invocation) (<-chan *event.Event, error) {
	ch := make(chan *event.Event, 1)
	go func() {
		defer close(ch)
		// Record whether the invocation was incorrectly marked as ended.
		m.invoked = true
		m.gotEndInvocation = inv.EndInvocation
		m.gotTraceNodeID = agent.InvocationTraceNodeID(inv)
		m.gotSurfaceRoot = agent.InvocationSurfaceRootNodeID(inv)
		m.gotMessage = inv.Message
		if m.emit {
			ch <- event.New(inv.InvocationID, m.name)
		}
	}()
	return ch, nil
}

// parentAgent implements FindSubAgent
type parentAgent struct{ child agent.Agent }

func (p *parentAgent) Info() agent.Info         { return agent.Info{Name: "parent"} }
func (p *parentAgent) SubAgents() []agent.Agent { return []agent.Agent{p.child} }
func (p *parentAgent) FindSubAgent(name string) agent.Agent {
	if p.child != nil && p.child.Info().Name == name {
		return p.child
	}
	return nil
}
func (p *parentAgent) Tools() []tool.Tool { return nil }
func (p *parentAgent) Run(ctx context.Context, inv *agent.Invocation) (<-chan *event.Event, error) {
	ch := make(chan *event.Event)
	close(ch)
	return ch, nil
}

func TestTransferResponseProc_Successful(t *testing.T) {
	target := &mockAgent{name: "child", emit: true}
	parent := &parentAgent{child: target}

	inv := &agent.Invocation{
		Agent:        parent,
		AgentName:    "parent",
		InvocationID: "inv",
		TransferInfo: &agent.TransferInfo{TargetAgentName: "child", Message: "hi"},
	}

	rsp := &model.Response{ID: "r1", Created: time.Now().Unix(), Model: "m"}

	out := make(chan *event.Event, 10)
	proc := NewTransferResponseProcessor(true)
	proc.ProcessResponse(context.Background(), inv, &model.Request{}, rsp, out)
	close(out)

	// Expect transfer event + child event
	evts := []*event.Event{}
	for e := range out {
		evts = append(evts, e)
	}
	require.Len(t, evts, 3)
	require.Equal(t, model.ObjectTypeTransfer, evts[0].Object)
	require.Equal(t, "child", evts[1].Author)
}

func TestTransferResponseProc_EmptyMessageDoesNotEchoInheritedInput(t *testing.T) {
	target := &mockAgent{name: "child"}
	parent := &parentAgent{child: target}
	inv := &agent.Invocation{
		Agent:        parent,
		AgentName:    "parent",
		InvocationID: "inv-empty-message",
		Message:      model.NewUserMessage("original user input"),
		TransferInfo: &agent.TransferInfo{TargetAgentName: "child"},
	}
	rsp := &model.Response{ID: "r-empty-message", Created: time.Now().Unix(), Model: "m"}
	out := make(chan *event.Event, 10)
	NewTransferResponseProcessor(true).ProcessResponse(context.Background(), inv, &model.Request{}, rsp, out)
	close(out)
	var evts []*event.Event
	for e := range out {
		evts = append(evts, e)
	}
	require.Len(t, evts, 1)
	require.Equal(t, model.ObjectTypeTransfer, evts[0].Object)
	require.Equal(t, model.RoleUser, target.gotMessage.Role)
	require.Equal(t, "original user input", target.gotMessage.Content)
}

func TestTransferResponseProc_CustomizerReceivesRawEmptyTransferMessage(t *testing.T) {
	target := &mockAgent{name: "child"}
	parent := &parentAgent{child: target}
	var gotTransferMessage string
	var gotTransferMessageOK bool
	inv := &agent.Invocation{
		Agent:        parent,
		AgentName:    "parent",
		InvocationID: "inv-empty-customize",
		Message:      model.NewUserMessage("original user input"),
		RunOptions: agent.RunOptions{
			RuntimeState: map[string]any{
				agent.RuntimeStateKeyTransferController: customizeTransferController{
					message:            model.NewUserMessage("custom child input"),
					transferMessage:    &gotTransferMessage,
					hasTransferMessage: &gotTransferMessageOK,
				},
			},
		},
		TransferInfo: &agent.TransferInfo{TargetAgentName: "child"},
	}
	rsp := &model.Response{ID: "r-empty-customize", Created: time.Now().Unix(), Model: "m"}
	out := make(chan *event.Event, 10)
	NewTransferResponseProcessor(true).ProcessResponse(context.Background(), inv, &model.Request{}, rsp, out)
	close(out)
	for range out {
	}
	require.True(t, gotTransferMessageOK)
	require.Empty(t, gotTransferMessage)
	require.Equal(t, "custom child input", target.gotMessage.Content)
}

func TestTransferResponseProc_Target404(t *testing.T) {
	parent := &parentAgent{child: nil}
	inv := &agent.Invocation{Agent: parent, AgentName: "parent", InvocationID: "inv", TransferInfo: &agent.TransferInfo{TargetAgentName: "missing"}}
	rsp := &model.Response{ID: "r"}
	out := make(chan *event.Event, 1)
	NewTransferResponseProcessor(true).ProcessResponse(context.Background(), inv, &model.Request{}, rsp, out)
	close(out)
	evt := <-out
	require.NotNil(t, evt.Error)
	require.Equal(t, model.ErrorTypeFlowError, evt.Error.Type)
	require.Nil(t, inv.TransferInfo)
}

type rejectTransferController struct{}

func (rejectTransferController) OnTransfer(
	context.Context,
	string,
	string,
) (time.Duration, error) {
	return 0, errors.New(testControllerRejectErr)
}

func TestTransferResponseProc_ControllerRejects(t *testing.T) {
	target := &mockAgent{name: "child", emit: true}
	parent := &parentAgent{child: target}

	inv := &agent.Invocation{
		Agent:        parent,
		AgentName:    "parent",
		InvocationID: "inv-ctrl",
		RunOptions: agent.RunOptions{
			RuntimeState: map[string]any{
				agent.RuntimeStateKeyTransferController: rejectTransferController{},
			},
		},
		TransferInfo: &agent.TransferInfo{TargetAgentName: "child"},
	}

	rsp := &model.Response{ID: "r-ctrl"}

	out := make(chan *event.Event, 10)
	NewTransferResponseProcessor(true).ProcessResponse(
		context.Background(),
		inv,
		&model.Request{},
		rsp,
		out,
	)
	close(out)

	evts := []*event.Event{}
	for e := range out {
		evts = append(evts, e)
	}
	require.Len(t, evts, 1)
	require.NotNil(t, evts[0].Error)
	require.Nil(t, inv.TransferInfo)
}

func TestTransferResponseProc_UsesSwarmRootTraceNodeIDForSiblingTransfer(t *testing.T) {
	target := &mockAgent{name: "beta", emit: true}
	parent := &parentAgent{child: target}
	inv := &agent.Invocation{
		Agent:        parent,
		AgentName:    "alpha",
		InvocationID: "inv-swarm",
		Session: &session.Session{
			State: session.StateMap{
				swarmTeamNameKey: []byte("swarm"),
			},
		},
		TransferInfo: &agent.TransferInfo{TargetAgentName: "beta", Message: "hi"},
	}
	agent.WithInvocationTraceNodeID("swarm/alpha")(inv)
	rsp := &model.Response{ID: "r-swarm"}
	out := make(chan *event.Event, 10)
	NewTransferResponseProcessor(true).ProcessResponse(context.Background(), inv, &model.Request{}, rsp, out)
	close(out)
	for range out {
	}
	require.Equal(t, "swarm/beta", target.gotTraceNodeID)
}

func TestTransferResponseProc_UsesMountedSwarmTraceRootForSiblingTransfer(t *testing.T) {
	target := &mockAgent{name: "beta", emit: true}
	parent := &parentAgent{child: target}
	inv := &agent.Invocation{
		Agent:        parent,
		AgentName:    "alpha",
		InvocationID: "inv-nested-swarm",
		Session: &session.Session{
			State: session.StateMap{
				swarmTeamNameKey:    []byte("swarm"),
				swarmTraceNodeIDKey: []byte("workflow/swarm"),
			},
		},
		TransferInfo: &agent.TransferInfo{TargetAgentName: "beta", Message: "hi"},
	}
	agent.WithInvocationTraceNodeID("workflow/swarm/alpha")(inv)
	rsp := &model.Response{ID: "r-nested-swarm"}
	out := make(chan *event.Event, 10)
	NewTransferResponseProcessor(true).ProcessResponse(context.Background(), inv, &model.Request{}, rsp, out)
	close(out)
	for range out {
	}
	require.Equal(t, "workflow/swarm/beta", target.gotTraceNodeID)
}

func TestTransferResponseProc_ReplacesMountedSurfaceRootForSiblingTransfer(t *testing.T) {
	target := &mockAgent{name: "beta", emit: true}
	parent := &parentAgent{child: target}
	inv := &agent.Invocation{
		Agent:        parent,
		AgentName:    "alpha",
		InvocationID: "inv-surface-swarm",
		Session: &session.Session{
			State: session.StateMap{
				swarmTeamNameKey:    []byte("swarm"),
				swarmTraceNodeIDKey: []byte("workflow/swarm"),
			},
		},
		TransferInfo: &agent.TransferInfo{TargetAgentName: "beta", Message: "hi"},
	}
	agent.WithInvocationTraceNodeID("workflow/swarm/alpha")(inv)
	agent.SetInvocationSurfaceRootNodeID(inv, "workflow/team/alpha")
	rsp := &model.Response{ID: "r-surface-swarm"}
	out := make(chan *event.Event, 10)
	NewTransferResponseProcessor(true).ProcessResponse(context.Background(), inv, &model.Request{}, rsp, out)
	close(out)
	for range out {
	}
	require.Equal(t, "workflow/swarm/beta", target.gotTraceNodeID)
	require.Equal(t, "workflow/team/beta", target.gotSurfaceRoot)
}

func TestTransferResponseProc_FallsBackToMountedSwarmTraceRootWhenSessionLacksStoredTraceRoot(t *testing.T) {
	target := &mockAgent{name: "beta", emit: true}
	parent := &parentAgent{child: target}
	inv := &agent.Invocation{
		Agent:        parent,
		AgentName:    "alpha",
		InvocationID: "inv-old-nested-swarm",
		Session: &session.Session{
			State: session.StateMap{
				swarmTeamNameKey: []byte("swarm"),
			},
		},
		TransferInfo: &agent.TransferInfo{TargetAgentName: "beta", Message: "hi"},
	}
	agent.WithInvocationTraceNodeID("workflow/swarm/alpha")(inv)
	rsp := &model.Response{ID: "r-old-nested-swarm"}
	out := make(chan *event.Event, 10)
	NewTransferResponseProcessor(true).ProcessResponse(context.Background(), inv, &model.Request{}, rsp, out)
	close(out)
	for range out {
	}
	require.Equal(t, "workflow/swarm/beta", target.gotTraceNodeID)
}

func TestTransferTargetTraceNodeID_NilAndParentFallbackBranches(t *testing.T) {
	require.Empty(t, transferTargetTraceNodeID(nil, &mockAgent{name: "beta"}))
	require.Empty(t, transferTargetTraceNodeID(&agent.Invocation{}, nil))
	require.Empty(t, transferTargetSurfaceRootNodeID(nil, &mockAgent{name: "beta"}))
	require.Empty(t, transferTargetSurfaceRootNodeID(&agent.Invocation{}, nil))
	require.Empty(t, parentTraceNodeID(""))
	require.Empty(t, parentTraceNodeID("swarm"))
}

type deadlineAgent struct {
	name        string
	gotDeadline bool
}

func (d *deadlineAgent) Info() agent.Info {
	return agent.Info{Name: d.name}
}

func (d *deadlineAgent) SubAgents() []agent.Agent { return nil }

func (d *deadlineAgent) FindSubAgent(string) agent.Agent { return nil }

func (d *deadlineAgent) Tools() []tool.Tool { return nil }

func (d *deadlineAgent) Run(
	ctx context.Context,
	inv *agent.Invocation,
) (<-chan *event.Event, error) {
	_, d.gotDeadline = ctx.Deadline()
	ch := make(chan *event.Event, 1)
	go func() {
		defer close(ch)
		ch <- event.New(inv.InvocationID, d.name)
	}()
	return ch, nil
}

type timeoutTransferController struct {
	timeout time.Duration
}

func (t timeoutTransferController) OnTransfer(
	context.Context,
	string,
	string,
) (time.Duration, error) {
	return t.timeout, nil
}

type customizeTransferController struct {
	message            model.Message
	err                error
	transferMessage    *string
	hasTransferMessage *bool
}

func (c customizeTransferController) OnTransfer(
	context.Context,
	string,
	string,
) (time.Duration, error) {
	return 0, nil
}

func (c customizeTransferController) CustomizeTransferInvocation(
	ctx context.Context,
	_ *agent.Invocation,
	target *agent.Invocation,
) error {
	transferMessage, ok := itransfer.TransferMessageFromContext(ctx)
	if c.transferMessage != nil {
		*c.transferMessage = transferMessage
	}
	if c.hasTransferMessage != nil {
		*c.hasTransferMessage = ok
	}
	if c.err != nil {
		return c.err
	}
	target.Message = c.message
	return nil
}

type structuredOutputCaptureModel struct {
	name       string
	mu         sync.Mutex
	invoked    bool
	seen       bool
	schemaName string
}

func (m *structuredOutputCaptureModel) Run(
	_ context.Context,
	inv *agent.Invocation,
) (<-chan *event.Event, error) {
	if inv != nil && inv.StructuredOutput == nil {
		inv.StructuredOutput = inv.RunOptions.StructuredOutput
		inv.StructuredOutputType = inv.RunOptions.StructuredOutputType
	}
	m.mu.Lock()
	m.invoked = true
	if inv != nil &&
		inv.StructuredOutput != nil &&
		inv.StructuredOutput.JSONSchema != nil {
		m.seen = true
		m.schemaName = inv.StructuredOutput.JSONSchema.Name
	}
	m.mu.Unlock()
	ch := make(chan *event.Event)
	close(ch)
	return ch, nil
}

func (m *structuredOutputCaptureModel) Tools() []tool.Tool { return nil }

func (m *structuredOutputCaptureModel) Info() agent.Info {
	return agent.Info{Name: m.name}
}

func (m *structuredOutputCaptureModel) SubAgents() []agent.Agent { return nil }

func (m *structuredOutputCaptureModel) FindSubAgent(string) agent.Agent { return nil }

func (m *structuredOutputCaptureModel) Snapshot() (bool, bool, string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.invoked, m.seen, m.schemaName
}

func TestTransferResponseProc_ControllerNodeTimeout(t *testing.T) {
	target := &deadlineAgent{name: "child"}
	parent := &parentAgent{child: target}

	inv := &agent.Invocation{
		Agent:        parent,
		AgentName:    "parent",
		InvocationID: "inv-timeout",
		RunOptions: agent.RunOptions{
			RuntimeState: map[string]any{
				agent.RuntimeStateKeyTransferController: timeoutTransferController{
					timeout: testNodeTimeout,
				},
			},
		},
		TransferInfo: &agent.TransferInfo{TargetAgentName: "child"},
	}

	rsp := &model.Response{ID: "r-timeout"}

	out := make(chan *event.Event, 10)
	NewTransferResponseProcessor(true).ProcessResponse(
		context.Background(),
		inv,
		&model.Request{},
		rsp,
		out,
	)
	close(out)

	for range out {
	}
	require.True(t, target.gotDeadline)
}

func TestTransferResponseProc_CustomizesTargetInvocation(t *testing.T) {
	target := &mockAgent{name: "child"}
	parent := &parentAgent{child: target}
	inv := &agent.Invocation{
		Agent:        parent,
		AgentName:    "parent",
		InvocationID: "inv-customize",
		RunOptions: agent.RunOptions{
			RuntimeState: map[string]any{
				agent.RuntimeStateKeyTransferController: customizeTransferController{
					message: model.NewUserMessage("custom child input"),
				},
			},
		},
		TransferInfo: &agent.TransferInfo{TargetAgentName: "child", Message: "parent transfer"},
	}
	rsp := &model.Response{ID: "r-customize"}
	out := make(chan *event.Event, 10)
	NewTransferResponseProcessor(true).ProcessResponse(
		context.Background(),
		inv,
		&model.Request{},
		rsp,
		out,
	)
	close(out)
	var echoedInput string
	for evt := range out {
		if evt != nil && evt.Author == "child" && evt.Tag == event.TransferTag && evt.Response != nil {
			require.NotEmpty(t, evt.Response.Choices)
			echoedInput = evt.Response.Choices[0].Message.Content
		}
	}
	require.Equal(t, model.RoleUser, target.gotMessage.Role)
	require.Equal(t, "custom child input", target.gotMessage.Content)
	require.Equal(t, "custom child input", echoedInput)
}

func TestTransferResponseProc_CustomizerRejects(t *testing.T) {
	target := &mockAgent{name: "child"}
	parent := &parentAgent{child: target}
	inv := &agent.Invocation{
		Agent:        parent,
		AgentName:    "parent",
		InvocationID: "inv-customize-reject",
		RunOptions: agent.RunOptions{
			RuntimeState: map[string]any{
				agent.RuntimeStateKeyTransferController: customizeTransferController{
					err: errors.New("custom rejected"),
				},
			},
		},
		TransferInfo: &agent.TransferInfo{TargetAgentName: "child", Message: "parent transfer"},
	}
	rsp := &model.Response{ID: "r-customize-reject"}
	out := make(chan *event.Event, 10)
	NewTransferResponseProcessor(true).ProcessResponse(
		context.Background(),
		inv,
		&model.Request{},
		rsp,
		out,
	)
	close(out)
	var errorEvent *event.Event
	var transferEvents int
	for evt := range out {
		if evt != nil && evt.Tag == event.TransferTag {
			transferEvents++
		}
		if evt != nil && evt.Error != nil {
			errorEvent = evt
		}
	}
	require.NotNil(t, errorEvent)
	require.Contains(t, errorEvent.Error.Message, "custom rejected")
	require.Zero(t, transferEvents)
	require.Nil(t, inv.TransferInfo)
	require.False(t, target.invoked)
}

func TestTransferResponseProc_PreservesRunStructuredOutput(t *testing.T) {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"status": map[string]any{"type": "string"},
		},
	}
	target := &structuredOutputCaptureModel{name: "child"}
	parent := &parentAgent{child: target}
	runOpts := agent.RunOptions{}
	agent.WithStructuredOutputJSONSchema(
		"run_output",
		schema,
		true,
		"Return one object.",
	)(&runOpts)
	inv := agent.NewInvocation(
		agent.WithInvocationAgent(parent),
		agent.WithInvocationID("inv-run-structured-output"),
		agent.WithInvocationRunOptions(runOpts),
		agent.WithInvocationTransferInfo(&agent.TransferInfo{TargetAgentName: "child"}),
	)
	rsp := &model.Response{ID: "r-run-structured-output"}
	out := make(chan *event.Event, 10)
	NewTransferResponseProcessor(true).ProcessResponse(
		context.Background(),
		inv,
		&model.Request{},
		rsp,
		out,
	)
	close(out)
	for range out {
	}
	invoked, seen, schemaName := target.Snapshot()
	require.True(t, invoked)
	require.True(t, seen)
	require.Equal(t, "run_output", schemaName)
}

func TestTransferResponseProc_DoesNotPropagateInvocationStructuredOutputWithoutRunOption(t *testing.T) {
	target := &structuredOutputCaptureModel{name: "child"}
	parent := &parentAgent{child: target}
	inv := agent.NewInvocation(
		agent.WithInvocationAgent(parent),
		agent.WithInvocationID("inv-static-structured-output"),
		agent.WithInvocationStructuredOutput(&model.StructuredOutput{
			Type: model.StructuredOutputJSONSchema,
			JSONSchema: &model.JSONSchemaConfig{
				Name:   "static_output",
				Schema: map[string]any{"type": "object"},
			},
		}),
		agent.WithInvocationTransferInfo(&agent.TransferInfo{TargetAgentName: "child"}),
	)
	rsp := &model.Response{ID: "r-static-structured-output"}
	out := make(chan *event.Event, 10)
	NewTransferResponseProcessor(true).ProcessResponse(
		context.Background(),
		inv,
		&model.Request{},
		rsp,
		out,
	)
	close(out)
	for range out {
	}
	invoked, seen, _ := target.Snapshot()
	require.True(t, invoked)
	require.False(t, seen)
}

func TestTransferResponseProc_SetsTransferTags(t *testing.T) {
	target := &mockAgent{name: "child", emit: true}
	parent := &parentAgent{child: target}

	inv := &agent.Invocation{
		Agent:        parent,
		AgentName:    "parent",
		InvocationID: "inv-tag",
		TransferInfo: &agent.TransferInfo{TargetAgentName: "child", Message: "hi"},
	}
	rsp := &model.Response{ID: "r-tag", Created: time.Now().Unix(), Model: "m"}

	out := make(chan *event.Event, 10)
	NewTransferResponseProcessor(true).ProcessResponse(context.Background(), inv, &model.Request{}, rsp, out)
	close(out)

	transferTagCount := 0
	for evt := range out {
		if evt.Tag == event.TransferTag {
			transferTagCount++
		}
	}

	require.GreaterOrEqual(t, transferTagCount, 2)
}

type doneResponseAgent struct {
	name string
}

func (d *doneResponseAgent) Info() agent.Info                { return agent.Info{Name: d.name} }
func (d *doneResponseAgent) SubAgents() []agent.Agent        { return nil }
func (d *doneResponseAgent) FindSubAgent(string) agent.Agent { return nil }
func (d *doneResponseAgent) Tools() []tool.Tool              { return nil }
func (d *doneResponseAgent) Run(_ context.Context, inv *agent.Invocation) (<-chan *event.Event, error) {
	ch := make(chan *event.Event, 1)
	go func() {
		defer close(ch)
		ch <- event.NewResponseEvent(
			inv.InvocationID,
			d.name,
			&model.Response{Done: true},
		)
	}()
	return ch, nil
}

type errorResponseAgent struct {
	name string
}

func (d *errorResponseAgent) Info() agent.Info                { return agent.Info{Name: d.name} }
func (d *errorResponseAgent) SubAgents() []agent.Agent        { return nil }
func (d *errorResponseAgent) FindSubAgent(string) agent.Agent { return nil }
func (d *errorResponseAgent) Tools() []tool.Tool              { return nil }
func (d *errorResponseAgent) Run(_ context.Context, inv *agent.Invocation) (<-chan *event.Event, error) {
	ch := make(chan *event.Event, 1)
	go func() {
		defer close(ch)
		ch <- event.NewErrorEvent(
			inv.InvocationID,
			d.name,
			model.ErrorTypeFlowError,
			"target failed",
		)
	}()
	return ch, nil
}

type nonTerminalErrorThenDoneAgent struct {
	name string
}

func (d *nonTerminalErrorThenDoneAgent) Info() agent.Info { return agent.Info{Name: d.name} }
func (d *nonTerminalErrorThenDoneAgent) SubAgents() []agent.Agent {
	return nil
}
func (d *nonTerminalErrorThenDoneAgent) FindSubAgent(string) agent.Agent { return nil }
func (d *nonTerminalErrorThenDoneAgent) Tools() []tool.Tool              { return nil }
func (d *nonTerminalErrorThenDoneAgent) Run(_ context.Context, inv *agent.Invocation) (<-chan *event.Event, error) {
	ch := make(chan *event.Event, 2)
	go func() {
		defer close(ch)
		ch <- event.NewResponseEvent(
			inv.InvocationID,
			d.name,
			&model.Response{
				Error: &model.ResponseError{
					Type:    model.ErrorTypeFlowError,
					Message: "recoverable target signal",
				},
			},
		)
		ch <- event.NewResponseEvent(
			inv.InvocationID,
			d.name,
			&model.Response{Done: true},
		)
	}()
	return ch, nil
}

type forwardedDoneResponseAgent struct {
	name string
}

func (d *forwardedDoneResponseAgent) Info() agent.Info { return agent.Info{Name: d.name} }
func (d *forwardedDoneResponseAgent) SubAgents() []agent.Agent {
	return nil
}
func (d *forwardedDoneResponseAgent) FindSubAgent(string) agent.Agent { return nil }
func (d *forwardedDoneResponseAgent) Tools() []tool.Tool              { return nil }
func (d *forwardedDoneResponseAgent) Run(_ context.Context, inv *agent.Invocation) (<-chan *event.Event, error) {
	ch := make(chan *event.Event, 2)
	go func() {
		defer close(ch)
		ch <- event.NewResponseEvent(
			inv.InvocationID,
			d.name,
			&model.Response{Done: true},
		)
		descendant := event.NewResponseEvent(
			"descendant-invocation",
			"descendant",
			&model.Response{Done: true},
		)
		descendant.ParentInvocationID = inv.InvocationID
		ch <- descendant
	}()
	return ch, nil
}

type descendantOnlyDoneResponseAgent struct {
	name string
}

func (d *descendantOnlyDoneResponseAgent) Info() agent.Info { return agent.Info{Name: d.name} }
func (d *descendantOnlyDoneResponseAgent) SubAgents() []agent.Agent {
	return nil
}
func (d *descendantOnlyDoneResponseAgent) FindSubAgent(string) agent.Agent { return nil }
func (d *descendantOnlyDoneResponseAgent) Tools() []tool.Tool              { return nil }
func (d *descendantOnlyDoneResponseAgent) Run(_ context.Context, inv *agent.Invocation) (<-chan *event.Event, error) {
	ch := make(chan *event.Event, 1)
	go func() {
		defer close(ch)
		descendant := event.NewResponseEvent(
			"descendant-invocation",
			"descendant",
			&model.Response{Done: true},
		)
		descendant.ParentInvocationID = inv.InvocationID
		ch <- descendant
	}()
	return ch, nil
}

type nestedDelegationResponseAgent struct {
	name string
}

func (d *nestedDelegationResponseAgent) Info() agent.Info { return agent.Info{Name: d.name} }
func (d *nestedDelegationResponseAgent) SubAgents() []agent.Agent {
	return nil
}
func (d *nestedDelegationResponseAgent) FindSubAgent(string) agent.Agent { return nil }
func (d *nestedDelegationResponseAgent) Tools() []tool.Tool              { return nil }
func (d *nestedDelegationResponseAgent) Run(_ context.Context, inv *agent.Invocation) (<-chan *event.Event, error) {
	ch := make(chan *event.Event, 2)
	go func() {
		defer close(ch)
		ch <- event.New(
			inv.InvocationID,
			d.name,
			event.WithObject(model.ObjectTypeTransfer),
			event.WithTag(event.TransferTag),
		)
		descendant := event.NewResponseEvent(
			"descendant-invocation",
			"descendant",
			&model.Response{Done: true},
		)
		descendant.ParentInvocationID = inv.InvocationID
		ch <- descendant
	}()
	return ch, nil
}

type recordingTransferCompletionObserver struct {
	sourceAgent string
	targetAgent string
	count       int
	done        bool
}

func (h *recordingTransferCompletionObserver) OnTransferComplete(
	_ context.Context,
	source *agent.Invocation,
	target *agent.Invocation,
	targetEvent *event.Event,
) {
	if source != nil {
		h.sourceAgent = source.AgentName
	}
	if target != nil {
		h.targetAgent = target.AgentName
	}
	h.count++
	h.done = targetEvent != nil && targetEvent.Response != nil && targetEvent.Response.Done
}

func TestTransferResponseProc_CompletionHandlerFromController(t *testing.T) {
	target := &doneResponseAgent{name: "child"}
	parent := &parentAgent{child: target}
	handler := &completionInvocationCustomizer{}
	inv := &agent.Invocation{
		Agent:        parent,
		AgentName:    "parent",
		InvocationID: "inv-completion",
		RunOptions: agent.RunOptions{RuntimeState: map[string]any{
			agent.RuntimeStateKeyTransferController: handler,
		}},
		TransferInfo: &agent.TransferInfo{TargetAgentName: "child"},
	}
	rsp := &model.Response{ID: "r1"}
	out := make(chan *event.Event, 10)
	NewTransferResponseProcessor(true).ProcessResponse(
		context.Background(),
		inv,
		&model.Request{},
		rsp,
		out,
	)
	close(out)
	for range out {
	}
	require.Equal(t, "parent", handler.sourceAgent)
	require.Equal(t, "child", handler.targetAgent)
	require.Equal(t, 1, handler.count)
	require.True(t, handler.done)
}

func TestTransferResponseProc_CompletionHandlerIgnoresForwardedDescendantDone(t *testing.T) {
	target := &forwardedDoneResponseAgent{name: "child"}
	parent := &parentAgent{child: target}
	handler := &completionInvocationCustomizer{}
	inv := &agent.Invocation{
		Agent:        parent,
		AgentName:    "parent",
		InvocationID: "inv-completion-forwarded",
		RunOptions: agent.RunOptions{RuntimeState: map[string]any{
			agent.RuntimeStateKeyTransferController: handler,
		}},
		TransferInfo: &agent.TransferInfo{TargetAgentName: "child"},
	}
	rsp := &model.Response{ID: "r-forwarded"}
	out := make(chan *event.Event, 10)
	NewTransferResponseProcessor(true).ProcessResponse(
		context.Background(),
		inv,
		&model.Request{},
		rsp,
		out,
	)
	close(out)
	for range out {
	}
	require.Equal(t, "parent", handler.sourceAgent)
	require.Equal(t, "child", handler.targetAgent)
	require.Equal(t, 1, handler.count)
	require.True(t, handler.done)
}

func TestTransferResponseProc_CompletionHandlerFallsBackWhenTargetForwardsOnlyDescendantDone(t *testing.T) {
	target := &descendantOnlyDoneResponseAgent{name: "child"}
	parent := &parentAgent{child: target}
	handler := &completionInvocationCustomizer{}
	inv := &agent.Invocation{
		Agent:        parent,
		AgentName:    "parent",
		InvocationID: "inv-completion-descendant-only",
		RunOptions: agent.RunOptions{RuntimeState: map[string]any{
			agent.RuntimeStateKeyTransferController: handler,
		}},
		TransferInfo: &agent.TransferInfo{TargetAgentName: "child"},
	}
	rsp := &model.Response{ID: "r-descendant-only"}
	out := make(chan *event.Event, 10)
	NewTransferResponseProcessor(true).ProcessResponse(
		context.Background(),
		inv,
		&model.Request{},
		rsp,
		out,
	)
	close(out)
	for range out {
	}
	require.Equal(t, "parent", handler.sourceAgent)
	require.Equal(t, "child", handler.targetAgent)
	require.Equal(t, 1, handler.count)
	require.True(t, handler.done)
}

func TestTransferResponseProc_CompletionHandlerDoesNotFallbackAfterNestedDelegation(t *testing.T) {
	target := &nestedDelegationResponseAgent{name: "child"}
	parent := &parentAgent{child: target}
	handler := &completionInvocationCustomizer{}
	inv := &agent.Invocation{
		Agent:        parent,
		AgentName:    "parent",
		InvocationID: "inv-completion-nested-delegation",
		RunOptions: agent.RunOptions{RuntimeState: map[string]any{
			agent.RuntimeStateKeyTransferController: handler,
		}},
		TransferInfo: &agent.TransferInfo{TargetAgentName: "child"},
	}
	rsp := &model.Response{ID: "r-nested-delegation"}
	out := make(chan *event.Event, 10)
	NewTransferResponseProcessor(true).ProcessResponse(
		context.Background(),
		inv,
		&model.Request{},
		rsp,
		out,
	)
	close(out)
	for range out {
	}
	require.Zero(t, handler.count)
}

func TestTransferResponseProc_CompletionHandlerDoesNotFallbackAfterTargetError(t *testing.T) {
	target := &errorResponseAgent{name: "child"}
	parent := &parentAgent{child: target}
	handler := &completionInvocationCustomizer{}
	inv := &agent.Invocation{
		Agent:        parent,
		AgentName:    "parent",
		InvocationID: "inv-completion-target-error",
		RunOptions: agent.RunOptions{RuntimeState: map[string]any{
			agent.RuntimeStateKeyTransferController: handler,
		}},
		TransferInfo: &agent.TransferInfo{TargetAgentName: "child"},
	}
	rsp := &model.Response{ID: "r-target-error"}
	out := make(chan *event.Event, 10)
	NewTransferResponseProcessor(true).ProcessResponse(
		context.Background(),
		inv,
		&model.Request{},
		rsp,
		out,
	)
	close(out)
	for range out {
	}
	require.Zero(t, handler.count)
}

func TestTransferResponseProc_TerminalErrorHandlerMutatesTargetErrorBeforeForward(t *testing.T) {
	target := &errorResponseAgent{name: "child"}
	parent := &parentAgent{child: target}
	handler := &terminalErrorTransferController{}
	inv := &agent.Invocation{
		Agent:        parent,
		AgentName:    "parent",
		InvocationID: "inv-terminal-error",
		RunOptions: agent.RunOptions{RuntimeState: map[string]any{
			agent.RuntimeStateKeyTransferController: handler,
		}},
		TransferInfo: &agent.TransferInfo{TargetAgentName: "child"},
	}
	rsp := &model.Response{ID: "r-terminal-error"}
	out := make(chan *event.Event, 10)
	NewTransferResponseProcessor(true).ProcessResponse(
		context.Background(),
		inv,
		&model.Request{},
		rsp,
		out,
	)
	close(out)
	var targetErr *event.Event
	for evt := range out {
		if evt != nil && evt.IsTerminalError() && evt.Author == "child" {
			targetErr = evt
		}
	}
	require.Equal(t, 1, handler.terminalErrors)
	require.NotNil(t, targetErr)
	require.Equal(t, []byte("child"), targetErr.StateDelta["owner"])
}

func TestTransferResponseProc_CompletionHandlerAllowsDoneAfterNonTerminalTargetError(t *testing.T) {
	target := &nonTerminalErrorThenDoneAgent{name: "child"}
	parent := &parentAgent{child: target}
	handler := &completionInvocationCustomizer{}
	inv := &agent.Invocation{
		Agent:        parent,
		AgentName:    "parent",
		InvocationID: "inv-completion-target-recovered",
		RunOptions: agent.RunOptions{RuntimeState: map[string]any{
			agent.RuntimeStateKeyTransferController: handler,
		}},
		TransferInfo: &agent.TransferInfo{TargetAgentName: "child"},
	}
	rsp := &model.Response{ID: "r-target-recovered"}
	out := make(chan *event.Event, 10)
	NewTransferResponseProcessor(true).ProcessResponse(
		context.Background(),
		inv,
		&model.Request{},
		rsp,
		out,
	)
	close(out)
	for range out {
	}
	require.Equal(t, 1, handler.count)
	require.True(t, handler.done)
}

type controllerCompletionObserver struct {
	recordingTransferCompletionObserver
}

func (h *controllerCompletionObserver) OnTransfer(
	context.Context,
	string,
	string,
) (time.Duration, error) {
	return 0, nil
}

type completionInvocationCustomizer struct {
	recordingTransferCompletionObserver
}

func (h *completionInvocationCustomizer) OnTransfer(
	context.Context,
	string,
	string,
) (time.Duration, error) {
	return 0, nil
}

func (h *completionInvocationCustomizer) CustomizeTransferInvocation(
	context.Context,
	*agent.Invocation,
	*agent.Invocation,
) error {
	return nil
}

type terminalErrorTransferController struct {
	terminalErrors int
}

func (h *terminalErrorTransferController) OnTransfer(
	context.Context,
	string,
	string,
) (time.Duration, error) {
	return 0, nil
}

func (h *terminalErrorTransferController) OnTransferTerminalError(
	_ context.Context,
	_ *agent.Invocation,
	target *agent.Invocation,
	targetEvent *event.Event,
) {
	h.terminalErrors++
	if targetEvent.StateDelta == nil {
		targetEvent.StateDelta = make(map[string][]byte)
	}
	targetEvent.StateDelta["owner"] = []byte(target.AgentName)
}

func TestTransferResponseProc_ControllerCompletionMethodIsObserved(t *testing.T) {
	target := &doneResponseAgent{name: "child"}
	parent := &parentAgent{child: target}
	handler := &controllerCompletionObserver{}
	inv := &agent.Invocation{
		Agent:        parent,
		AgentName:    "parent",
		InvocationID: "inv-completion-controller",
		RunOptions: agent.RunOptions{RuntimeState: map[string]any{
			agent.RuntimeStateKeyTransferController: handler,
		}},
		TransferInfo: &agent.TransferInfo{TargetAgentName: "child"},
	}
	rsp := &model.Response{ID: "r-controller"}
	out := make(chan *event.Event, 10)
	NewTransferResponseProcessor(true).ProcessResponse(
		context.Background(),
		inv,
		&model.Request{},
		rsp,
		out,
	)
	close(out)
	for range out {
	}
	require.Equal(t, 1, handler.count)
}
