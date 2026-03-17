//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
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
	"net"
	"os"
	"strings"

	"go.uber.org/zap"
	a2alog "trpc.group/trpc-go/trpc-a2a-go/log"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/graph"
	agentlog "trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
)

type exampleRunResult struct {
	completionEvent *event.Event
	remoteTrace     []remoteTraceEntry
}

type remoteTraceEntry struct {
	phase    string
	nodeID   string
	nodeType string
}

func setupLogging() {
	config := zap.NewProductionConfig()
	config.Level = zap.NewAtomicLevelAt(zap.ErrorLevel)
	logger, _ := config.Build()
	a2alog.Default = logger.Sugar()
	agentlog.Default = logger.Sugar()
}

func getEnvOrDefault(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

func resolveHost(raw string) (string, error) {
	if strings.TrimSpace(raw) != "" {
		return strings.TrimSpace(raw), nil
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", fmt.Errorf("allocate tcp port: %w", err)
	}
	defer listener.Close()

	return listener.Addr().String(), nil
}

func buildOpenAIOptions(baseURL, apiKey string) []openai.Option {
	var opts []openai.Option
	if strings.TrimSpace(baseURL) != "" {
		opts = append(opts, openai.WithBaseURL(baseURL))
	}
	if strings.TrimSpace(apiKey) != "" {
		opts = append(opts, openai.WithAPIKey(apiKey))
	}
	return opts
}

func runOnce(
	ctx context.Context,
	agt agent.Agent,
	userInput string,
) (*exampleRunResult, error) {
	invocation := agent.NewInvocation(
		agent.WithInvocationAgent(agt),
		agent.WithInvocationMessage(model.NewUserMessage(userInput)),
	)

	eventCh, err := agt.Run(ctx, invocation)
	if err != nil {
		return nil, err
	}

	var completionEvent *event.Event
	var fallbackCompletionEvent *event.Event
	var lastErr error
	var remoteTrace []remoteTraceEntry
	for ev := range eventCh {
		if ev == nil {
			continue
		}
		if traceEntry, ok := buildRemoteGraphTraceEntry(ev); ok {
			remoteTrace = append(remoteTrace, traceEntry)
		}
		if *verboseEvents {
			fmt.Printf(
				"event author=%s object=%s done=%v partial=%v keys=%s\n",
				ev.Author,
				ev.Object,
				ev.Done,
				ev.IsPartial,
				formatStateKeys(ev.StateDelta),
			)
		}
		if ev.IsError() {
			if ev.Error != nil && ev.Error.Message != "" {
				lastErr = fmt.Errorf("agent returned error event: %s", ev.Error.Message)
			} else {
				lastErr = fmt.Errorf("agent returned error event with object %q", ev.Object)
			}
		}
		if isGraphCompletionEvent(ev) {
			if isParentGraphCompletion(ev) {
				completionEvent = ev
				continue
			}
			fallbackCompletionEvent = ev
		}
	}

	if completionEvent == nil {
		if lastErr != nil {
			return nil, lastErr
		}
	}
	if completionEvent == nil && fallbackCompletionEvent != nil {
		completionEvent = fallbackCompletionEvent
	}
	if completionEvent == nil {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("run context ended before graph completion: %w", err)
		}
		return nil, fmt.Errorf("no graph completion event received")
	}
	return &exampleRunResult{
		completionEvent: completionEvent,
		remoteTrace:     remoteTrace,
	}, nil
}

func isGraphCompletionEvent(ev *event.Event) bool {
	if ev == nil || !ev.Done {
		return false
	}
	if ev.Object == graph.ObjectTypeGraphExecution {
		return true
	}
	return ev.Response != nil && ev.Response.Object == graph.ObjectTypeGraphExecution
}

func isParentGraphCompletion(ev *event.Event) bool {
	if ev == nil {
		return false
	}
	if ev.Author == graph.AuthorGraphExecutor {
		return true
	}
	return hasStateKey(ev.StateDelta, parentStateKeyValue) ||
		hasStateKey(ev.StateDelta, parentStateKeyPayload) ||
		hasStateKey(ev.StateDelta, parentStateKeyEcho)
}

func buildRemoteGraphTraceEntry(ev *event.Event) (remoteTraceEntry, bool) {
	if ev == nil {
		return remoteTraceEntry{}, false
	}

	meta, ok := decodeNodeExecutionMetadata(ev.StateDelta)
	if !ok || !isRemoteGraphNode(meta.NodeID) {
		return remoteTraceEntry{}, false
	}

	switch ev.Object {
	case graph.ObjectTypeGraphNodeStart:
		return remoteTraceEntry{
			phase:    "START",
			nodeID:   meta.NodeID,
			nodeType: meta.NodeType.String(),
		}, true
	case graph.ObjectTypeGraphNodeComplete:
		return remoteTraceEntry{
			phase:    "END",
			nodeID:   meta.NodeID,
			nodeType: meta.NodeType.String(),
		}, true
	case graph.ObjectTypeGraphNodeError:
		return remoteTraceEntry{
			phase:    "ERROR",
			nodeID:   meta.NodeID,
			nodeType: meta.NodeType.String(),
		}, true
	default:
		return remoteTraceEntry{}, false
	}
}

func formatRemoteTrace(entries []remoteTraceEntry) string {
	if len(entries) == 0 {
		return "  (none)"
	}

	phaseWidth := len("PHASE")
	nodeWidth := len("NODE")
	typeWidth := len("TYPE")
	for _, entry := range entries {
		if len(entry.phase) > phaseWidth {
			phaseWidth = len(entry.phase)
		}
		if len(entry.nodeID) > nodeWidth {
			nodeWidth = len(entry.nodeID)
		}
		if len(entry.nodeType) > typeWidth {
			typeWidth = len(entry.nodeType)
		}
	}

	var lines []string
	lines = append(lines, fmt.Sprintf(
		"  %-*s  %-*s  %-*s",
		phaseWidth, "PHASE",
		nodeWidth, "NODE",
		typeWidth, "TYPE",
	))
	lines = append(lines, fmt.Sprintf(
		"  %-*s  %-*s  %-*s",
		phaseWidth, strings.Repeat("-", phaseWidth),
		nodeWidth, strings.Repeat("-", nodeWidth),
		typeWidth, strings.Repeat("-", typeWidth),
	))
	for _, entry := range entries {
		lines = append(lines, fmt.Sprintf(
			"  %-*s  %-*s  %-*s",
			phaseWidth, entry.phase,
			nodeWidth, entry.nodeID,
			typeWidth, entry.nodeType,
		))
	}
	return strings.Join(lines, "\n")
}

func decodeNodeExecutionMetadata(stateDelta map[string][]byte) (*graph.NodeExecutionMetadata, bool) {
	if len(stateDelta) == 0 {
		return nil, false
	}
	raw, ok := stateDelta[graph.MetadataKeyNode]
	if !ok || len(raw) == 0 {
		return nil, false
	}

	var meta graph.NodeExecutionMetadata
	if err := json.Unmarshal(raw, &meta); err != nil {
		return nil, false
	}
	if meta.NodeID == "" {
		return nil, false
	}
	return &meta, true
}

func isRemoteGraphNode(nodeID string) bool {
	switch nodeID {
	case remoteNodeInput, remoteNodeModel, remoteNodeCapture:
		return true
	default:
		return false
	}
}

func finalResponseText(ev *event.Event) string {
	if ev == nil || ev.Response == nil || len(ev.Response.Choices) == 0 {
		return ""
	}
	return ev.Response.Choices[0].Message.Content
}

func decodeJSONString(stateDelta map[string][]byte, key string) (string, error) {
	raw, ok := stateDelta[key]
	if !ok {
		return "", fmt.Errorf("missing state key %q; available keys: %s", key, formatStateKeys(stateDelta))
	}

	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", fmt.Errorf("decode state key %q: %w", key, err)
	}
	return value, nil
}

func decodeJSONBool(stateDelta map[string][]byte, key string) (bool, error) {
	raw, ok := stateDelta[key]
	if !ok {
		return false, fmt.Errorf("missing state key %q; available keys: %s", key, formatStateKeys(stateDelta))
	}

	var value bool
	if err := json.Unmarshal(raw, &value); err != nil {
		return false, fmt.Errorf("decode state key %q: %w", key, err)
	}
	return value, nil
}

func decodeJSONMap(stateDelta map[string][]byte, key string) (map[string]any, error) {
	raw, ok := stateDelta[key]
	if !ok {
		return nil, fmt.Errorf("missing state key %q; available keys: %s", key, formatStateKeys(stateDelta))
	}

	var value map[string]any
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, fmt.Errorf("decode state key %q: %w", key, err)
	}
	return value, nil
}

func decodeStateMap(state graph.State, key string) map[string]any {
	if state == nil {
		return nil
	}
	if value, ok := state[key].(map[string]any); ok {
		return value
	}
	return nil
}

func decodeRawString(rawState map[string][]byte, key string) string {
	if len(rawState) == 0 {
		return ""
	}
	raw, ok := rawState[key]
	if !ok {
		return ""
	}

	var decoded string
	if err := json.Unmarshal(raw, &decoded); err == nil {
		return decoded
	}
	return string(raw)
}

func decodeRawMap(rawState map[string][]byte, key string) map[string]any {
	if len(rawState) == 0 {
		return nil
	}
	raw, ok := rawState[key]
	if !ok {
		return nil
	}

	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return nil
	}
	return decoded
}

func formatStateKeys(stateDelta map[string][]byte) string {
	if len(stateDelta) == 0 {
		return "(none)"
	}
	keys := make([]string, 0, len(stateDelta))
	for key := range stateDelta {
		keys = append(keys, key)
	}
	return strings.Join(keys, ", ")
}

func hasStateKey(stateDelta map[string][]byte, key string) bool {
	if len(stateDelta) == 0 {
		return false
	}
	_, ok := stateDelta[key]
	return ok
}

func mapKeys(state graph.State) []string {
	if len(state) == 0 {
		return nil
	}
	keys := make([]string, 0, len(state))
	for key := range state {
		keys = append(keys, key)
	}
	return keys
}

func prettyJSON(value any) string {
	bytes, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Sprintf("%v", value)
	}
	return string(bytes)
}

func intPtr(v int) *int {
	return &v
}

func floatPtr(v float64) *float64 {
	return &v
}
