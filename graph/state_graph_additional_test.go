//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package graph

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	oteltrace "go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/embedded"
	"go.opentelemetry.io/otel/trace/noop"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	itelemetry "trpc.group/trpc-go/trpc-agent-go/internal/telemetry"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/plugin"
	"trpc.group/trpc-go/trpc-agent-go/session"
	semconvmetrics "trpc.group/trpc-go/trpc-agent-go/telemetry/semconv/metrics"
	semconvtrace "trpc.group/trpc-go/trpc-agent-go/telemetry/semconv/trace"
	teletrace "trpc.group/trpc-go/trpc-agent-go/telemetry/trace"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// captureModel captures the last request passed to GenerateContent.
type captureModel struct{ lastReq *model.Request }

func (c *captureModel) GenerateContent(ctx context.Context, req *model.Request) (<-chan *model.Response, error) {
	c.lastReq = req
	ch := make(chan *model.Response, 1)
	// Mark Done=true to avoid emitting streaming response events and keep focus on model start/complete events.
	ch <- &model.Response{Done: true, Choices: []model.Choice{{Index: 0, Message: model.NewAssistantMessage("ok")}}}
	close(ch)
	return ch, nil
}

func (c *captureModel) Info() model.Info { return model.Info{Name: "capture"} }

// echoTool is a minimal CallableTool used for ToolSet injection tests.
type echoTool struct{ name string }

func (e *echoTool) Declaration() *tool.Declaration { return &tool.Declaration{Name: e.name} }
func (e *echoTool) Call(ctx context.Context, jsonArgs []byte) (any, error) {
	return map[string]any{"ok": true}, nil
}

// simpleToolSet returns a fixed set of tools.
type simpleToolSet struct {
	name string
}

func (s *simpleToolSet) Tools(ctx context.Context) []tool.Tool {
	return []tool.Tool{&echoTool{name: "echo"}}
}
func (s *simpleToolSet) Close() error { return nil }
func (s *simpleToolSet) Name() string { return s.name }

func useSpanRecorder(t *testing.T) *tracetest.SpanRecorder {
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	originalProvider := teletrace.TracerProvider
	originalTracer := teletrace.Tracer
	teletrace.TracerProvider = provider
	teletrace.Tracer = provider.Tracer("state-graph-disable-tracing-test")
	t.Cleanup(func() {
		_ = provider.Shutdown(context.Background())
		teletrace.TracerProvider = originalProvider
		teletrace.Tracer = originalTracer
	})
	return recorder
}

type trackingSpan struct {
	embedded.Span
	mu             sync.Mutex
	attributes     []attribute.KeyValue
	recordedErrors []error
	statusCode     codes.Code
}

func (s *trackingSpan) End(options ...oteltrace.SpanEndOption)                 {}
func (s *trackingSpan) AddEvent(name string, options ...oteltrace.EventOption) {}
func (s *trackingSpan) AddLink(link oteltrace.Link)                            {}
func (s *trackingSpan) IsRecording() bool                                      { return true }

func (s *trackingSpan) RecordError(err error, options ...oteltrace.EventOption) {
	if err == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.recordedErrors = append(s.recordedErrors, err)
}

func (s *trackingSpan) SpanContext() oteltrace.SpanContext {
	return oteltrace.NewSpanContext(oteltrace.SpanContextConfig{})
}

func (s *trackingSpan) SetStatus(code codes.Code, description string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.statusCode = code
}

func (s *trackingSpan) SetName(name string) {}

func (s *trackingSpan) SetAttributes(kv ...attribute.KeyValue) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.attributes = append(s.attributes, kv...)
}

func (s *trackingSpan) TracerProvider() oteltrace.TracerProvider { return noop.NewTracerProvider() }

// stubAgent is a minimal agent implementation used for subgraph tests.
type stubAgent struct {
	name string
}

type iterErrorModel struct {
	err error
}

func (m *iterErrorModel) GenerateContent(ctx context.Context, req *model.Request) (<-chan *model.Response, error) {
	ch := make(chan *model.Response)
	close(ch)
	return ch, nil
}

func (m *iterErrorModel) GenerateContentIter(ctx context.Context, req *model.Request) (model.Seq[*model.Response], error) {
	return nil, m.err
}

func (m *iterErrorModel) Info() model.Info {
	return model.Info{Name: "iter-error-model"}
}

type nilIterModel struct{}

func (m *nilIterModel) GenerateContent(ctx context.Context, req *model.Request) (<-chan *model.Response, error) {
	ch := make(chan *model.Response)
	close(ch)
	return ch, nil
}

func (m *nilIterModel) GenerateContentIter(ctx context.Context, req *model.Request) (model.Seq[*model.Response], error) {
	return nil, nil
}

func (m *nilIterModel) Info() model.Info {
	return model.Info{Name: "nil-iter-model"}
}

type noResponseModel struct{}

func (m *noResponseModel) GenerateContent(ctx context.Context, req *model.Request) (<-chan *model.Response, error) {
	ch := make(chan *model.Response)
	close(ch)
	return ch, nil
}

func (m *noResponseModel) Info() model.Info {
	return model.Info{Name: "no-response-model"}
}

type multiResponseModel struct {
	responses []*model.Response
	delay     time.Duration
}

func (m *multiResponseModel) GenerateContent(ctx context.Context, req *model.Request) (<-chan *model.Response, error) {
	ch := make(chan *model.Response, len(m.responses))
	for _, response := range m.responses {
		if m.delay > 0 {
			time.Sleep(m.delay)
		}
		ch <- response
	}
	close(ch)
	return ch, nil
}

func (m *multiResponseModel) Info() model.Info {
	return model.Info{Name: "multi-response-model"}
}

// graphRecordingSpan captures trace attributes for assertions.
type graphRecordingSpan struct {
	oteltrace.Span
	attrs []attribute.KeyValue
}

func newGraphRecordingSpan() *graphRecordingSpan {
	_, span := oteltrace.NewNoopTracerProvider().Tracer("test").Start(context.Background(), "graph-test")
	return &graphRecordingSpan{Span: span}
}

func (s *graphRecordingSpan) IsRecording() bool {
	return true
}

func (s *graphRecordingSpan) SetAttributes(kv ...attribute.KeyValue) {
	s.attrs = append(s.attrs, kv...)
	s.Span.SetAttributes(kv...)
}

func graphHasAttr(attrs []attribute.KeyValue, key string, want any) bool {
	for _, kv := range attrs {
		if string(kv.Key) == key && kv.Value.AsInterface() == want {
			return true
		}
	}
	return false
}

func graphHasNonEmptyStringAttr(attrs []attribute.KeyValue, key string) bool {
	for _, kv := range attrs {
		if string(kv.Key) == key && kv.Value.AsString() != "" {
			return true
		}
	}
	return false
}

func TestWorkflowTypeFromNodeType(t *testing.T) {
	tests := []struct {
		name     string
		nodeType NodeType
		want     itelemetry.WorkflowType
	}{
		{name: "function", nodeType: NodeTypeFunction, want: itelemetry.WorkflowTypeFunction},
		{name: "llm", nodeType: NodeTypeLLM, want: itelemetry.WorkflowTypeLLM},
		{name: "tool", nodeType: NodeTypeTool, want: itelemetry.WorkflowTypeTool},
		{name: "agent", nodeType: NodeTypeAgent, want: itelemetry.WorkflowTypeAgent},
		{name: "join", nodeType: NodeTypeJoin, want: itelemetry.WorkflowTypeJoin},
		{name: "router", nodeType: NodeTypeRouter, want: itelemetry.WorkflowTypeRouter},
		{name: "unknown passthrough", nodeType: NodeType("custom"), want: itelemetry.WorkflowType("custom")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, workflowTypeFromNodeType(tt.nodeType))
		})
	}
}

// collectModelExecutionPhases drains model execution events from the channel.
func collectModelExecutionPhases(ch <-chan *event.Event) []ModelExecutionPhase {
	var phases []ModelExecutionPhase
	for {
		select {
		case e, ok := <-ch:
			if !ok {
				return phases
			}
			if e == nil || e.StateDelta == nil {
				continue
			}
			b, ok := e.StateDelta[MetadataKeyModel]
			if !ok {
				continue
			}
			var meta ModelExecutionMetadata
			_ = json.Unmarshal(b, &meta)
			phases = append(phases, meta.Phase)
		default:
			return phases
		}
	}
}

func collectModelExecutionEvents(ch <-chan *event.Event) []*event.Event {
	var events []*event.Event
	for {
		select {
		case e, ok := <-ch:
			if !ok {
				return events
			}
			if e == nil || e.StateDelta == nil {
				continue
			}
			if _, ok := e.StateDelta[MetadataKeyModel]; !ok {
				continue
			}
			events = append(events, e)
		default:
			return events
		}
	}
}

func requireModelExecutionEventMetadata(
	t *testing.T,
	events []*event.Event,
	invocationID string,
	parentInvocationID string,
	branch string,
	filterKey string,
	requestID string,
) {
	t.Helper()
	require.Len(t, events, 2)
	for _, evt := range events {
		var meta ModelExecutionMetadata
		require.NoError(t, json.Unmarshal(evt.StateDelta[MetadataKeyModel], &meta))
		require.Equal(t, invocationID, evt.InvocationID)
		require.Equal(t, invocationID, meta.InvocationID)
		require.Equal(t, parentInvocationID, evt.ParentInvocationID)
		require.Equal(t, branch, evt.Branch)
		require.Equal(t, filterKey, evt.FilterKey)
		require.Equal(t, requestID, evt.RequestID)
	}
}

func collectModelExecutionPhasesFromEvents(events []*event.Event) []ModelExecutionPhase {
	phases := make([]ModelExecutionPhase, 0, len(events))
	for _, evt := range events {
		if evt == nil || evt.StateDelta == nil {
			continue
		}
		b, ok := evt.StateDelta[MetadataKeyModel]
		if !ok {
			continue
		}
		var meta ModelExecutionMetadata
		if err := json.Unmarshal(b, &meta); err != nil {
			continue
		}
		phases = append(phases, meta.Phase)
	}
	return phases
}

func (a *stubAgent) Run(
	ctx context.Context,
	inv *agent.Invocation,
) (<-chan *event.Event, error) {
	return nil, nil
}

func (a *stubAgent) Tools() []tool.Tool { return nil }

func (a *stubAgent) Info() agent.Info {
	return agent.Info{Name: a.name}
}

func (a *stubAgent) SubAgents() []agent.Agent { return nil }

func (a *stubAgent) FindSubAgent(name string) agent.Agent { return nil }

func TestAddLLMNode_ToolSetInjection_And_ModelEventInput(t *testing.T) {
	schema := MessagesStateSchema()
	cm := &captureModel{}
	sg := NewStateGraph(schema)
	// Inject toolset via node options
	sg.AddLLMNode(
		"llm",
		cm,
		"inst",
		nil,
		WithToolSets([]tool.ToolSet{&simpleToolSet{"simple"}}),
	)
	// Ensure node type is LLM
	n, ok := sg.graph.nodes["llm"]
	require.True(t, ok)
	require.Equal(t, NodeTypeLLM, n.Type)

	// Build a minimal exec context to receive events
	ch := make(chan *event.Event, 8)
	exec := &ExecutionContext{InvocationID: "inv-llm", EventChan: ch}
	state := State{StateKeyExecContext: exec, StateKeyCurrentNodeID: "llm", StateKeyUserInput: "hi"}

	// Call the node function directly
	_, err := n.Function(context.Background(), state)
	require.NoError(t, err)

	// Verify model received tools injected from ToolSet
	require.NotNil(t, cm.lastReq)
	require.Contains(t, cm.lastReq.Tools, "simple_echo") // Tool name is now namespaced with toolset name

	// Drain available events and verify model start/complete include input built from instruction+user_input
	var modelInputs []string
	for {
		select {
		case e := <-ch:
			if e != nil && e.StateDelta != nil {
				if b, ok := e.StateDelta[MetadataKeyModel]; ok {
					var meta ModelExecutionMetadata
					_ = json.Unmarshal(b, &meta)
					if meta.Input != "" {
						modelInputs = append(modelInputs, meta.Input)
					}
				}
			}
		default:
			goto DONE
		}
	}
DONE:
	// Expect at least one model event carrying the combined input string
	require.NotEmpty(t, modelInputs)
	found := false
	for _, in := range modelInputs {
		if in == "inst\n\nhi" || (len(in) >= 2 && in[0:4] == "inst") {
			found = true
			break
		}
	}
	require.True(t, found, "expected model event input to contain instruction and user input: %v", modelInputs)
}

func TestAddLLMNode_GenerationConfigOption(t *testing.T) {
	schema := MessagesStateSchema()
	cm := &captureModel{}
	sg := NewStateGraph(schema)

	temp := 0.2
	maxTok := 128

	cfg := model.GenerationConfig{
		Stream:      false,
		Temperature: &temp,
		MaxTokens:   &maxTok,
		Stop:        []string{"END"},
	}

	sg.AddLLMNode("llm", cm, "inst", nil, WithGenerationConfig(cfg))

	// Sanity: node exists
	n := sg.graph.nodes["llm"]
	require.NotNil(t, n)

	// Execute node
	_, err := n.Function(context.Background(), State{StateKeyUserInput: "hi"})
	require.NoError(t, err)

	// Verify request picked up generation config
	require.NotNil(t, cm.lastReq)
	got := cm.lastReq.GenerationConfig
	require.Equal(t, cfg.Stream, got.Stream)
	if cfg.Temperature != nil {
		require.NotNil(t, got.Temperature)
		require.InDelta(t, *cfg.Temperature, *got.Temperature, 1e-9)
	}
	if cfg.MaxTokens != nil {
		require.NotNil(t, got.MaxTokens)
		require.Equal(t, *cfg.MaxTokens, *got.MaxTokens)
	}
	require.Equal(t, cfg.Stop, got.Stop)

}

func TestRunModelStream_IterModelError(t *testing.T) {
	iterErr := errors.New("iter boom")

	_, _, err := runModelStream(
		context.Background(),
		nil,
		nil,
		&iterErrorModel{err: iterErr},
		&model.Request{},
		nil,
	)
	require.ErrorIs(t, err, iterErr)
	require.ErrorContains(t, err, "failed to generate content")
}

func TestRunModel_NilIterSequence(t *testing.T) {
	ctx, ch, err := runModel(
		context.Background(),
		nil,
		&nilIterModel{},
		&model.Request{},
	)
	require.ErrorContains(t, err, errMsgNoModelResponse)
	require.Nil(t, ch)
	require.NotNil(t, ctx)
}

func TestExecuteModelAndProcessResponses_NilIterSequence(t *testing.T) {
	resp, err := executeModelAndProcessResponses(
		context.Background(),
		modelExecutionConfig{
			LLMModel:     &nilIterModel{},
			Request:      &model.Request{},
			InvocationID: "inv-nil-iter",
			Span:         noop.Span{},
			NodeID:       "llm",
		},
	)
	require.Nil(t, resp)
	require.ErrorContains(t, err, errMsgNoModelResponse)
}

func TestEmitFastModelResponseEvent_DisablesPartialMetadata(t *testing.T) {
	t.Run("partial response omits generated ID and timestamp", func(t *testing.T) {
		ch := make(chan *event.Event, 1)
		respTimestamp := time.Unix(1, 0).UTC()
		resp := &model.Response{
			IsPartial: true,
			Timestamp: respTimestamp,
			Choices: []model.Choice{
				{Message: model.NewAssistantMessage("partial")},
			},
		}

		ev, err := emitFastModelResponseEvent(
			context.Background(),
			agent.NewInvocation(agent.WithInvocationID("inv-fast")),
			modelExecutionConfig{
				InvocationID: "inv-fast",
				EventChan:    ch,
				Span:         noop.Span{},
			},
			resp,
			"llm",
			true,
			true,
			nil,
		)
		require.NoError(t, err)
		require.NotNil(t, ev)
		require.Empty(t, ev.ID)
		require.Equal(t, respTimestamp, ev.Timestamp)
		require.Same(t, ev, <-ch)
	})

	t.Run("response error is surfaced", func(t *testing.T) {
		resp := &model.Response{
			Error: &model.ResponseError{Message: "api boom"},
		}

		ev, err := emitFastModelResponseEvent(
			context.Background(),
			agent.NewInvocation(agent.WithInvocationID("inv-fast-err")),
			modelExecutionConfig{
				InvocationID: "inv-fast-err",
				Span:         noop.Span{},
			},
			resp,
			"llm",
			false,
			false,
			&event.Event{},
		)
		require.ErrorContains(t, err, "model API error: api boom")
		require.NotNil(t, ev)
		require.Same(t, resp, ev.Response)
	})

	t.Run("partial response uses event creation time by default", func(t *testing.T) {
		ch := make(chan *event.Event, 1)
		respTimestamp := time.Unix(1, 0).UTC()
		resp := &model.Response{
			IsPartial: true,
			Timestamp: respTimestamp,
			Choices: []model.Choice{
				{Message: model.NewAssistantMessage("partial")},
			},
		}
		ev, err := emitFastModelResponseEvent(
			context.Background(),
			agent.NewInvocation(agent.WithInvocationID("inv-fast-default-ts")),
			modelExecutionConfig{
				InvocationID: "inv-fast-default-ts",
				EventChan:    ch,
				Span:         noop.Span{},
			},
			resp,
			"llm",
			false,
			false,
			nil,
		)
		require.NoError(t, err)
		require.NotNil(t, ev)
		require.False(t, ev.Timestamp.IsZero())
		require.True(t, ev.Timestamp.After(respTimestamp))
		require.Same(t, ev, <-ch)
	})

	t.Run("done response still keeps trace metadata without emitting", func(t *testing.T) {
		ch := make(chan *event.Event, 1)
		resp := &model.Response{
			Done: true,
			Choices: []model.Choice{
				{Message: model.NewAssistantMessage("final")},
			},
		}

		ev, err := emitFastModelResponseEvent(
			context.Background(),
			agent.NewInvocation(agent.WithInvocationID("inv-fast-done")),
			modelExecutionConfig{
				InvocationID: "inv-fast-done",
				EventChan:    ch,
				Span:         noop.Span{},
			},
			resp,
			"llm",
			false,
			false,
			nil,
		)
		require.NoError(t, err)
		require.NotNil(t, ev)
		require.NotEmpty(t, ev.ID)
		require.False(t, ev.Timestamp.IsZero())
		select {
		case emitted := <-ch:
			require.FailNowf(t, "unexpected emitted event", "got %+v", emitted)
		default:
		}
	})
}

func TestModelResponseProcessorConsume_FastPathSeq(t *testing.T) {
	ch := make(chan *event.Event, 2)
	invocation := agent.NewInvocation(
		agent.WithInvocationID("inv-fast-seq"),
		agent.WithInvocationRunOptions(agent.RunOptions{
			DisableResponseUsageTracking:  true,
			DisablePartialEventIDs:        true,
			DisablePartialEventTimestamps: true,
		}),
	)
	var runErr error
	processor := newModelResponseProcessor(
		context.Background(),
		modelExecutionConfig{
			Invocation:   invocation,
			InvocationID: invocation.InvocationID,
			EventChan:    ch,
			Request:      &model.Request{},
			Span:         noop.Span{},
			NodeID:       "llm",
		},
		invocation,
		&runErr,
	)
	require.True(t, processor.fastResponsePath)

	err := processor.consume(modelResponseStream{
		Seq: func(yield func(*model.Response) bool) {
			if !yield(nil) {
				return
			}
			yield(&model.Response{
				IsPartial: true,
				Choices: []model.Choice{
					{Message: model.NewAssistantMessage("partial")},
				},
				Timestamp: time.Now(),
			})
		},
	})
	require.NoError(t, err)
	require.NotNil(t, processor.lastEvent)
	require.NotNil(t, processor.finalResponse)
	require.Same(t, processor.lastEvent, <-ch)
}

func TestModelResponseProcessorConsume_FastPathDoneResponse_TracesEventID(t *testing.T) {
	ch := make(chan *event.Event, 1)
	invocation := agent.NewInvocation(
		agent.WithInvocationID("inv-fast-done-trace"),
		agent.WithInvocationRunOptions(agent.RunOptions{
			DisableResponseUsageTracking: true,
		}),
	)
	span := newGraphRecordingSpan()
	var runErr error
	processor := newModelResponseProcessor(
		context.Background(),
		modelExecutionConfig{
			Invocation:   invocation,
			InvocationID: invocation.InvocationID,
			EventChan:    ch,
			Request:      &model.Request{},
			Span:         span,
			NodeID:       "llm",
		},
		invocation,
		&runErr,
	)
	require.True(t, processor.fastResponsePath)
	err := processor.consume(modelResponseStream{
		Seq: func(yield func(*model.Response) bool) {
			yield(&model.Response{
				Done: true,
				Choices: []model.Choice{
					{Message: model.NewAssistantMessage("final")},
				},
			})
		},
	})
	require.NoError(t, err)
	require.NotNil(t, processor.lastEvent)
	require.NotEmpty(t, processor.lastEvent.ID)
	require.True(t, graphHasNonEmptyStringAttr(span.attrs, semconvtrace.KeyEventID))
	select {
	case emitted := <-ch:
		require.FailNowf(t, "unexpected emitted event", "got %+v", emitted)
	default:
	}
}

func TestNewModelResponseProcessor_FastPathWhenOnlyOnePartialToggleIsDisabled(t *testing.T) {
	tests := []struct {
		name       string
		runOptions agent.RunOptions
	}{
		{
			name: "disable partial ids only",
			runOptions: agent.RunOptions{
				DisablePartialEventIDs: true,
			},
		},
		{
			name: "disable partial timestamps only",
			runOptions: agent.RunOptions{
				DisablePartialEventTimestamps: true,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			invocation := agent.NewInvocation(
				agent.WithInvocationID("inv-fast-path"),
				agent.WithInvocationRunOptions(tt.runOptions),
			)
			var runErr error
			processor := newModelResponseProcessor(
				context.Background(),
				modelExecutionConfig{
					Invocation:   invocation,
					InvocationID: invocation.InvocationID,
					EventChan:    make(chan *event.Event, 1),
					Request:      &model.Request{},
					Span:         noop.Span{},
					NodeID:       "llm",
				},
				invocation,
				&runErr,
			)
			require.True(t, processor.fastResponsePath)
		})
	}
}

func TestNewModelResponseProcessor_FastPathWithBeforeModelCallbacksOnly(t *testing.T) {
	tests := []struct {
		name           string
		invocation     *agent.Invocation
		modelCallbacks *model.Callbacks
	}{
		{
			name: "local before model callbacks only",
			invocation: agent.NewInvocation(
				agent.WithInvocationID("inv-local-before-only"),
			),
			modelCallbacks: model.NewCallbacks().RegisterBeforeModel(
				func(ctx context.Context, args *model.BeforeModelArgs) (*model.BeforeModelResult, error) {
					return &model.BeforeModelResult{}, nil
				},
			),
		},
		{
			name: "plugin before model callbacks only",
			invocation: agent.NewInvocation(
				agent.WithInvocationID("inv-plugin-before-only"),
				agent.WithInvocationPlugins(plugin.MustNewManager(&hookPlugin{
					name: "before-only",
					reg: func(r *plugin.Registry) {
						r.BeforeModel(func(ctx context.Context, args *model.BeforeModelArgs) (*model.BeforeModelResult, error) {
							return &model.BeforeModelResult{}, nil
						})
					},
				})),
			),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var runErr error
			processor := newModelResponseProcessor(
				context.Background(),
				modelExecutionConfig{
					Invocation:     tt.invocation,
					InvocationID:   tt.invocation.InvocationID,
					ModelCallbacks: tt.modelCallbacks,
					EventChan:      make(chan *event.Event, 1),
					Request:        &model.Request{},
					Span:           noop.Span{},
					NodeID:         "llm",
				},
				tt.invocation,
				&runErr,
			)
			require.True(t, processor.fastResponsePath)
		})
	}
}

func TestProcessModelResponse_DisablesPartialMetadataOnSlowPath(t *testing.T) {
	ch := make(chan *event.Event, 1)
	invocation := agent.NewInvocation(
		agent.WithInvocationID("inv-slow-partial"),
		agent.WithInvocationRunOptions(agent.RunOptions{
			DisablePartialEventIDs:        true,
			DisablePartialEventTimestamps: true,
		}),
	)
	var ctx context.Context = agent.NewInvocationContext(context.Background(), invocation)
	resp := &model.Response{
		IsPartial: true,
		Choices: []model.Choice{
			{Message: model.NewAssistantMessage("partial")},
		},
	}
	ctx, ev, err := processModelResponse(ctx, modelResponseConfig{
		Response:     resp,
		EventChan:    ch,
		InvocationID: invocation.InvocationID,
		Request:      &model.Request{},
		Span:         noop.Span{},
		NodeID:       "llm",
	})
	require.NoError(t, err)
	require.NotNil(t, ctx)
	require.NotNil(t, ev)
	require.Empty(t, ev.ID)
	require.True(t, ev.Timestamp.IsZero())
	require.Same(t, ev, <-ch)
}

func TestProcessModelResponse_UsesFallbackInvocationFromConfig(t *testing.T) {
	ch := make(chan *event.Event, 1)
	invocation := agent.NewInvocation(
		agent.WithInvocationID("inv-slow-fallback"),
		agent.WithInvocationEventFilterKey("graph/llm"),
		agent.WithInvocationRunOptions(agent.RunOptions{
			RequestID:                     "req-slow-fallback",
			DisablePartialEventIDs:        true,
			DisablePartialEventTimestamps: true,
		}),
	)
	resp := &model.Response{
		IsPartial: true,
		Choices: []model.Choice{
			{Message: model.NewAssistantMessage("partial")},
		},
	}
	ctx, ev, err := processModelResponse(context.Background(), modelResponseConfig{
		Response:     resp,
		Invocation:   invocation,
		EventChan:    ch,
		InvocationID: "inv-config-only",
		Request:      &model.Request{},
		Span:         noop.Span{},
		NodeID:       "llm",
	})
	require.NoError(t, err)
	require.NotNil(t, ctx)
	require.NotNil(t, ev)
	require.Equal(t, invocation.InvocationID, ev.InvocationID)
	require.Equal(t, invocation.RunOptions.RequestID, ev.RequestID)
	require.Equal(t, invocation.GetEventFilterKey(), ev.FilterKey)
	require.Empty(t, ev.ID)
	require.True(t, ev.Timestamp.IsZero())
	require.Same(t, ev, <-ch)
}

func TestProcessModelResponse_PreservesStableEventMetadataWhenContextInvocationIsSparse(t *testing.T) {
	ch := make(chan *event.Event, 1)
	baseInvocation := agent.NewInvocation(
		agent.WithInvocationID("inv-slow-base"),
		agent.WithInvocationEventFilterKey("graph/base"),
		agent.WithInvocationRunOptions(agent.RunOptions{
			RequestID: "req-slow-base",
		}),
	)
	updatedInvocation := agent.NewInvocation(
		agent.WithInvocationID("inv-slow-updated"),
		agent.WithInvocationRunOptions(agent.RunOptions{
			DisablePartialEventIDs:        true,
			DisablePartialEventTimestamps: true,
		}),
	)
	resp := &model.Response{
		IsPartial: true,
		Choices: []model.Choice{
			{Message: model.NewAssistantMessage("partial")},
		},
	}
	ctx, ev, err := processModelResponse(
		agent.NewInvocationContext(context.Background(), updatedInvocation),
		modelResponseConfig{
			Response:         resp,
			Invocation:       baseInvocation,
			StableInvocation: baseInvocation,
			EventChan:        ch,
			InvocationID:     baseInvocation.InvocationID,
			Request:          &model.Request{},
			Span:             noop.Span{},
			NodeID:           "llm",
		},
	)
	require.NoError(t, err)
	require.NotNil(t, ctx)
	require.NotNil(t, ev)
	require.Equal(t, baseInvocation.InvocationID, ev.InvocationID)
	require.Equal(t, baseInvocation.RunOptions.RequestID, ev.RequestID)
	require.Equal(t, baseInvocation.GetEventFilterKey(), ev.FilterKey)
	require.Empty(t, ev.ID)
	require.True(t, ev.Timestamp.IsZero())
	require.Same(t, ev, <-ch)
}

func TestExecuteModelAndProcessResponses_UsesInvocationFromCallbackContext(t *testing.T) {
	baseInvocation := agent.NewInvocation(
		agent.WithInvocationID("inv-base"),
		agent.WithInvocationRunOptions(agent.RunOptions{}),
	)
	updatedInvocation := agent.NewInvocation(
		agent.WithInvocationID("inv-updated"),
		agent.WithInvocationRunOptions(agent.RunOptions{
			DisableResponseUsageTracking: true,
		}),
	)
	callbacks := model.NewCallbacks().RegisterBeforeModel(
		func(ctx context.Context, args *model.BeforeModelArgs) (*model.BeforeModelResult, error) {
			return &model.BeforeModelResult{
				Context: agent.NewInvocationContext(ctx, updatedInvocation),
			}, nil
		},
	)
	resp, err := executeModelAndProcessResponses(
		agent.NewInvocationContext(context.Background(), baseInvocation),
		modelExecutionConfig{
			Invocation:     baseInvocation,
			ModelCallbacks: callbacks,
			LLMModel:       &captureModel{},
			Request:        &model.Request{},
			InvocationID:   baseInvocation.InvocationID,
			Span:           noop.Span{},
			NodeID:         "llm",
		},
	)
	require.NoError(t, err)
	finalResponse, ok := resp.(*model.Response)
	require.True(t, ok)
	require.Nil(t, finalResponse.Usage)
}

func TestExecuteModelAndProcessResponses_DisableResponseUsageTrackingStillRecordsMetrics(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	originalProvider := itelemetry.MeterProvider
	originalMeter := itelemetry.ChatMeter
	originalRequestCnt := itelemetry.ChatMetricTRPCAgentGoClientRequestCnt
	originalTokenUsage := itelemetry.ChatMetricGenAIClientTokenUsage
	originalOperationDuration := itelemetry.ChatMetricGenAIClientOperationDuration
	originalServerTimeToFirstToken := itelemetry.ChatMetricGenAIServerTimeToFirstToken
	originalClientTimeToFirstToken := itelemetry.ChatMetricTRPCAgentGoClientTimeToFirstToken
	originalTimePerOutputToken := itelemetry.ChatMetricTRPCAgentGoClientTimePerOutputToken
	originalOutputTokenPerTime := itelemetry.ChatMetricTRPCAgentGoClientOutputTokenPerTime
	defer func() {
		itelemetry.MeterProvider = originalProvider
		itelemetry.ChatMeter = originalMeter
		itelemetry.ChatMetricTRPCAgentGoClientRequestCnt = originalRequestCnt
		itelemetry.ChatMetricGenAIClientTokenUsage = originalTokenUsage
		itelemetry.ChatMetricGenAIClientOperationDuration = originalOperationDuration
		itelemetry.ChatMetricGenAIServerTimeToFirstToken = originalServerTimeToFirstToken
		itelemetry.ChatMetricTRPCAgentGoClientTimeToFirstToken = originalClientTimeToFirstToken
		itelemetry.ChatMetricTRPCAgentGoClientTimePerOutputToken = originalTimePerOutputToken
		itelemetry.ChatMetricTRPCAgentGoClientOutputTokenPerTime = originalOutputTokenPerTime
	}()
	itelemetry.MeterProvider = provider
	itelemetry.ChatMeter = provider.Meter(semconvmetrics.MeterNameChat)
	requestCnt, err := itelemetry.ChatMeter.Int64Counter("trpc_agent_go.client.request.cnt")
	require.NoError(t, err)
	itelemetry.ChatMetricTRPCAgentGoClientRequestCnt = requestCnt
	itelemetry.ChatMetricGenAIClientTokenUsage = nil
	itelemetry.ChatMetricGenAIClientOperationDuration = nil
	itelemetry.ChatMetricGenAIServerTimeToFirstToken = nil
	itelemetry.ChatMetricTRPCAgentGoClientTimeToFirstToken = nil
	itelemetry.ChatMetricTRPCAgentGoClientTimePerOutputToken = nil
	itelemetry.ChatMetricTRPCAgentGoClientOutputTokenPerTime = nil
	invocation := agent.NewInvocation(
		agent.WithInvocationID("inv-disable-usage-metrics"),
		agent.WithInvocationModel(&captureModel{}),
		agent.WithInvocationRunOptions(agent.RunOptions{
			DisableResponseUsageTracking: true,
		}),
		agent.WithInvocationSession(&session.Session{ID: "sess-disable-usage-metrics"}),
	)
	resp, err := executeModelAndProcessResponses(
		agent.NewInvocationContext(context.Background(), invocation),
		modelExecutionConfig{
			Invocation:   invocation,
			LLMModel:     invocation.Model,
			Request:      &model.Request{},
			InvocationID: invocation.InvocationID,
			SessionID:    invocation.Session.ID,
			Span:         noop.Span{},
			NodeID:       "llm",
		},
	)
	require.NoError(t, err)
	finalResponse, ok := resp.(*model.Response)
	require.True(t, ok)
	require.Nil(t, finalResponse.Usage)
	var rm metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(context.Background(), &rm))
	require.NotEmpty(t, rm.ScopeMetrics)
}

func TestExecuteModelAndProcessResponses_UsesStableInvocationForMetricsMetadata(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	originalProvider := itelemetry.MeterProvider
	originalMeter := itelemetry.ChatMeter
	originalRequestCnt := itelemetry.ChatMetricTRPCAgentGoClientRequestCnt
	originalTokenUsage := itelemetry.ChatMetricGenAIClientTokenUsage
	originalOperationDuration := itelemetry.ChatMetricGenAIClientOperationDuration
	originalServerTimeToFirstToken := itelemetry.ChatMetricGenAIServerTimeToFirstToken
	originalClientTimeToFirstToken := itelemetry.ChatMetricTRPCAgentGoClientTimeToFirstToken
	originalTimePerOutputToken := itelemetry.ChatMetricTRPCAgentGoClientTimePerOutputToken
	originalOutputTokenPerTime := itelemetry.ChatMetricTRPCAgentGoClientOutputTokenPerTime
	defer func() {
		itelemetry.MeterProvider = originalProvider
		itelemetry.ChatMeter = originalMeter
		itelemetry.ChatMetricTRPCAgentGoClientRequestCnt = originalRequestCnt
		itelemetry.ChatMetricGenAIClientTokenUsage = originalTokenUsage
		itelemetry.ChatMetricGenAIClientOperationDuration = originalOperationDuration
		itelemetry.ChatMetricGenAIServerTimeToFirstToken = originalServerTimeToFirstToken
		itelemetry.ChatMetricTRPCAgentGoClientTimeToFirstToken = originalClientTimeToFirstToken
		itelemetry.ChatMetricTRPCAgentGoClientTimePerOutputToken = originalTimePerOutputToken
		itelemetry.ChatMetricTRPCAgentGoClientOutputTokenPerTime = originalOutputTokenPerTime
	}()
	itelemetry.MeterProvider = provider
	itelemetry.ChatMeter = provider.Meter(semconvmetrics.MeterNameChat)
	requestCnt, err := itelemetry.ChatMeter.Int64Counter("trpc_agent_go.client.request.cnt")
	require.NoError(t, err)
	itelemetry.ChatMetricTRPCAgentGoClientRequestCnt = requestCnt
	itelemetry.ChatMetricGenAIClientTokenUsage = nil
	itelemetry.ChatMetricGenAIClientOperationDuration = nil
	itelemetry.ChatMetricGenAIServerTimeToFirstToken = nil
	itelemetry.ChatMetricTRPCAgentGoClientTimeToFirstToken = nil
	itelemetry.ChatMetricTRPCAgentGoClientTimePerOutputToken = nil
	itelemetry.ChatMetricTRPCAgentGoClientOutputTokenPerTime = nil
	baseModel := &mockModel{name: "base-metrics-model"}
	baseInvocation := agent.NewInvocation(
		agent.WithInvocationID("inv-base-metrics"),
		agent.WithInvocationModel(baseModel),
		agent.WithInvocationSession(&session.Session{
			ID:      "sess-base-metrics",
			UserID:  "user-base-metrics",
			AppName: "app-base-metrics",
		}),
	)
	baseInvocation.AgentName = "agent-base-metrics"
	updatedInvocation := agent.NewInvocation(
		agent.WithInvocationID("inv-updated-metrics"),
		agent.WithInvocationModel(&mockModel{name: "updated-metrics-model"}),
		agent.WithInvocationSession(&session.Session{
			ID: "sess-updated-metrics",
		}),
	)
	updatedInvocation.AgentName = "agent-updated-metrics"
	callbacks := model.NewCallbacks().RegisterBeforeModel(
		func(ctx context.Context, args *model.BeforeModelArgs) (*model.BeforeModelResult, error) {
			return &model.BeforeModelResult{
				Context: agent.NewInvocationContext(ctx, updatedInvocation),
			}, nil
		},
	)
	resp, err := executeModelAndProcessResponses(
		agent.NewInvocationContext(context.Background(), baseInvocation),
		modelExecutionConfig{
			Invocation:     baseInvocation,
			ModelCallbacks: callbacks,
			LLMModel:       baseModel,
			Request:        &model.Request{},
			InvocationID:   baseInvocation.InvocationID,
			SessionID:      baseInvocation.Session.ID,
			Span:           noop.Span{},
			NodeID:         "llm",
		},
	)
	require.NoError(t, err)
	require.NotNil(t, resp)
	var rm metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(context.Background(), &rm))
	require.True(t, resourceMetricsContainAttribute(rm, semconvtrace.KeyGenAIAgentName, baseInvocation.AgentName))
	require.True(t, resourceMetricsContainAttribute(rm, semconvtrace.KeyGenAIRequestModel, baseModel.Info().Name))
	require.True(t, resourceMetricsContainAttribute(rm, semconvtrace.KeyGenAIConversationID, baseInvocation.Session.ID))
	require.True(t, resourceMetricsContainAttribute(rm, semconvtrace.KeyTRPCAgentGoUserID, baseInvocation.Session.UserID))
	require.True(t, resourceMetricsContainAttribute(rm, semconvtrace.KeyTRPCAgentGoAppName, baseInvocation.Session.AppName))
	require.False(t, resourceMetricsContainAttribute(rm, semconvtrace.KeyGenAIAgentName, updatedInvocation.AgentName))
	require.False(t, resourceMetricsContainAttribute(rm, semconvtrace.KeyGenAIRequestModel, updatedInvocation.Model.Info().Name))
	require.False(t, resourceMetricsContainAttribute(rm, semconvtrace.KeyGenAIConversationID, updatedInvocation.Session.ID))
}

func TestExecuteModelAndProcessResponses_UsesUpdatedInvocationForMetricsMetadataWhenBaseIsSparse(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	originalProvider := itelemetry.MeterProvider
	originalMeter := itelemetry.ChatMeter
	originalRequestCnt := itelemetry.ChatMetricTRPCAgentGoClientRequestCnt
	originalTokenUsage := itelemetry.ChatMetricGenAIClientTokenUsage
	originalOperationDuration := itelemetry.ChatMetricGenAIClientOperationDuration
	originalServerTimeToFirstToken := itelemetry.ChatMetricGenAIServerTimeToFirstToken
	originalClientTimeToFirstToken := itelemetry.ChatMetricTRPCAgentGoClientTimeToFirstToken
	originalTimePerOutputToken := itelemetry.ChatMetricTRPCAgentGoClientTimePerOutputToken
	originalOutputTokenPerTime := itelemetry.ChatMetricTRPCAgentGoClientOutputTokenPerTime
	defer func() {
		itelemetry.MeterProvider = originalProvider
		itelemetry.ChatMeter = originalMeter
		itelemetry.ChatMetricTRPCAgentGoClientRequestCnt = originalRequestCnt
		itelemetry.ChatMetricGenAIClientTokenUsage = originalTokenUsage
		itelemetry.ChatMetricGenAIClientOperationDuration = originalOperationDuration
		itelemetry.ChatMetricGenAIServerTimeToFirstToken = originalServerTimeToFirstToken
		itelemetry.ChatMetricTRPCAgentGoClientTimeToFirstToken = originalClientTimeToFirstToken
		itelemetry.ChatMetricTRPCAgentGoClientTimePerOutputToken = originalTimePerOutputToken
		itelemetry.ChatMetricTRPCAgentGoClientOutputTokenPerTime = originalOutputTokenPerTime
	}()
	itelemetry.MeterProvider = provider
	itelemetry.ChatMeter = provider.Meter(semconvmetrics.MeterNameChat)
	requestCnt, err := itelemetry.ChatMeter.Int64Counter("trpc_agent_go.client.request.cnt")
	require.NoError(t, err)
	itelemetry.ChatMetricTRPCAgentGoClientRequestCnt = requestCnt
	itelemetry.ChatMetricGenAIClientTokenUsage = nil
	itelemetry.ChatMetricGenAIClientOperationDuration = nil
	itelemetry.ChatMetricGenAIServerTimeToFirstToken = nil
	itelemetry.ChatMetricTRPCAgentGoClientTimeToFirstToken = nil
	itelemetry.ChatMetricTRPCAgentGoClientTimePerOutputToken = nil
	itelemetry.ChatMetricTRPCAgentGoClientOutputTokenPerTime = nil
	baseInvocation := agent.NewInvocation(
		agent.WithInvocationID("inv-base-sparse-metrics"),
		agent.WithInvocationRunOptions(agent.RunOptions{
			DisableResponseUsageTracking: true,
		}),
	)
	updatedModel := &mockModel{name: "updated-metrics-model"}
	updatedInvocation := agent.NewInvocation(
		agent.WithInvocationID("inv-updated-full-metrics"),
		agent.WithInvocationModel(updatedModel),
		agent.WithInvocationSession(&session.Session{
			ID:      "sess-updated-full-metrics",
			UserID:  "user-updated-full-metrics",
			AppName: "app-updated-full-metrics",
		}),
	)
	updatedInvocation.AgentName = "agent-updated-full-metrics"
	callbacks := model.NewCallbacks().RegisterBeforeModel(
		func(ctx context.Context, args *model.BeforeModelArgs) (*model.BeforeModelResult, error) {
			return &model.BeforeModelResult{
				Context: agent.NewInvocationContext(ctx, updatedInvocation),
			}, nil
		},
	)
	resp, err := executeModelAndProcessResponses(
		agent.NewInvocationContext(context.Background(), updatedInvocation),
		modelExecutionConfig{
			Invocation:     baseInvocation,
			ModelCallbacks: callbacks,
			LLMModel:       updatedModel,
			Request:        &model.Request{},
			InvocationID:   baseInvocation.InvocationID,
			SessionID:      updatedInvocation.Session.ID,
			Span:           noop.Span{},
			NodeID:         "llm",
		},
	)
	require.NoError(t, err)
	require.NotNil(t, resp)
	var rm metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(context.Background(), &rm))
	require.True(t, resourceMetricsContainAttribute(rm, semconvtrace.KeyGenAIAgentName, updatedInvocation.AgentName))
	require.True(t, resourceMetricsContainAttribute(rm, semconvtrace.KeyGenAIRequestModel, updatedModel.Info().Name))
	require.True(t, resourceMetricsContainAttribute(rm, semconvtrace.KeyGenAIConversationID, updatedInvocation.Session.ID))
	require.True(t, resourceMetricsContainAttribute(rm, semconvtrace.KeyTRPCAgentGoUserID, updatedInvocation.Session.UserID))
	require.True(t, resourceMetricsContainAttribute(rm, semconvtrace.KeyTRPCAgentGoAppName, updatedInvocation.Session.AppName))
}

func TestExecuteModelAndProcessResponses_AnchorsMetricsRequestModelToLLMModel(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	originalProvider := itelemetry.MeterProvider
	originalMeter := itelemetry.ChatMeter
	originalRequestCnt := itelemetry.ChatMetricTRPCAgentGoClientRequestCnt
	originalTokenUsage := itelemetry.ChatMetricGenAIClientTokenUsage
	originalOperationDuration := itelemetry.ChatMetricGenAIClientOperationDuration
	originalServerTimeToFirstToken := itelemetry.ChatMetricGenAIServerTimeToFirstToken
	originalClientTimeToFirstToken := itelemetry.ChatMetricTRPCAgentGoClientTimeToFirstToken
	originalTimePerOutputToken := itelemetry.ChatMetricTRPCAgentGoClientTimePerOutputToken
	originalOutputTokenPerTime := itelemetry.ChatMetricTRPCAgentGoClientOutputTokenPerTime
	defer func() {
		itelemetry.MeterProvider = originalProvider
		itelemetry.ChatMeter = originalMeter
		itelemetry.ChatMetricTRPCAgentGoClientRequestCnt = originalRequestCnt
		itelemetry.ChatMetricGenAIClientTokenUsage = originalTokenUsage
		itelemetry.ChatMetricGenAIClientOperationDuration = originalOperationDuration
		itelemetry.ChatMetricGenAIServerTimeToFirstToken = originalServerTimeToFirstToken
		itelemetry.ChatMetricTRPCAgentGoClientTimeToFirstToken = originalClientTimeToFirstToken
		itelemetry.ChatMetricTRPCAgentGoClientTimePerOutputToken = originalTimePerOutputToken
		itelemetry.ChatMetricTRPCAgentGoClientOutputTokenPerTime = originalOutputTokenPerTime
	}()
	itelemetry.MeterProvider = provider
	itelemetry.ChatMeter = provider.Meter(semconvmetrics.MeterNameChat)
	requestCnt, err := itelemetry.ChatMeter.Int64Counter("trpc_agent_go.client.request.cnt")
	require.NoError(t, err)
	itelemetry.ChatMetricTRPCAgentGoClientRequestCnt = requestCnt
	itelemetry.ChatMetricGenAIClientTokenUsage = nil
	itelemetry.ChatMetricGenAIClientOperationDuration = nil
	itelemetry.ChatMetricGenAIServerTimeToFirstToken = nil
	itelemetry.ChatMetricTRPCAgentGoClientTimeToFirstToken = nil
	itelemetry.ChatMetricTRPCAgentGoClientTimePerOutputToken = nil
	itelemetry.ChatMetricTRPCAgentGoClientOutputTokenPerTime = nil
	requestModel := &mockModel{name: "request-model"}
	callbackModel := &mockModel{name: "callback-model"}
	baseInvocation := agent.NewInvocation(
		agent.WithInvocationID("inv-base-no-model"),
	)
	updatedInvocation := agent.NewInvocation(
		agent.WithInvocationID("inv-updated-with-model"),
		agent.WithInvocationModel(callbackModel),
		agent.WithInvocationSession(&session.Session{
			ID:      "sess-updated-with-model",
			UserID:  "user-updated-with-model",
			AppName: "app-updated-with-model",
		}),
	)
	updatedInvocation.AgentName = "agent-updated-with-model"
	callbacks := model.NewCallbacks().RegisterBeforeModel(
		func(ctx context.Context, args *model.BeforeModelArgs) (*model.BeforeModelResult, error) {
			return &model.BeforeModelResult{
				Context: agent.NewInvocationContext(ctx, updatedInvocation),
			}, nil
		},
	)
	resp, err := executeModelAndProcessResponses(
		agent.NewInvocationContext(context.Background(), baseInvocation),
		modelExecutionConfig{
			Invocation:     baseInvocation,
			ModelCallbacks: callbacks,
			LLMModel:       requestModel,
			Request:        &model.Request{},
			InvocationID:   baseInvocation.InvocationID,
			SessionID:      updatedInvocation.Session.ID,
			Span:           noop.Span{},
			NodeID:         "llm",
		},
	)
	require.NoError(t, err)
	require.NotNil(t, resp)
	var rm metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(context.Background(), &rm))
	require.True(t, resourceMetricsContainAttribute(rm, semconvtrace.KeyGenAIAgentName, updatedInvocation.AgentName))
	require.True(t, resourceMetricsContainAttribute(rm, semconvtrace.KeyGenAIRequestModel, requestModel.Info().Name))
	require.False(t, resourceMetricsContainAttribute(rm, semconvtrace.KeyGenAIRequestModel, callbackModel.Info().Name))
	require.True(t, resourceMetricsContainAttribute(rm, semconvtrace.KeyGenAIConversationID, updatedInvocation.Session.ID))
	require.True(t, resourceMetricsContainAttribute(rm, semconvtrace.KeyTRPCAgentGoUserID, updatedInvocation.Session.UserID))
	require.True(t, resourceMetricsContainAttribute(rm, semconvtrace.KeyTRPCAgentGoAppName, updatedInvocation.Session.AppName))
}

func TestExecuteModelAndProcessResponses_UsesConfigFallbackForSparseStableMetricsMetadata(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	originalProvider := itelemetry.MeterProvider
	originalMeter := itelemetry.ChatMeter
	originalRequestCnt := itelemetry.ChatMetricTRPCAgentGoClientRequestCnt
	originalTokenUsage := itelemetry.ChatMetricGenAIClientTokenUsage
	originalOperationDuration := itelemetry.ChatMetricGenAIClientOperationDuration
	originalServerTimeToFirstToken := itelemetry.ChatMetricGenAIServerTimeToFirstToken
	originalClientTimeToFirstToken := itelemetry.ChatMetricTRPCAgentGoClientTimeToFirstToken
	originalTimePerOutputToken := itelemetry.ChatMetricTRPCAgentGoClientTimePerOutputToken
	originalOutputTokenPerTime := itelemetry.ChatMetricTRPCAgentGoClientOutputTokenPerTime
	defer func() {
		itelemetry.MeterProvider = originalProvider
		itelemetry.ChatMeter = originalMeter
		itelemetry.ChatMetricTRPCAgentGoClientRequestCnt = originalRequestCnt
		itelemetry.ChatMetricGenAIClientTokenUsage = originalTokenUsage
		itelemetry.ChatMetricGenAIClientOperationDuration = originalOperationDuration
		itelemetry.ChatMetricGenAIServerTimeToFirstToken = originalServerTimeToFirstToken
		itelemetry.ChatMetricTRPCAgentGoClientTimeToFirstToken = originalClientTimeToFirstToken
		itelemetry.ChatMetricTRPCAgentGoClientTimePerOutputToken = originalTimePerOutputToken
		itelemetry.ChatMetricTRPCAgentGoClientOutputTokenPerTime = originalOutputTokenPerTime
	}()
	itelemetry.MeterProvider = provider
	itelemetry.ChatMeter = provider.Meter(semconvmetrics.MeterNameChat)
	requestCnt, err := itelemetry.ChatMeter.Int64Counter("trpc_agent_go.client.request.cnt")
	require.NoError(t, err)
	itelemetry.ChatMetricTRPCAgentGoClientRequestCnt = requestCnt
	itelemetry.ChatMetricGenAIClientTokenUsage = nil
	itelemetry.ChatMetricGenAIClientOperationDuration = nil
	itelemetry.ChatMetricGenAIServerTimeToFirstToken = nil
	itelemetry.ChatMetricTRPCAgentGoClientTimeToFirstToken = nil
	itelemetry.ChatMetricTRPCAgentGoClientTimePerOutputToken = nil
	itelemetry.ChatMetricTRPCAgentGoClientOutputTokenPerTime = nil
	requestModel := &mockModel{name: "config-fallback-model"}
	invocation := agent.NewInvocation(
		agent.WithInvocationID("inv-sparse-config-fallback"),
	)
	invocation.AgentName = "agent-sparse-config-fallback"
	resp, err := executeModelAndProcessResponses(
		agent.NewInvocationContext(context.Background(), invocation),
		modelExecutionConfig{
			Invocation:   invocation,
			LLMModel:     requestModel,
			Request:      &model.Request{},
			InvocationID: invocation.InvocationID,
			SessionID:    "sess-config-fallback",
			UserID:       "user-config-fallback",
			AppName:      "app-config-fallback",
			Span:         noop.Span{},
			NodeID:       "llm",
		},
	)
	require.NoError(t, err)
	require.NotNil(t, resp)
	var rm metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(context.Background(), &rm))
	require.True(t, resourceMetricsContainAttribute(rm, semconvtrace.KeyGenAIAgentName, invocation.AgentName))
	require.True(t, resourceMetricsContainAttribute(rm, semconvtrace.KeyGenAIRequestModel, requestModel.Info().Name))
	require.True(t, resourceMetricsContainAttribute(rm, semconvtrace.KeyGenAIConversationID, "sess-config-fallback"))
	require.True(t, resourceMetricsContainAttribute(rm, semconvtrace.KeyTRPCAgentGoUserID, "user-config-fallback"))
	require.True(t, resourceMetricsContainAttribute(rm, semconvtrace.KeyTRPCAgentGoAppName, "app-config-fallback"))
}

func TestExecuteModelAndProcessResponses_UsesUpdatedInvocationForMetricsMetadataAfterCallbackOnSingleChunk(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	originalProvider := itelemetry.MeterProvider
	originalMeter := itelemetry.ChatMeter
	originalRequestCnt := itelemetry.ChatMetricTRPCAgentGoClientRequestCnt
	originalTokenUsage := itelemetry.ChatMetricGenAIClientTokenUsage
	originalOperationDuration := itelemetry.ChatMetricGenAIClientOperationDuration
	originalServerTimeToFirstToken := itelemetry.ChatMetricGenAIServerTimeToFirstToken
	originalClientTimeToFirstToken := itelemetry.ChatMetricTRPCAgentGoClientTimeToFirstToken
	originalTimePerOutputToken := itelemetry.ChatMetricTRPCAgentGoClientTimePerOutputToken
	originalOutputTokenPerTime := itelemetry.ChatMetricTRPCAgentGoClientOutputTokenPerTime
	defer func() {
		itelemetry.MeterProvider = originalProvider
		itelemetry.ChatMeter = originalMeter
		itelemetry.ChatMetricTRPCAgentGoClientRequestCnt = originalRequestCnt
		itelemetry.ChatMetricGenAIClientTokenUsage = originalTokenUsage
		itelemetry.ChatMetricGenAIClientOperationDuration = originalOperationDuration
		itelemetry.ChatMetricGenAIServerTimeToFirstToken = originalServerTimeToFirstToken
		itelemetry.ChatMetricTRPCAgentGoClientTimeToFirstToken = originalClientTimeToFirstToken
		itelemetry.ChatMetricTRPCAgentGoClientTimePerOutputToken = originalTimePerOutputToken
		itelemetry.ChatMetricTRPCAgentGoClientOutputTokenPerTime = originalOutputTokenPerTime
	}()
	itelemetry.MeterProvider = provider
	itelemetry.ChatMeter = provider.Meter(semconvmetrics.MeterNameChat)
	requestCnt, err := itelemetry.ChatMeter.Int64Counter("trpc_agent_go.client.request.cnt")
	require.NoError(t, err)
	itelemetry.ChatMetricTRPCAgentGoClientRequestCnt = requestCnt
	itelemetry.ChatMetricGenAIClientTokenUsage = nil
	itelemetry.ChatMetricGenAIClientOperationDuration = nil
	itelemetry.ChatMetricGenAIServerTimeToFirstToken = nil
	itelemetry.ChatMetricTRPCAgentGoClientTimeToFirstToken = nil
	itelemetry.ChatMetricTRPCAgentGoClientTimePerOutputToken = nil
	itelemetry.ChatMetricTRPCAgentGoClientOutputTokenPerTime = nil
	baseModel := &mockModel{name: "base-after-metrics-model"}
	baseInvocation := agent.NewInvocation(
		agent.WithInvocationID("inv-base-after-metrics"),
	)
	updatedInvocation := agent.NewInvocation(
		agent.WithInvocationID("inv-updated-after-metrics"),
		agent.WithInvocationModel(baseModel),
		agent.WithInvocationSession(&session.Session{
			ID:      "sess-updated-after-metrics",
			UserID:  "user-updated-after-metrics",
			AppName: "app-updated-after-metrics",
		}),
	)
	updatedInvocation.AgentName = "agent-updated-after-metrics"
	callbacks := model.NewCallbacks().RegisterAfterModel(
		func(ctx context.Context, args *model.AfterModelArgs) (*model.AfterModelResult, error) {
			return &model.AfterModelResult{
				Context: agent.NewInvocationContext(ctx, updatedInvocation),
			}, nil
		},
	)
	resp, err := executeModelAndProcessResponses(
		agent.NewInvocationContext(context.Background(), baseInvocation),
		modelExecutionConfig{
			Invocation:     baseInvocation,
			ModelCallbacks: callbacks,
			LLMModel:       baseModel,
			Request:        &model.Request{},
			InvocationID:   baseInvocation.InvocationID,
			SessionID:      updatedInvocation.Session.ID,
			Span:           noop.Span{},
			NodeID:         "llm",
		},
	)
	require.NoError(t, err)
	require.NotNil(t, resp)
	var rm metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(context.Background(), &rm))
	require.True(t, resourceMetricsContainAttribute(rm, semconvtrace.KeyGenAIAgentName, updatedInvocation.AgentName))
	require.True(t, resourceMetricsContainAttribute(rm, semconvtrace.KeyGenAIRequestModel, baseModel.Info().Name))
	require.True(t, resourceMetricsContainAttribute(rm, semconvtrace.KeyGenAIConversationID, updatedInvocation.Session.ID))
	require.True(t, resourceMetricsContainAttribute(rm, semconvtrace.KeyTRPCAgentGoUserID, updatedInvocation.Session.UserID))
	require.True(t, resourceMetricsContainAttribute(rm, semconvtrace.KeyTRPCAgentGoAppName, updatedInvocation.Session.AppName))
}

func TestExecuteModelAndProcessResponses_UsesUpdatedInvocationForResponseUsageTimingOnSingleChunk(t *testing.T) {
	baseInvocation := agent.NewInvocation(
		agent.WithInvocationID("inv-base-single-usage"),
	)
	updatedInvocation := agent.NewInvocation(
		agent.WithInvocationID("inv-updated-single-usage"),
		agent.WithInvocationRunOptions(agent.RunOptions{
			DisableResponseUsageTracking: true,
		}),
	)
	callbacks := model.NewCallbacks().RegisterAfterModel(
		func(ctx context.Context, args *model.AfterModelArgs) (*model.AfterModelResult, error) {
			return &model.AfterModelResult{
				Context: agent.NewInvocationContext(ctx, updatedInvocation),
			}, nil
		},
	)
	resp, err := executeModelAndProcessResponses(
		agent.NewInvocationContext(context.Background(), baseInvocation),
		modelExecutionConfig{
			Invocation:     baseInvocation,
			ModelCallbacks: callbacks,
			LLMModel:       &mockModel{name: "single-usage-model"},
			Request:        &model.Request{},
			InvocationID:   baseInvocation.InvocationID,
			Span:           noop.Span{},
			NodeID:         "llm",
		},
	)
	require.NoError(t, err)
	finalResponse, ok := resp.(*model.Response)
	require.True(t, ok)
	require.Nil(t, finalResponse.Usage)
}

func TestExecuteModelAndProcessResponses_UsesUpdatedInvocationForResponseUsageTiming(t *testing.T) {
	baseInvocation := agent.NewInvocation(
		agent.WithInvocationID("inv-usage-base"),
	)
	updatedInvocation := agent.NewInvocation(
		agent.WithInvocationID("inv-usage-updated"),
		agent.WithInvocationRunOptions(agent.RunOptions{
			DisableResponseUsageTracking: true,
		}),
	)
	responses := []*model.Response{
		{
			IsPartial: true,
			Choices: []model.Choice{
				{Message: model.NewAssistantMessage("partial")},
			},
		},
		{
			IsPartial: true,
			Choices: []model.Choice{
				{Message: model.NewAssistantMessage("partial-updated")},
			},
		},
		{
			Done: true,
			Choices: []model.Choice{
				{Message: model.NewAssistantMessage("done")},
			},
		},
	}
	var callbackCount int
	callbacks := model.NewCallbacks().RegisterAfterModel(
		func(ctx context.Context, args *model.AfterModelArgs) (*model.AfterModelResult, error) {
			callbackCount++
			if callbackCount == 2 {
				return &model.AfterModelResult{
					Context: agent.NewInvocationContext(ctx, updatedInvocation),
				}, nil
			}
			return &model.AfterModelResult{}, nil
		},
	)
	resp, err := executeModelAndProcessResponses(
		agent.NewInvocationContext(context.Background(), baseInvocation),
		modelExecutionConfig{
			Invocation:     baseInvocation,
			ModelCallbacks: callbacks,
			LLMModel: &multiResponseModel{
				responses: responses,
			},
			Request:      &model.Request{},
			InvocationID: baseInvocation.InvocationID,
			Span:         noop.Span{},
			NodeID:       "llm",
		},
	)
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.NotNil(t, responses[0].Usage)
	require.Nil(t, responses[1].Usage)
}

func TestExecuteModelAndProcessResponses_PreservesTimingInfoWhenInvocationChanges(t *testing.T) {
	baseInvocation := agent.NewInvocation(
		agent.WithInvocationID("inv-usage-base"),
	)
	updatedInvocation := agent.NewInvocation(
		agent.WithInvocationID("inv-usage-updated"),
	)
	responses := []*model.Response{
		{
			IsPartial: true,
			Choices: []model.Choice{
				{Message: model.NewAssistantMessage("partial")},
			},
		},
		{
			IsPartial: true,
			Choices: []model.Choice{
				{Message: model.NewAssistantMessage("partial-updated")},
			},
		},
		{
			Done: true,
			Choices: []model.Choice{
				{Message: model.NewAssistantMessage("done")},
			},
		},
	}
	var callbackCount int
	callbacks := model.NewCallbacks().RegisterAfterModel(
		func(ctx context.Context, args *model.AfterModelArgs) (*model.AfterModelResult, error) {
			callbackCount++
			if callbackCount == 2 {
				return &model.AfterModelResult{
					Context: agent.NewInvocationContext(ctx, updatedInvocation),
				}, nil
			}
			return &model.AfterModelResult{}, nil
		},
	)
	resp, err := executeModelAndProcessResponses(
		agent.NewInvocationContext(context.Background(), baseInvocation),
		modelExecutionConfig{
			Invocation:     baseInvocation,
			ModelCallbacks: callbacks,
			LLMModel: &multiResponseModel{
				responses: responses,
				delay:     time.Millisecond,
			},
			Request:      &model.Request{},
			InvocationID: baseInvocation.InvocationID,
			Span:         noop.Span{},
			NodeID:       "llm",
		},
	)
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.NotNil(t, responses[0].Usage)
	require.NotNil(t, responses[1].Usage)
	require.NotSame(t, responses[0].Usage, responses[1].Usage)
	require.Same(t, baseInvocation.GetOrCreateTimingInfo(), responses[0].Usage.TimingInfo)
	require.Same(t, updatedInvocation.GetOrCreateTimingInfo(), responses[1].Usage.TimingInfo)
	require.NotZero(t, responses[1].Usage.TimingInfo.FirstTokenDuration)
	require.Equal(
		t,
		responses[0].Usage.TimingInfo.FirstTokenDuration,
		responses[1].Usage.TimingInfo.FirstTokenDuration,
	)
}

func TestExecuteModelAndProcessResponses_PreservesReasoningTimingWhenInvocationChanges(t *testing.T) {
	baseInvocation := agent.NewInvocation(
		agent.WithInvocationID("inv-reasoning-base"),
	)
	updatedInvocation := agent.NewInvocation(
		agent.WithInvocationID("inv-reasoning-updated"),
	)
	responses := []*model.Response{
		{
			IsPartial: true,
			Choices: []model.Choice{
				{Delta: model.Message{ReasoningContent: "thinking"}},
			},
		},
		{
			IsPartial: true,
			Choices: []model.Choice{
				{Delta: model.Message{ReasoningContent: "thinking-more"}},
			},
		},
		{
			Done: true,
			Choices: []model.Choice{
				{Delta: model.Message{Content: "done"}},
			},
		},
	}
	var callbackCount int
	callbacks := model.NewCallbacks().RegisterAfterModel(
		func(ctx context.Context, args *model.AfterModelArgs) (*model.AfterModelResult, error) {
			callbackCount++
			if callbackCount == 1 {
				return &model.AfterModelResult{
					Context: agent.NewInvocationContext(ctx, updatedInvocation),
				}, nil
			}
			return &model.AfterModelResult{}, nil
		},
	)
	resp, err := executeModelAndProcessResponses(
		agent.NewInvocationContext(context.Background(), baseInvocation),
		modelExecutionConfig{
			Invocation:     baseInvocation,
			ModelCallbacks: callbacks,
			LLMModel: &multiResponseModel{
				responses: responses,
				delay:     time.Millisecond,
			},
			Request: &model.Request{
				GenerationConfig: model.GenerationConfig{
					Stream: true,
				},
			},
			InvocationID: baseInvocation.InvocationID,
			Span:         noop.Span{},
			NodeID:       "llm",
		},
	)
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.Zero(t, baseInvocation.GetOrCreateTimingInfo().ReasoningDuration)
	require.Greater(t, updatedInvocation.GetOrCreateTimingInfo().ReasoningDuration, time.Duration(0))
}

func TestExecuteModelAndProcessResponses_PreservesReasoningTimingWhenTrackingDisabledMidStream(t *testing.T) {
	baseInvocation := agent.NewInvocation(
		agent.WithInvocationID("inv-disable-reasoning-base"),
	)
	updatedInvocation := agent.NewInvocation(
		agent.WithInvocationID("inv-disable-reasoning-updated"),
		agent.WithInvocationRunOptions(agent.RunOptions{
			DisableResponseUsageTracking: true,
		}),
	)
	responses := []*model.Response{
		{
			IsPartial: true,
			Choices: []model.Choice{
				{Delta: model.Message{ReasoningContent: "thinking"}},
			},
		},
		{
			IsPartial: true,
			Choices: []model.Choice{
				{Delta: model.Message{ReasoningContent: "thinking-more"}},
			},
		},
		{
			Done: true,
			Choices: []model.Choice{
				{Delta: model.Message{Content: "done"}},
			},
		},
	}
	var callbackCount int
	callbacks := model.NewCallbacks().RegisterAfterModel(
		func(ctx context.Context, args *model.AfterModelArgs) (*model.AfterModelResult, error) {
			callbackCount++
			if callbackCount == 1 {
				return &model.AfterModelResult{
					Context: agent.NewInvocationContext(ctx, updatedInvocation),
				}, nil
			}
			return &model.AfterModelResult{}, nil
		},
	)
	resp, err := executeModelAndProcessResponses(
		agent.NewInvocationContext(context.Background(), baseInvocation),
		modelExecutionConfig{
			Invocation:     baseInvocation,
			ModelCallbacks: callbacks,
			LLMModel: &multiResponseModel{
				responses: responses,
				delay:     time.Millisecond,
			},
			Request: &model.Request{
				GenerationConfig: model.GenerationConfig{
					Stream: true,
				},
			},
			InvocationID: baseInvocation.InvocationID,
			Span:         noop.Span{},
			NodeID:       "llm",
		},
	)
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.Greater(t, baseInvocation.GetOrCreateTimingInfo().ReasoningDuration, time.Duration(0))
	require.Nil(t, responses[1].Usage)
	require.Nil(t, responses[2].Usage)
}

func TestExecuteModelAndProcessResponses_PreservesStableEventMetadataOnFastPath(t *testing.T) {
	baseInvocation := agent.NewInvocation(
		agent.WithInvocationID("inv-fast-base"),
		agent.WithInvocationEventFilterKey("graph/fast"),
		agent.WithInvocationRunOptions(agent.RunOptions{
			RequestID: "req-fast-base",
		}),
	)
	updatedInvocation := agent.NewInvocation(
		agent.WithInvocationID("inv-fast-updated"),
		agent.WithInvocationRunOptions(agent.RunOptions{
			DisablePartialEventIDs:        true,
			DisablePartialEventTimestamps: true,
		}),
	)
	responses := []*model.Response{
		{
			IsPartial: true,
			Choices: []model.Choice{
				{Message: model.NewAssistantMessage("partial")},
			},
		},
		{
			Done: true,
			Choices: []model.Choice{
				{Message: model.NewAssistantMessage("done")},
			},
		},
	}
	callbacks := model.NewCallbacks().RegisterBeforeModel(
		func(ctx context.Context, args *model.BeforeModelArgs) (*model.BeforeModelResult, error) {
			return &model.BeforeModelResult{
				Context: agent.NewInvocationContext(ctx, updatedInvocation),
			}, nil
		},
	)
	ch := make(chan *event.Event, 4)
	resp, err := executeModelAndProcessResponses(
		agent.NewInvocationContext(context.Background(), baseInvocation),
		modelExecutionConfig{
			Invocation:     baseInvocation,
			ModelCallbacks: callbacks,
			LLMModel: &multiResponseModel{
				responses: responses,
			},
			Request:      &model.Request{},
			EventChan:    ch,
			InvocationID: baseInvocation.InvocationID,
			Span:         noop.Span{},
			NodeID:       "llm",
		},
	)
	require.NoError(t, err)
	require.NotNil(t, resp)
	ev := <-ch
	require.NotNil(t, ev)
	require.Equal(t, baseInvocation.InvocationID, ev.InvocationID)
	require.Equal(t, baseInvocation.RunOptions.RequestID, ev.RequestID)
	require.Equal(t, baseInvocation.GetEventFilterKey(), ev.FilterKey)
	require.Empty(t, ev.ID)
	require.True(t, ev.Timestamp.IsZero())
}

func TestExecuteModelAndProcessResponses_UsesStableInvocationForTraceMetadata(t *testing.T) {
	modelImpl := &captureModel{}
	baseInvocation := agent.NewInvocation(
		agent.WithInvocationID("inv-trace-base"),
		agent.WithInvocationModel(modelImpl),
		agent.WithInvocationSession(&session.Session{
			ID:     "sess-trace-base",
			UserID: "user-trace-base",
		}),
	)
	updatedInvocation := agent.NewInvocation(
		agent.WithInvocationID("inv-trace-updated"),
	)
	callbacks := model.NewCallbacks().RegisterAfterModel(
		func(ctx context.Context, args *model.AfterModelArgs) (*model.AfterModelResult, error) {
			return &model.AfterModelResult{
				Context: agent.NewInvocationContext(ctx, updatedInvocation),
			}, nil
		},
	)
	span := newGraphRecordingSpan()
	resp, err := executeModelAndProcessResponses(
		agent.NewInvocationContext(context.Background(), baseInvocation),
		modelExecutionConfig{
			Invocation:     baseInvocation,
			ModelCallbacks: callbacks,
			LLMModel:       modelImpl,
			Request:        &model.Request{},
			InvocationID:   baseInvocation.InvocationID,
			SessionID:      baseInvocation.Session.ID,
			Span:           span,
			NodeID:         "llm",
		},
	)
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.True(t, graphHasAttr(span.attrs, semconvtrace.KeyInvocationID, baseInvocation.InvocationID))
	require.True(t, graphHasAttr(span.attrs, semconvtrace.KeyGenAIConversationID, baseInvocation.Session.ID))
	require.True(t, graphHasAttr(span.attrs, semconvtrace.KeyRunnerUserID, baseInvocation.Session.UserID))
	require.True(t, graphHasAttr(span.attrs, semconvtrace.KeyGenAIRequestModel, modelImpl.Info().Name))
}

func TestExecuteModelAndProcessResponses_UsesConfigFallbackForSparseStableTraceMetadata(t *testing.T) {
	modelImpl := &captureModel{}
	invocation := agent.NewInvocation(
		agent.WithInvocationID("inv-trace-sparse-fallback"),
	)
	span := newGraphRecordingSpan()
	resp, err := executeModelAndProcessResponses(
		agent.NewInvocationContext(context.Background(), invocation),
		modelExecutionConfig{
			Invocation:   invocation,
			LLMModel:     modelImpl,
			Request:      &model.Request{},
			InvocationID: invocation.InvocationID,
			SessionID:    "sess-trace-fallback",
			UserID:       "user-trace-fallback",
			AppName:      "app-trace-fallback",
			Span:         span,
			NodeID:       "llm",
		},
	)
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.True(t, graphHasAttr(span.attrs, semconvtrace.KeyInvocationID, invocation.InvocationID))
	require.True(t, graphHasAttr(span.attrs, semconvtrace.KeyGenAIConversationID, "sess-trace-fallback"))
	require.True(t, graphHasAttr(span.attrs, semconvtrace.KeyRunnerUserID, "user-trace-fallback"))
	require.True(t, graphHasAttr(span.attrs, semconvtrace.KeyGenAIRequestModel, modelImpl.Info().Name))
}

func TestExecuteModelAndProcessResponses_RefreshesStableTraceMetadataAfterInPlaceInvocationUpdate(t *testing.T) {
	modelImpl := &captureModel{}
	invocation := agent.NewInvocation(
		agent.WithInvocationID("inv-trace-in-place"),
	)
	callbacks := model.NewCallbacks().RegisterAfterModel(
		func(ctx context.Context, args *model.AfterModelArgs) (*model.AfterModelResult, error) {
			invocation.Session = &session.Session{
				ID:     "sess-trace-in-place",
				UserID: "user-trace-in-place",
			}
			return nil, nil
		},
	)
	span := newGraphRecordingSpan()
	resp, err := executeModelAndProcessResponses(
		agent.NewInvocationContext(context.Background(), invocation),
		modelExecutionConfig{
			Invocation:     invocation,
			ModelCallbacks: callbacks,
			LLMModel:       modelImpl,
			Request:        &model.Request{},
			InvocationID:   invocation.InvocationID,
			Span:           span,
			NodeID:         "llm",
		},
	)
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.True(t, graphHasAttr(span.attrs, semconvtrace.KeyInvocationID, invocation.InvocationID))
	require.True(t, graphHasAttr(span.attrs, semconvtrace.KeyGenAIConversationID, invocation.Session.ID))
	require.True(t, graphHasAttr(span.attrs, semconvtrace.KeyRunnerUserID, invocation.Session.UserID))
	require.True(t, graphHasAttr(span.attrs, semconvtrace.KeyGenAIRequestModel, modelImpl.Info().Name))
}

func TestRefreshObservabilityInvocationView_ReusesViewWithoutNewStableMetadata(t *testing.T) {
	modelImpl := &captureModel{}
	invocation := agent.NewInvocation(
		agent.WithInvocationID("inv-observability-refresh"),
	)
	view := observabilityInvocationView(invocation, modelExecutionConfig{
		InvocationID: invocation.InvocationID,
		LLMModel:     modelImpl,
	})
	require.NotNil(t, view)
	refreshed := refreshObservabilityInvocationView(view, invocation, modelExecutionConfig{
		InvocationID: invocation.InvocationID,
		LLMModel:     modelImpl,
	})
	require.Same(t, view, refreshed)
	invocation.Session = &session.Session{
		ID:     "sess-observability-refresh",
		UserID: "user-observability-refresh",
	}
	refreshed = refreshObservabilityInvocationView(view, invocation, modelExecutionConfig{
		InvocationID: invocation.InvocationID,
		LLMModel:     modelImpl,
	})
	require.NotSame(t, view, refreshed)
	require.Equal(t, invocation.Session.ID, refreshed.Session.ID)
	require.Equal(t, invocation.Session.UserID, refreshed.Session.UserID)
}

func TestAddLLMNode_SkipsModelExecutionEventsWhenCallbackDisablesModelExecutionEvents(t *testing.T) {
	sg := NewStateGraph(MessagesStateSchema())
	sg.AddLLMNode("llm", &captureModel{}, "inst", nil)
	n, ok := sg.graph.nodes["llm"]
	require.True(t, ok)
	baseInvocation := agent.NewInvocation(
		agent.WithInvocationID("inv-base"),
	)
	updatedInvocation := agent.NewInvocation(
		agent.WithInvocationID("inv-updated"),
		agent.WithInvocationRunOptions(agent.RunOptions{
			DisableModelExecutionEvents: true,
		}),
	)
	callbacks := model.NewCallbacks().RegisterBeforeModel(
		func(ctx context.Context, args *model.BeforeModelArgs) (*model.BeforeModelResult, error) {
			return &model.BeforeModelResult{
				Context: agent.NewInvocationContext(ctx, updatedInvocation),
			}, nil
		},
	)
	ch := make(chan *event.Event, 8)
	exec := &ExecutionContext{InvocationID: "inv-llm", EventChan: ch}
	state := State{
		StateKeyExecContext:    exec,
		StateKeyCurrentNodeID:  "llm",
		StateKeyUserInput:      "hi",
		StateKeyModelCallbacks: callbacks,
	}
	_, err := n.Function(
		agent.NewInvocationContext(context.Background(), baseInvocation),
		state,
	)
	require.NoError(t, err)
	phases := collectModelExecutionPhases(ch)
	require.Empty(t, phases)
}

func TestAddLLMNode_EmitsCompleteWhenAfterModelDisablesModelExecutionEvents(t *testing.T) {
	sg := NewStateGraph(MessagesStateSchema())
	sg.AddLLMNode("llm", &captureModel{}, "inst", nil)
	n, ok := sg.graph.nodes["llm"]
	require.True(t, ok)
	baseInvocation := agent.NewInvocation(
		agent.WithInvocationID("inv-base"),
	)
	updatedInvocation := agent.NewInvocation(
		agent.WithInvocationID("inv-updated"),
		agent.WithInvocationRunOptions(agent.RunOptions{
			DisableModelExecutionEvents: true,
		}),
	)
	callbacks := model.NewCallbacks().RegisterAfterModel(
		func(ctx context.Context, args *model.AfterModelArgs) (*model.AfterModelResult, error) {
			return &model.AfterModelResult{
				Context: agent.NewInvocationContext(ctx, updatedInvocation),
			}, nil
		},
	)
	ch := make(chan *event.Event, 8)
	exec := &ExecutionContext{InvocationID: "inv-llm", EventChan: ch}
	state := State{
		StateKeyExecContext:    exec,
		StateKeyCurrentNodeID:  "llm",
		StateKeyUserInput:      "hi",
		StateKeyModelCallbacks: callbacks,
	}
	_, err := n.Function(
		agent.NewInvocationContext(context.Background(), baseInvocation),
		state,
	)
	require.NoError(t, err)
	phases := collectModelExecutionPhases(ch)
	require.Equal(t, []ModelExecutionPhase{
		ModelExecutionPhaseStart,
		ModelExecutionPhaseComplete,
	}, phases)
}

func TestAddLLMNode_UsesUpdatedInvocationIDInModelExecutionEvents(t *testing.T) {
	sg := NewStateGraph(MessagesStateSchema())
	sg.AddLLMNode("llm", &captureModel{}, "inst", nil)
	n, ok := sg.graph.nodes["llm"]
	require.True(t, ok)
	baseInvocation := agent.NewInvocation(
		agent.WithInvocationID("inv-base"),
	)
	updatedInvocation := agent.NewInvocation(
		agent.WithInvocationID("inv-updated"),
	)
	callbacks := model.NewCallbacks().RegisterBeforeModel(
		func(ctx context.Context, args *model.BeforeModelArgs) (*model.BeforeModelResult, error) {
			return &model.BeforeModelResult{
				Context: agent.NewInvocationContext(ctx, updatedInvocation),
			}, nil
		},
	)
	ch := make(chan *event.Event, 8)
	exec := &ExecutionContext{InvocationID: "inv-llm", EventChan: ch}
	state := State{
		StateKeyExecContext:    exec,
		StateKeyCurrentNodeID:  "llm",
		StateKeyUserInput:      "hi",
		StateKeyModelCallbacks: callbacks,
	}
	_, err := n.Function(
		agent.NewInvocationContext(context.Background(), baseInvocation),
		state,
	)
	require.NoError(t, err)
	events := collectModelExecutionEvents(ch)
	require.Len(t, events, 2)
	for _, evt := range events {
		var meta ModelExecutionMetadata
		require.NoError(t, json.Unmarshal(evt.StateDelta[MetadataKeyModel], &meta))
		require.Equal(t, updatedInvocation.InvocationID, evt.InvocationID)
		require.Equal(t, updatedInvocation.InvocationID, meta.InvocationID)
	}
}

func TestAddLLMNode_PreservesStableRequestMetadataInModelExecutionEvents(t *testing.T) {
	sg := NewStateGraph(MessagesStateSchema())
	sg.AddLLMNode("llm", &captureModel{}, "inst", nil)
	n, ok := sg.graph.nodes["llm"]
	require.True(t, ok)
	baseInvocation := agent.NewInvocation(
		agent.WithInvocationID("inv-base-stable"),
		agent.WithInvocationEventFilterKey("graph/stable"),
		agent.WithInvocationRunOptions(agent.RunOptions{
			RequestID: "req-stable",
		}),
	)
	updatedInvocation := agent.NewInvocation(
		agent.WithInvocationID("inv-updated-stable"),
	)
	callbacks := model.NewCallbacks().RegisterBeforeModel(
		func(ctx context.Context, args *model.BeforeModelArgs) (*model.BeforeModelResult, error) {
			return &model.BeforeModelResult{
				Context: agent.NewInvocationContext(ctx, updatedInvocation),
			}, nil
		},
	)
	ch := make(chan *event.Event, 8)
	exec := &ExecutionContext{InvocationID: "inv-llm", EventChan: ch}
	state := State{
		StateKeyExecContext:    exec,
		StateKeyCurrentNodeID:  "llm",
		StateKeyUserInput:      "hi",
		StateKeyModelCallbacks: callbacks,
	}
	_, err := n.Function(
		agent.NewInvocationContext(context.Background(), baseInvocation),
		state,
	)
	require.NoError(t, err)
	events := collectModelExecutionEvents(ch)
	require.Len(t, events, 2)
	for _, evt := range events {
		var meta ModelExecutionMetadata
		require.NoError(t, json.Unmarshal(evt.StateDelta[MetadataKeyModel], &meta))
		require.Equal(t, updatedInvocation.InvocationID, evt.InvocationID)
		require.Equal(t, updatedInvocation.InvocationID, meta.InvocationID)
		require.Equal(t, baseInvocation.RunOptions.RequestID, evt.RequestID)
		require.Equal(t, baseInvocation.GetEventFilterKey(), evt.FilterKey)
	}
}

func TestAddLLMNode_UsesUpdatedInvocationLineageInModelExecutionEvents(t *testing.T) {
	sg := NewStateGraph(MessagesStateSchema())
	sg.AddLLMNode("llm", &captureModel{}, "inst", nil)
	n, ok := sg.graph.nodes["llm"]
	require.True(t, ok)
	baseInvocation := agent.NewInvocation(
		agent.WithInvocationID("inv-base-lineage"),
		agent.WithInvocationBranch("graph/base"),
		agent.WithInvocationEventFilterKey("graph/base/filter"),
		agent.WithInvocationRunOptions(agent.RunOptions{
			RequestID: "req-base-lineage",
		}),
	)
	updatedInvocation := baseInvocation.Clone(
		agent.WithInvocationID("inv-updated-lineage"),
		agent.WithInvocationBranch("graph/updated"),
		agent.WithInvocationEventFilterKey("graph/updated/filter"),
		agent.WithInvocationRunOptions(agent.RunOptions{
			RequestID: "req-updated-lineage",
		}),
	)
	callbacks := model.NewCallbacks().RegisterBeforeModel(
		func(ctx context.Context, args *model.BeforeModelArgs) (*model.BeforeModelResult, error) {
			return &model.BeforeModelResult{
				Context: agent.NewInvocationContext(ctx, updatedInvocation),
			}, nil
		},
	)
	ch := make(chan *event.Event, 8)
	exec := &ExecutionContext{InvocationID: "inv-llm", EventChan: ch}
	state := State{
		StateKeyExecContext:    exec,
		StateKeyCurrentNodeID:  "llm",
		StateKeyUserInput:      "hi",
		StateKeyModelCallbacks: callbacks,
	}
	_, err := n.Function(
		agent.NewInvocationContext(context.Background(), baseInvocation),
		state,
	)
	require.NoError(t, err)
	events := collectModelExecutionEvents(ch)
	require.Len(t, events, 2)
	for _, evt := range events {
		var meta ModelExecutionMetadata
		require.NoError(t, json.Unmarshal(evt.StateDelta[MetadataKeyModel], &meta))
		require.Equal(t, updatedInvocation.InvocationID, evt.InvocationID)
		require.Equal(t, updatedInvocation.InvocationID, meta.InvocationID)
		require.Equal(t, baseInvocation.InvocationID, evt.ParentInvocationID)
		require.Equal(t, updatedInvocation.Branch, evt.Branch)
		require.Equal(t, updatedInvocation.GetEventFilterKey(), evt.FilterKey)
		require.Equal(t, updatedInvocation.RunOptions.RequestID, evt.RequestID)
	}
}

func TestAddLLMNode_MergesSparseUpdatedInvocationLineageInModelExecutionEvents(t *testing.T) {
	sg := NewStateGraph(MessagesStateSchema())
	sg.AddLLMNode("llm", &captureModel{}, "inst", nil)
	n, ok := sg.graph.nodes["llm"]
	require.True(t, ok)
	parentInvocation := agent.NewInvocation(
		agent.WithInvocationID("inv-parent-lineage"),
	)
	baseInvocation := parentInvocation.Clone(
		agent.WithInvocationID("inv-base-lineage"),
		agent.WithInvocationBranch("graph/base"),
		agent.WithInvocationEventFilterKey("graph/base/filter"),
		agent.WithInvocationRunOptions(agent.RunOptions{
			RequestID: "req-base-lineage",
		}),
	)
	updatedInvocation := agent.NewInvocation(
		agent.WithInvocationID("inv-updated-sparse-lineage"),
		agent.WithInvocationRunOptions(agent.RunOptions{
			RequestID: "req-updated-lineage",
		}),
	)
	callbacks := model.NewCallbacks().RegisterBeforeModel(
		func(ctx context.Context, args *model.BeforeModelArgs) (*model.BeforeModelResult, error) {
			return &model.BeforeModelResult{
				Context: agent.NewInvocationContext(ctx, updatedInvocation),
			}, nil
		},
	)
	ch := make(chan *event.Event, 8)
	exec := &ExecutionContext{InvocationID: "inv-llm", EventChan: ch}
	state := State{
		StateKeyExecContext:    exec,
		StateKeyCurrentNodeID:  "llm",
		StateKeyUserInput:      "hi",
		StateKeyModelCallbacks: callbacks,
	}
	_, err := n.Function(
		agent.NewInvocationContext(context.Background(), baseInvocation),
		state,
	)
	require.NoError(t, err)
	events := collectModelExecutionEvents(ch)
	require.Len(t, events, 2)
	for _, evt := range events {
		var meta ModelExecutionMetadata
		require.NoError(t, json.Unmarshal(evt.StateDelta[MetadataKeyModel], &meta))
		require.Equal(t, updatedInvocation.InvocationID, evt.InvocationID)
		require.Equal(t, updatedInvocation.InvocationID, meta.InvocationID)
		require.Equal(t, baseInvocation.GetParentInvocation().InvocationID, evt.ParentInvocationID)
		require.Equal(t, baseInvocation.Branch, evt.Branch)
		require.Equal(t, baseInvocation.GetEventFilterKey(), evt.FilterKey)
		require.Equal(t, updatedInvocation.RunOptions.RequestID, evt.RequestID)
	}
}

func TestAddLLMNode_DoesNotFallbackParentForUpdatedRootInvocationInModelExecutionEvents(t *testing.T) {
	sg := NewStateGraph(MessagesStateSchema())
	sg.AddLLMNode("llm", &captureModel{}, "inst", nil)
	n, ok := sg.graph.nodes["llm"]
	require.True(t, ok)
	parentInvocation := agent.NewInvocation(
		agent.WithInvocationID("inv-root-parent"),
	)
	baseInvocation := parentInvocation.Clone(
		agent.WithInvocationID("inv-root-base"),
		agent.WithInvocationBranch("graph/root-base"),
		agent.WithInvocationEventFilterKey("graph/root-base/filter"),
		agent.WithInvocationRunOptions(agent.RunOptions{
			RequestID: "req-root-base",
		}),
	)
	updatedInvocation := agent.NewInvocation(
		agent.WithInvocationID("inv-root-updated"),
		agent.WithInvocationBranch("graph/root-updated"),
		agent.WithInvocationEventFilterKey("graph/root-updated/filter"),
		agent.WithInvocationRunOptions(agent.RunOptions{
			RequestID: "req-root-updated",
		}),
	)
	callbacks := model.NewCallbacks().RegisterBeforeModel(
		func(ctx context.Context, args *model.BeforeModelArgs) (*model.BeforeModelResult, error) {
			return &model.BeforeModelResult{
				Context: agent.NewInvocationContext(ctx, updatedInvocation),
			}, nil
		},
	)
	ch := make(chan *event.Event, 8)
	exec := &ExecutionContext{InvocationID: "inv-llm", EventChan: ch}
	state := State{
		StateKeyExecContext:    exec,
		StateKeyCurrentNodeID:  "llm",
		StateKeyUserInput:      "hi",
		StateKeyModelCallbacks: callbacks,
	}
	_, err := n.Function(
		agent.NewInvocationContext(context.Background(), baseInvocation),
		state,
	)
	require.NoError(t, err)
	events := collectModelExecutionEvents(ch)
	require.Len(t, events, 2)
	for _, evt := range events {
		var meta ModelExecutionMetadata
		require.NoError(t, json.Unmarshal(evt.StateDelta[MetadataKeyModel], &meta))
		require.Equal(t, updatedInvocation.InvocationID, evt.InvocationID)
		require.Equal(t, updatedInvocation.InvocationID, meta.InvocationID)
		require.Empty(t, evt.ParentInvocationID)
		require.Equal(t, updatedInvocation.Branch, evt.Branch)
		require.Equal(t, updatedInvocation.GetEventFilterKey(), evt.FilterKey)
		require.Equal(t, updatedInvocation.RunOptions.RequestID, evt.RequestID)
	}
}

func TestAddLLMNode_EmitsModelExecutionEventsForPluginBeforeModelCustomResponse(t *testing.T) {
	sg := NewStateGraph(MessagesStateSchema())
	cm := &captureModel{}
	sg.AddLLMNode("llm", cm, "inst", nil)
	n, ok := sg.graph.nodes["llm"]
	require.True(t, ok)
	p := &hookPlugin{
		name: "p",
		reg: func(r *plugin.Registry) {
			r.BeforeModel(func(
				ctx context.Context,
				args *model.BeforeModelArgs,
			) (*model.BeforeModelResult, error) {
				return &model.BeforeModelResult{
					CustomResponse: &model.Response{
						Done: true,
						Choices: []model.Choice{
							{Message: model.NewAssistantMessage("plugin-custom")},
						},
					},
				}, nil
			})
		},
	}
	pm := plugin.MustNewManager(p)
	parentInvocation := agent.NewInvocation(
		agent.WithInvocationID("inv-plugin-parent"),
	)
	baseInvocation := parentInvocation.Clone(
		agent.WithInvocationID("inv-plugin-base"),
		agent.WithInvocationBranch("graph/plugin-custom"),
		agent.WithInvocationEventFilterKey("graph/plugin-custom/filter"),
		agent.WithInvocationRunOptions(agent.RunOptions{
			RequestID: "req-plugin-custom",
		}),
	)
	baseInvocation.Plugins = pm
	ch := make(chan *event.Event, 8)
	exec := &ExecutionContext{InvocationID: "inv-llm", EventChan: ch}
	state := State{
		StateKeyExecContext:   exec,
		StateKeyCurrentNodeID: "llm",
		StateKeyUserInput:     "hi",
	}
	_, err := n.Function(
		agent.NewInvocationContext(context.Background(), baseInvocation),
		state,
	)
	require.NoError(t, err)
	events := collectModelExecutionEvents(ch)
	require.Nil(t, cm.lastReq)
	phases := collectModelExecutionPhasesFromEvents(events)
	require.Equal(t, []ModelExecutionPhase{
		ModelExecutionPhaseStart,
		ModelExecutionPhaseComplete,
	}, phases)
	requireModelExecutionEventMetadata(
		t,
		events,
		baseInvocation.InvocationID,
		parentInvocation.InvocationID,
		baseInvocation.Branch,
		baseInvocation.GetEventFilterKey(),
		baseInvocation.RunOptions.RequestID,
	)
}

func TestAddLLMNode_MergesSparseExecutionMetadataForBeforeModelCustomResponseContext(t *testing.T) {
	sg := NewStateGraph(MessagesStateSchema())
	cm := &captureModel{}
	sg.AddLLMNode("llm", cm, "inst", nil)
	n, ok := sg.graph.nodes["llm"]
	require.True(t, ok)
	parentInvocation := agent.NewInvocation(
		agent.WithInvocationID("inv-before-custom-context-parent"),
	)
	baseInvocation := parentInvocation.Clone(
		agent.WithInvocationID("inv-before-custom-context-base"),
		agent.WithInvocationBranch("graph/before-custom-context"),
		agent.WithInvocationEventFilterKey("graph/before-custom-context/filter"),
		agent.WithInvocationRunOptions(agent.RunOptions{
			RequestID: "req-before-custom-context-base",
		}),
	)
	updatedInvocation := agent.NewInvocation(
		agent.WithInvocationID("inv-before-custom-context-updated"),
		agent.WithInvocationRunOptions(agent.RunOptions{
			RequestID: "req-before-custom-context-updated",
		}),
	)
	callbacks := model.NewCallbacks().RegisterBeforeModel(
		func(ctx context.Context, args *model.BeforeModelArgs) (*model.BeforeModelResult, error) {
			return &model.BeforeModelResult{
				Context: agent.NewInvocationContext(ctx, updatedInvocation),
				CustomResponse: &model.Response{
					Done: true,
					Choices: []model.Choice{
						{Message: model.NewAssistantMessage("custom")},
					},
				},
			}, nil
		},
	)
	ch := make(chan *event.Event, 8)
	exec := &ExecutionContext{InvocationID: "inv-llm", EventChan: ch}
	state := State{
		StateKeyExecContext:    exec,
		StateKeyCurrentNodeID:  "llm",
		StateKeyUserInput:      "hi",
		StateKeyModelCallbacks: callbacks,
	}
	_, err := n.Function(
		agent.NewInvocationContext(context.Background(), baseInvocation),
		state,
	)
	require.NoError(t, err)
	events := collectModelExecutionEvents(ch)
	require.Nil(t, cm.lastReq)
	require.Equal(t, []ModelExecutionPhase{
		ModelExecutionPhaseStart,
		ModelExecutionPhaseComplete,
	}, collectModelExecutionPhasesFromEvents(events))
	requireModelExecutionEventMetadata(
		t,
		events,
		updatedInvocation.InvocationID,
		parentInvocation.InvocationID,
		baseInvocation.Branch,
		baseInvocation.GetEventFilterKey(),
		updatedInvocation.RunOptions.RequestID,
	)
}

func TestAddLLMNode_EmitsModelExecutionEventsForBeforeModelCustomResponse(t *testing.T) {
	sg := NewStateGraph(MessagesStateSchema())
	cm := &captureModel{}
	sg.AddLLMNode("llm", cm, "inst", nil)
	n, ok := sg.graph.nodes["llm"]
	require.True(t, ok)
	parentInvocation := agent.NewInvocation(
		agent.WithInvocationID("inv-before-custom-parent"),
	)
	baseInvocation := parentInvocation.Clone(
		agent.WithInvocationID("inv-before-custom"),
		agent.WithInvocationBranch("graph/before-custom"),
		agent.WithInvocationEventFilterKey("graph/before-custom/filter"),
		agent.WithInvocationRunOptions(agent.RunOptions{
			RequestID: "req-before-custom",
		}),
	)
	callbacks := model.NewCallbacks().RegisterBeforeModel(
		func(ctx context.Context, args *model.BeforeModelArgs) (*model.BeforeModelResult, error) {
			return &model.BeforeModelResult{
				CustomResponse: &model.Response{
					Done: true,
					Choices: []model.Choice{
						{Message: model.NewAssistantMessage("custom")},
					},
				},
			}, nil
		},
	)
	ch := make(chan *event.Event, 8)
	exec := &ExecutionContext{InvocationID: "inv-llm", EventChan: ch}
	state := State{
		StateKeyExecContext:    exec,
		StateKeyCurrentNodeID:  "llm",
		StateKeyUserInput:      "hi",
		StateKeyModelCallbacks: callbacks,
	}
	_, err := n.Function(
		agent.NewInvocationContext(context.Background(), baseInvocation),
		state,
	)
	require.NoError(t, err)
	events := collectModelExecutionEvents(ch)
	require.Nil(t, cm.lastReq)
	phases := collectModelExecutionPhasesFromEvents(events)
	require.Equal(t, []ModelExecutionPhase{
		ModelExecutionPhaseStart,
		ModelExecutionPhaseComplete,
	}, phases)
	requireModelExecutionEventMetadata(
		t,
		events,
		baseInvocation.InvocationID,
		parentInvocation.InvocationID,
		baseInvocation.Branch,
		baseInvocation.GetEventFilterKey(),
		baseInvocation.RunOptions.RequestID,
	)
}

func TestAddLLMNode_UsesRootExecutionMetadataForPluginBeforeModelCustomResponseContext(t *testing.T) {
	sg := NewStateGraph(MessagesStateSchema())
	cm := &captureModel{}
	sg.AddLLMNode("llm", cm, "inst", nil)
	n, ok := sg.graph.nodes["llm"]
	require.True(t, ok)
	p := &hookPlugin{
		name: "p",
		reg: func(r *plugin.Registry) {
			r.BeforeModel(func(
				ctx context.Context,
				args *model.BeforeModelArgs,
			) (*model.BeforeModelResult, error) {
				updatedInvocation := agent.NewInvocation(
					agent.WithInvocationID("inv-plugin-custom-context-updated"),
					agent.WithInvocationBranch("graph/plugin-custom-context-updated"),
					agent.WithInvocationEventFilterKey("graph/plugin-custom-context-updated/filter"),
					agent.WithInvocationRunOptions(agent.RunOptions{
						RequestID: "req-plugin-custom-context-updated",
					}),
				)
				return &model.BeforeModelResult{
					Context: agent.NewInvocationContext(ctx, updatedInvocation),
					CustomResponse: &model.Response{
						Done: true,
						Choices: []model.Choice{
							{Message: model.NewAssistantMessage("plugin-custom")},
						},
					},
				}, nil
			})
		},
	}
	pm := plugin.MustNewManager(p)
	parentInvocation := agent.NewInvocation(
		agent.WithInvocationID("inv-plugin-custom-context-parent"),
	)
	baseInvocation := parentInvocation.Clone(
		agent.WithInvocationID("inv-plugin-custom-context-base"),
		agent.WithInvocationBranch("graph/plugin-custom-context-base"),
		agent.WithInvocationEventFilterKey("graph/plugin-custom-context-base/filter"),
		agent.WithInvocationRunOptions(agent.RunOptions{
			RequestID: "req-plugin-custom-context-base",
		}),
	)
	baseInvocation.Plugins = pm
	ch := make(chan *event.Event, 8)
	exec := &ExecutionContext{InvocationID: "inv-llm", EventChan: ch}
	state := State{
		StateKeyExecContext:   exec,
		StateKeyCurrentNodeID: "llm",
		StateKeyUserInput:     "hi",
	}
	_, err := n.Function(
		agent.NewInvocationContext(context.Background(), baseInvocation),
		state,
	)
	require.NoError(t, err)
	events := collectModelExecutionEvents(ch)
	require.Nil(t, cm.lastReq)
	require.Equal(t, []ModelExecutionPhase{
		ModelExecutionPhaseStart,
		ModelExecutionPhaseComplete,
	}, collectModelExecutionPhasesFromEvents(events))
	require.Len(t, events, 2)
	for _, evt := range events {
		var meta ModelExecutionMetadata
		require.NoError(t, json.Unmarshal(evt.StateDelta[MetadataKeyModel], &meta))
		require.Equal(t, "inv-plugin-custom-context-updated", evt.InvocationID)
		require.Equal(t, "inv-plugin-custom-context-updated", meta.InvocationID)
		require.Empty(t, evt.ParentInvocationID)
		require.Equal(t, "graph/plugin-custom-context-updated", evt.Branch)
		require.Equal(t, "graph/plugin-custom-context-updated/filter", evt.FilterKey)
		require.Equal(t, "req-plugin-custom-context-updated", evt.RequestID)
	}
}

func TestAddLLMNode_EmitsModelExecutionEventsForBeforeModelError(t *testing.T) {
	sg := NewStateGraph(MessagesStateSchema())
	cm := &captureModel{}
	sg.AddLLMNode("llm", cm, "inst", nil)
	n, ok := sg.graph.nodes["llm"]
	require.True(t, ok)
	parentInvocation := agent.NewInvocation(
		agent.WithInvocationID("inv-before-error-parent"),
	)
	baseInvocation := parentInvocation.Clone(
		agent.WithInvocationID("inv-before-error"),
		agent.WithInvocationBranch("graph/before-error"),
		agent.WithInvocationEventFilterKey("graph/before-error/filter"),
		agent.WithInvocationRunOptions(agent.RunOptions{
			RequestID: "req-before-error",
		}),
	)
	callbacks := model.NewCallbacks().RegisterBeforeModel(
		func(ctx context.Context, args *model.BeforeModelArgs) (*model.BeforeModelResult, error) {
			return nil, errors.New("before failed")
		},
	)
	ch := make(chan *event.Event, 8)
	exec := &ExecutionContext{InvocationID: "inv-llm", EventChan: ch}
	state := State{
		StateKeyExecContext:    exec,
		StateKeyCurrentNodeID:  "llm",
		StateKeyUserInput:      "hi",
		StateKeyModelCallbacks: callbacks,
	}
	_, err := n.Function(
		agent.NewInvocationContext(context.Background(), baseInvocation),
		state,
	)
	require.ErrorContains(t, err, "callback before model error: before failed")
	events := collectModelExecutionEvents(ch)
	require.Nil(t, cm.lastReq)
	phases := collectModelExecutionPhasesFromEvents(events)
	require.Equal(t, []ModelExecutionPhase{
		ModelExecutionPhaseStart,
		ModelExecutionPhaseComplete,
	}, phases)
	requireModelExecutionEventMetadata(
		t,
		events,
		baseInvocation.InvocationID,
		parentInvocation.InvocationID,
		baseInvocation.Branch,
		baseInvocation.GetEventFilterKey(),
		baseInvocation.RunOptions.RequestID,
	)
}

func TestAddLLMNode_EmitsModelExecutionEventsForPluginBeforeModelError(t *testing.T) {
	sg := NewStateGraph(MessagesStateSchema())
	cm := &captureModel{}
	sg.AddLLMNode("llm", cm, "inst", nil)
	n, ok := sg.graph.nodes["llm"]
	require.True(t, ok)
	p := &hookPlugin{
		name: "p",
		reg: func(r *plugin.Registry) {
			r.BeforeModel(func(
				ctx context.Context,
				args *model.BeforeModelArgs,
			) (*model.BeforeModelResult, error) {
				return nil, errors.New("plugin before failed")
			})
		},
	}
	pm := plugin.MustNewManager(p)
	parentInvocation := agent.NewInvocation(
		agent.WithInvocationID("inv-plugin-before-error-parent"),
	)
	baseInvocation := parentInvocation.Clone(
		agent.WithInvocationID("inv-plugin-before-error"),
		agent.WithInvocationBranch("graph/plugin-before-error"),
		agent.WithInvocationEventFilterKey("graph/plugin-before-error/filter"),
		agent.WithInvocationRunOptions(agent.RunOptions{
			RequestID: "req-plugin-before-error",
		}),
	)
	baseInvocation.Plugins = pm
	ch := make(chan *event.Event, 8)
	exec := &ExecutionContext{InvocationID: "inv-llm", EventChan: ch}
	state := State{
		StateKeyExecContext:   exec,
		StateKeyCurrentNodeID: "llm",
		StateKeyUserInput:     "hi",
	}
	_, err := n.Function(
		agent.NewInvocationContext(context.Background(), baseInvocation),
		state,
	)
	require.ErrorContains(t, err, "callback before model error:")
	require.ErrorContains(t, err, "plugin before failed")
	events := collectModelExecutionEvents(ch)
	require.Nil(t, cm.lastReq)
	phases := collectModelExecutionPhasesFromEvents(events)
	require.Equal(t, []ModelExecutionPhase{
		ModelExecutionPhaseStart,
		ModelExecutionPhaseComplete,
	}, phases)
	requireModelExecutionEventMetadata(
		t,
		events,
		baseInvocation.InvocationID,
		parentInvocation.InvocationID,
		baseInvocation.Branch,
		baseInvocation.GetEventFilterKey(),
		baseInvocation.RunOptions.RequestID,
	)
}

func TestExecuteModelAndProcessResponses_TracksFinalizeErrors(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	originalProvider := itelemetry.MeterProvider
	originalMeter := itelemetry.ChatMeter
	originalRequestCnt := itelemetry.ChatMetricTRPCAgentGoClientRequestCnt
	defer func() {
		itelemetry.MeterProvider = originalProvider
		itelemetry.ChatMeter = originalMeter
		itelemetry.ChatMetricTRPCAgentGoClientRequestCnt = originalRequestCnt
	}()
	itelemetry.MeterProvider = provider
	itelemetry.ChatMeter = provider.Meter(semconvmetrics.MeterNameChat)
	requestCnt, err := itelemetry.ChatMeter.Int64Counter("trpc_agent_go.client.request.cnt")
	require.NoError(t, err)
	itelemetry.ChatMetricTRPCAgentGoClientRequestCnt = requestCnt
	invocation := agent.NewInvocation(
		agent.WithInvocationID("inv-metrics"),
		agent.WithInvocationModel(&noResponseModel{}),
		agent.WithInvocationSession(&session.Session{ID: "sess-metrics"}),
	)
	_, err = executeModelAndProcessResponses(
		agent.NewInvocationContext(context.Background(), invocation),
		modelExecutionConfig{
			Invocation:   invocation,
			LLMModel:     invocation.Model,
			Request:      &model.Request{},
			InvocationID: invocation.InvocationID,
			SessionID:    invocation.Session.ID,
			Span:         noop.Span{},
			NodeID:       "llm",
		},
	)
	require.ErrorContains(t, err, errMsgNoModelResponse)
	var rm metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(context.Background(), &rm))
	require.True(t, resourceMetricsContainAttribute(rm, semconvtrace.KeyErrorType, semconvtrace.ValueDefaultErrorType))
}

func TestExecuteModelAndProcessResponses_TracksFinalizeErrorsForNilIterSequence(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	originalProvider := itelemetry.MeterProvider
	originalMeter := itelemetry.ChatMeter
	originalRequestCnt := itelemetry.ChatMetricTRPCAgentGoClientRequestCnt
	defer func() {
		itelemetry.MeterProvider = originalProvider
		itelemetry.ChatMeter = originalMeter
		itelemetry.ChatMetricTRPCAgentGoClientRequestCnt = originalRequestCnt
	}()
	itelemetry.MeterProvider = provider
	itelemetry.ChatMeter = provider.Meter(semconvmetrics.MeterNameChat)
	requestCnt, err := itelemetry.ChatMeter.Int64Counter("trpc_agent_go.client.request.cnt")
	require.NoError(t, err)
	itelemetry.ChatMetricTRPCAgentGoClientRequestCnt = requestCnt
	invocation := agent.NewInvocation(
		agent.WithInvocationID("inv-metrics-nil-iter"),
		agent.WithInvocationModel(&nilIterModel{}),
		agent.WithInvocationSession(&session.Session{ID: "sess-metrics-nil-iter"}),
	)
	_, err = executeModelAndProcessResponses(
		agent.NewInvocationContext(context.Background(), invocation),
		modelExecutionConfig{
			Invocation:   invocation,
			LLMModel:     invocation.Model,
			Request:      &model.Request{},
			InvocationID: invocation.InvocationID,
			SessionID:    invocation.Session.ID,
			Span:         noop.Span{},
			NodeID:       "llm",
		},
	)
	require.ErrorContains(t, err, errMsgNoModelResponse)
	var rm metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(context.Background(), &rm))
	require.True(t, resourceMetricsContainAttribute(rm, semconvtrace.KeyErrorType, semconvtrace.ValueDefaultErrorType))
}

func resourceMetricsContainAttribute(rm metricdata.ResourceMetrics, key, value string) bool {
	for _, scopeMetric := range rm.ScopeMetrics {
		for _, metric := range scopeMetric.Metrics {
			switch data := metric.Data.(type) {
			case metricdata.Sum[int64]:
				for _, point := range data.DataPoints {
					if attributeSetContains(point.Attributes, key, value) {
						return true
					}
				}
			case metricdata.Sum[float64]:
				for _, point := range data.DataPoints {
					if attributeSetContains(point.Attributes, key, value) {
						return true
					}
				}
			case metricdata.Histogram[int64]:
				for _, point := range data.DataPoints {
					if attributeSetContains(point.Attributes, key, value) {
						return true
					}
				}
			case metricdata.Histogram[float64]:
				for _, point := range data.DataPoints {
					if attributeSetContains(point.Attributes, key, value) {
						return true
					}
				}
			}
		}
	}
	return false
}

func attributeSetContains(set attribute.Set, key, value string) bool {
	for _, kv := range set.ToSlice() {
		if string(kv.Key) == key && kv.Value.AsString() == value {
			return true
		}
	}
	return false
}

func TestBuilderOptions_Destinations_And_Callbacks(t *testing.T) {
	sg := NewStateGraph(NewStateSchema())

	before1 := func(ctx context.Context, cb *NodeCallbackContext, st State) (any, error) { return nil, nil }
	after1 := func(ctx context.Context, cb *NodeCallbackContext, st State, result any, nodeErr error) (any, error) {
		return nil, nil
	}
	onErr1 := func(ctx context.Context, cb *NodeCallbackContext, st State, err error) {}

	cbs := NewNodeCallbacks().
		RegisterBeforeNode(before1).
		RegisterAfterNode(after1).
		RegisterOnNodeError(onErr1)

	// Add node with destinations and per-node callbacks
	// Also add the declared destination node "A" so validation succeeds.
	sg.AddNode("A", func(ctx context.Context, st State) (any, error) { return st, nil })
	sg.AddNode("n", func(ctx context.Context, st State) (any, error) { return st, nil },
		WithDestinations(map[string]string{"A": "toA"}),
		WithNodeCallbacks(cbs),
		WithPreNodeCallback(func(ctx context.Context, cb *NodeCallbackContext, st State) (any, error) { return nil, nil }),
		WithPostNodeCallback(func(ctx context.Context, cb *NodeCallbackContext, st State, result any, err error) (any, error) {
			return nil, nil
		}),
		WithNodeErrorCallback(func(ctx context.Context, cb *NodeCallbackContext, st State, err error) {}),
		WithAgentNodeEventCallback(func(ctx context.Context, cb *NodeCallbackContext, st State, e *event.Event) {}),
	)

	// Compile to validate graph
	_, err := sg.SetEntryPoint("n").SetFinishPoint("n").Compile()
	require.NoError(t, err)

	node := sg.graph.nodes["n"]
	require.NotNil(t, node)
	require.Contains(t, node.destinations, "A")
	require.NotNil(t, node.callbacks)
	require.Len(t, node.callbacks.BeforeNode, 2)
	require.Len(t, node.callbacks.AfterNode, 2)
	require.Len(t, node.callbacks.OnNodeError, 2)
	require.Len(t, node.callbacks.AgentEvent, 1)
}

func TestAddEdge_PregelSetup(t *testing.T) {
	sg := NewStateGraph(NewStateSchema())
	pass := func(ctx context.Context, st State) (any, error) { return st, nil }
	sg.AddNode("A", pass)
	sg.AddNode("B", pass)
	sg.AddEdge("A", "B")
	_, err := sg.SetEntryPoint("A").SetFinishPoint("B").Compile()
	require.NoError(t, err)

	// Channel mapping should include branch:to:B -> [B]
	triggers := sg.graph.getTriggerToNodes()
	require.Contains(t, triggers, "branch:to:B")
	require.Contains(t, triggers["branch:to:B"], "B")

	// Writers on A should include the branch channel
	nodeA := sg.graph.nodes["A"]
	found := false
	for _, w := range nodeA.writers {
		if w.Channel == "branch:to:B" {
			found = true
			break
		}
	}
	require.True(t, found, "expected writer to branch:to:B on node A")
}

func TestAddToolsAndAgentNode_Types(t *testing.T) {
	sg := NewStateGraph(NewStateSchema())
	sg.AddToolsNode("tools", map[string]tool.Tool{"echo": &echoTool{name: "echo"}})
	sg.AddAgentNode("agent")
	require.Equal(t, NodeTypeTool, sg.graph.nodes["tools"].Type)
	require.Equal(t, NodeTypeAgent, sg.graph.nodes["agent"].Type)
}

func TestLLMNode_PlaceholdersInjected_FromSessionState(t *testing.T) {
	schema := MessagesStateSchema()
	cm := &captureModel{}
	sg := NewStateGraph(schema)
	instr := "Hello {research_topics}. {user:topics?} - {app:banner?}"
	sg.AddLLMNode("llm", cm, instr, nil)

	// Build a minimal exec context and session with state for placeholder injection.
	ch := make(chan *event.Event, 8)
	exec := &ExecutionContext{InvocationID: "inv-ph", EventChan: ch}
	sess := &session.Session{ID: "s1", State: session.StateMap{
		"research_topics": []byte("AI"),
		"user:topics":     []byte("DL"),
		"app:banner":      []byte("Banner"),
	}}
	state := State{
		StateKeyExecContext:   exec,
		StateKeyCurrentNodeID: "llm",
		StateKeySession:       sess,
		StateKeyUserInput:     "ask",
	}

	n := sg.graph.nodes["llm"]
	_, err := n.Function(context.Background(), state)
	require.NoError(t, err)

	// Verify request has system message with injected content.
	require.NotNil(t, cm.lastReq)
	require.GreaterOrEqual(t, len(cm.lastReq.Messages), 1)
	sys := cm.lastReq.Messages[0]
	require.Equal(t, model.RoleSystem, sys.Role)
	require.Contains(t, sys.Content, "AI")
	require.Contains(t, sys.Content, "DL")
	require.Contains(t, sys.Content, "Banner")
	require.NotContains(t, sys.Content, "{research_topics}")
	require.NotContains(t, sys.Content, "{user:topics}")
	require.NotContains(t, sys.Content, "{app:banner}")

	// Drain model events and verify model input uses injected instruction.
	var inputs []string
	for {
		select {
		case e := <-ch:
			if e != nil && e.StateDelta != nil {
				if b, ok := e.StateDelta[MetadataKeyModel]; ok {
					var meta ModelExecutionMetadata
					_ = json.Unmarshal(b, &meta)
					if meta.Input != "" {
						inputs = append(inputs, meta.Input)
					}
				}
			}
		default:
			goto DONE
		}
	}
DONE:
	require.NotEmpty(t, inputs)
	found := false
	for _, in := range inputs {
		if in == "Hello AI. DL - Banner\n\nask" || in == "Hello AI. DL - Banner" {
			found = true
			break
		}
	}
	require.True(t, found, "model input should contain injected instruction: %v", inputs)
}

func TestLLMNode_PlaceholdersOptionalMissing(t *testing.T) {
	schema := MessagesStateSchema()
	cm := &captureModel{}
	sg := NewStateGraph(schema)
	instr := "Show {research_topics} {user:topics?} {app:banner?}"
	sg.AddLLMNode("llm", cm, instr, nil)

	ch := make(chan *event.Event, 4)
	exec := &ExecutionContext{InvocationID: "inv-ph2", EventChan: ch}
	// Only provide research_topics; optional prefixed keys are omitted.
	sess := &session.Session{ID: "s2", State: session.StateMap{
		"research_topics": []byte("AI"),
	}}
	state := State{StateKeyExecContext: exec, StateKeyCurrentNodeID: "llm", StateKeySession: sess}

	n := sg.graph.nodes["llm"]
	_, err := n.Function(context.Background(), state)
	require.NoError(t, err)

	require.NotNil(t, cm.lastReq)
	require.GreaterOrEqual(t, len(cm.lastReq.Messages), 1)
	sys := cm.lastReq.Messages[0]
	require.Equal(t, model.RoleSystem, sys.Role)
	// research_topics is injected; optional ones are blanked out (no braces remain)
	require.Contains(t, sys.Content, "AI")
	require.NotContains(t, sys.Content, "{user:topics?")
	require.NotContains(t, sys.Content, "{app:banner?")
}

func TestLLMNode_DisableTracingUsesCurrentSpan(t *testing.T) {
	schema := MessagesStateSchema()
	cm := &captureModel{}
	sg := NewStateGraph(schema)
	sg.AddLLMNode("llm", cm, "inst", nil)

	invocation := agent.NewInvocation(
		agent.WithInvocationRunOptions(agent.RunOptions{DisableTracing: true}),
	)
	ctx := agent.NewInvocationContext(context.Background(), invocation)
	node := sg.graph.nodes["llm"]

	_, err := node.Function(ctx, State{StateKeyUserInput: "hi"})
	require.NoError(t, err)
	require.NotNil(t, cm.lastReq)

	_, ch, err := runModel(ctx, nil, cm, &model.Request{Messages: []model.Message{model.NewUserMessage("hi")}})
	require.NoError(t, err)
	for range ch {
	}
}

func TestAddLLMNode_DisableTracingSkipsSpanCreation(t *testing.T) {
	recorder := useSpanRecorder(t)
	schema := MessagesStateSchema()
	cm := &captureModel{}
	sg := NewStateGraph(schema)
	sg.AddLLMNode("llm", cm, "inst", nil)

	invocation := agent.NewInvocation(
		agent.WithInvocationRunOptions(agent.RunOptions{DisableTracing: true}),
	)
	ctx := agent.NewInvocationContext(context.Background(), invocation)
	node := sg.graph.nodes["llm"]

	_, err := node.Function(ctx, State{StateKeyUserInput: "hi"})
	require.NoError(t, err)
	require.NotNil(t, cm.lastReq)
	require.Empty(t, recorder.Ended())
}

func TestStartNodeSpan_DisableTracingUsesNoopSpan(t *testing.T) {
	parentSpan := &trackingSpan{}
	invocation := agent.NewInvocation(
		agent.WithInvocationRunOptions(agent.RunOptions{DisableTracing: true}),
	)
	ctx := agent.NewInvocationContext(
		oteltrace.ContextWithSpan(context.Background(), parentSpan),
		invocation,
	)

	_, span, startedSpan := startNodeSpan(ctx, "ignored")
	require.False(t, startedSpan)

	span.SetAttributes(attribute.String("key", "value"))
	span.RecordError(errors.New("boom"))
	span.SetStatus(codes.Error, "boom")
	itelemetry.TraceAfterInvokeAgent(
		span,
		event.NewResponseEvent(
			"invocation-id",
			"agent-name",
			&model.Response{
				ID:      "response-id",
				Choices: []model.Choice{{Message: model.NewAssistantMessage("ok")}},
			},
		),
		nil,
		0,
	)

	require.Empty(t, parentSpan.attributes)
	require.Empty(t, parentSpan.recordedErrors)
	require.NotEqual(t, codes.Error, parentSpan.statusCode)
}

func TestTraceProcessedModelResponse_DisableTracing(t *testing.T) {
	invocation := agent.NewInvocation(
		agent.WithInvocationRunOptions(agent.RunOptions{DisableTracing: true}),
	)
	tracker := itelemetry.NewChatMetricsTracker(
		context.Background(),
		invocation,
		&model.Request{},
		nil,
		nil,
		nil,
	)

	traceProcessedModelResponse(
		noop.Span{},
		tracker,
		invocation,
		&model.Request{},
		&model.Response{Choices: []model.Choice{{Message: model.NewAssistantMessage("ok")}}},
		&event.Event{ID: "evt-1"},
	)
}

func TestToolsNode_DisableTracingSkipsSpanCreation(t *testing.T) {
	recorder := useSpanRecorder(t)
	sg := NewStateGraph(MessagesStateSchema())
	sg.AddToolsNode("tools", map[string]tool.Tool{"echo": &echoTool{name: "echo"}})
	invocation := agent.NewInvocation(
		agent.WithInvocationRunOptions(agent.RunOptions{DisableTracing: true}),
	)
	ctx := agent.NewInvocationContext(context.Background(), invocation)
	node := sg.graph.nodes["tools"]
	state := State{
		StateKeyMessages: []model.Message{
			model.NewUserMessage("hi"),
			{
				Role: model.RoleAssistant,
				ToolCalls: []model.ToolCall{{
					Type: "function",
					ID:   "call-1",
					Function: model.FunctionDefinitionParam{
						Name:      "echo",
						Arguments: []byte(`{}`),
					},
				}},
			},
		},
	}
	_, err := node.Function(ctx, state)
	require.NoError(t, err)
	require.Empty(t, recorder.Ended())
}

func TestAddToolsNode_WorkflowSpanIncludesToolType(t *testing.T) {
	recorder := useSpanRecorder(t)
	sg := NewStateGraph(MessagesStateSchema())
	sg.AddToolsNode("tools", map[string]tool.Tool{"echo": &echoTool{name: "echo"}})

	node := sg.graph.nodes["tools"]
	state := State{
		StateKeyMessages: []model.Message{
			model.NewUserMessage("hi"),
			{
				Role: model.RoleAssistant,
				ToolCalls: []model.ToolCall{{
					Type: "function",
					ID:   "call-1",
					Function: model.FunctionDefinitionParam{
						Name:      "echo",
						Arguments: []byte(`{}`),
					},
				}},
			},
		},
	}

	_, err := node.Function(context.Background(), state)
	require.NoError(t, err)

	var workflowSpan sdktrace.ReadOnlySpan
	for _, span := range recorder.Ended() {
		if span.Name() == itelemetry.NewWorkflowSpanName("execute_tools_node") {
			workflowSpan = span
			break
		}
	}
	require.NotNil(t, workflowSpan)
	require.True(
		t,
		graphHasAttr(
			workflowSpan.Attributes(),
			semconvtrace.KeyGenAIWorkflowType,
			itelemetry.WorkflowTypeTool.String(),
		),
	)
}

// Verify StateSchema.ApplyUpdate skips unknown internal keys while still
// applying other unknown keys using default override behavior.
func TestStateSchema_ApplyUpdate_SkipsInternalUnknownKeys(t *testing.T) {
	schema := NewStateSchema().
		AddField("x", StateField{Type: reflect.TypeOf(0), Reducer: DefaultReducer})

	current := State{"x": 1}
	update := State{
		StateKeyExecContext: map[string]any{"should": "skip"},
		"y":                 2,
	}

	result := schema.ApplyUpdate(current, update)

	// Internal key should be ignored entirely.
	require.NotContains(t, result, StateKeyExecContext)
	// Unknown non-internal key should be applied with default override.
	require.Equal(t, 2, result["y"])
	// Existing schema field remains unless overridden.
	require.Equal(t, 1, result["x"])
}

func TestBuildAgentInvocationWithStateAndScope_ParentAndScope(t *testing.T) {
	parent := agent.NewInvocation(
		agent.WithInvocationEventFilterKey("root"),
	)
	ctx := agent.NewInvocationContext(context.Background(), parent)

	target := &stubAgent{name: "child"}
	inv := buildAgentInvocationWithStateAndScope(
		ctx,
		State{},
		State{},
		target,
		"",
		"scope",
	)

	key := inv.GetEventFilterKey()
	parts := strings.Split(key, event.FilterKeyDelimiter)
	// FilterKey is now stable without UUID: "root/scope"
	require.Len(t, parts, 2)
	require.Equal(t, "root", parts[0])
	require.Equal(t, "scope", parts[1])
}

func TestBuildAgentInvocationWithStateAndScope_ParentNoScope(t *testing.T) {
	parent := agent.NewInvocation(
		agent.WithInvocationEventFilterKey("root"),
	)
	ctx := agent.NewInvocationContext(context.Background(), parent)

	target := &stubAgent{name: "child"}
	inv := buildAgentInvocationWithStateAndScope(
		ctx,
		State{},
		State{},
		target,
		"",
		"",
	)

	key := inv.GetEventFilterKey()
	parts := strings.Split(key, event.FilterKeyDelimiter)
	// FilterKey is now stable without UUID: "root/child"
	require.Len(t, parts, 2)
	require.Equal(t, "root", parts[0])
	require.Equal(t, "child", parts[1])
}

func TestBuildAgentInvocationWithStateAndScope_NoParentKey(t *testing.T) {
	// Parent invocation without an explicit filter key.
	parent := &agent.Invocation{}
	ctx := agent.NewInvocationContext(context.Background(), parent)

	target := &stubAgent{name: "child"}
	inv := buildAgentInvocationWithStateAndScope(
		ctx,
		State{},
		State{},
		target,
		"",
		"scope",
	)

	key := inv.GetEventFilterKey()
	// FilterKey is now stable without UUID: just "scope"
	require.Equal(t, "scope", key)
}
