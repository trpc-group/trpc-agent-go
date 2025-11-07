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
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/graph/internal/channel"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

func TestFormatNodeAuthor(t *testing.T) {
	require.Equal(t, "n1", formatNodeAuthor("n1", AuthorGraphNode))
	require.Equal(t, AuthorGraphNode, formatNodeAuthor("", AuthorGraphNode))
}

func TestWithMetadataHelpers(t *testing.T) {
	e := event.New("inv-1", AuthorGraphNode)

	nodeMeta := NodeExecutionMetadata{NodeID: "n1", NodeType: NodeTypeFunction, Phase: ExecutionPhaseStart}
	WithNodeMetadata(nodeMeta)(e)
	require.Contains(t, e.StateDelta, MetadataKeyNode)

	toolMeta := ToolExecutionMetadata{ToolName: "t", ToolID: "id", Phase: ToolExecutionPhaseStart}
	WithToolMetadata(toolMeta)(e)
	require.Contains(t, e.StateDelta, MetadataKeyTool)

	modelMeta := ModelExecutionMetadata{ModelName: "m", NodeID: "n1", Phase: ModelExecutionPhaseStart}
	WithModelMetadata(modelMeta)(e)
	require.Contains(t, e.StateDelta, MetadataKeyModel)

	prMeta := PregelStepMetadata{StepNumber: 1, Phase: PregelPhasePlanning}
	WithPregelMetadata(prMeta)(e)
	require.Contains(t, e.StateDelta, MetadataKeyPregel)

	chMeta := ChannelUpdateMetadata{ChannelName: "c", ChannelType: channel.BehaviorTopic, ValueCount: 2}
	WithChannelMetadata(chMeta)(e)
	require.Contains(t, e.StateDelta, MetadataKeyChannel)

	stMeta := StateUpdateMetadata{UpdatedKeys: []string{"a"}, StateSize: 1}
	WithStateMetadata(stMeta)(e)
	require.Contains(t, e.StateDelta, MetadataKeyState)
}

func TestNewNodeEvents(t *testing.T) {
	start := time.Now().Add(-time.Second).UTC()
	end := start.Add(150 * time.Millisecond)

	e1 := NewNodeStartEvent(
		WithNodeEventInvocationID("inv"),
		WithNodeEventNodeID("node-1"),
		WithNodeEventNodeType(NodeTypeLLM),
		WithNodeEventStepNumber(3),
		WithNodeEventStartTime(start),
		WithNodeEventInputKeys([]string{"a"}),
		WithNodeEventModelName("gpt"),
		WithNodeEventModelInput("hello"),
	)
	require.Equal(t, ObjectTypeGraphNodeStart, e1.Object)
	var meta1 NodeExecutionMetadata
	require.NoError(t, json.Unmarshal(e1.StateDelta[MetadataKeyNode], &meta1))
	require.Equal(t, ExecutionPhaseStart, meta1.Phase)
	require.Equal(t, 3, meta1.StepNumber)
	require.Equal(t, "gpt", meta1.ModelName)
	require.Equal(t, "hello", meta1.ModelInput)

	e2 := NewNodeCompleteEvent(
		WithNodeEventInvocationID("inv"),
		WithNodeEventNodeID("node-1"),
		WithNodeEventNodeType(NodeTypeTool),
		WithNodeEventStepNumber(4),
		WithNodeEventStartTime(start),
		WithNodeEventEndTime(end),
		WithNodeEventOutputKeys([]string{"b"}),
		WithNodeEventToolCalls([]model.ToolCall{{ID: "t1", Function: model.FunctionDefinitionParam{Name: "tool"}}}),
		WithNodeEventModelName("gpt-2"),
	)
	require.Equal(t, ObjectTypeGraphNodeComplete, e2.Object)
	var meta2 NodeExecutionMetadata
	require.NoError(t, json.Unmarshal(e2.StateDelta[MetadataKeyNode], &meta2))
	require.Equal(t, ExecutionPhaseComplete, meta2.Phase)
	require.Equal(t, 4, meta2.StepNumber)
	require.Greater(t, meta2.Duration, time.Duration(0))
	require.Equal(t, "gpt-2", meta2.ModelName)
	require.Equal(t, 1, len(meta2.ToolCalls))

	e3 := NewNodeErrorEvent(
		WithNodeEventInvocationID("inv"),
		WithNodeEventNodeID("node-err"),
		WithNodeEventNodeType(NodeTypeFunction),
		WithNodeEventStepNumber(5),
		WithNodeEventStartTime(start),
		WithNodeEventEndTime(end),
		WithNodeEventError("boom"),
	)
	require.Equal(t, model.ObjectTypeError, e3.Object)
	var meta3 NodeExecutionMetadata
	require.NoError(t, json.Unmarshal(e3.StateDelta[MetadataKeyNode], &meta3))
	require.Equal(t, ExecutionPhaseError, meta3.Phase)
	require.Equal(t, "boom", meta3.Error)
	require.NotNil(t, e3.Response)
	require.NotNil(t, e3.Response.Error)
	require.Equal(t, model.ErrorTypeFlowError, e3.Response.Error.Type)
	require.Equal(t, "boom", e3.Response.Error.Message)
}

func TestNewToolAndModelEvents(t *testing.T) {
	start := time.Now().UTC()
	end := start.Add(10 * time.Millisecond)
	toolErr := errors.New("tool failed")

	te := NewToolExecutionEvent(
		WithToolEventInvocationID("inv"),
		WithToolEventToolName("fetch"),
		WithToolEventToolID("t-1"),
		WithToolEventNodeID("node-1"),
		WithToolEventPhase(ToolExecutionPhaseComplete),
		WithToolEventStartTime(start),
		WithToolEventEndTime(end),
		WithToolEventInput("in"),
		WithToolEventOutput("out"),
		WithToolEventError(toolErr),
		WithToolEventIncludeResponse(true),
	)
	require.Equal(t, model.ObjectTypeToolResponse, te.Object)
	var tmeta ToolExecutionMetadata
	require.NoError(t, json.Unmarshal(te.StateDelta[MetadataKeyTool], &tmeta))
	require.Equal(t, "fetch", tmeta.ToolName)
	require.Equal(t, "t-1", tmeta.ToolID)
	require.Equal(t, ToolExecutionPhaseComplete, tmeta.Phase)
	require.Equal(t, "out", tmeta.Output)
	require.Equal(t, "in", tmeta.Input)
	require.Equal(t, "tool failed", tmeta.Error)
	require.NotNil(t, te.Response)
	require.Equal(t, model.ObjectTypeToolResponse, te.Response.Object)
	require.True(t, te.Response.Done)
	require.Len(t, te.Response.Choices, 1)
	require.Equal(t, model.RoleTool, te.Response.Choices[0].Message.Role)
	require.Equal(t, "t-1", te.Response.Choices[0].Message.ToolID)
	require.Equal(t, "fetch", te.Response.Choices[0].Message.ToolName)
	require.Equal(t, "out", te.Response.Choices[0].Message.Content)
	require.NotNil(t, te.Response.Error)
	require.Equal(t, "tool failed", te.Response.Error.Message)
	require.Equal(t, model.ErrorTypeFlowError, te.Response.Error.Type)
	require.GreaterOrEqual(t, tmeta.Duration, time.Duration(0))

	me := NewModelExecutionEvent(
		WithModelEventInvocationID("inv"),
		WithModelEventModelName("gpt"),
		WithModelEventNodeID("node-1"),
		WithModelEventPhase(ModelExecutionPhaseError),
		WithModelEventStartTime(start),
		WithModelEventEndTime(end),
		WithModelEventInput("hi"),
		WithModelEventOutput("bye"),
		WithModelEventError(errors.New("oops")),
		WithModelEventStepNumber(9),
	)
	require.Equal(t, ObjectTypeGraphNodeExecution, me.Object)
	var mmeta ModelExecutionMetadata
	require.NoError(t, json.Unmarshal(me.StateDelta[MetadataKeyModel], &mmeta))
	require.Equal(t, ModelExecutionPhaseError, mmeta.Phase)
	require.Equal(t, 9, mmeta.StepNumber)
	require.Equal(t, "oops", mmeta.Error)
}

func TestNewPregelAndChannelStateEvents(t *testing.T) {
	start := time.Now().UTC()
	end := start.Add(5 * time.Millisecond)

	ps := NewPregelStepEvent(
		WithPregelEventInvocationID("inv"),
		WithPregelEventStepNumber(1),
		WithPregelEventPhase(PregelPhasePlanning),
		WithPregelEventTaskCount(2),
		WithPregelEventUpdatedChannels([]string{"a"}),
		WithPregelEventActiveNodes([]string{"n1"}),
		WithPregelEventStartTime(start),
		WithPregelEventEndTime(end),
	)
	require.Equal(t, ObjectTypeGraphPregelStep, ps.Object)
	var pmeta PregelStepMetadata
	require.NoError(t, json.Unmarshal(ps.StateDelta[MetadataKeyPregel], &pmeta))
	require.Equal(t, PregelPhasePlanning, pmeta.Phase)
	require.Equal(t, 1, pmeta.StepNumber)

	pe := NewPregelErrorEvent(
		WithPregelEventInvocationID("inv"),
		WithPregelEventStepNumber(2),
		WithPregelEventPhase(PregelPhaseError),
		WithPregelEventStartTime(start),
		WithPregelEventEndTime(end),
		WithPregelEventError("fail"),
	)
	require.Equal(t, ObjectTypeGraphPregelStep, pe.Object)
	require.NoError(t, json.Unmarshal(pe.StateDelta[MetadataKeyPregel], &pmeta))
	require.Equal(t, "fail", pmeta.Error)
	// Also mirrored to Event.Error for convenient consumption
	require.NotNil(t, pe.Response)
	require.NotNil(t, pe.Response.Error)
	require.Equal(t, model.ErrorTypeFlowError, pe.Response.Error.Type)
	require.Equal(t, "fail", pe.Response.Error.Message)

	pi := NewPregelInterruptEvent(
		WithPregelEventInvocationID("inv"),
		WithPregelEventStepNumber(3),
		WithPregelEventPhase(PregelPhaseUpdate),
		WithPregelEventStartTime(start),
		WithPregelEventEndTime(end),
		WithPregelEventNodeID("nodeX"),
		WithPregelEventInterruptValue("ask"),
	)
	require.Equal(t, ObjectTypeGraphPregelStep, pi.Object)
	require.NoError(t, json.Unmarshal(pi.StateDelta[MetadataKeyPregel], &pmeta))
	require.Equal(t, "nodeX", pmeta.NodeID)
	require.Equal(t, "ask", pmeta.InterruptValue)

	ce := NewChannelUpdateEvent(
		WithChannelEventInvocationID("inv"),
		WithChannelEventChannelName("ch"),
		WithChannelEventChannelType(channel.BehaviorTopic),
		WithChannelEventValueCount(3),
		WithChannelEventAvailable(true),
		WithChannelEventTriggeredNodes([]string{"n2"}),
	)
	require.Equal(t, ObjectTypeGraphChannelUpdate, ce.Object)
	var cmeta ChannelUpdateMetadata
	require.NoError(t, json.Unmarshal(ce.StateDelta[MetadataKeyChannel], &cmeta))
	require.Equal(t, "ch", cmeta.ChannelName)
	require.True(t, cmeta.Available)

	se := NewStateUpdateEvent(
		WithStateEventInvocationID("inv"),
		WithStateEventUpdatedKeys([]string{"x"}),
		WithStateEventRemovedKeys([]string{"y"}),
		WithStateEventStateSize(7),
	)
	require.Equal(t, ObjectTypeGraphStateUpdate, se.Object)
	var smeta StateUpdateMetadata
	require.NoError(t, json.Unmarshal(se.StateDelta[MetadataKeyState], &smeta))
	require.Equal(t, []string{"x"}, smeta.UpdatedKeys)
	require.Equal(t, []string{"y"}, smeta.RemovedKeys)
	require.Equal(t, 7, smeta.StateSize)
}

func TestNewGraphCompletionEvent(t *testing.T) {
	final := State{
		StateKeyLastResponse: "done message",
		"k1":                 123,
		"k2":                 "v",
	}
	e := NewGraphCompletionEvent(
		WithCompletionEventInvocationID("inv"),
		WithCompletionEventFinalState(final),
		WithCompletionEventTotalSteps(10),
		WithCompletionEventTotalDuration(100*time.Millisecond),
	)
	require.True(t, e.Response.Done)
	require.Len(t, e.Response.Choices, 1)
	// completion metadata present
	require.Contains(t, e.StateDelta, MetadataKeyCompletion)
	// final state keys serialized
	require.Contains(t, e.StateDelta, "k1")
	require.Contains(t, e.StateDelta, "k2")

	// Verify metadata can unmarshal
	var cm CompletionMetadata
	require.NoError(t, json.Unmarshal(e.StateDelta[MetadataKeyCompletion], &cm))
	require.Equal(t, 10, cm.TotalSteps)
	require.Equal(t, 3, cm.FinalStateKeys) // includes StateKeyLastResponse + k1 + k2
}

func TestNewGraphCompletionEvent_SerializeFinalStateSkipsInternalAndUnserializable(t *testing.T) {
	final := State{
		StateKeyLastResponse: "ok",
		"keep1":              1,
		"keep2":              map[string]any{"a": 1},
		// internal keys that must be skipped.
		MetadataKeyNode:        []byte("x"),
		MetadataKeyPregel:      []byte("y"),
		MetadataKeyChannel:     []byte("z"),
		MetadataKeyState:       []byte("w"),
		MetadataKeyCompletion:  []byte("v"),
		StateKeyExecContext:    "ctx",
		StateKeyParentAgent:    "agent",
		StateKeyNodeCallbacks:  NewNodeCallbacks(),
		StateKeyToolCallbacks:  "tc",
		StateKeyModelCallbacks: "mc",
		StateKeyAgentCallbacks: "ac",
		StateKeyCurrentNodeID:  "nid",
		StateKeySession:        "sid",
		// Unserializable value; json.Marshal should fail and be ignored.
		"bad": func() {},
	}
	e := NewGraphCompletionEvent(
		WithCompletionEventInvocationID("inv"),
		WithCompletionEventFinalState(final),
		WithCompletionEventTotalSteps(2),
		WithCompletionEventTotalDuration(1*time.Millisecond),
	)
	// Should include keep1 and keep2, exclude internal keys and unserializable key.
	require.Contains(t, e.StateDelta, "keep1")
	require.Contains(t, e.StateDelta, "keep2")
	require.NotContains(t, e.StateDelta, MetadataKeyNode)
	require.NotContains(t, e.StateDelta, MetadataKeyPregel)
	require.NotContains(t, e.StateDelta, MetadataKeyChannel)
	require.NotContains(t, e.StateDelta, MetadataKeyState)
	require.NotContains(t, e.StateDelta, StateKeyExecContext)
	require.NotContains(t, e.StateDelta, StateKeyParentAgent)
	require.NotContains(t, e.StateDelta, StateKeyNodeCallbacks)
	require.NotContains(t, e.StateDelta, StateKeyToolCallbacks)
	require.NotContains(t, e.StateDelta, StateKeyModelCallbacks)
	require.NotContains(t, e.StateDelta, StateKeyAgentCallbacks)
	require.NotContains(t, e.StateDelta, StateKeyCurrentNodeID)
	require.NotContains(t, e.StateDelta, StateKeySession)
	require.NotContains(t, e.StateDelta, "bad")
}

func TestNewGraphCompletionEvent_NilFinalState(t *testing.T) {
	e := NewGraphCompletionEvent(
		WithCompletionEventInvocationID("inv"),
		WithCompletionEventFinalState(nil),
		WithCompletionEventTotalSteps(0),
		WithCompletionEventTotalDuration(0),
	)
	// StateDelta should be initialized.
	require.NotNil(t, e.StateDelta)
	// No assistant choice expected when there is no StateKeyLastResponse.
	if len(e.Response.Choices) > 0 {
		t.Fatalf("expected no choices when final state is nil")
	}
	// Completion metadata should exist and FinalStateKeys should be 0.
	var cm CompletionMetadata
	require.NoError(t, json.Unmarshal(e.StateDelta[MetadataKeyCompletion], &cm))
	require.Equal(t, 0, cm.FinalStateKeys)
}
