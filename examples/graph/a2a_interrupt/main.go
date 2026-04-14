//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package main demonstrates interrupt/resume across a parent graph and a
// remote graph reached through A2A.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"reflect"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/a2aagent"
	"trpc.group/trpc-go/trpc-agent-go/agent/graphagent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/graph"
	checkpointinmemory "trpc.group/trpc-go/trpc-agent-go/graph/checkpoint/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	a2aserver "trpc.group/trpc-go/trpc-agent-go/server/a2a"
)

const (
	defaultModelName = "deepseek-chat"

	remoteAgentName = "remote_interrupt_graph"
	parentAgentName = "parent_interrupt_graph"

	remoteNodeRiskSignals    = "remote_risk_signals"
	remoteNodeCaptureCase    = "remote_capture_case_brief"
	remoteNodePrepareVerdict = "remote_prepare_risk_verdict"
	remoteNodeRiskVerdict    = "remote_risk_verdict"
	remoteNodeAsk            = "remote_ask_approval"
	remoteNodeFinalize       = "remote_finalize"

	parentNodeIntake   = "parent_intake"
	parentNodeDecision = "parent_decision_draft"
	parentNodeFinalize = "parent_finalize"

	parentStateKeyCaseBrief     = "case_brief"
	parentStateKeyApproved      = "approved_from_remote"
	parentStateKeyRemoteSummary = "remote_summary"
	parentStateKeyDecisionDraft = "decision_draft"
	parentStateKeyFinalMessage  = "parent_final_message"

	remoteStateKeyCaseBrief    = "remote_case_brief"
	remoteStateKeyRiskSignals  = "remote_risk_signals"
	remoteStateKeyVerdictInput = "remote_risk_verdict_input"
	remoteStateKeyRiskVerdict  = "remote_risk_verdict"
	remoteStateKeyApproved     = "remote_approved"
	remoteStateKeySummary      = "remote_summary"

	defaultLineageID = "demo-a2a-interrupt"
	defaultParentNS  = "parent-a2a-interrupt"
	defaultRemoteNS  = "remote-a2a-interrupt"
	defaultTimeout   = 2 * time.Minute
	defaultInput     = "Payment request: transfer USD 250,000 to a new beneficiary in a high-risk region within 30 minutes."
	pollInterval     = 20 * time.Millisecond
	pollTimeout      = 5 * time.Second
)

var (
	modelName = flag.String("model", getEnvOrDefault("MODEL_NAME", defaultModelName), "OpenAI-compatible model name")
	host      = flag.String("host", "", "A2A host, for example 127.0.0.1:28883 (default: random local port)")
	streaming = flag.Bool("streaming", true, "Use A2A streaming between parent and remote graph")
	timeout   = flag.Duration("timeout", defaultTimeout, "Overall timeout")
)

func main() {
	flag.Parse()

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	if err := run(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	resolvedHost, err := resolveHost(*host)
	if err != nil {
		return err
	}
	modelInstance := openai.New(*modelName)
	remoteAgent, err := buildRemoteGraphAgent(modelInstance)
	if err != nil {
		return fmt.Errorf("build remote graph: %w", err)
	}

	server, err := a2aserver.New(
		a2aserver.WithAgent(remoteAgent, *streaming),
		a2aserver.WithHost(resolvedHost),
		a2aserver.WithGraphEventObjectAllowlist(
			graph.ObjectTypeGraphExecution,
			graph.ObjectTypeGraphNodeCustom,
			graph.ObjectTypeGraphPregelStep,
			graph.ObjectTypeGraphCheckpointInterrupt,
		),
		// Message streaming keeps graph metadata (state_delta)
		// on a direct Message event path, which is more robust for interrupt propagation.
		a2aserver.WithStreamingEventType(a2aserver.StreamingEventTypeMessage),
	)
	if err != nil {
		return fmt.Errorf("create a2a server: %w", err)
	}
	defer func() {
		_ = server.Stop(context.Background())
	}()

	serverErrCh := make(chan error, 1)
	go func() {
		if err := server.Start(resolvedHost); err != nil {
			serverErrCh <- err
		}
	}()

	cardURL := fmt.Sprintf("http://%s/.well-known/agent.json", resolvedHost)
	if err := waitForServer(ctx, cardURL, serverErrCh); err != nil {
		return err
	}

	subAgent, err := a2aagent.New(
		a2aagent.WithAgentCardURL(fmt.Sprintf("http://%s", resolvedHost)),
		a2aagent.WithName(remoteAgentName),
		a2aagent.WithEnableStreaming(*streaming),
		a2aagent.WithTransferStateKey("*"),
	)
	if err != nil {
		return fmt.Errorf("create a2a sub-agent: %w", err)
	}

	parentAgent, err := buildParentGraphAgent(subAgent, modelInstance)
	if err != nil {
		return fmt.Errorf("build parent graph: %w", err)
	}

	manager := parentAgent.Executor().CheckpointManager()
	if manager == nil {
		return errors.New("parent checkpoint manager is nil")
	}

	baseState := graph.State{
		graph.CfgKeyLineageID:    defaultLineageID,
		graph.CfgKeyCheckpointNS: defaultParentNS,
	}

	printSection("Scenario")
	printParagraph(
		"Parent graph delegates a high-risk transfer review to a remote graph over A2A.\n"+
			"The remote graph pauses at a manual approval gate, then the parent resumes it from checkpoint.",
		2,
	)

	printSection("Config")
	printKeyValueCard([][2]string{
		{"Model", *modelName},
		{"A2A host", resolvedHost},
		{"Streaming", fmt.Sprintf("%v", *streaming)},
		{"Timeout", timeout.String()},
		{"Lineage", defaultLineageID},
		{"Parent namespace", defaultParentNS},
		{"Remote namespace", defaultRemoteNS},
		{"OPENAI_BASE_URL set", fmt.Sprintf("%v", strings.TrimSpace(os.Getenv("OPENAI_BASE_URL")) != "")},
		{"OPENAI_API_KEY set", fmt.Sprintf("%v", strings.TrimSpace(os.Getenv("OPENAI_API_KEY")) != "")},
	})

	printSection("Input")
	printKeyValueCard([][2]string{
		{"User request", defaultInput},
	})
	fmt.Println(strings.Repeat("-", 56))
	fmt.Println("Phase 1: run parent graph, expect remote approval interrupt")

	phase1Result, err := runGraphAgent(ctx, parentAgent, "phase-1", defaultInput, baseState)
	if err != nil {
		return fmt.Errorf("phase 1 failed: %w", err)
	}

	latest, err := manager.Latest(ctx, defaultLineageID, defaultParentNS)
	if err != nil {
		return fmt.Errorf("query latest parent checkpoint: %w", err)
	}
	interruptedTuple, err := findLatestInterruptedCheckpoint(
		ctx,
		manager,
		defaultLineageID,
		defaultParentNS,
	)
	if err != nil {
		return err
	}
	if interruptedTuple == nil || interruptedTuple.Checkpoint == nil {
		return fmt.Errorf(
			"expected interrupted parent checkpoint, got latest: %#v (phase1 saw pregel interrupt event=%v)",
			latest,
			phase1Result.sawPregelInterrupt,
		)
	}
	parentCheckpointID := interruptedTuple.Checkpoint.ID

	subgraphInfo := extractSubgraphInterruptInfo(interruptedTuple.Checkpoint.ChannelValues)
	printSection("Phase 1 Result")
	rows := [][2]string{
		{"Status", "Parent graph stopped because remote graph raised an interrupt"},
		{"Parent checkpoint", parentCheckpointID},
	}
	if len(subgraphInfo) > 0 {
		rows = append(rows,
			[2]string{"Remote agent", stringValue(subgraphInfo, "child_agent_name")},
			[2]string{"Remote checkpoint", stringValue(subgraphInfo, "child_checkpoint_id")},
			[2]string{"Remote namespace", stringValue(subgraphInfo, "child_checkpoint_ns")},
			[2]string{"Interrupt key", stringValue(subgraphInfo, "child_task_id")},
		)
	}
	rows = append(rows, [2]string{"Resume payload", "ResumeMap{remote_ask_approval: true}"})
	printKeyValueCard(rows)
	printTraceTranscript("Phase 1 Walkthrough", phase1Result.traces)

	printSection("Phase 2")
	fmt.Println("Resume parent graph with approval=true")
	resumeState := graph.State{
		graph.CfgKeyLineageID:    defaultLineageID,
		graph.CfgKeyCheckpointNS: defaultParentNS,
		graph.CfgKeyCheckpointID: parentCheckpointID,
		graph.StateKeyCommand: &graph.Command{
			ResumeMap: map[string]any{
				remoteNodeAsk: true,
			},
		},
	}

	phase2Result, err := runGraphAgent(ctx, parentAgent, "phase-2", "resume", resumeState)
	if err != nil {
		return fmt.Errorf("phase 2 failed: %w", err)
	}
	completion := phase2Result.completion
	if completion == nil {
		return fmt.Errorf("phase 2: no graph completion event received")
	}

	finalMessage, err := decodeJSONString(
		completion.StateDelta,
		parentStateKeyFinalMessage,
	)
	if err != nil {
		return err
	}
	printSection("Phase 2 Result")
	fmt.Printf("%s\n", finalMessage)
	printTraceTranscript("Execution Walkthrough", phase2Result.traces)
	fmt.Println("Done.")
	return nil
}

func buildRemoteGraphAgent(modelInstance model.Model) (*graphagent.GraphAgent, error) {
	schema := graph.MessagesStateSchema()
	genConfig := compactGenerationConfig()
	schema.AddField(remoteStateKeyCaseBrief, graph.StateField{
		Type: reflect.TypeOf(""),
	})
	schema.AddField(remoteStateKeyRiskSignals, graph.StateField{
		Type: reflect.TypeOf(""),
	})
	schema.AddField(remoteStateKeyVerdictInput, graph.StateField{
		Type: reflect.TypeOf(""),
	})
	schema.AddField(remoteStateKeyRiskVerdict, graph.StateField{
		Type: reflect.TypeOf(""),
	})
	schema.AddField(remoteStateKeyApproved, graph.StateField{
		Type: reflect.TypeOf(true),
	})
	schema.AddField(remoteStateKeySummary, graph.StateField{
		Type: reflect.TypeOf(""),
	})

	compiled, err := graph.NewStateGraph(schema).
		AddNode(remoteNodeCaptureCase, func(ctx context.Context, state graph.State) (any, error) {
			caseBrief, _ := graph.GetStateValue[string](state, graph.StateKeyUserInput)
			if strings.TrimSpace(caseBrief) == "" {
				caseBrief = findLastUserMessage(state)
			}
			emitDemoTrace(ctx, state, "remote", remoteNodeCaptureCase, caseBrief)
			return graph.State{
				remoteStateKeyCaseBrief: caseBrief,
			}, nil
		}).
		AddLLMNode(
			remoteNodeRiskSignals,
			modelInstance,
			`ROLE:remote-risk-signals
You are a payment risk analyst. Analyze the user request and output one short line beginning with "Signals:".
List concrete fraud/risk signals only, separated by semicolons.`,
			nil,
			graph.WithGenerationConfig(genConfig),
		).
		AddLLMNode(
			remoteNodeRiskVerdict,
			modelInstance,
			`ROLE:remote-risk-verdict
You are the risk decision engine. Based on the provided risk signals, output exactly:
RISK_LEVEL=<LOW|MEDIUM|HIGH>; REASON=<one concise sentence>`,
			nil,
			graph.WithUserInputKey(remoteStateKeyVerdictInput),
			graph.WithGenerationConfig(genConfig),
		).
		AddNode(remoteNodeAsk, func(ctx context.Context, state graph.State) (any, error) {
			riskVerdict, _ := graph.GetStateValue[string](state, remoteStateKeyRiskVerdict)
			emitDemoTrace(ctx, state, "remote", remoteNodeAsk, "Manual approval required.\n"+riskVerdict)
			value, err := graph.Interrupt(
				ctx,
				state,
				remoteNodeAsk,
				fmt.Sprintf("Manual approval required. %s. Approve transfer? (true/false)", riskVerdict),
			)
			if err != nil {
				return nil, err
			}
			approved, _ := value.(bool)
			return graph.State{
				remoteStateKeyApproved: approved,
			}, nil
		}).
		AddNode(remoteNodeFinalize, func(ctx context.Context, state graph.State) (any, error) {
			approved, _ := graph.GetStateValue[bool](state, remoteStateKeyApproved)
			summary := buildRemoteReviewSummary(approved)
			emitDemoTrace(ctx, state, "remote", remoteNodeFinalize, summary)
			return graph.State{
				remoteStateKeySummary: summary,
				graph.StateKeyLastResponse: fmt.Sprintf(
					"Remote risk review finished, approved=%v",
					approved,
				),
			}, nil
		}).
		AddNode("capture_remote_risk_signals", func(ctx context.Context, state graph.State) (any, error) {
			caseBrief, _ := graph.GetStateValue[string](state, remoteStateKeyCaseBrief)
			if strings.TrimSpace(caseBrief) == "" {
				caseBrief = findLastUserMessage(state)
			}
			signals := buildRiskSignalsText()
			emitDemoTrace(ctx, state, "remote", remoteNodeRiskSignals, signals)
			return graph.State{
				remoteStateKeyCaseBrief:   caseBrief,
				remoteStateKeyRiskSignals: signals,
			}, nil
		}).
		AddNode(remoteNodePrepareVerdict, func(ctx context.Context, state graph.State) (any, error) {
			signals, _ := graph.GetStateValue[string](state, remoteStateKeyRiskSignals)
			userInput, _ := graph.GetStateValue[string](state, remoteStateKeyCaseBrief)
			verdictInput := fmt.Sprintf(
				"User request: %s\nRisk signals: %s",
				userInput,
				signals,
			)
			emitDemoTrace(ctx, state, "remote", remoteNodePrepareVerdict, verdictInput)
			return graph.State{
				remoteStateKeyVerdictInput: verdictInput,
			}, nil
		}).
		AddNode("capture_remote_risk_verdict", func(ctx context.Context, state graph.State) (any, error) {
			verdict := buildRiskVerdictText()
			emitDemoTrace(ctx, state, "remote", remoteNodeRiskVerdict, verdict)
			return graph.State{
				remoteStateKeyRiskVerdict: verdict,
			}, nil
		}).
		AddEdge(remoteNodeCaptureCase, remoteNodeRiskSignals).
		AddEdge(remoteNodeRiskSignals, "capture_remote_risk_signals").
		AddEdge("capture_remote_risk_signals", remoteNodePrepareVerdict).
		AddEdge(remoteNodePrepareVerdict, remoteNodeRiskVerdict).
		AddEdge(remoteNodeRiskVerdict, "capture_remote_risk_verdict").
		AddEdge("capture_remote_risk_verdict", remoteNodeAsk).
		AddEdge(remoteNodeAsk, remoteNodeFinalize).
		SetEntryPoint(remoteNodeCaptureCase).
		SetFinishPoint(remoteNodeFinalize).
		Compile()
	if err != nil {
		return nil, err
	}

	return graphagent.New(
		remoteAgentName,
		compiled,
		graphagent.WithCheckpointSaver(checkpointinmemory.NewSaver()),
	)
}

func buildParentGraphAgent(
	subAgent agent.Agent,
	modelInstance model.Model,
) (*graphagent.GraphAgent, error) {
	schema := graph.MessagesStateSchema()
	genConfig := compactGenerationConfig()
	schema.AddField(parentStateKeyCaseBrief, graph.StateField{
		Type: reflect.TypeOf(""),
	})
	schema.AddField(parentStateKeyApproved, graph.StateField{
		Type: reflect.TypeOf(true),
	})
	schema.AddField(parentStateKeyRemoteSummary, graph.StateField{
		Type: reflect.TypeOf(""),
	})
	schema.AddField(parentStateKeyDecisionDraft, graph.StateField{
		Type: reflect.TypeOf(""),
	})
	schema.AddField(parentStateKeyFinalMessage, graph.StateField{
		Type: reflect.TypeOf(""),
	})

	mapper := func(
		_ graph.State,
		res graph.SubgraphResult,
	) graph.State {
		out := graph.State{}
		if approved, ok := graph.GetStateValue[bool](res.FinalState, remoteStateKeyApproved); ok {
			out[parentStateKeyApproved] = approved
		}
		if summary, ok := graph.GetStateValue[string](res.FinalState, remoteStateKeySummary); ok {
			out[parentStateKeyRemoteSummary] = summary
		}
		return out
	}

	compiled, err := graph.NewStateGraph(schema).
		AddLLMNode(
			parentNodeIntake,
			modelInstance,
			`ROLE:parent-intake
You are an operations analyst. Summarize the user payment request into one sentence starting with "Case brief:".
Focus on amount, urgency, counterparty, and geography risk.`,
			nil,
			graph.WithGenerationConfig(genConfig),
		).
		AddNode("capture_parent_case_brief", func(ctx context.Context, state graph.State) (any, error) {
			lastResponse, _ := graph.GetStateValue[string](state, graph.StateKeyLastResponse)
			emitDemoTrace(ctx, state, "local", parentNodeIntake, lastResponse)
			return graph.State{
				parentStateKeyCaseBrief: lastResponse,
			}, nil
		}).
		AddAgentNode(
			remoteAgentName,
			graph.WithSubgraphInputMapper(func(parent graph.State) graph.State {
				caseBrief, _ := graph.GetStateValue[string](parent, parentStateKeyCaseBrief)
				if caseBrief == "" {
					caseBrief, _ = graph.GetStateValue[string](parent, graph.StateKeyUserInput)
				}
				lineageID, _ := graph.GetStateValue[string](parent, graph.CfgKeyLineageID)
				if lineageID == "" {
					lineageID = defaultLineageID
				}
				return graph.State{
					remoteStateKeyCaseBrief:     caseBrief,
					graph.CfgKeyLineageID:       lineageID,
					graph.CfgKeyCheckpointNS:    defaultRemoteNS,
					graph.CfgKeyIncludeContents: "none",
					graph.StateKeyUserInput:     caseBrief,
				}
			}),
			// parent_intake leaves the case brief in last_response; use it as the
			// child invocation input so remote A2A message content is not empty.
			graph.WithSubgraphInputFromLastResponse(),
			graph.WithSubgraphIsolatedMessages(true),
			graph.WithSubgraphOutputMapper(mapper),
		).
		AddNode(parentNodeDecision, func(ctx context.Context, state graph.State) (any, error) {
			approved, _ := graph.GetStateValue[bool](state, parentStateKeyApproved)
			summary, _ := graph.GetStateValue[string](state, parentStateKeyRemoteSummary)
			decisionDraft := "Decision draft: Transfer cannot proceed because manual approval was not granted."
			if approved {
				decisionDraft = "Decision draft: Manual approval granted after remote risk review, transfer can proceed with audit logging and post-transfer monitoring."
			}
			emitDemoTrace(ctx, state, "local", parentNodeDecision, decisionDraft+"\n"+summary)
			return graph.State{
				parentStateKeyDecisionDraft: decisionDraft,
				graph.StateKeyLastResponse:  decisionDraft + "\n" + summary,
			}, nil
		}).
		AddNode(parentNodeFinalize, func(ctx context.Context, state graph.State) (any, error) {
			caseBrief, _ := graph.GetStateValue[string](state, parentStateKeyCaseBrief)
			approved, _ := graph.GetStateValue[bool](state, parentStateKeyApproved)
			summary, _ := graph.GetStateValue[string](state, parentStateKeyRemoteSummary)
			decisionDraft, _ := graph.GetStateValue[string](state, parentStateKeyDecisionDraft)
			final := fmt.Sprintf(
				"Final decision\nCase: %s\nRemote review: %s\nAction: %s",
				caseBrief,
				summary,
				decisionDraft,
			)
			if !approved {
				final += "\nTransfer remains blocked because manual approval=false."
			}
			emitDemoTrace(ctx, state, "local", parentNodeFinalize, final)
			return graph.State{
				parentStateKeyFinalMessage: final,
				graph.StateKeyLastResponse: final,
			}, nil
		}).
		AddEdge(parentNodeIntake, "capture_parent_case_brief").
		AddEdge("capture_parent_case_brief", remoteAgentName).
		AddEdge(remoteAgentName, parentNodeDecision).
		AddEdge(parentNodeDecision, parentNodeFinalize).
		SetEntryPoint(parentNodeIntake).
		SetFinishPoint(parentNodeFinalize).
		Compile()
	if err != nil {
		return nil, err
	}

	return graphagent.New(
		parentAgentName,
		compiled,
		graphagent.WithCheckpointSaver(checkpointinmemory.NewSaver()),
		graphagent.WithSubAgents([]agent.Agent{subAgent}),
	)
}

type graphRunResult struct {
	completion         *event.Event
	sawPregelInterrupt bool
	traces             []demoTrace
}

type demoTrace struct {
	scope   string
	node    string
	summary string
}

func runGraphAgent(
	ctx context.Context,
	agt agent.Agent,
	invocationID string,
	userInput string,
	runtimeState graph.State,
) (*graphRunResult, error) {
	inv := agent.NewInvocation(
		agent.WithInvocationAgent(agt),
		agent.WithInvocationID(invocationID),
		agent.WithInvocationMessage(model.NewUserMessage(userInput)),
		agent.WithInvocationRunOptions(agent.RunOptions{
			RuntimeState: map[string]any(runtimeState.Clone()),
		}),
	)
	eventCh, err := agt.Run(ctx, inv)
	if err != nil {
		return nil, err
	}
	var completion *event.Event
	sawPregelInterrupt := false
	traces := make([]demoTrace, 0, 8)
	for ev := range eventCh {
		if ev == nil {
			continue
		}
		if isPregelInterruptEvent(ev) {
			sawPregelInterrupt = true
		}
		if ev.IsError() {
			if ev.Error != nil && ev.Error.Message != "" {
				return nil, fmt.Errorf("error event: %s", ev.Error.Message)
			}
			return nil, fmt.Errorf("error event: object=%s", ev.Object)
		}
		if isGraphCompletionEvent(ev) {
			completion = ev
		}
		if trace, ok := decodeDemoTrace(ev); ok {
			traces = append(traces, trace)
		}
	}
	return &graphRunResult{
		completion:         completion,
		sawPregelInterrupt: sawPregelInterrupt,
		traces:             traces,
	}, nil
}

func isGraphCompletionEvent(ev *event.Event) bool {
	return graph.IsGraphCompletionEvent(ev) || graph.IsVisibleGraphCompletionEvent(ev)
}

func isPregelInterruptEvent(ev *event.Event) bool {
	if ev == nil || ev.Response == nil || ev.Response.Object != graph.ObjectTypeGraphPregelStep {
		return false
	}
	raw, ok := ev.StateDelta[graph.MetadataKeyPregel]
	if !ok || len(raw) == 0 {
		return false
	}
	// Pregel interrupt metadata uses "interruptValue" when an interrupt is raised.
	return strings.Contains(string(raw), "interruptValue")
}

func extractSubgraphInterruptInfo(
	values map[string]any,
) map[string]any {
	if len(values) == 0 {
		return nil
	}
	raw, ok := values[graph.StateKeySubgraphInterrupt]
	if !ok {
		return nil
	}
	info, ok := raw.(map[string]any)
	if !ok {
		return nil
	}
	return info
}

func decodeJSONString(stateDelta map[string][]byte, key string) (string, error) {
	raw, ok := stateDelta[key]
	if !ok || len(raw) == 0 {
		return "", fmt.Errorf("missing state key %q in completion event", key)
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", fmt.Errorf("decode key %q: %w", key, err)
	}
	return value, nil
}

func findLatestInterruptedCheckpoint(
	ctx context.Context,
	manager *graph.CheckpointManager,
	lineageID string,
	namespace string,
) (*graph.CheckpointTuple, error) {
	if manager == nil {
		return nil, errors.New("checkpoint manager is nil")
	}
	config := graph.CreateCheckpointConfig(lineageID, "", namespace)
	tuples, err := manager.ListCheckpoints(ctx, config, &graph.CheckpointFilter{Limit: 64})
	if err != nil {
		return nil, fmt.Errorf("list parent checkpoints: %w", err)
	}
	for _, tuple := range tuples {
		if tuple == nil || tuple.Checkpoint == nil {
			continue
		}
		if tuple.Checkpoint.IsInterrupted() {
			return tuple, nil
		}
	}
	return nil, nil
}

func emitDemoTrace(
	ctx context.Context,
	state graph.State,
	scope string,
	node string,
	summary string,
) {
	if strings.TrimSpace(summary) == "" {
		return
	}
	_ = graph.EmitCustomStateDelta(
		ctx,
		state,
		graph.State{
			"demo_trace_scope":   scope,
			"demo_trace_node":    node,
			"demo_trace_summary": strings.TrimSpace(summary),
		},
		graph.WithStateDeltaEventType("demo_trace"),
		graph.WithStateDeltaEventMessage(node),
	)
}

func stringValue(values map[string]any, key string) string {
	if len(values) == 0 {
		return ""
	}
	if value, ok := values[key].(string); ok {
		return value
	}
	return ""
}

func decodeDemoTrace(ev *event.Event) (demoTrace, bool) {
	if ev == nil || ev.Object != graph.ObjectTypeGraphNodeCustom || ev.StateDelta == nil {
		return demoTrace{}, false
	}
	scope, err := decodeJSONString(ev.StateDelta, "demo_trace_scope")
	if err != nil || scope == "" {
		return demoTrace{}, false
	}
	node, err := decodeJSONString(ev.StateDelta, "demo_trace_node")
	if err != nil || node == "" {
		return demoTrace{}, false
	}
	summary, err := decodeJSONString(ev.StateDelta, "demo_trace_summary")
	if err != nil || summary == "" {
		return demoTrace{}, false
	}
	return demoTrace{
		scope:   scope,
		node:    node,
		summary: summary,
	}, true
}

func printTraceTranscript(title string, traces []demoTrace) {
	if len(traces) == 0 {
		return
	}
	printSection(title)
	for i, trace := range traces {
		label := traceDisplayName(trace.scope, trace.node)
		if label == "" {
			continue
		}
		fmt.Printf("  [%02d] %s\n", i+1, label)
		fmt.Printf("%s\n", indentBlockWrapped(trace.summary, "       ", 92))
		if i != len(traces)-1 {
			fmt.Println("       " + strings.Repeat("-", 54))
		}
	}
}

func traceDisplayName(scope, node string) string {
	switch node {
	case parentNodeIntake:
		return "Local / Parent intake"
	case remoteNodeCaptureCase:
		return "Remote / Capture case brief"
	case remoteNodeRiskSignals:
		return "Remote / Risk signals"
	case remoteNodePrepareVerdict:
		return "Remote / Verdict input"
	case remoteNodeRiskVerdict:
		return "Remote / Risk verdict"
	case remoteNodeAsk:
		return "Remote / Approval gate"
	case remoteNodeFinalize:
		return "Remote / Final summary"
	case parentNodeDecision:
		return "Local / Parent decision"
	case parentNodeFinalize:
		return "Local / Parent final output"
	default:
		if scope == "" {
			return node
		}
		return scope + " / " + node
	}
}

func indentBlockWrapped(s, prefix string, width int) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return prefix
	}
	lines := strings.Split(s, "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		wrapped := wrapText(line, width-len(prefix))
		if len(wrapped) == 0 {
			out = append(out, prefix)
			continue
		}
		for _, w := range wrapped {
			out = append(out, prefix+w)
		}
	}
	return strings.Join(out, "\n")
}

func wrapText(s string, width int) []string {
	if width <= 0 {
		return []string{strings.TrimSpace(s)}
	}
	words := strings.Fields(strings.TrimSpace(s))
	if len(words) == 0 {
		return nil
	}
	lines := make([]string, 0, len(words))
	line := words[0]
	for _, word := range words[1:] {
		if len(line)+1+len(word) > width {
			lines = append(lines, line)
			line = word
			continue
		}
		line += " " + word
	}
	lines = append(lines, line)
	return lines
}

func buildRemoteReviewSummary(approved bool) string {
	assessment := demoRiskAssessment()
	return fmt.Sprintf(
		"risk=%s; signals=%s; reason=%s; manual_approved=%v",
		assessment.level,
		strings.Join(assessment.signals, ", "),
		assessment.reason,
		approved,
	)
}

func buildRiskSignalsText() string {
	assessment := demoRiskAssessment()
	return "Signals: " + strings.Join(assessment.signals, "; ")
}

func buildRiskVerdictText() string {
	assessment := demoRiskAssessment()
	return fmt.Sprintf("RISK_LEVEL=%s; REASON=%s", assessment.level, assessment.reason)
}

type transferRiskAssessment struct {
	level   string
	signals []string
	reason  string
}

func demoRiskAssessment() transferRiskAssessment {
	// Demo simplification: always return a fixed high-risk assessment so the
	// interrupt path and resume behavior are deterministic in examples.
	return transferRiskAssessment{
		level: "HIGH",
		signals: []string{
			"large amount",
			"new beneficiary",
			"high-risk region",
			"urgent timeline",
		},
		reason: "Large amount, beneficiary novelty, geography risk, and urgency together require manual approval.",
	}
}

func findLastUserMessage(state graph.State) string {
	messages, ok := graph.GetStateValue[[]model.Message](state, graph.StateKeyMessages)
	if !ok || len(messages) == 0 {
		return ""
	}
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == model.RoleUser && strings.TrimSpace(messages[i].Content) != "" {
			return strings.TrimSpace(messages[i].Content)
		}
	}
	return ""
}

func printSection(title string) {
	fmt.Println()
	border := strings.Repeat("=", 72)
	fmt.Println(border)
	fmt.Println(title)
	fmt.Println(border)
}

func printKeyValueCard(rows [][2]string) {
	if len(rows) == 0 {
		return
	}
	maxKeyLen := 0
	for _, row := range rows {
		if len(row[0]) > maxKeyLen {
			maxKeyLen = len(row[0])
		}
	}
	for _, row := range rows {
		padding := maxKeyLen - len(row[0])
		fmt.Printf("  %s%s : %s\n", row[0], strings.Repeat(" ", padding), row[1])
	}
}

func printParagraph(text string, indent int) {
	if strings.TrimSpace(text) == "" {
		return
	}
	prefix := strings.Repeat(" ", indent)
	for _, line := range wrapText(text, 88-indent) {
		fmt.Printf("%s%s\n", prefix, line)
	}
}

func getEnvOrDefault(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

func compactGenerationConfig() model.GenerationConfig {
	return model.GenerationConfig{
		Stream: false,
	}
}

func resolveHost(raw string) (string, error) {
	if strings.TrimSpace(raw) != "" {
		return strings.TrimSpace(raw), nil
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", fmt.Errorf("allocate host: %w", err)
	}
	defer listener.Close()
	return listener.Addr().String(), nil
}

func waitForServer(ctx context.Context, url string, serverErr <-chan error) error {
	pollCtx, cancel := context.WithTimeout(ctx, pollTimeout)
	defer cancel()
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	client := &http.Client{Timeout: pollInterval}
	for {
		select {
		case err := <-serverErr:
			return fmt.Errorf("start a2a server: %w", err)
		case <-pollCtx.Done():
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return fmt.Errorf("a2a server did not become ready within %s", pollTimeout)
		case <-ticker.C:
			resp, err := client.Get(url) //nolint:noctx
			if err == nil {
				_ = resp.Body.Close()
				if resp.StatusCode == http.StatusOK {
					return nil
				}
			}
		}
	}
}
