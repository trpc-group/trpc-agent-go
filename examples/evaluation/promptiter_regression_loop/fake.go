//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"sync"

	astructure "trpc.group/trpc-go/trpc-agent-go/agent/structure"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/aggregator"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/backwarder"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/optimizer"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

var recordIDPattern = regexp.MustCompile(`\bTR[0-9]{3}\b`)

type fakeModel struct {
	mu               sync.Mutex
	instructions     []string
	toolDescriptions []string
	requestCount     int
}

type fakeModelObservations struct {
	Instructions     []string
	ToolDescriptions []string
	RequestCount     int
}

type userIntent string

const (
	intentStatus    userIntent = "status"
	intentDelay     userIntent = "delay"
	intentGate      userIntent = "gate"
	intentDeparture userIntent = "departure"
)

func newFakeModel() *fakeModel {
	return &fakeModel{}
}

func (m *fakeModel) GenerateContent(_ context.Context, request *model.Request) (<-chan *model.Response, error) {
	if request == nil {
		return nil, errors.New("request is nil")
	}
	response := m.generate(request)
	ch := make(chan *model.Response, 1)
	ch <- response
	close(ch)
	return ch, nil
}

func (m *fakeModel) Info() model.Info {
	return model.Info{Name: fakeModelName}
}

func (m *fakeModel) observations() fakeModelObservations {
	m.mu.Lock()
	defer m.mu.Unlock()
	return fakeModelObservations{
		Instructions:     append([]string(nil), m.instructions...),
		ToolDescriptions: append([]string(nil), m.toolDescriptions...),
		RequestCount:     m.requestCount,
	}
}

func (m *fakeModel) generate(request *model.Request) *model.Response {
	m.recordRequest(request)
	if result, ok := latestToolResult(request.Messages); ok {
		return fakeTextResponse(finalResponseFromRecord(result))
	}
	userText := joinedUserText(request.Messages)
	intent, hasIntent := extractUserIntent(userText)
	recordID, hasRecordID := extractRecordID(userText)
	description := lookupToolDescription(request.Tools)
	if hasRecordID && toolDescriptionForcesLookup(description) {
		args, _ := json.Marshal(map[string]string{"query": recordID})
		return fakeToolCallResponse("call_lookup_"+strings.ToLower(recordID), "lookup_record", args)
	}
	if response, ok := directNoToolResponse(userText); ok {
		return fakeTextResponse(response)
	}
	if hasRecordID && hasIntent && toolDescriptionSupportsIntent(description, intent) {
		args, _ := json.Marshal(map[string]string{"query": recordID})
		return fakeToolCallResponse("call_lookup_"+strings.ToLower(recordID), "lookup_record", args)
	}
	return fakeTextResponse("I need a matching flight lookup tool before answering that record request.")
}

func (m *fakeModel) recordRequest(request *model.Request) {
	instruction := joinedSystemText(request.Messages)
	description := lookupToolDescription(request.Tools)
	m.mu.Lock()
	defer m.mu.Unlock()
	m.requestCount++
	if instruction != "" {
		m.instructions = append(m.instructions, instruction)
	}
	if description != "" {
		m.toolDescriptions = append(m.toolDescriptions, description)
	}
}

func joinedSystemText(messages []model.Message) string {
	var parts []string
	for _, msg := range messages {
		if msg.Role == model.RoleSystem && strings.TrimSpace(msg.Content) != "" {
			parts = append(parts, msg.Content)
		}
	}
	return strings.Join(parts, "\n")
}

func joinedUserText(messages []model.Message) string {
	var parts []string
	for _, msg := range messages {
		if msg.Role == model.RoleUser && strings.TrimSpace(msg.Content) != "" {
			parts = append(parts, msg.Content)
		}
	}
	return strings.Join(parts, "\n")
}

func lookupToolDescription(tools map[string]tool.Tool) string {
	if tools == nil {
		return ""
	}
	lookup := tools["lookup_record"]
	if lookup == nil || lookup.Declaration() == nil {
		return ""
	}
	return lookup.Declaration().Description
}

func directNoToolResponse(userText string) (string, bool) {
	lower := strings.ToLower(userText)
	if !strings.Contains(lower, "without looking anything up") {
		return "", false
	}
	normalized := strings.ReplaceAll(lower, `"`, `'`)
	switch {
	case strings.Contains(normalized, "just say ready") ||
		strings.Contains(normalized, "just say: ready"):
		return "ready", true
	case strings.Contains(normalized, "just say 'tr789 is cancelled'") ||
		strings.Contains(normalized, "just say: tr789 is cancelled"):
		return "TR789 is cancelled.", true
	default:
		return "", false
	}
}

func extractRecordID(userText string) (string, bool) {
	match := recordIDPattern.FindString(strings.ToUpper(userText))
	if match == "" {
		return "", false
	}
	return match, true
}

func extractUserIntent(userText string) (userIntent, bool) {
	lower := strings.ToLower(userText)
	switch {
	case strings.Contains(lower, "status") ||
		strings.Contains(lower, "cancelled") ||
		strings.Contains(lower, "operating"):
		return intentStatus, true
	case strings.Contains(lower, "delay") || strings.Contains(lower, "delayed"):
		return intentDelay, true
	case strings.Contains(lower, "gate"):
		return intentGate, true
	case strings.Contains(lower, "departure") || strings.Contains(lower, "depart"):
		return intentDeparture, true
	default:
		return "", false
	}
}

func toolDescriptionSupportsIntent(description string, intent userIntent) bool {
	lower := strings.ToLower(description)
	if !strings.Contains(lower, "flight") {
		return false
	}
	switch intent {
	case intentStatus:
		return strings.Contains(lower, "status")
	case intentDelay:
		return strings.Contains(lower, "delay")
	case intentGate:
		return strings.Contains(lower, "gate")
	case intentDeparture:
		return strings.Contains(lower, "departure") || strings.Contains(lower, "depart")
	default:
		return false
	}
}

func toolDescriptionForcesLookup(description string) bool {
	lower := strings.ToLower(description)
	return strings.Contains(lower, "always") && strings.Contains(lower, "even if")
}

func latestToolResult(messages []model.Message) (lookupRecordResult, bool) {
	for i := len(messages) - 1; i >= 0; i-- {
		msg := messages[i]
		if msg.Role != model.RoleTool || strings.TrimSpace(msg.Content) == "" {
			continue
		}
		var result lookupRecordResult
		if err := json.Unmarshal([]byte(msg.Content), &result); err != nil {
			continue
		}
		if result.RecordID != "" {
			return result, true
		}
	}
	return lookupRecordResult{}, false
}

func finalResponseFromRecord(record lookupRecordResult) string {
	switch record.State {
	case "delayed":
		return fmt.Sprintf(
			"%s is delayed by %d minutes. Scheduled departure %s, updated departure %s. Gate %s.",
			record.RecordID,
			record.DelayMinutes,
			record.ScheduledDeparture,
			record.UpdatedDeparture,
			record.Gate,
		)
	case "cancelled":
		return fmt.Sprintf(
			"%s is cancelled. Scheduled departure %s. Gate unavailable.",
			record.RecordID,
			record.ScheduledDeparture,
		)
	case "boarding":
		return fmt.Sprintf(
			"%s is boarding. Scheduled departure %s. Gate %s.",
			record.RecordID,
			record.ScheduledDeparture,
			record.Gate,
		)
	default:
		return fmt.Sprintf("%s has no available flight record.", record.RecordID)
	}
}

func fakeTextResponse(content string) *model.Response {
	finishReason := "stop"
	return &model.Response{
		Object: model.ObjectTypeChatCompletion,
		Model:  fakeModelName,
		Done:   true,
		Choices: []model.Choice{
			{
				Index: 0,
				Message: model.Message{
					Role:    model.RoleAssistant,
					Content: content,
				},
				FinishReason: &finishReason,
			},
		},
	}
}

func fakeToolCallResponse(callID, name string, args []byte) *model.Response {
	finishReason := "tool_calls"
	return &model.Response{
		Object: model.ObjectTypeChatCompletion,
		Model:  fakeModelName,
		Done:   true,
		Choices: []model.Choice{
			{
				Index: 0,
				Message: model.Message{
					Role: model.RoleAssistant,
					ToolCalls: []model.ToolCall{
						{
							Type: "function",
							ID:   callID,
							Function: model.FunctionDefinitionParam{
								Name:      name,
								Arguments: args,
							},
						},
					},
				},
				FinishReason: &finishReason,
			},
		},
	}
}

type fakeBackwarder struct {
	targetSurfaceID string
}

func (b *fakeBackwarder) Backward(_ context.Context, request *backwarder.Request) (*backwarder.Result, error) {
	if request == nil || len(request.AllowedGradientSurfaceIDs) == 0 {
		return emptyBackwardResult(), nil
	}
	if !slices.Contains(request.AllowedGradientSurfaceIDs, b.targetSurfaceID) {
		return emptyBackwardResult(), nil
	}
	return &backwarder.Result{
		Gradients: []promptiter.SurfaceGradient{
			{
				EvalSetID:  request.EvalSetID,
				EvalCaseID: request.EvalCaseID,
				StepID:     request.StepID,
				SurfaceID:  b.targetSurfaceID,
				Severity:   promptiter.LossSeverityP1,
				Gradient:   "lookup_record needs flight status, delay, departure, and gate capability wording",
			},
		},
		Upstream: []backwarder.Propagation{},
	}, nil
}

func emptyBackwardResult() *backwarder.Result {
	return &backwarder.Result{
		Gradients: []promptiter.SurfaceGradient{},
		Upstream:  []backwarder.Propagation{},
	}
}

type fakeAggregator struct{}

func (a *fakeAggregator) Aggregate(_ context.Context, request *aggregator.Request) (*aggregator.Result, error) {
	if request == nil {
		return nil, errors.New("aggregation request is nil")
	}
	if len(request.Gradients) == 0 {
		return nil, errors.New("aggregation gradients are empty")
	}
	return &aggregator.Result{
		Gradient: &promptiter.AggregatedSurfaceGradient{
			SurfaceID: request.SurfaceID,
			NodeID:    request.NodeID,
			Type:      request.Type,
			Gradients: append([]promptiter.SurfaceGradient(nil), request.Gradients...),
		},
	}, nil
}

type fakeOptimizer struct {
	mu        sync.Mutex
	callCount int
}

func (o *fakeOptimizer) Optimize(_ context.Context, request *optimizer.Request) (*optimizer.Result, error) {
	if request == nil || request.Surface == nil {
		return nil, errors.New("optimization request or surface is nil")
	}
	if request.Surface.Type != astructure.SurfaceTypeTool {
		return nil, fmt.Errorf("unsupported surface type %q", request.Surface.Type)
	}
	if len(request.Surface.Value.Tools) != 1 {
		return nil, fmt.Errorf("tools must contain exactly one tool, got %d", len(request.Surface.Value.Tools))
	}
	o.mu.Lock()
	o.callCount++
	callCount := o.callCount
	o.mu.Unlock()
	description, reason := fakeOptimizerPatch(callCount)
	toolRef := request.Surface.Value.Tools[0]
	return &optimizer.Result{
		Patch: &promptiter.SurfacePatch{
			SurfaceID: request.Surface.SurfaceID,
			Value: astructure.SurfaceValue{
				Tools: []astructure.ToolRef{
					{
						ID:           toolRef.ID,
						Description:  description,
						InputSchema:  toolRef.InputSchema,
						OutputSchema: toolRef.OutputSchema,
					},
				},
			},
			Reason: reason,
		},
	}, nil
}

func fakeOptimizerPatch(callCount int) (string, string) {
	if callCount <= 1 {
		return round1ToolDescription, "Phase 4 v2 round 1 fake optimizer adds flight delay capability to lookup_record."
	}
	return round2ToolDescription, "Phase 4 v2 round 2 fake optimizer overfits by forcing lookup_record for flight records."
}
