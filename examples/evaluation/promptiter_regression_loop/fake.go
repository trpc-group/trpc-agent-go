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
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"

	astructure "trpc.group/trpc-go/trpc-agent-go/agent/structure"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/aggregator"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/backwarder"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/optimizer"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

const (
	initialLookupDescription = "Look up a traveler loyalty-profile record."
	partialLookupDescription = "Use lookup_record only for flight delay questions."
	overfitLookupDescription = "Use lookup_record for all flight questions, including delay, gate, and departure. Always look up any TR record before answering."
	partialToolPatchReason   = "Teach the lookup_record declaration that delay questions can be answered from flight records."
	overfitToolPatchReason   = "Over-generalize lookup_record so every TR flight question is routed through the tool."
	fakeResponseCreatedUnix  = int64(1700000000)
)

var recordIDPattern = regexp.MustCompile(`\bTR[0-9]+\b`)

type deterministicFlightModel struct {
	mu                       sync.Mutex
	callCount                int
	observedToolDescriptions []string
	observedSystemMessages   []string
	sawToolResult            bool
}

func (m *deterministicFlightModel) GenerateContent(
	ctx context.Context,
	request *model.Request,
) (<-chan *model.Response, error) {
	if request == nil {
		return nil, fmt.Errorf("request is nil")
	}
	m.recordRequest(request)
	last := lastMessage(request.Messages)
	if last != nil && last.Role == model.RoleTool {
		m.markSawToolResult()
		userContent := lastUserContent(request.Messages)
		record := recordFromToolContent(last.Content, recordIDFromText(userContent))
		return singleResponse(finalMessage(finalResponseForRecord(userContent, record))), nil
	}
	userContent := lastUserContent(request.Messages)
	recordID := recordIDFromText(userContent)
	if recordID == "" {
		return singleResponse(finalMessage("I can only answer deterministic flight record examples in fake mode.")), nil
	}
	description := lookupDescription(request.Tools)
	if descriptionForcesFlightLookup(description) {
		return singleResponse(toolCallMessage(recordID)), nil
	}
	if isDirectNoToolRequest(userContent) {
		return singleResponse(finalMessage(directNoToolResponse(userContent))), nil
	}
	if !descriptionSupportsFlightIntent(description, userContent) {
		return singleResponse(finalMessage("I do not have enough flight status information to answer that.")), nil
	}
	return singleResponse(toolCallMessage(recordID)), nil
}

func toolCallMessage(recordID string) model.Message {
	args, err := json.Marshal(map[string]string{"query": recordID})
	if err != nil {
		return finalMessage("I could not build the deterministic lookup request.")
	}
	call := model.ToolCall{
		Type: "function",
		ID:   "call_lookup_" + strings.ToLower(recordID),
		Function: model.FunctionDefinitionParam{
			Name:      "lookup_record",
			Arguments: args,
		},
	}
	return model.Message{
		Role:      model.RoleAssistant,
		ToolCalls: []model.ToolCall{call},
	}
}

func (m *deterministicFlightModel) Info() model.Info {
	return model.Info{Name: "deterministic-flight-fake"}
}

func (m *deterministicFlightModel) recordRequest(request *model.Request) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.callCount++
	for _, msg := range request.Messages {
		if msg.Role == model.RoleSystem {
			m.observedSystemMessages = append(m.observedSystemMessages, msg.Content)
		}
	}
	if description := lookupDescription(request.Tools); description != "" {
		m.observedToolDescriptions = append(m.observedToolDescriptions, description)
	}
}

func (m *deterministicFlightModel) markSawToolResult() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sawToolResult = true
}

func (m *deterministicFlightModel) CallCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.callCount
}

func (m *deterministicFlightModel) ObservedToolDescriptions() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string(nil), m.observedToolDescriptions...)
}

func (m *deterministicFlightModel) ObservedSystemMessages() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string(nil), m.observedSystemMessages...)
}

func (m *deterministicFlightModel) SawToolResult() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.sawToolResult
}

func singleResponse(msg model.Message) <-chan *model.Response {
	ch := make(chan *model.Response, 1)
	ch <- &model.Response{
		ID:      "resp_fake_flight",
		Object:  model.ObjectTypeChatCompletion,
		Created: fakeResponseCreatedUnix,
		Model:   "deterministic-flight-fake",
		Choices: []model.Choice{{
			Index:   0,
			Message: msg,
		}},
		Done: true,
	}
	close(ch)
	return ch
}

func finalMessage(content string) model.Message {
	return model.Message{Role: model.RoleAssistant, Content: content}
}

func lastMessage(messages []model.Message) *model.Message {
	if len(messages) == 0 {
		return nil
	}
	return &messages[len(messages)-1]
}

func lastUserContent(messages []model.Message) string {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == model.RoleUser {
			return messages[i].Content
		}
	}
	return ""
}

func lookupDescription(tools map[string]tool.Tool) string {
	if len(tools) == 0 {
		return ""
	}
	lookup := tools["lookup_record"]
	if lookup == nil || lookup.Declaration() == nil {
		return ""
	}
	return lookup.Declaration().Description
}

func descriptionForcesFlightLookup(description string) bool {
	lower := strings.ToLower(description)
	return strings.Contains(lower, "all flight questions") ||
		strings.Contains(lower, "always look up")
}

func descriptionSupportsFlightIntent(description string, userContent string) bool {
	lowerDescription := strings.ToLower(description)
	if !strings.Contains(lowerDescription, "flight") {
		return false
	}
	intent := flightIntent(userContent)
	switch intent {
	case "delay":
		return strings.Contains(lowerDescription, "delay")
	case "gate":
		return strings.Contains(lowerDescription, "gate")
	case "departure":
		return strings.Contains(lowerDescription, "departure")
	case "status":
		return strings.Contains(lowerDescription, "status") || strings.Contains(lowerDescription, "cancel")
	default:
		return false
	}
}

func flightIntent(userContent string) string {
	lower := strings.ToLower(userContent)
	switch {
	case strings.Contains(lower, "delay") || strings.Contains(lower, "delayed"):
		return "delay"
	case strings.Contains(lower, "gate"):
		return "gate"
	case strings.Contains(lower, "departure") || strings.Contains(lower, "depart"):
		return "departure"
	case strings.Contains(lower, "status") ||
		strings.Contains(lower, "operating") ||
		strings.Contains(lower, "cancelled") ||
		strings.Contains(lower, "canceled"):
		return "status"
	default:
		return ""
	}
}

func recordIDFromText(text string) string {
	return recordIDPattern.FindString(strings.ToUpper(text))
}

func isDirectNoToolRequest(text string) bool {
	lower := strings.ToLower(text)
	return strings.Contains(lower, "already have the record") ||
		strings.Contains(lower, "already in front of me") ||
		strings.Contains(lower, "dispatch record is already in front of me") ||
		strings.Contains(lower, "without looking anything up")
}

func directNoToolResponse(text string) string {
	switch recordIDFromText(text) {
	case "TR222":
		return "Flight TR222 is boarding at gate E04 and remains scheduled to depart at 09:20 with no delay."
	case "TR789":
		return "Flight TR789 is currently cancelled, so it is not operating tonight."
	case "TR888":
		return "Flight TR888 is delayed by 10 minutes and is now estimated to depart at 17:40 from gate F09."
	default:
		return "The provided flight record is already sufficient, so no lookup is needed."
	}
}

func recordFromToolContent(content string, fallbackID string) recordLookupResult {
	var record recordLookupResult
	if err := json.Unmarshal([]byte(content), &record); err == nil && record.RecordID != "" {
		return record
	}
	if fallbackID == "" {
		fallbackID = recordIDFromText(content)
	}
	record, ok := recordByID(fallbackID)
	if ok {
		return record
	}
	return recordLookupResult{RecordID: fallbackID, State: "unknown"}
}

func finalResponseForRecord(userContent string, record recordLookupResult) string {
	if isDirectNoToolRequest(userContent) && record.RecordID == "TR789" {
		return "Lookup result for TR789: cancelled."
	}
	switch record.RecordID {
	case "TR123":
		return "Flight TR123 is delayed by 35 minutes and is now estimated to depart at 10:45 from gate B12."
	case "TR456":
		return "Flight TR456 is delayed by 15 minutes and is estimated to depart at 12:45 from gate A07."
	case "TR789":
		return "Flight TR789 is currently cancelled, so it is not operating tonight."
	case "TR321":
		return "Flight TR321 is delayed by 20 minutes and is estimated to depart at 14:40 from gate C03."
	case "TR654":
		return "Flight TR654 is boarding at gate D18."
	case "TR987":
		return "Flight TR987 is delayed by 5 minutes and is estimated to depart at 19:05 from gate G11."
	}
	if record.State == "cancelled" {
		return fmt.Sprintf("Flight %s is currently cancelled.", record.RecordID)
	}
	if strings.Contains(strings.ToLower(userContent), "gate") && record.Location != "" {
		return fmt.Sprintf("Flight %s is %s at gate %s.", record.RecordID, record.State, record.Location)
	}
	return fmt.Sprintf(
		"Flight %s is %s and is estimated to depart at %s from gate %s.",
		record.RecordID,
		record.State,
		record.Updated,
		record.Location,
	)
}

func newTravelTools(travelLookupToolDescription string) []tool.Tool {
	statusTool := function.NewFunctionTool(
		getFlightStatus,
		function.WithName("lookup_record"),
		function.WithDescription(travelLookupToolDescription),
	)
	return []tool.Tool{statusTool}
}

type flightStatusArgs struct {
	Query string `json:"query" jsonschema:"description=Record key to look up,required"`
}

type recordLookupResult struct {
	RecordID  string `json:"recordId" jsonschema:"description=Resolved record identifier"`
	State     string `json:"state" jsonschema:"description=Primary record state"`
	Minutes   int    `json:"minutes" jsonschema:"description=Relevant minute value"`
	Location  string `json:"location" jsonschema:"description=Relevant location code"`
	Scheduled string `json:"scheduled" jsonschema:"description=Scheduled local time"`
	Updated   string `json:"updated" jsonschema:"description=Updated local time"`
}

func getFlightStatus(_ context.Context, args flightStatusArgs) (recordLookupResult, error) {
	record, ok := recordByID(args.Query)
	if ok {
		return record, nil
	}
	return recordLookupResult{RecordID: args.Query, State: "unknown"}, nil
}

func recordByID(recordID string) (recordLookupResult, bool) {
	switch strings.ToUpper(recordID) {
	case "TR123":
		return recordLookupResult{RecordID: "TR123", State: "delayed", Minutes: 35, Location: "B12", Scheduled: "10:10", Updated: "10:45"}, true
	case "TR456":
		return recordLookupResult{RecordID: "TR456", State: "delayed", Minutes: 15, Location: "A07", Scheduled: "12:30", Updated: "12:45"}, true
	case "TR789":
		return recordLookupResult{RecordID: "TR789", State: "cancelled", Minutes: 0, Location: "", Scheduled: "18:00", Updated: ""}, true
	case "TR321":
		return recordLookupResult{RecordID: "TR321", State: "delayed", Minutes: 20, Location: "C03", Scheduled: "14:20", Updated: "14:40"}, true
	case "TR654":
		return recordLookupResult{RecordID: "TR654", State: "boarding", Minutes: 0, Location: "D18", Scheduled: "16:05", Updated: "16:05"}, true
	case "TR987":
		return recordLookupResult{RecordID: "TR987", State: "delayed", Minutes: 5, Location: "G11", Scheduled: "19:00", Updated: "19:05"}, true
	default:
		return recordLookupResult{}, false
	}
}

type fakeBackwarder struct {
	targetSurfaceID string
	callCount       int
}

func (b *fakeBackwarder) Backward(_ context.Context, request *backwarder.Request) (*backwarder.Result, error) {
	b.callCount++
	if request == nil || len(request.AllowedGradientSurfaceIDs) == 0 {
		return &backwarder.Result{Gradients: []promptiter.SurfaceGradient{}, Upstream: []backwarder.Propagation{}}, nil
	}
	if !containsString(request.AllowedGradientSurfaceIDs, b.targetSurfaceID) {
		return &backwarder.Result{Gradients: []promptiter.SurfaceGradient{}, Upstream: []backwarder.Propagation{}}, nil
	}
	return &backwarder.Result{
		Gradients: []promptiter.SurfaceGradient{{
			SurfaceID: b.targetSurfaceID,
			Severity:  promptiter.LossSeverityP1,
			Gradient:  "Tool declaration does not describe flight status lookup.",
		}},
		Upstream: []backwarder.Propagation{},
	}, nil
}

type fakeAggregator struct {
	callCount int
}

func (a *fakeAggregator) Aggregate(_ context.Context, request *aggregator.Request) (*aggregator.Result, error) {
	a.callCount++
	if request == nil {
		return nil, fmt.Errorf("request is nil")
	}
	if request.SurfaceID == "" {
		return nil, fmt.Errorf("surface id is empty")
	}
	if request.NodeID == "" {
		return nil, fmt.Errorf("node id is empty")
	}
	if request.Type != astructure.SurfaceTypeTool {
		return nil, fmt.Errorf("surface type %q is not supported", request.Type)
	}
	if len(request.Gradients) == 0 {
		return nil, fmt.Errorf("gradients are empty")
	}
	seen := map[string]promptiter.SurfaceGradient{}
	for _, gradient := range request.Gradients {
		if gradient.SurfaceID != request.SurfaceID || strings.TrimSpace(gradient.Gradient) == "" {
			continue
		}
		seen[gradient.Gradient] = gradient
	}
	merged := make([]promptiter.SurfaceGradient, 0, len(seen))
	for _, gradient := range seen {
		merged = append(merged, gradient)
	}
	sort.SliceStable(merged, func(i, j int) bool {
		return merged[i].Gradient < merged[j].Gradient
	})
	if len(merged) == 0 {
		return nil, fmt.Errorf("aggregated gradient is empty")
	}
	return &aggregator.Result{
		Gradient: &promptiter.AggregatedSurfaceGradient{
			SurfaceID: request.SurfaceID,
			NodeID:    request.NodeID,
			Type:      request.Type,
			Gradients: merged,
		},
	}, nil
}

type fakeOptimizer struct {
	callCount int
}

func (o *fakeOptimizer) Optimize(_ context.Context, request *optimizer.Request) (*optimizer.Result, error) {
	o.callCount++
	if request == nil {
		return nil, fmt.Errorf("request is nil")
	}
	if request.Surface == nil {
		return nil, fmt.Errorf("surface is nil")
	}
	if request.Gradient == nil {
		return nil, fmt.Errorf("aggregated gradient is nil")
	}
	if len(request.Surface.Value.Tools) != 1 {
		return nil, fmt.Errorf("expected one tool ref, got %d", len(request.Surface.Value.Tools))
	}
	toolRef := request.Surface.Value.Tools[0]
	description := partialLookupDescription
	reason := partialToolPatchReason
	if o.callCount >= 2 {
		description = overfitLookupDescription
		reason = overfitToolPatchReason
	}
	return &optimizer.Result{
		Patch: &promptiter.SurfacePatch{
			SurfaceID: request.Surface.SurfaceID,
			Value: astructure.SurfaceValue{
				Tools: []astructure.ToolRef{{
					ID:          toolRef.ID,
					Description: description,
				}},
			},
			Reason: reason,
		},
	}, nil
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
