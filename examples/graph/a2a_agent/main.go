//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package main demonstrates a parent GraphAgent calling a remote GraphAgent
// through an A2A sub-agent, then reading the remote agent's final state back
// from the parent graph state.
//
// Unlike the minimal smoke-test version, this example uses a real LLM node in
// the remote graph so it matches the shape of other graph examples more
// closely. The remote graph writes both a natural-language reply and a
// structured payload into graph state; the parent graph receives those fields
// through A2A state_delta transport and confirms the handoff succeeded.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"reflect"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/a2aagent"
	"trpc.group/trpc-go/trpc-agent-go/agent/graphagent"
	"trpc.group/trpc-go/trpc-agent-go/graph"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	a2aserver "trpc.group/trpc-go/trpc-agent-go/server/a2a"
)

const (
	defaultModelName = "deepseek-chat"

	parentAgentName = "parent_graph"
	remoteAgentName = "remote_graph"

	remoteNodeInput    = "stash_remote_input"
	remoteNodeModel    = "remote_reply"
	remoteNodeCapture  = "capture_remote_state"
	parentNodeFinalize = "finalize"

	remoteStateKeyOriginalInput = "remote_original_input"
	remoteStateKeyValue         = "remote_child_value"
	remoteStateKeyPayload       = "remote_child_payload"

	parentStateKeyValue      = "value_from_remote"
	parentStateKeyEcho       = "echo_from_remote"
	parentStateKeyPayload    = "remote_state_payload"
	parentStateKeyRawDeltaOK = "raw_state_delta_present"

	defaultInput       = "Please explain why state handoff through the remote agent matters."
	defaultRunTimeout  = 90 * time.Second
	serverPollInterval = 20 * time.Millisecond
	serverPollTimeout  = 5 * time.Second

	remoteTransportValue = "a2a"
)

var (
	modelName      = flag.String("model", getEnvOrDefault("MODEL_NAME", defaultModelName), "OpenAI-compatible model name")
	baseURL        = flag.String("base-url", os.Getenv("OPENAI_BASE_URL"), "OpenAI-compatible base URL")
	apiKey         = flag.String("api-key", os.Getenv("OPENAI_API_KEY"), "API key")
	input          = flag.String("input", defaultInput, "Input sent into the parent graph")
	host           = flag.String("host", "", "Host for the in-process A2A server, for example 127.0.0.1:28883")
	streaming      = flag.Bool("streaming", true, "Use A2A streaming between parent and remote graph")
	modelStreaming = flag.Bool("model-streaming", false, "Use streaming when the remote graph calls the model")
	timeout        = flag.Duration("timeout", defaultRunTimeout, "Overall timeout for the example run")
	verboseEvents  = flag.Bool("verbose-events", false, "Print every event observed from the parent graph run")
)

func main() {
	flag.Parse()
	setupLogging()

	fmt.Printf("Graph A2A Agent Example\n")
	fmt.Printf("Model: %s\n", *modelName)
	fmt.Println(strings.Repeat("=", 56))
	if *apiKey == "" && os.Getenv("OPENAI_API_KEY") == "" {
		fmt.Println("Hint: provide -api-key/-base-url or set OPENAI_API_KEY/OPENAI_BASE_URL.")
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	if err := run(ctx); err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			fmt.Fprintf(
				os.Stderr,
				"error: %v\nhint: try a longer -timeout or disable remote model streaming with -model-streaming=false\n",
				err,
			)
			os.Exit(1)
		}
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	resolvedHost, err := resolveHost(*host)
	if err != nil {
		return err
	}

	remoteAgent, err := buildRemoteGraphAgent(*modelName, *baseURL, *apiKey, *modelStreaming)
	if err != nil {
		return fmt.Errorf("build remote graph agent: %w", err)
	}

	server, err := a2aserver.New(
		a2aserver.WithAgent(remoteAgent, *streaming),
		a2aserver.WithHost(resolvedHost),
		a2aserver.WithGraphEventObjectAllowlist(graph.ObjectTypeGraphExecution, "graph.node.*"),
		a2aserver.WithDebugLogging(false),
	)
	if err != nil {
		return fmt.Errorf("create a2a server: %w", err)
	}
	defer func() {
		_ = server.Stop(context.Background())
	}()

	serverErr := make(chan error, 1)
	go func() {
		if err := server.Start(resolvedHost); err != nil {
			serverErr <- err
		}
	}()

	// Poll the agent-card endpoint until the server is ready, instead of
	// using a fixed sleep that may be too short on slow machines.
	agentCardURL := fmt.Sprintf("http://%s/.well-known/agent.json", resolvedHost)
	if err := waitForServer(ctx, agentCardURL, serverErr); err != nil {
		return err
	}

	remoteURL := fmt.Sprintf("http://%s", resolvedHost)
	a2aSubAgent, err := a2aagent.New(
		a2aagent.WithAgentCardURL(remoteURL),
		a2aagent.WithName(remoteAgentName),
		a2aagent.WithEnableStreaming(*streaming),
	)
	if err != nil {
		return fmt.Errorf("create a2a sub-agent: %w", err)
	}

	parentAgent, err := buildParentGraphAgent(a2aSubAgent)
	if err != nil {
		return fmt.Errorf("build parent graph agent: %w", err)
	}

	runResult, err := runOnce(ctx, parentAgent, *input)
	if err != nil {
		return err
	}
	completionEvent := runResult.completionEvent

	remoteReply, err := decodeJSONString(completionEvent.StateDelta, parentStateKeyValue)
	if err != nil {
		return err
	}
	echoValue, err := decodeJSONString(completionEvent.StateDelta, parentStateKeyEcho)
	if err != nil {
		return err
	}
	rawDeltaOK, err := decodeJSONBool(completionEvent.StateDelta, parentStateKeyRawDeltaOK)
	if err != nil {
		return err
	}
	remotePayload, err := decodeJSONMap(completionEvent.StateDelta, parentStateKeyPayload)
	if err != nil {
		return err
	}

	if remoteReply == "" {
		return fmt.Errorf("remote reply restored from state is empty")
	}
	if !rawDeltaOK {
		return fmt.Errorf("remote graph state_delta was not preserved across A2A")
	}
	if echoValue != *input {
		return fmt.Errorf("unexpected echo value %q", echoValue)
	}
	if transport, _ := remotePayload["transport"].(string); transport != remoteTransportValue {
		return fmt.Errorf("unexpected transport %q", transport)
	}

	fmt.Printf("A2A host: %s\n", resolvedHost)
	fmt.Printf("A2A streaming: %v\n", *streaming)
	fmt.Printf("Remote model streaming: %v\n", *modelStreaming)
	fmt.Printf("Input: %s\n\n", *input)
	if len(runResult.remoteTrace) > 0 {
		fmt.Printf("Remote graph event trace:\n%s\n\n", formatRemoteTrace(runResult.remoteTrace))
	}
	fmt.Printf("Remote agent reply:\n%s\n\n", remoteReply)
	fmt.Printf("Transferred remote state: OK\n%s\n\n", prettyJSON(remotePayload))
	fmt.Printf("Raw state delta seen by parent mapper: %v\n", rawDeltaOK)
	fmt.Printf("Parent graph confirmation:\n%s\n", finalResponseText(completionEvent))
	return nil
}

// waitForServer polls the given URL until it returns HTTP 200, the server
// goroutine reports an error, or the context is cancelled.
func waitForServer(ctx context.Context, url string, serverErr <-chan error) error {
	pollCtx, cancel := context.WithTimeout(ctx, serverPollTimeout)
	defer cancel()

	ticker := time.NewTicker(serverPollInterval)
	defer ticker.Stop()

	httpClient := &http.Client{Timeout: serverPollInterval}
	for {
		select {
		case err := <-serverErr:
			return fmt.Errorf("start a2a server: %w", err)
		case <-pollCtx.Done():
			if ctx.Err() != nil {
				return fmt.Errorf("wait for a2a server: %w", ctx.Err())
			}
			return fmt.Errorf("a2a server did not become ready within %s", serverPollTimeout)
		case <-ticker.C:
			resp, err := httpClient.Get(url) //nolint:noctx
			if err == nil {
				_ = resp.Body.Close()
				if resp.StatusCode == http.StatusOK {
					return nil
				}
			}
		}
	}
}

func buildRemoteGraphAgent(modelName, baseURL, apiKey string, modelStreaming bool) (agent.Agent, error) {
	schema := graph.MessagesStateSchema()
	schema.AddField(
		remoteStateKeyOriginalInput,
		graph.StateField{Type: reflect.TypeOf("")},
	)
	schema.AddField(
		remoteStateKeyValue,
		graph.StateField{Type: reflect.TypeOf("")},
	)
	schema.AddField(
		remoteStateKeyPayload,
		graph.StateField{Type: reflect.TypeOf(map[string]any{})},
	)

	modelInstance := openai.New(modelName, buildOpenAIOptions(baseURL, apiKey)...)
	genConfig := model.GenerationConfig{
		Stream:      modelStreaming,
		Temperature: floatPtr(0.2),
		MaxTokens:   intPtr(200),
	}

	compiled, err := graph.NewStateGraph(schema).
		AddNode(remoteNodeInput, stashRemoteInput).
		AddLLMNode(
			remoteNodeModel,
			modelInstance,
			"You are the remote graph agent behind an A2A server. Reply in exactly one concise English sentence that starts with 'Remote agent:' and directly addresses the user's request. No markdown, no bullet list, no quotation marks.",
			nil,
			graph.WithGenerationConfig(genConfig),
		).
		AddNode(remoteNodeCapture, buildRemoteStateCaptureNode(modelName)).
		AddEdge(remoteNodeInput, remoteNodeModel).
		AddEdge(remoteNodeModel, remoteNodeCapture).
		SetEntryPoint(remoteNodeInput).
		SetFinishPoint(remoteNodeCapture).
		Compile()
	if err != nil {
		return nil, err
	}

	return graphagent.New(
		remoteAgentName,
		compiled,
		graphagent.WithDescription("Remote graph exposed through A2A with an LLM node"),
		graphagent.WithInitialState(graph.State{}),
	)
}

func stashRemoteInput(_ context.Context, state graph.State) (any, error) {
	userInput, ok := graph.GetStateValue[string](state, graph.StateKeyUserInput)
	if !ok {
		return nil, fmt.Errorf("missing user input for remote graph")
	}

	return graph.State{
		remoteStateKeyOriginalInput: userInput,
	}, nil
}

func buildRemoteStateCaptureNode(modelName string) graph.NodeFunc {
	return func(_ context.Context, state graph.State) (any, error) {
		reply, _ := graph.GetStateValue[string](state, graph.StateKeyLastResponse)
		reply = strings.TrimSpace(reply)
		if reply == "" {
			return nil, fmt.Errorf("remote model returned empty response")
		}

		userInput, _ := graph.GetStateValue[string](state, remoteStateKeyOriginalInput)
		if userInput == "" {
			userInput, _ = graph.GetStateValue[string](state, graph.StateKeyUserInput)
		}
		payload := map[string]any{
			"echo":              userInput,
			"model":             modelName,
			"source_agent":      remoteAgentName,
			"transport":         remoteTransportValue,
			"reply_chars":       len(reply),
			"transfer_verified": true,
		}

		return graph.State{
			remoteStateKeyValue:   reply,
			remoteStateKeyPayload: payload,
			graph.StateKeyLastResponse: fmt.Sprintf(
				"Remote graph completed with %d characters of reply.",
				len(reply),
			),
		}, nil
	}
}

func buildParentGraphAgent(remote agent.Agent) (agent.Agent, error) {
	schema := graph.MessagesStateSchema()
	schema.AddField(
		parentStateKeyValue,
		graph.StateField{Type: reflect.TypeOf("")},
	)
	schema.AddField(
		parentStateKeyEcho,
		graph.StateField{Type: reflect.TypeOf("")},
	)
	schema.AddField(
		parentStateKeyPayload,
		graph.StateField{Type: reflect.TypeOf(map[string]any{})},
	)
	schema.AddField(
		parentStateKeyRawDeltaOK,
		graph.StateField{Type: reflect.TypeOf(true)},
	)

	compiled, err := graph.NewStateGraph(schema).
		AddAgentNode(
			remoteAgentName,
			graph.WithSubgraphOutputMapper(mapRemoteFinalState),
		).
		AddNode(parentNodeFinalize, finalizeParentState).
		AddEdge(remoteAgentName, parentNodeFinalize).
		SetEntryPoint(remoteAgentName).
		SetFinishPoint(parentNodeFinalize).
		Compile()
	if err != nil {
		return nil, err
	}

	return graphagent.New(
		parentAgentName,
		compiled,
		graphagent.WithDescription("Parent graph using a remote A2A graph sub-agent"),
		graphagent.WithInitialState(graph.State{}),
		graphagent.WithSubAgents([]agent.Agent{remote}),
	)
}

func mapRemoteFinalState(_ graph.State, result graph.SubgraphResult) graph.State {
	if *verboseEvents {
		fmt.Printf(
			"output mapper finalState keys=%v rawDelta keys=%s\n",
			mapKeys(result.FinalState),
			formatStateKeys(result.RawStateDelta),
		)
	}

	value, _ := graph.GetStateValue[string](result.FinalState, remoteStateKeyValue)
	if value == "" {
		value = decodeRawString(result.RawStateDelta, remoteStateKeyValue)
	}

	payload := decodeStateMap(result.FinalState, remoteStateKeyPayload)
	if len(payload) == 0 {
		payload = decodeRawMap(result.RawStateDelta, remoteStateKeyPayload)
	}

	echoValue, _ := payload["echo"].(string)
	_, rawDeltaOK := result.RawStateDelta[remoteStateKeyValue]

	return graph.State{
		parentStateKeyValue:      value,
		parentStateKeyEcho:       echoValue,
		parentStateKeyPayload:    payload,
		parentStateKeyRawDeltaOK: rawDeltaOK,
	}
}

func finalizeParentState(_ context.Context, state graph.State) (any, error) {
	mappedValue, ok := graph.GetStateValue[string](state, parentStateKeyValue)
	if !ok || mappedValue == "" {
		return nil, fmt.Errorf("missing mapped remote value")
	}

	echoValue, ok := graph.GetStateValue[string](state, parentStateKeyEcho)
	if !ok || echoValue == "" {
		return nil, fmt.Errorf("missing mapped remote payload echo")
	}

	rawDeltaOK, ok := graph.GetStateValue[bool](state, parentStateKeyRawDeltaOK)
	if !ok || !rawDeltaOK {
		return nil, fmt.Errorf("remote state_delta was not available to the parent output mapper")
	}

	payload := decodeStateMap(state, parentStateKeyPayload)
	sourceAgent, _ := payload["source_agent"].(string)
	transport, _ := payload["transport"].(string)
	if sourceAgent == "" {
		sourceAgent = remoteAgentName
	}
	if transport == "" {
		transport = remoteTransportValue
	}

	return graph.State{
		graph.StateKeyLastResponse: fmt.Sprintf(
			"Parent graph confirmed remote state handoff from %s via %s. Echo=%q.",
			sourceAgent,
			strings.ToUpper(transport),
			echoValue,
		),
	}, nil
}
