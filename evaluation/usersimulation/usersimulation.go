//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package usersimulation

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/runner"
)

// Simulator starts a simulated user conversation for one eval case.
type Simulator interface {
	// Start creates a conversation handle for a single eval case.
	Start(ctx context.Context, req *StartRequest) (Conversation, error)
}

// Conversation advances a simulated user one turn at a time.
type Conversation interface {
	// Next returns the next simulated user action for the target agent.
	Next(ctx context.Context, req *TurnRequest) (*Decision, error)
	// Close releases resources owned by the conversation.
	Close() error
}

// StartRequest carries the stable inputs for one simulated conversation.
type StartRequest struct {
	// EvalCaseID identifies the eval case that owns this conversation.
	EvalCaseID string
	// Scenario defines the simulated conversation plan.
	Scenario *evalset.ConversationScenario
	// InitialSession contains the target agent session seed.
	InitialSession *evalset.SessionInput
	// SessionID identifies the target agent session.
	SessionID string
}

// TurnRequest carries the target agent output from the previous turn.
type TurnRequest struct {
	// LastTargetResponse is the final response from the previous target turn.
	LastTargetResponse *model.Message
}

// Decision describes the next action produced by the simulator.
type Decision struct {
	// Message is the next user message for the target agent.
	Message *model.Message
	// Stop signals that the conversation should end before another target turn.
	Stop bool
}

var _ Simulator = (*userSimulator)(nil)
var _ Conversation = (*conversation)(nil)

type userSimulator struct {
	simRunner runner.Runner
	options   *options
}

type conversation struct {
	simRunner              runner.Runner
	initialSession         *evalset.SessionInput
	startingPrompt         string
	stopSignal             string
	maxAllowedInvocations  int
	simUserID              string
	simSessionID           string
	injectedContextMessage []model.Message
	started                bool
	generatedInputs        int
	closed                 bool
}

// New builds the default simulator implementation backed by a runner.
func New(simRunner runner.Runner, opt ...Option) (Simulator, error) {
	if simRunner == nil {
		return nil, errors.New("sim runner is nil")
	}
	opts := newOptions(opt...)
	if opts.maxAllowedInvocations != nil && *opts.maxAllowedInvocations < 0 {
		return nil, errors.New("max allowed invocations must be greater than or equal to 0")
	}
	if opts.userIDSupplier == nil {
		return nil, errors.New("user id supplier is nil")
	}
	if opts.sessionIDSupplier == nil {
		return nil, errors.New("session id supplier is nil")
	}
	if opts.systemPromptBuilder == nil {
		return nil, errors.New("system prompt builder is nil")
	}
	return &userSimulator{simRunner: simRunner, options: opts}, nil
}

// Start creates a new default simulated conversation.
func (s *userSimulator) Start(ctx context.Context, req *StartRequest) (Conversation, error) {
	if req == nil {
		return nil, errors.New("start request is nil")
	}
	if req.Scenario == nil {
		return nil, errors.New("scenario is nil")
	}
	if req.InitialSession == nil {
		return nil, errors.New("initial session is nil")
	}
	if strings.TrimSpace(req.Scenario.ConversationPlan) == "" {
		return nil, errors.New("conversation plan is empty")
	}
	stopSignal := strings.TrimSpace(req.Scenario.StopSignal)
	if s.options.stopSignal != nil {
		stopSignal = strings.TrimSpace(*s.options.stopSignal)
	}
	maxAllowedInvocations := 0
	if req.Scenario.MaxAllowedInvocations != nil {
		maxAllowedInvocations = *req.Scenario.MaxAllowedInvocations
	}
	if s.options.maxAllowedInvocations != nil {
		maxAllowedInvocations = *s.options.maxAllowedInvocations
	}
	if maxAllowedInvocations < 0 {
		return nil, errors.New("max allowed invocations must be greater than or equal to 0")
	}
	if stopSignal == "" && maxAllowedInvocations == 0 {
		return nil, errors.New("stop signal and max allowed invocations cannot both be disabled")
	}
	effectiveScenario := buildEffectiveScenario(req.Scenario, stopSignal, maxAllowedInvocations)
	injectedContextMessage, err := buildScenarioContextMessages(ctx, effectiveScenario, s.options.systemPromptBuilder)
	if err != nil {
		return nil, err
	}
	simUserID := s.options.userIDSupplier(ctx)
	if simUserID == "" {
		return nil, errors.New("sim user id is empty")
	}
	simSessionID := s.options.sessionIDSupplier(ctx)
	if simSessionID == "" {
		return nil, errors.New("sim session id is empty")
	}
	return &conversation{
		simRunner:              s.simRunner,
		initialSession:         req.InitialSession,
		startingPrompt:         effectiveScenario.StartingPrompt,
		stopSignal:             effectiveScenario.StopSignal,
		maxAllowedInvocations:  maxAllowedInvocations,
		simUserID:              simUserID,
		simSessionID:           simSessionID,
		injectedContextMessage: injectedContextMessage,
	}, nil
}

// Next returns the next user message or a stop decision.
func (c *conversation) Next(ctx context.Context, req *TurnRequest) (*Decision, error) {
	if c.closed {
		return nil, errors.New("conversation is closed")
	}
	if c.maxAllowedInvocations > 0 && c.generatedInputs >= c.maxAllowedInvocations {
		return &Decision{Stop: true}, nil
	}
	if !c.started {
		c.started = true
		if c.startingPrompt != "" {
			c.generatedInputs++
			message := model.NewUserMessage(c.startingPrompt)
			return &Decision{Message: &message}, nil
		}
	}
	lastTargetResponse := (*model.Message)(nil)
	if req != nil {
		lastTargetResponse = req.LastTargetResponse
	}
	nextMessage, err := c.generateWithRunner(ctx, lastTargetResponse)
	if err != nil {
		return nil, err
	}
	if containsStopSignal(nextMessage, c.stopSignal) {
		return &Decision{Stop: true}, nil
	}
	c.generatedInputs++
	message := model.NewUserMessage(nextMessage)
	return &Decision{Message: &message}, nil
}

// Close marks the conversation as closed.
func (c *conversation) Close() error {
	c.closed = true
	return nil
}

func (c *conversation) generateWithRunner(ctx context.Context, lastTargetResponse *model.Message) (string, error) {
	inputMessage := model.NewUserMessage("")
	if lastTargetResponse != nil {
		inputMessage = model.NewUserMessage(lastTargetResponse.Content)
	}
	events, err := c.simRunner.Run(
		ctx,
		c.simUserID,
		c.simSessionID,
		inputMessage,
		agent.WithInjectedContextMessages(c.injectedContextMessage),
		agent.WithRuntimeState(maps.Clone(c.initialSession.State)),
	)
	if err != nil {
		return "", fmt.Errorf("run sim runner: %w", err)
	}
	return collectFinalResponseContent(events)
}

func collectFinalResponseContent(events <-chan *event.Event) (string, error) {
	finalContent := ""
	invocationID := ""
	finalByInvID := make(map[string]string)
	fallbackFinal := ""
	eventErr := error(nil)
	hasFinalResponse := false
	for evt := range events {
		if evt == nil {
			continue
		}
		if evt.IsRunnerCompletion() {
			if evt.InvocationID != "" {
				invocationID = evt.InvocationID
			}
		} else if invocationID == "" && evt.InvocationID != "" {
			invocationID = evt.InvocationID
		}
		content, ok, err := eventFinalResponseContent(evt)
		if err != nil {
			eventErr = errors.Join(eventErr, err)
		} else if ok {
			hasFinalResponse = true
			if evt.IsRunnerCompletion() {
				finalContent = content
			} else if evt.InvocationID != "" {
				finalByInvID[evt.InvocationID] = content
			} else {
				fallbackFinal = content
			}
		}
		if evt.Error != nil {
			eventErr = errors.Join(eventErr, fmt.Errorf("event: %w", evt.Error))
		}
		if evt.Response != nil && evt.Response.Error != nil {
			eventErr = errors.Join(eventErr, fmt.Errorf("response error: %w", evt.Response.Error))
		}
	}
	if finalContent == "" && invocationID != "" {
		finalContent = finalByInvID[invocationID]
	}
	if finalContent == "" {
		finalContent = fallbackFinal
	}
	if !hasFinalResponse {
		if eventErr != nil {
			return "", errors.Join(errors.New("final response is missing"), eventErr)
		}
		return "", errors.New("final response is missing")
	}
	if strings.TrimSpace(finalContent) == "" {
		if eventErr != nil {
			return "", errors.Join(errors.New("final response content is empty"), eventErr)
		}
		return "", errors.New("final response content is empty")
	}
	if eventErr != nil {
		return "", eventErr
	}
	return finalContent, nil
}

func eventFinalResponseContent(evt *event.Event) (string, bool, error) {
	if evt == nil || !evt.IsFinalResponse() {
		return "", false, nil
	}
	if evt.Response == nil || len(evt.Response.Choices) == 0 {
		return "", false, errors.New("final response has no choices")
	}
	return evt.Response.Choices[0].Message.Content, true, nil
}

func buildScenarioContextMessages(
	ctx context.Context,
	scenario *evalset.ConversationScenario,
	builder SystemPromptBuilder,
) ([]model.Message, error) {
	if scenario == nil {
		return nil, nil
	}
	prompt := builder(ctx, scenario)
	if strings.TrimSpace(prompt) == "" {
		return nil, errors.New("system prompt is empty")
	}
	systemMessage := model.NewSystemMessage(prompt)
	return []model.Message{systemMessage}, nil
}

func buildDefaultSystemPrompt(ctx context.Context, scenario *evalset.ConversationScenario) string {
	parts := []string{
		"You are simulating the user in an evaluation conversation.",
		"Your job is to produce the single best next user message that moves the conversation toward the user's goal.",
		"",
		"What you will receive each turn:",
		"- The target agent's latest reply.",
		"- If that reply is empty, the conversation is just starting.",
		"",
		"How to respond:",
		"- Output exactly one next user message.",
		"- Write as the user would naturally speak in the conversation.",
		"- Treat the conversation plan as the primary instruction for how the simulated user should behave.",
		"- Follow the conversation plan closely when deciding the next user message.",
		"- Stay consistent with the user's goal, constraints, and prior direction.",
		"- Prefer the most helpful next step: answer the agent's question, provide missing details, ask for the next needed action, or correct the agent if needed.",
		"- Keep the message concise, but include enough detail to let the agent make progress.",
		"- Do not explain your reasoning.",
		"- Do not describe the conversation plan.",
		"- Do not output multiple turns.",
	}
	if scenario.StopSignal != "" {
		parts = append(parts, "- When the user's goal is already satisfied, output only this stop signal and nothing else: "+scenario.StopSignal+".")
	}
	if scenario.StartingPrompt != "" {
		parts = append(parts,
			"",
			"The conversation already started with this fixed first user message:",
			scenario.StartingPrompt,
		)
	}
	parts = append(parts, "", "Conversation plan:", scenario.ConversationPlan)
	return strings.Join(parts, "\n")
}

func buildEffectiveScenario(
	scenario *evalset.ConversationScenario,
	stopSignal string,
	maxAllowedInvocations int,
) *evalset.ConversationScenario {
	if scenario == nil {
		return nil
	}
	driver := scenario.Driver
	if driver == "" {
		driver = evalset.ConversationScenarioDriverActual
	}
	effectiveMaxAllowedInvocations := maxAllowedInvocations
	return &evalset.ConversationScenario{
		Driver:                driver,
		StartingPrompt:        scenario.StartingPrompt,
		ConversationPlan:      scenario.ConversationPlan,
		StopSignal:            strings.TrimSpace(stopSignal),
		MaxAllowedInvocations: &effectiveMaxAllowedInvocations,
	}
}

func containsStopSignal(content string, stopSignal string) bool {
	if stopSignal == "" {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(content), strings.TrimSpace(stopSignal))
}
