//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package agent_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	invokeagenttelemetry "trpc.group/trpc-go/trpc-agent-go/internal/invokeagenttelemetry"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/plugin"
	"trpc.group/trpc-go/trpc-agent-go/telemetry/metric/histogram"
	metricsemconv "trpc.group/trpc-go/trpc-agent-go/telemetry/semconv/metrics"
	semconvtrace "trpc.group/trpc-go/trpc-agent-go/telemetry/semconv/trace"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

type testAgent struct {
	mu     sync.Mutex
	called bool
}

func (a *testAgent) Run(
	ctx context.Context,
	inv *agent.Invocation,
) (<-chan *event.Event, error) {
	a.mu.Lock()
	a.called = true
	a.mu.Unlock()

	ch := make(chan *event.Event, 1)
	go func() {
		defer close(ch)
		rsp := &model.Response{
			Done: true,
			Choices: []model.Choice{{
				Index:   0,
				Message: model.NewAssistantMessage("orig"),
			}},
		}
		_ = agent.EmitEvent(
			ctx,
			inv,
			ch,
			event.NewResponseEvent(inv.InvocationID, inv.AgentName, rsp),
		)
	}()
	return ch, nil
}

func (a *testAgent) Tools() []tool.Tool { return nil }

func (a *testAgent) Info() agent.Info {
	return agent.Info{Name: "a", Description: "test"}
}

func (a *testAgent) SubAgents() []agent.Agent        { return nil }
func (a *testAgent) FindSubAgent(string) agent.Agent { return nil }

func (a *testAgent) wasCalled() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.called
}

type cbPlugin struct {
	name string
	reg  func(r *plugin.Registry)
}

func (p *cbPlugin) Name() string { return p.name }

func (p *cbPlugin) Register(r *plugin.Registry) {
	if p.reg != nil {
		p.reg(r)
	}
}

type preparingTestAgent struct {
	testAgent
	name         string
	description  string
	instructions string
}

func (a *preparingTestAgent) PrepareInvocation(inv *agent.Invocation) {
	if inv == nil {
		return
	}
	inv.Agent = a
	inv.AgentName = a.name
	inv.InvokeAgentDescription = a.description
	inv.InvokeAgentInstructions = a.instructions
}

func (a *preparingTestAgent) Info() agent.Info {
	return agent.Info{Name: a.name, Description: "fallback"}
}

type syncErrorAgent struct {
	name        string
	description string
	err         error
}

func (a *syncErrorAgent) Run(
	ctx context.Context,
	inv *agent.Invocation,
) (<-chan *event.Event, error) {
	return nil, a.err
}

func (a *syncErrorAgent) Tools() []tool.Tool { return nil }

func (a *syncErrorAgent) Info() agent.Info {
	return agent.Info{Name: a.name, Description: a.description}
}

func (a *syncErrorAgent) SubAgents() []agent.Agent        { return nil }
func (a *syncErrorAgent) FindSubAgent(string) agent.Agent { return nil }

func useRunWithPluginsSpanRecorder(t *testing.T) *tracetest.SpanRecorder {
	t.Helper()
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	originalProvider := otel.GetTracerProvider()
	otel.SetTracerProvider(provider)
	t.Cleanup(func() {
		_ = provider.Shutdown(context.Background())
		otel.SetTracerProvider(originalProvider)
	})
	return recorder
}

func useRunWithPluginsMetricReader(t *testing.T) *sdkmetric.ManualReader {
	t.Helper()
	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))

	originalProvider := invokeagenttelemetry.MeterProvider
	originalMeter := invokeagenttelemetry.InvokeAgentMeter
	originalRequestCnt := invokeagenttelemetry.InvokeAgentMetricGenAIRequestCnt
	originalTokenUsage := invokeagenttelemetry.InvokeAgentMetricGenAIClientTokenUsage
	originalTimeToFirstToken := invokeagenttelemetry.InvokeAgentMetricGenAIClientTimeToFirstToken
	originalDuration := invokeagenttelemetry.InvokeAgentMetricGenAIClientOperationDuration
	t.Cleanup(func() {
		invokeagenttelemetry.MeterProvider = originalProvider
		invokeagenttelemetry.InvokeAgentMeter = originalMeter
		invokeagenttelemetry.InvokeAgentMetricGenAIRequestCnt = originalRequestCnt
		invokeagenttelemetry.InvokeAgentMetricGenAIClientTokenUsage = originalTokenUsage
		invokeagenttelemetry.InvokeAgentMetricGenAIClientTimeToFirstToken = originalTimeToFirstToken
		invokeagenttelemetry.InvokeAgentMetricGenAIClientOperationDuration = originalDuration
	})

	invokeagenttelemetry.MeterProvider = provider
	invokeagenttelemetry.InvokeAgentMeter = provider.Meter(metricsemconv.MeterNameInvokeAgent)
	var err error
	invokeagenttelemetry.InvokeAgentMetricGenAIRequestCnt, err = invokeagenttelemetry.InvokeAgentMeter.Int64Counter(
		metricsemconv.MetricTRPCAgentGoClientRequestCnt,
	)
	require.NoError(t, err)
	invokeagenttelemetry.InvokeAgentMetricGenAIClientTokenUsage, err = histogram.NewDynamicInt64Histogram(
		provider,
		metricsemconv.MeterNameInvokeAgent,
		metricsemconv.MetricGenAIClientTokenUsage,
	)
	require.NoError(t, err)
	invokeagenttelemetry.InvokeAgentMetricGenAIClientTimeToFirstToken, err = histogram.NewDynamicFloat64Histogram(
		provider,
		metricsemconv.MeterNameInvokeAgent,
		metricsemconv.MetricTRPCAgentGoClientTimeToFirstToken,
		metric.WithUnit("s"),
	)
	require.NoError(t, err)
	invokeagenttelemetry.InvokeAgentMetricGenAIClientOperationDuration, err = histogram.NewDynamicFloat64Histogram(
		provider,
		metricsemconv.MeterNameInvokeAgent,
		metricsemconv.MetricGenAIClientOperationDuration,
		metric.WithUnit("s"),
	)
	require.NoError(t, err)
	return reader
}

func collectRunWithPluginsMetrics(
	t *testing.T,
	reader *sdkmetric.ManualReader,
) metricdata.ResourceMetrics {
	t.Helper()
	var rm metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(context.Background(), &rm))
	return rm
}

func hasInvokeAgentMetricStringAttribute(
	rm metricdata.ResourceMetrics,
	metricName string,
	key string,
	value string,
) bool {
	for _, scopeMetric := range rm.ScopeMetrics {
		for _, metric := range scopeMetric.Metrics {
			if metric.Name != metricName {
				continue
			}
			sum, ok := metric.Data.(metricdata.Sum[int64])
			if !ok {
				continue
			}
			for _, point := range sum.DataPoints {
				for _, attr := range point.Attributes.ToSlice() {
					if string(attr.Key) == key && attr.Value.AsString() == value {
						return true
					}
				}
			}
		}
	}
	return false
}

func findEndedSpanByName(
	spans []sdktrace.ReadOnlySpan,
	name string,
) sdktrace.ReadOnlySpan {
	for _, span := range spans {
		if span.Name() == name {
			return span
		}
	}
	return nil
}

func hasStringAttr(
	attrs []attribute.KeyValue,
	key string,
	value string,
) bool {
	for _, attr := range attrs {
		if string(attr.Key) == key && attr.Value.AsString() == value {
			return true
		}
	}
	return false
}

func getStringAttr(attrs []attribute.KeyValue, key string) string {
	for _, attr := range attrs {
		if string(attr.Key) == key {
			return attr.Value.AsString()
		}
	}
	return ""
}

func TestRunWithPlugins_BeforeAgentCanShortCircuit(t *testing.T) {
	ag := &testAgent{}
	p := &cbPlugin{
		name: "p",
		reg: func(r *plugin.Registry) {
			r.BeforeAgent(func(
				ctx context.Context,
				args *agent.BeforeAgentArgs,
			) (*agent.BeforeAgentResult, error) {
				return &agent.BeforeAgentResult{
					CustomResponse: &model.Response{
						Done: true,
						Choices: []model.Choice{{
							Index: 0,
							Message: model.NewAssistantMessage(
								"early",
							),
						}},
					},
				}, nil
			})
		},
	}

	pm := plugin.MustNewManager(p)
	inv := agent.NewInvocation(
		agent.WithInvocationAgent(ag),
		agent.WithInvocationPlugins(pm),
	)
	ctx := agent.NewInvocationContext(context.Background(), inv)

	ch, err := agent.RunWithPlugins(ctx, inv, ag)
	require.NoError(t, err)

	var events []*event.Event
	for e := range ch {
		events = append(events, e)
	}

	require.False(t, ag.wasCalled())
	require.Len(t, events, 1)
	require.NotNil(t, events[0].Response)
	require.Equal(
		t,
		"early",
		events[0].Response.Choices[0].Message.Content,
	)
}

func TestRunWithPlugins_AfterAgentCanAppendEvent(t *testing.T) {
	ag := &testAgent{}
	p := &cbPlugin{
		name: "p",
		reg: func(r *plugin.Registry) {
			r.AfterAgent(func(
				ctx context.Context,
				args *agent.AfterAgentArgs,
			) (*agent.AfterAgentResult, error) {
				return &agent.AfterAgentResult{
					CustomResponse: &model.Response{
						Done: true,
						Choices: []model.Choice{{
							Index: 0,
							Message: model.NewAssistantMessage(
								"after",
							),
						}},
					},
				}, nil
			})
		},
	}

	pm := plugin.MustNewManager(p)
	inv := agent.NewInvocation(
		agent.WithInvocationAgent(ag),
		agent.WithInvocationPlugins(pm),
	)
	ctx := agent.NewInvocationContext(context.Background(), inv)

	ch, err := agent.RunWithPlugins(ctx, inv, ag)
	require.NoError(t, err)

	var events []*event.Event
	for e := range ch {
		events = append(events, e)
	}

	require.True(t, ag.wasCalled())
	require.Len(t, events, 2)
	require.Equal(
		t,
		"orig",
		events[0].Response.Choices[0].Message.Content,
	)
	require.Equal(
		t,
		"after",
		events[1].Response.Choices[0].Message.Content,
	)
}

type ctxValueAgent struct {
	wantKey any
	wantVal any
}

func (a *ctxValueAgent) Run(
	ctx context.Context,
	inv *agent.Invocation,
) (<-chan *event.Event, error) {
	got := ctx.Value(a.wantKey)
	if got != a.wantVal {
		return nil, errors.New("context value missing")
	}
	ch := make(chan *event.Event, 1)
	go func() {
		defer close(ch)
		rsp := &model.Response{Done: true}
		_ = agent.EmitEvent(
			ctx,
			inv,
			ch,
			event.NewResponseEvent(inv.InvocationID, inv.AgentName, rsp),
		)
	}()
	return ch, nil
}

func (a *ctxValueAgent) Tools() []tool.Tool { return nil }

func (a *ctxValueAgent) Info() agent.Info {
	return agent.Info{Name: "ctx-agent", Description: "test"}
}

func (a *ctxValueAgent) SubAgents() []agent.Agent        { return nil }
func (a *ctxValueAgent) FindSubAgent(string) agent.Agent { return nil }

type testCtxKey struct{}

func TestRunWithPlugins_NilAgent_ReturnsError(t *testing.T) {
	_, err := agent.RunWithPlugins(context.Background(), nil, nil)
	require.Error(t, err)
}

func TestRunWithPlugins_NoPlugins_RunsAgentDirectly(t *testing.T) {
	ag := &testAgent{}
	inv := agent.NewInvocation(agent.WithInvocationAgent(ag))
	ctx := agent.NewInvocationContext(context.Background(), inv)

	ch, err := agent.RunWithPlugins(ctx, inv, ag)
	require.NoError(t, err)
	for range ch {
	}
	require.True(t, ag.wasCalled())
}

func TestRunWithPlugins_NoPlugins_RecordsInvokeAgentSpan(t *testing.T) {
	recorder := useRunWithPluginsSpanRecorder(t)
	ag := &preparingTestAgent{
		name:         "prep-agent",
		description:  "prepared description",
		instructions: "prepared instructions",
	}
	inv := agent.NewInvocation(agent.WithInvocationAgent(ag))
	ctx := agent.NewInvocationContext(context.Background(), inv)

	ch, err := agent.RunWithPlugins(ctx, inv, ag)
	require.NoError(t, err)
	for range ch {
	}
	require.True(t, ag.wasCalled())

	span := findEndedSpanByName(
		recorder.Ended(),
		invokeagenttelemetry.OperationInvokeAgent+" "+ag.name,
	)
	require.NotNil(t, span, "expected invoke_agent span to be recorded")
	require.True(
		t,
		hasStringAttr(
			span.Attributes(),
			semconvtrace.KeyGenAIAgentDescription,
			"prepared description",
		),
	)
	require.True(
		t,
		hasStringAttr(
			span.Attributes(),
			semconvtrace.KeyGenAISystemInstructions,
			"prepared instructions",
		),
	)
}

func TestRunWithPlugins_BeforeAgentContextPropagates(t *testing.T) {
	const pluginName = "p"
	ag := &ctxValueAgent{wantKey: testCtxKey{}, wantVal: "v"}

	p := &cbPlugin{
		name: pluginName,
		reg: func(r *plugin.Registry) {
			r.BeforeAgent(func(
				ctx context.Context,
				args *agent.BeforeAgentArgs,
			) (*agent.BeforeAgentResult, error) {
				return &agent.BeforeAgentResult{
					Context: context.WithValue(ctx, testCtxKey{}, "v"),
				}, nil
			})
		},
	}

	pm := plugin.MustNewManager(p)
	inv := agent.NewInvocation(
		agent.WithInvocationAgent(ag),
		agent.WithInvocationPlugins(pm),
	)
	ctx := agent.NewInvocationContext(context.Background(), inv)

	ch, err := agent.RunWithPlugins(ctx, inv, ag)
	require.NoError(t, err)
	for range ch {
	}
}

func TestRunWithPlugins_RunError_RecordsInvokeAgentMetric(t *testing.T) {
	reader := useRunWithPluginsMetricReader(t)
	ag := &syncErrorAgent{
		name:        "err-agent",
		description: "sync error agent",
		err:         errors.New("boom"),
	}
	inv := agent.NewInvocation(agent.WithInvocationAgent(ag))
	ctx := agent.NewInvocationContext(context.Background(), inv)

	ch, err := agent.RunWithPlugins(ctx, inv, ag)
	require.Error(t, err)
	require.Nil(t, ch)

	rm := collectRunWithPluginsMetrics(t, reader)
	require.True(
		t,
		hasInvokeAgentMetricStringAttribute(
			rm,
			metricsemconv.MetricTRPCAgentGoClientRequestCnt,
			semconvtrace.KeyErrorType,
			model.ErrorTypeRunError,
		),
		"expected invoke_agent request count metric to record sync run errors",
	)
}

func TestRunWithPlugins_AfterAgentError_EmitsErrorEvent(t *testing.T) {
	const pluginName = "p"
	ag := &testAgent{}
	p := &cbPlugin{
		name: pluginName,
		reg: func(r *plugin.Registry) {
			r.AfterAgent(func(
				ctx context.Context,
				args *agent.AfterAgentArgs,
			) (*agent.AfterAgentResult, error) {
				return nil, errors.New("boom")
			})
		},
	}

	pm := plugin.MustNewManager(p)
	inv := agent.NewInvocation(
		agent.WithInvocationAgent(ag),
		agent.WithInvocationPlugins(pm),
	)
	ctx := agent.NewInvocationContext(context.Background(), inv)

	ch, err := agent.RunWithPlugins(ctx, inv, ag)
	require.NoError(t, err)

	var events []*event.Event
	for e := range ch {
		events = append(events, e)
	}
	require.Len(t, events, 2)
	require.NotNil(t, events[1].Error)
	require.Equal(
		t,
		agent.ErrorTypeAgentCallbackError,
		events[1].Error.Type,
	)
}

type errorResponseAgent struct{}

func (a *errorResponseAgent) Run(
	ctx context.Context,
	inv *agent.Invocation,
) (<-chan *event.Event, error) {
	ch := make(chan *event.Event, 1)
	go func() {
		defer close(ch)
		rsp := &model.Response{
			Done: true,
			Error: &model.ResponseError{
				Type:    "test",
				Message: "boom",
			},
		}
		_ = agent.EmitEvent(
			ctx,
			inv,
			ch,
			event.NewResponseEvent(inv.InvocationID, inv.AgentName, rsp),
		)
	}()
	return ch, nil
}

func (a *errorResponseAgent) Tools() []tool.Tool { return nil }

func (a *errorResponseAgent) Info() agent.Info {
	return agent.Info{Name: "err-agent", Description: "test"}
}

func (a *errorResponseAgent) SubAgents() []agent.Agent        { return nil }
func (a *errorResponseAgent) FindSubAgent(string) agent.Agent { return nil }

func TestRunWithPlugins_AfterAgentReceivesResponseError(t *testing.T) {
	const pluginName = "p"
	sawError := ""
	p := &cbPlugin{
		name: pluginName,
		reg: func(r *plugin.Registry) {
			r.AfterAgent(func(
				ctx context.Context,
				args *agent.AfterAgentArgs,
			) (*agent.AfterAgentResult, error) {
				if args != nil && args.Error != nil {
					sawError = args.Error.Error()
				}
				return nil, nil
			})
		},
	}

	pm := plugin.MustNewManager(p)
	ag := &errorResponseAgent{}
	inv := agent.NewInvocation(
		agent.WithInvocationAgent(ag),
		agent.WithInvocationPlugins(pm),
	)
	ctx := agent.NewInvocationContext(context.Background(), inv)

	ch, err := agent.RunWithPlugins(ctx, inv, ag)
	require.NoError(t, err)
	for range ch {
	}
	require.Contains(t, sawError, "test")
	require.Contains(t, sawError, "boom")
}

func TestRunWithPlugins_BeforeAgentCustomResponse_RecordsInvokeAgentSpan(t *testing.T) {
	recorder := useRunWithPluginsSpanRecorder(t)
	ag := &preparingTestAgent{
		name:         "prep-agent",
		description:  "prepared description",
		instructions: "prepared instructions",
	}
	p := &cbPlugin{
		name: "p",
		reg: func(r *plugin.Registry) {
			r.BeforeAgent(func(
				ctx context.Context,
				args *agent.BeforeAgentArgs,
			) (*agent.BeforeAgentResult, error) {
				return &agent.BeforeAgentResult{
					CustomResponse: &model.Response{
						Done: true,
						Choices: []model.Choice{{
							Index:   0,
							Message: model.NewAssistantMessage("short-circuit"),
						}},
					},
				}, nil
			})
		},
	}

	pm := plugin.MustNewManager(p)
	inv := agent.NewInvocation(
		agent.WithInvocationAgent(ag),
		agent.WithInvocationPlugins(pm),
	)
	ctx := agent.NewInvocationContext(context.Background(), inv)

	ch, err := agent.RunWithPlugins(ctx, inv, ag)
	require.NoError(t, err)
	var events []*event.Event
	for evt := range ch {
		events = append(events, evt)
	}

	require.False(t, ag.wasCalled())
	require.Len(t, events, 1)
	span := findEndedSpanByName(
		recorder.Ended(),
		invokeagenttelemetry.OperationInvokeAgent+" "+ag.name,
	)
	require.NotNil(t, span, "expected invoke_agent span to be recorded")
	outputMessages := getStringAttr(span.Attributes(), semconvtrace.KeyGenAIOutputMessages)
	require.NotEmpty(t, outputMessages)
	require.True(t, strings.Contains(outputMessages, "short-circuit"))
}
