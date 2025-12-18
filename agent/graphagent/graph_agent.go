//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package graphagent provides a graph-based agent implementation.
package graphagent

import (
	"context"
	"fmt"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/graph"
	"trpc.group/trpc-go/trpc-agent-go/internal/flow/llmflow"
	"trpc.group/trpc-go/trpc-agent-go/internal/flow/processor"
	"trpc.group/trpc-go/trpc-agent-go/internal/state/barrier"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// GraphAgent is an agent that executes a graph.
type GraphAgent struct {
	name              string
	description       string
	graph             *graph.Graph
	executor          *graph.Executor
	subAgents         []agent.Agent
	agentCallbacks    *agent.Callbacks
	initialState      graph.State
	channelBufferSize int
	options           Options
}

// New creates a new GraphAgent with the given graph and options.
func New(name string, g *graph.Graph, opts ...Option) (*GraphAgent, error) {
	// set default channel buffer size.
	options := defaultOptions

	// Apply function options.
	for _, opt := range opts {
		opt(&options)
	}

	// Build executor options.
	var executorOpts []graph.ExecutorOption
	executorOpts = append(executorOpts,
		graph.WithChannelBufferSize(options.ChannelBufferSize))
	if options.CheckpointSaver != nil {
		executorOpts = append(executorOpts,
			graph.WithCheckpointSaver(options.CheckpointSaver))
	}

	executor, err := graph.NewExecutor(g, executorOpts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create graph executor: %w", err)
	}

	return &GraphAgent{
		name:              name,
		description:       options.Description,
		graph:             g,
		executor:          executor,
		subAgents:         options.SubAgents,
		agentCallbacks:    options.AgentCallbacks,
		initialState:      options.InitialState,
		channelBufferSize: options.ChannelBufferSize,
		options:           options,
	}, nil
}

// Run executes the graph with the provided invocation.
func (ga *GraphAgent) Run(ctx context.Context, invocation *agent.Invocation) (<-chan *event.Event, error) {
	// Setup invocation.
	ga.setupInvocation(invocation)
	out := make(chan *event.Event, ga.channelBufferSize)
	runCtx := agent.CloneContext(ctx)
	go ga.runWithBarrier(runCtx, invocation, out)
	return out, nil
}

// runWithBarrier emits a start barrier, waits for completion, then runs the graph with callbacks
// pipeline and forwards all events to the provided output channel.
func (ga *GraphAgent) runWithBarrier(ctx context.Context, invocation *agent.Invocation, out chan<- *event.Event) {
	defer close(out)
	// Emit a barrier event and wait for completion in a dedicated goroutine so that the runner can append all prior
	// events before GraphAgent reads history.
	if err := ga.emitStartBarrierAndWait(ctx, invocation, out); err != nil {
		evt := event.NewErrorEvent(invocation.InvocationID, invocation.AgentName,
			model.ErrorTypeFlowError, err.Error())
		if emitErr := agent.EmitEvent(ctx, invocation, out, evt); emitErr != nil {
			log.Errorf("graphagent: emit error event failed: %v", emitErr)
		}
		return
	}
	innerChan, err := ga.runWithCallbacks(ctx, invocation)
	if err != nil {
		evt := event.NewErrorEvent(invocation.InvocationID, invocation.AgentName,
			model.ErrorTypeFlowError, err.Error())
		if emitErr := agent.EmitEvent(ctx, invocation, out, evt); emitErr != nil {
			log.Errorf("graphagent: emit error event failed: %v.", emitErr)
		}
		return
	}
	for evt := range innerChan {
		if err := event.EmitEvent(ctx, out, evt); err != nil {
			log.Errorf("graphagent: emit event failed: %v.", err)
			return
		}
	}
}

// emitStartBarrierAndWait emits a barrier event and waits until the runner has processed it,
// ensuring that all prior events have been appended to the session before GraphAgent reads history.
func (ga *GraphAgent) emitStartBarrierAndWait(ctx context.Context, invocation *agent.Invocation,
	ch chan<- *event.Event) error {
	// If graph barrier is not enabled, skip.
	if !barrier.Enabled(invocation) {
		return nil
	}
	barrier := event.New(invocation.InvocationID, invocation.AgentName,
		event.WithObject(graph.ObjectTypeGraphBarrier))
	barrier.RequiresCompletion = true
	completionID := agent.GetAppendEventNoticeKey(barrier.ID)
	if noticeCh := invocation.AddNoticeChannel(ctx, completionID); noticeCh == nil {
		return fmt.Errorf("add notice channel for %s", completionID)
	}
	if err := agent.EmitEvent(ctx, invocation, ch, barrier); err != nil {
		return fmt.Errorf("emit barrier event: %w", err)
	}
	timeout := llmflow.WaitEventTimeout(ctx)
	if err := invocation.AddNoticeChannelAndWait(ctx, completionID, timeout); err != nil {
		return fmt.Errorf("wait for barrier completion: %w", err)
	}
	return nil
}

// runWithCallbacks executes the GraphAgent flow: prepare initial state, run before-agent callbacks, execute the graph,
// and wrap with after-agent callbacks when present.
func (ga *GraphAgent) runWithCallbacks(ctx context.Context, invocation *agent.Invocation) (<-chan *event.Event, error) {
	// Execute the graph.
	if ga.agentCallbacks != nil {
		result, err := ga.agentCallbacks.RunBeforeAgent(ctx, &agent.BeforeAgentArgs{
			Invocation: invocation,
		})
		if err != nil {
			return nil, fmt.Errorf("before agent callback failed: %w", err)
		}
		// Use the context from result if provided.
		if result != nil && result.Context != nil {
			ctx = result.Context
		}
		if result != nil && result.CustomResponse != nil {
			// Create a channel that returns the custom response and then closes.
			eventChan := make(chan *event.Event, 1)
			// Create an event from the custom response.
			customevent := event.NewResponseEvent(invocation.InvocationID, invocation.AgentName, result.CustomResponse)
			agent.EmitEvent(ctx, invocation, eventChan, customevent)
			close(eventChan)
			return eventChan, nil
		}
	}

	// Prepare initial state after callbacks so that any modifications
	// made by callbacks to the invocation (for example, RuntimeState,
	// Session, or Message) are visible to the graph execution.
	initialState := ga.createInitialState(ctx, invocation)

	// Execute the graph.
	eventChan, err := ga.executor.Execute(ctx, initialState, invocation)
	if err != nil {
		return nil, err
	}
	if ga.agentCallbacks != nil {
		return ga.wrapEventChannel(ctx, invocation, eventChan), nil
	}
	return eventChan, nil
}

func (ga *GraphAgent) createInitialState(ctx context.Context, invocation *agent.Invocation) graph.State {
	var initialState graph.State

	if ga.initialState != nil {
		// Clone the base initial state to avoid modifying the original.
		initialState = ga.initialState.Clone()
	} else {
		initialState = make(graph.State)
	}

	// Merge runtime state from RunOptions if provided.
	if invocation.RunOptions.RuntimeState != nil {
		for key, value := range invocation.RunOptions.RuntimeState {
			initialState[key] = value
		}
	}

	// Seed messages from session events so multi‑turn runs share history.
	// This mirrors ContentRequestProcessor behavior used by non-graph flows.
	if invocation.Session != nil {
		// Build a temporary request to reuse the processor logic.
		req := &model.Request{}

		// Default processor: include (possibly overridden) + preserve same branch.
		p := processor.NewContentRequestProcessor(
			processor.WithBranchFilterMode(ga.options.messageBranchFilterMode),
			processor.WithAddSessionSummary(ga.options.AddSessionSummary),
			processor.WithMaxHistoryRuns(ga.options.MaxHistoryRuns),
			processor.WithPreserveSameBranch(true),
			processor.WithTimelineFilterMode(ga.options.messageTimelineFilterMode),
			processor.WithReasoningContentMode(ga.options.ReasoningContentMode),
		)
		// We only need messages side effect; no output channel needed.
		p.ProcessRequest(ctx, invocation, req, nil)
		if len(req.Messages) > 0 {
			initialState[graph.StateKeyMessages] = req.Messages
		}
	}

	// Add invocation message to state.
	// When resuming from checkpoint, only add user input if it's meaningful content
	// (not just a resume signal), following LangGraph's pattern.
	isResuming := invocation.RunOptions.RuntimeState != nil &&
		invocation.RunOptions.RuntimeState[graph.CfgKeyCheckpointID] != nil

	if invocation.Message.Content != "" {
		// If resuming and the message is just "resume", don't add it as input
		// This allows pure checkpoint resumption without input interference
		if isResuming && invocation.Message.Content == "resume" {
			// Skip adding user_input to preserve checkpoint state
		} else {
			// Add user input for normal execution or resume with meaningful input
			initialState[graph.StateKeyUserInput] = invocation.Message.Content
		}
	}
	// Add session context if available.
	if invocation.Session != nil {
		initialState[graph.StateKeySession] = invocation.Session
	}
	// Add parent agent to state so agent nodes can access sub-agents.
	initialState[graph.StateKeyParentAgent] = ga
	// Set checkpoint namespace if not already set.
	if ns, ok := initialState[graph.CfgKeyCheckpointNS].(string); !ok || ns == "" {
		initialState[graph.CfgKeyCheckpointNS] = ga.name
	}

	return initialState
}

func (ga *GraphAgent) setupInvocation(invocation *agent.Invocation) {
	// Set agent and agent name.
	invocation.Agent = ga
	invocation.AgentName = ga.name
}

// Tools returns the list of tools available to this agent.
func (ga *GraphAgent) Tools() []tool.Tool { return nil }

// Info returns the basic information about this agent.
func (ga *GraphAgent) Info() agent.Info {
	return agent.Info{
		Name:        ga.name,
		Description: ga.description,
	}
}

// SubAgents returns the list of sub-agents available to this agent.
func (ga *GraphAgent) SubAgents() []agent.Agent {
	return ga.subAgents
}

// FindSubAgent finds a sub-agent by name.
func (ga *GraphAgent) FindSubAgent(name string) agent.Agent {
	for _, subAgent := range ga.subAgents {
		if subAgent.Info().Name == name {
			return subAgent
		}
	}
	return nil
}

// wrapEventChannel wraps the event channel to apply after agent callbacks.
func (ga *GraphAgent) wrapEventChannel(
	ctx context.Context,
	invocation *agent.Invocation,
	originalChan <-chan *event.Event,
) <-chan *event.Event {
	wrappedChan := make(chan *event.Event, ga.channelBufferSize)
	runCtx := agent.CloneContext(ctx)
	go func(ctx context.Context) {
		defer close(wrappedChan)
		var fullRespEvent *event.Event
		// Forward all events from the original channel
		for evt := range originalChan {
			if evt != nil && evt.Response != nil && !evt.Response.IsPartial {
				fullRespEvent = evt
			}
			if err := event.EmitEvent(ctx, wrappedChan, evt); err != nil {
				return
			}
		}

		// Collect error from the final response event so after-agent
		// callbacks can observe execution failures, matching LLMAgent
		// semantics.
		var agentErr error
		if fullRespEvent != nil &&
			fullRespEvent.Response != nil &&
			fullRespEvent.Response.Error != nil {
			agentErr = fmt.Errorf(
				"%s: %s",
				fullRespEvent.Response.Error.Type,
				fullRespEvent.Response.Error.Message,
			)
		}

		// After all events are processed, run after agent callbacks
		result, err := ga.agentCallbacks.RunAfterAgent(ctx, &agent.AfterAgentArgs{
			Invocation:        invocation,
			Error:             agentErr,
			FullResponseEvent: fullRespEvent,
		})
		// Use the context from result if provided.
		if result != nil && result.Context != nil {
			ctx = result.Context
		}
		var evt *event.Event
		if err != nil {
			// Send error event.
			evt = event.NewErrorEvent(
				invocation.InvocationID,
				invocation.AgentName,
				agent.ErrorTypeAgentCallbackError,
				err.Error(),
			)
		} else if result != nil && result.CustomResponse != nil {
			// Create an event from the custom response.
			evt = event.NewResponseEvent(
				invocation.InvocationID,
				invocation.AgentName,
				result.CustomResponse,
			)
		}

		agent.EmitEvent(ctx, invocation, wrappedChan, evt)
	}(runCtx)
	return wrappedChan
}

// Executor returns the graph executor for direct access to checkpoint management.
func (ga *GraphAgent) Executor() *graph.Executor {
	return ga.executor
}
