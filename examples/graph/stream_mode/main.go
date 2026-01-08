//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main demonstrates Runner StreamMode (LangGraph-style) filtering for
// graph workflows.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/graphagent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/graph"
	checkpointinmemory "trpc.group/trpc-go/trpc-agent-go/graph/checkpoint/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	sessioninmemory "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

const (
	appName        = "stream-mode-demo"
	graphAgentName = "stream-mode-agent"

	userID    = "user"
	sessionID = "session"

	nodeEmitCustom = "emit_custom"
	nodeAsk        = "ask"

	customEventType = "demo.custom"
	payloadKeyInput = "input"

	flagInput      = "input"
	flagStreamMode = "stream-mode"
	flagDelay      = "delay"

	defaultInputText = "Explain StreamMode in one sentence."

	streamModeAll = "all"

	defaultChunkSize = 8
)

var (
	inputText = flag.String(
		flagInput,
		defaultInputText,
		"User input text",
	)
	streamMode = flag.String(
		flagStreamMode,
		streamModeAll,
		"all or comma-separated: messages,updates,checkpoints,tasks,debug,custom",
	)
	delayPerChunk = flag.Duration(
		flagDelay,
		20*time.Millisecond,
		"Delay per chunk in the toy streaming model",
	)
)

func main() {
	flag.Parse()

	ctx := context.Background()

	mdl := newToyModel(*delayPerChunk, defaultChunkSize)
	g, err := buildGraph(mdl)
	if err != nil {
		log.Fatalf("build graph failed: %v", err)
	}

	sess := sessioninmemory.NewSessionService()
	ga, err := graphagent.New(
		graphAgentName,
		g,
		graphagent.WithCheckpointSaver(checkpointinmemory.NewSaver()),
	)
	if err != nil {
		log.Fatalf("create graph agent failed: %v", err)
	}

	r := runner.NewRunner(
		appName,
		ga,
		runner.WithSessionService(sess),
	)
	defer r.Close()

	runOpts, err := runOptionsFromStreamModeFlag(*streamMode)
	if err != nil {
		log.Fatalf("invalid %s: %v", flagStreamMode, err)
	}

	eventCh, err := r.Run(
		ctx,
		userID,
		sessionID,
		model.NewUserMessage(*inputText),
		runOpts...,
	)
	if err != nil {
		log.Fatalf("runner run failed: %v", err)
	}

	counts := make(map[string]int)
	for e := range eventCh {
		printEvent(e)
		if e != nil {
			counts[e.Object]++
		}
	}
	printCounts(counts)
}

func buildGraph(mdl model.Model) (*graph.Graph, error) {
	schema := graph.MessagesStateSchema()
	sg := graph.NewStateGraph(schema)
	sg.AddNode(nodeEmitCustom, emitCustomNode)
	sg.AddLLMNode(nodeAsk, mdl, llmInstruction, nil)
	sg.SetEntryPoint(nodeEmitCustom)
	sg.AddEdge(nodeEmitCustom, nodeAsk)
	sg.SetFinishPoint(nodeAsk)
	return sg.Compile()
}

func emitCustomNode(ctx context.Context, state graph.State) (any, error) {
	input, _ := state[graph.StateKeyUserInput].(string)
	payload := map[string]any{
		payloadKeyInput: input,
	}
	_ = graph.GetEventEmitter(state).EmitCustom(customEventType, payload)
	return graph.State{}, nil
}

func runOptionsFromStreamModeFlag(raw string) ([]agent.RunOption, error) {
	modes, err := parseStreamModes(raw)
	if err != nil {
		return nil, err
	}
	if len(modes) == 0 {
		return nil, nil
	}
	return []agent.RunOption{agent.WithStreamMode(modes...)}, nil
}

func parseStreamModes(raw string) ([]agent.StreamMode, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == streamModeAll {
		return nil, nil
	}

	parts := strings.Split(raw, ",")
	modes := make([]agent.StreamMode, 0, len(parts))
	for _, part := range parts {
		mode, err := parseStreamMode(strings.TrimSpace(part))
		if err != nil {
			return nil, err
		}
		modes = append(modes, mode)
	}
	return modes, nil
}

func parseStreamMode(raw string) (agent.StreamMode, error) {
	switch raw {
	case string(agent.StreamModeMessages):
		return agent.StreamModeMessages, nil
	case string(agent.StreamModeUpdates):
		return agent.StreamModeUpdates, nil
	case string(agent.StreamModeCheckpoints):
		return agent.StreamModeCheckpoints, nil
	case string(agent.StreamModeTasks):
		return agent.StreamModeTasks, nil
	case string(agent.StreamModeDebug):
		return agent.StreamModeDebug, nil
	case string(agent.StreamModeCustom):
		return agent.StreamModeCustom, nil
	default:
		return "", fmt.Errorf("unknown stream mode %q", raw)
	}
}

func printEvent(e *event.Event) {
	if e == nil {
		return
	}

	switch e.Object {
	case model.ObjectTypeChatCompletionChunk:
		delta := firstDeltaContent(e)
		if delta == "" {
			return
		}
		fmt.Printf("[%s] %s\n", e.Object, delta)
	case model.ObjectTypeChatCompletion:
		msg := firstMessageContent(e)
		if msg == "" {
			return
		}
		fmt.Printf("[%s] %s\n", e.Object, msg)
	case model.ObjectTypeRunnerCompletion:
		fmt.Printf("[%s] %s\n", e.Object, runnerSummary(e))
	case graph.ObjectTypeGraphNodeCustom:
		fmt.Printf("[%s] %s\n", e.Object, nodeCustomSummary(e))
	case graph.ObjectTypeGraphCheckpointCreated,
		graph.ObjectTypeGraphCheckpointCommitted,
		graph.ObjectTypeGraphCheckpointInterrupt,
		graph.ObjectTypeGraphCheckpoint:
		fmt.Printf("[%s] %s\n", e.Object, checkpointSummary(e))
	default:
		fmt.Printf("[%s] author=%s\n", e.Object, e.Author)
	}
}

func firstDeltaContent(e *event.Event) string {
	if e == nil || e.Response == nil || len(e.Choices) == 0 {
		return ""
	}
	return e.Choices[0].Delta.Content
}

func firstMessageContent(e *event.Event) string {
	if e == nil || e.Response == nil || len(e.Choices) == 0 {
		return ""
	}
	return e.Choices[0].Message.Content
}

func nodeCustomSummary(e *event.Event) string {
	if e == nil || e.StateDelta == nil {
		return ""
	}
	raw, ok := e.StateDelta[graph.MetadataKeyNodeCustom]
	if !ok {
		return ""
	}
	var md graph.NodeCustomEventMetadata
	if err := json.Unmarshal(raw, &md); err != nil {
		return ""
	}
	if md.EventType == "" {
		return ""
	}
	if md.Message != "" {
		return fmt.Sprintf("type=%s message=%s", md.EventType, md.Message)
	}
	return fmt.Sprintf("type=%s", md.EventType)
}

func checkpointSummary(e *event.Event) string {
	if e == nil || e.StateDelta == nil {
		return ""
	}
	raw, ok := e.StateDelta[graph.MetadataKeyCheckpoint]
	if !ok {
		return ""
	}
	var md map[string]any
	if err := json.Unmarshal(raw, &md); err != nil {
		return ""
	}
	checkpointID, _ := md[graph.CfgKeyCheckpointID].(string)
	source, _ := md[graph.EventKeySource].(string)
	step := intFromJSON(md[graph.EventKeyStep])
	return fmt.Sprintf(
		"id=%s source=%s step=%d",
		checkpointID,
		source,
		step,
	)
}

func runnerSummary(e *event.Event) string {
	if e == nil || e.StateDelta == nil {
		return ""
	}
	raw, ok := e.StateDelta[graph.StateKeyLastResponse]
	if !ok {
		return ""
	}
	var out string
	if err := json.Unmarshal(raw, &out); err != nil {
		return ""
	}
	return "final=" + out
}

func intFromJSON(v any) int {
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case float64:
		return int(n)
	default:
		return 0
	}
}

func printCounts(counts map[string]int) {
	if len(counts) == 0 {
		return
	}
	objects := make([]string, 0, len(counts))
	for obj := range counts {
		objects = append(objects, obj)
	}
	sort.Strings(objects)

	fmt.Println("\nEvent counts:")
	for _, obj := range objects {
		fmt.Printf("- %s: %d\n", obj, counts[obj])
	}
}

const llmInstruction = `You are a helpful assistant.
Answer the user in one concise sentence.`

type toyModel struct {
	delay     time.Duration
	chunkSize int
}

func newToyModel(delay time.Duration, chunkSize int) model.Model {
	if chunkSize <= 0 {
		chunkSize = defaultChunkSize
	}
	return &toyModel{
		delay:     delay,
		chunkSize: chunkSize,
	}
}

func (m *toyModel) Info() model.Info {
	return model.Info{Name: "toy-model"}
}

func (m *toyModel) GenerateContent(
	ctx context.Context,
	req *model.Request,
) (<-chan *model.Response, error) {
	const errNilRequest = "toy model: request is nil"
	if req == nil {
		return nil, fmt.Errorf(errNilRequest)
	}

	userText := lastUserText(req.Messages)
	answer := "Toy model reply: " + userText

	out := make(chan *model.Response, 8)
	go func() {
		defer close(out)

		if !req.Stream {
			out <- finalResponse(answer)
			return
		}

		for _, chunk := range splitRunes(answer, m.chunkSize) {
			if !sendChunk(ctx, out, chunk) {
				return
			}
			if m.delay > 0 {
				timer := time.NewTimer(m.delay)
				select {
				case <-ctx.Done():
					timer.Stop()
					return
				case <-timer.C:
				}
			}
		}
		sendFinal(ctx, out, answer)
	}()

	return out, nil
}

func lastUserText(messages []model.Message) string {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == model.RoleUser {
			return messages[i].Content
		}
	}
	return ""
}

func splitRunes(s string, chunkSize int) []string {
	if chunkSize <= 0 || s == "" {
		return []string{s}
	}

	runes := []rune(s)
	out := make([]string, 0, (len(runes)+chunkSize-1)/chunkSize)
	for len(runes) > 0 {
		n := chunkSize
		if n > len(runes) {
			n = len(runes)
		}
		out = append(out, string(runes[:n]))
		runes = runes[n:]
	}
	return out
}

func sendChunk(
	ctx context.Context,
	out chan<- *model.Response,
	content string,
) bool {
	if ctx.Err() != nil {
		return false
	}
	select {
	case <-ctx.Done():
		return false
	case out <- chunkResponse(content):
		return true
	}
}

func sendFinal(
	ctx context.Context,
	out chan<- *model.Response,
	content string,
) {
	if ctx.Err() != nil {
		return
	}
	select {
	case <-ctx.Done():
		return
	case out <- finalResponse(content):
	}
}

func chunkResponse(content string) *model.Response {
	const (
		responseID = "toy-response"
		modelName  = "toy-model"
	)
	return &model.Response{
		ID:        responseID,
		Object:    model.ObjectTypeChatCompletionChunk,
		Model:     modelName,
		Done:      false,
		IsPartial: true,
		Choices: []model.Choice{{
			Index: 0,
			Delta: model.Message{
				Role:    model.RoleAssistant,
				Content: content,
			},
		}},
	}
}

func finalResponse(content string) *model.Response {
	const (
		responseID = "toy-response"
		modelName  = "toy-model"
	)
	return &model.Response{
		ID:        responseID,
		Object:    model.ObjectTypeChatCompletion,
		Model:     modelName,
		Done:      true,
		IsPartial: false,
		Choices: []model.Choice{{
			Index: 0,
			Message: model.Message{
				Role:    model.RoleAssistant,
				Content: content,
			},
		}},
	}
}
