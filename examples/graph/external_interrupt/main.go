//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main demonstrates external graph interrupts ("pause button") and
// resumable checkpoints.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"reflect"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/graph"
	"trpc.group/trpc-go/trpc-agent-go/graph/checkpoint/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
)

const (
	defaultModelName = "deepseek-chat"
	defaultDemoMode  = "both"
	defaultUserText  = "Say one short sentence about graphs."
	defaultEngine    = "bsp"

	envOpenAIAPIKey = "OPENAI_API_KEY"
	envModelName    = "MODEL_NAME"
)

const (
	demoPlanned = "planned"
	demoForced  = "forced"
	demoBoth    = "both"
)

const (
	engineBSP = "bsp"
	engineDAG = "dag"
)

const (
	nodePrepare   = "prepare"
	nodeCallModel = "call_model"
	nodeFinalize  = "finalize"

	nodeSlow = "slow"
	nodeDone = "done"
)

const (
	stateKeyResult = "result"
	stateKeySlowOK = "slow_ok"
)

const (
	waitStartedTimeout  = 2 * time.Second
	prepareSleep        = 300 * time.Millisecond
	slowWorkDuration    = 300 * time.Millisecond
	forcedInterruptWait = 50 * time.Millisecond
)

var (
	demoMode = flag.String(
		"demo",
		defaultDemoMode,
		"Demo to run: planned|forced|both",
	)
	engine = flag.String(
		"engine",
		defaultEngine,
		"Execution engine: bsp|dag",
	)
	modelName = flag.String(
		"model",
		defaultModelFromEnv(),
		"Model name used by planned demo",
	)
	userText = flag.String(
		"text",
		defaultUserText,
		"User text for the planned demo",
	)
)

type interruptMeta struct {
	NodeID         string          `json:"nodeID,omitempty"`
	InterruptKey   string          `json:"interruptKey,omitempty"`
	LineageID      string          `json:"lineageId,omitempty"`
	CheckpointID   string          `json:"checkpointId,omitempty"`
	InterruptValue json.RawMessage `json:"interruptValue,omitempty"`
}

func main() {
	flag.Parse()

	mode := strings.ToLower(strings.TrimSpace(*demoMode))
	if mode == "" {
		mode = defaultDemoMode
	}

	switch mode {
	case demoPlanned:
		runPlannedDemo()
	case demoForced:
		runForcedDemo()
	case demoBoth:
		runPlannedDemo()
		runForcedDemo()
	default:
		log.Fatalf("unknown -demo value: %q", *demoMode)
	}
}

func runPlannedDemo() {
	fmt.Println(strings.Repeat("=", 60))
	fmt.Println("Planned external interrupt demo")

	started := make(chan struct{}, 1)
	g, err := buildPlannedGraph(started, *modelName, *userText)
	if err != nil {
		log.Fatalf("build planned graph failed: %v", err)
	}

	saver := inmemory.NewSaver()
	exec, err := graph.NewExecutor(
		g,
		graph.WithExecutionEngine(parseEngineOrExit()),
		graph.WithCheckpointSaver(saver),
	)
	if err != nil {
		log.Fatalf("create executor failed: %v", err)
	}

	lineageID := fmt.Sprintf("external-planned-%d", time.Now().UnixNano())
	fmt.Printf("Lineage: %s\n", lineageID)
	fmt.Printf("Engine: %s\n", *engine)

	ctx, interrupt := graph.WithGraphInterrupt(context.Background())

	st := graph.State{graph.CfgKeyLineageID: lineageID}
	inv1 := newInvocation(lineageID)
	ch, err := exec.Execute(ctx, st, inv1)
	if err != nil {
		log.Fatalf("start run failed: %v", err)
	}

	waitForStartedOrExit(started)
	interrupt()

	meta, done, err := drainEvents(ch)
	if err != nil {
		log.Fatalf("drain events failed: %v", err)
	}
	if done != nil {
		log.Fatalf("expected interrupt, got completion")
	}
	if meta == nil || meta.CheckpointID == "" {
		log.Fatalf("missing interrupt checkpoint ID")
	}

	payload, ok := decodeExternalPayload(meta)
	if !ok {
		log.Fatalf("missing external interrupt payload")
	}
	fmt.Printf("Paused: key=%s forced=%v checkpoint=%s\n",
		payload.Key,
		payload.Forced,
		meta.CheckpointID,
	)

	resumeState := graph.State{
		graph.CfgKeyLineageID:    lineageID,
		graph.CfgKeyCheckpointID: meta.CheckpointID,
	}
	inv2 := newInvocation(lineageID)
	ch2, err := exec.Execute(context.Background(), resumeState, inv2)
	if err != nil {
		log.Fatalf("resume run failed: %v", err)
	}

	_, done2, err := drainEvents(ch2)
	if err != nil {
		log.Fatalf("resume drain failed: %v", err)
	}
	if done2 == nil {
		log.Fatalf("expected completion after resume")
	}

	result, err := decodeStringState(done2, stateKeyResult)
	if err != nil {
		log.Fatalf("decode result failed: %v", err)
	}
	fmt.Printf("Completed: %s\n", shorten(result, 80))
}

func runForcedDemo() {
	fmt.Println(strings.Repeat("=", 60))
	fmt.Println("Forced external interrupt demo (timeout)")

	started := make(chan struct{}, 1)
	g, err := buildForcedGraph(started)
	if err != nil {
		log.Fatalf("build forced graph failed: %v", err)
	}

	saver := inmemory.NewSaver()
	exec, err := graph.NewExecutor(
		g,
		graph.WithExecutionEngine(parseEngineOrExit()),
		graph.WithCheckpointSaver(saver),
	)
	if err != nil {
		log.Fatalf("create executor failed: %v", err)
	}

	lineageID := fmt.Sprintf("external-forced-%d", time.Now().UnixNano())
	fmt.Printf("Lineage: %s\n", lineageID)
	fmt.Printf("Engine: %s\n", *engine)

	ctx, interrupt := graph.WithGraphInterrupt(context.Background())

	st := graph.State{graph.CfgKeyLineageID: lineageID}
	inv1 := newInvocation(lineageID)
	ch, err := exec.Execute(ctx, st, inv1)
	if err != nil {
		log.Fatalf("start run failed: %v", err)
	}

	waitForStartedOrExit(started)
	interrupt(graph.WithGraphInterruptTimeout(forcedInterruptWait))

	meta, done, err := drainEvents(ch)
	if err != nil {
		log.Fatalf("drain events failed: %v", err)
	}
	if done != nil {
		log.Fatalf("expected forced interrupt, got completion")
	}
	if meta == nil || meta.CheckpointID == "" {
		log.Fatalf("missing interrupt checkpoint ID")
	}

	payload, ok := decodeExternalPayload(meta)
	if !ok {
		log.Fatalf("missing external interrupt payload")
	}
	fmt.Printf("Paused: key=%s forced=%v checkpoint=%s\n",
		payload.Key,
		payload.Forced,
		meta.CheckpointID,
	)
	if !payload.Forced {
		log.Fatalf("expected forced=true")
	}

	resumeState := graph.State{
		graph.CfgKeyLineageID:    lineageID,
		graph.CfgKeyCheckpointID: meta.CheckpointID,
	}
	inv2 := newInvocation(lineageID)
	ch2, err := exec.Execute(context.Background(), resumeState, inv2)
	if err != nil {
		log.Fatalf("resume run failed: %v", err)
	}

	_, done2, err := drainEvents(ch2)
	if err != nil {
		log.Fatalf("resume drain failed: %v", err)
	}
	if done2 == nil {
		log.Fatalf("expected completion after resume")
	}

	_, err = decodeBoolState(done2, stateKeySlowOK)
	if err != nil {
		log.Fatalf("decode slow ok failed: %v", err)
	}
	fmt.Println("Completed after resume")
}

func defaultModelFromEnv() string {
	if name := strings.TrimSpace(os.Getenv(envModelName)); name != "" {
		return name
	}
	return defaultModelName
}

func buildPlannedGraph(
	started chan<- struct{},
	modelName string,
	userText string,
) (*graph.Graph, error) {
	schema := graph.MessagesStateSchema()
	sg := graph.NewStateGraph(schema)

	prepareNode := func(ctx context.Context, st graph.State) (any, error) {
		select {
		case started <- struct{}{}:
		default:
		}
		time.Sleep(prepareSleep)

		msgs := []model.Message{
			model.NewUserMessage(userText),
		}
		return graph.State{graph.StateKeyOneShotMessages: msgs}, nil
	}
	sg.AddNode(nodePrepare, prepareNode)

	if os.Getenv(envOpenAIAPIKey) != "" {
		sg.AddLLMNode(
			nodeCallModel,
			openai.New(modelName),
			modelInstruction(),
			nil,
			graph.WithGenerationConfig(generationConfig()),
		)
	} else {
		fmt.Println("OPENAI_API_KEY is not set, using a local stub model node")
		stubNode := func(ctx context.Context, st graph.State) (any, error) {
			const stub = "stub model response"
			return graph.State{graph.StateKeyLastResponse: stub}, nil
		}
		sg.AddNode(nodeCallModel, stubNode)
	}

	finalizeNode := func(ctx context.Context, st graph.State) (any, error) {
		last := assistantTextFromState(st)
		if last == "" {
			return nil, errors.New("missing model output")
		}
		return graph.State{stateKeyResult: last}, nil
	}
	sg.AddNode(nodeFinalize, finalizeNode)

	sg.SetEntryPoint(nodePrepare)
	sg.AddEdge(nodePrepare, nodeCallModel)
	sg.AddEdge(nodeCallModel, nodeFinalize)
	sg.SetFinishPoint(nodeFinalize)

	return sg.Compile()
}

func modelInstruction() string {
	return strings.Join([]string{
		"You are a helpful assistant.",
		"Reply with one short sentence.",
	}, "\n")
}

func generationConfig() model.GenerationConfig {
	return model.GenerationConfig{
		Stream: false,
	}
}

func buildForcedGraph(started chan<- struct{}) (*graph.Graph, error) {
	schema := graph.NewStateSchema()
	schema.AddField(stateKeySlowOK, graph.StateField{
		Type:    reflect.TypeOf(false),
		Reducer: graph.DefaultReducer,
		Default: func() any { return false },
	})

	sg := graph.NewStateGraph(schema)

	sg.AddNode(nodeSlow, func(ctx context.Context, st graph.State) (any, error) {
		select {
		case started <- struct{}{}:
		default:
		}
		if err := waitOrCancel(ctx, slowWorkDuration); err != nil {
			return nil, err
		}
		return graph.State{stateKeySlowOK: true}, nil
	})

	sg.AddNode(nodeDone, func(ctx context.Context, st graph.State) (any, error) {
		return graph.State{stateKeyResult: "done"}, nil
	})

	sg.SetEntryPoint(nodeSlow)
	sg.AddEdge(nodeSlow, nodeDone)
	sg.SetFinishPoint(nodeDone)
	return sg.Compile()
}

func waitOrCancel(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func newInvocation(lineageID string) *agent.Invocation {
	return &agent.Invocation{
		InvocationID: fmt.Sprintf("%s-%d", lineageID, time.Now().UnixNano()),
	}
}

func waitForStartedOrExit(started <-chan struct{}) {
	select {
	case <-started:
	case <-time.After(waitStartedTimeout):
		log.Fatalf("timeout waiting for node to start")
	}
}

func drainEvents(
	ch <-chan *event.Event,
) (*interruptMeta, *event.Event, error) {
	if ch == nil {
		return nil, nil, errors.New("nil event channel")
	}

	var (
		meta *interruptMeta
		done *event.Event
		msg  string
	)
	for evt := range ch {
		if evt == nil {
			continue
		}
		if msg == "" && evt.Response != nil && evt.Response.Error != nil {
			msg = evt.Response.Error.Message
		}
		if evt.Done {
			done = evt
		}
		if m := extractInterruptMeta(evt); m != nil {
			meta = m
		}
	}

	if meta != nil && done != nil {
		return meta, nil, nil
	}
	if msg != "" {
		return meta, done, errors.New(msg)
	}
	return meta, done, nil
}

func extractInterruptMeta(evt *event.Event) *interruptMeta {
	if evt == nil || evt.Object != graph.ObjectTypeGraphPregelStep {
		return nil
	}
	if evt.StateDelta == nil {
		return nil
	}
	raw, ok := evt.StateDelta[graph.MetadataKeyPregel]
	if !ok {
		return nil
	}

	var meta interruptMeta
	if err := json.Unmarshal(raw, &meta); err != nil {
		return nil
	}
	if meta.InterruptKey == "" || meta.CheckpointID == "" {
		return nil
	}
	return &meta
}

func decodeExternalPayload(
	meta *interruptMeta,
) (graph.ExternalInterruptPayload, bool) {
	if meta == nil || len(meta.InterruptValue) == 0 {
		return graph.ExternalInterruptPayload{}, false
	}
	var payload graph.ExternalInterruptPayload
	if err := json.Unmarshal(meta.InterruptValue, &payload); err != nil {
		return graph.ExternalInterruptPayload{}, false
	}
	return payload, payload.Key != ""
}

func decodeStringState(done *event.Event, key string) (string, error) {
	if done == nil || done.StateDelta == nil {
		return "", errors.New("missing done state delta")
	}
	raw, ok := done.StateDelta[key]
	if !ok {
		return "", fmt.Errorf("missing %q in state delta", key)
	}
	var out string
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", fmt.Errorf("decode %q: %w", key, err)
	}
	return out, nil
}

func decodeBoolState(done *event.Event, key string) (bool, error) {
	if done == nil || done.StateDelta == nil {
		return false, errors.New("missing done state delta")
	}
	raw, ok := done.StateDelta[key]
	if !ok {
		return false, fmt.Errorf("missing %q in state delta", key)
	}
	var out bool
	if err := json.Unmarshal(raw, &out); err != nil {
		return false, fmt.Errorf("decode %q: %w", key, err)
	}
	return out, nil
}

func assistantTextFromState(st graph.State) string {
	if st == nil {
		return ""
	}
	if v, ok := st[graph.StateKeyLastResponse].(string); ok {
		if text := strings.TrimSpace(v); text != "" {
			return text
		}
	}

	msgs, ok := st[graph.StateKeyMessages].([]model.Message)
	if !ok || len(msgs) == 0 {
		return ""
	}

	for i := len(msgs) - 1; i >= 0; i-- {
		msg := msgs[i]
		if msg.Role != model.RoleAssistant {
			continue
		}
		if text := strings.TrimSpace(msg.Content); text != "" {
			return text
		}
		if text := textFromParts(msg.ContentParts); text != "" {
			return text
		}
	}
	return ""
}

func textFromParts(parts []model.ContentPart) string {
	if len(parts) == 0 {
		return ""
	}

	var builder strings.Builder
	for _, part := range parts {
		if part.Type != model.ContentTypeText || part.Text == nil {
			continue
		}
		if builder.Len() > 0 {
			builder.WriteByte('\n')
		}
		builder.WriteString(*part.Text)
	}
	return strings.TrimSpace(builder.String())
}

func shorten(s string, max int) string {
	s = strings.TrimSpace(s)
	if max <= 0 || len(s) <= max {
		return s
	}
	return s[:max] + "..."
}

func parseEngineOrExit() graph.ExecutionEngine {
	name := strings.ToLower(strings.TrimSpace(*engine))
	switch name {
	case engineBSP:
		return graph.ExecutionEngineBSP
	case engineDAG:
		return graph.ExecutionEngineDAG
	default:
		log.Fatalf("unknown -engine value: %q", *engine)
		return graph.ExecutionEngineBSP
	}
}
