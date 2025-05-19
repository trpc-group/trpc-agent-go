// Package react provides the ReAct agent implementation.
package react

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent/agents"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/message"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

var (
	// ErrModelRequired is returned when a ReAct agent is initialized without a model.
	ErrModelRequired = errors.New("model is required for ReAct agent")

	// ErrNoToolsProvided is returned when no tools are provided to the ReAct agent.
	ErrNoToolsProvided = errors.New("ReAct agent requires at least one tool")

	// ErrThoughtGeneratorRequired is returned when ThoughtGenerator is not provided.
	ErrThoughtGeneratorRequired = errors.New("ThoughtGenerator is required")

	// ErrActionSelectorRequired is returned when ActionSelector is not provided.
	ErrActionSelectorRequired = errors.New("ActionSelector is required")

	// ErrResponseGeneratorRequired is returned when ResponseGenerator is not provided.
	ErrResponseGeneratorRequired = errors.New("ResponseGenerator is required")

	// ErrCycleManagerRequired is returned when CycleManager is not provided.
	ErrCycleManagerRequired = errors.New("CycleManager is required")

	// ErrMaxIterationsReached is returned when the agent exceeds max iterations.
	ErrMaxIterationsReached = errors.New("max iterations reached")
)

// ObservationType represents the type of observation from a tool.
type ObservationType string

const (
	// ObservationTypeText indicates the observation is plain text.
	ObservationTypeText ObservationType = "text"

	// ObservationTypeJSON indicates the observation is JSON data.
	ObservationTypeJSON ObservationType = "json"

	// ObservationTypeError indicates the observation is an error message.
	ObservationTypeError ObservationType = "error"
)

// Observation represents feedback from tool execution.
type Observation struct {
	// Type is the type of observation.
	Type ObservationType `json:"type"`

	// Content is the observation content.
	Content string `json:"content"`

	// ToolName is the name of the tool that produced this observation.
	ToolName string `json:"tool_name"`

	// Metadata contains additional information about the observation.
	Metadata map[string]interface{} `json:"metadata,omitempty"`
}

// NewTextObservation creates a new text observation.
func NewTextObservation(content, toolName string) *Observation {
	return &Observation{
		Type:     ObservationTypeText,
		Content:  content,
		ToolName: toolName,
	}
}

// NewJSONObservation creates a new JSON observation.
func NewJSONObservation(content, toolName string) *Observation {
	return &Observation{
		Type:     ObservationTypeJSON,
		Content:  content,
		ToolName: toolName,
	}
}

// NewErrorObservation creates a new error observation.
func NewErrorObservation(err error, toolName string) *Observation {
	return &Observation{
		Type:     ObservationTypeError,
		Content:  err.Error(),
		ToolName: toolName,
	}
}

// Step represents a single step in the ReAct thought process.
type Step struct {
	// Thought is the reasoning trace.
	Thought string `json:"thought,omitempty"`

	// Action is the name of the tool to execute.
	Action string `json:"action,omitempty"`

	// ActionParams are the parameters to pass to the tool.
	ActionParams map[string]interface{} `json:"action_params,omitempty"`

	// Observation is the result of executing the action.
	Observation *Observation `json:"observation,omitempty"`
}

// Memory extends the base memory to store ReAct-specific information.
type Memory interface {
	memory.Memory

	// StoreStep stores a ReAct step.
	StoreStep(ctx context.Context, step *Step) error

	// RetrieveSteps retrieves all ReAct steps.
	RetrieveSteps(ctx context.Context) ([]*Step, error)

	// LastStep retrieves the most recent ReAct step.
	LastStep(ctx context.Context) (*Step, error)
}

// AgentConfig contains configuration for a ReAct agent.
type AgentConfig struct {
	// Name of the agent.
	Name string `json:"name"`

	// Description of the agent.
	Description string `json:"description"`

	// Model to use for generating responses.
	Model model.Model `json:"model"`

	// Memory system to use for storing conversation history.
	Memory memory.Memory

	// MaxHistoryMessages is the maximum number of messages to include in context.
	MaxHistoryMessages int `json:"max_history_messages"`

	// SystemPrompt is the system prompt to use for the model.
	SystemPrompt string `json:"system_prompt"`

	// Tools are the tools available to the agent.
	Tools []tool.Tool `json:"tools"`

	// ThoughtFormat is the format for reasoning traces.
	ThoughtFormat ThoughtFormat `json:"thought_format"`

	// MaxIterations is the maximum number of thought-action-observation cycles.
	MaxIterations int `json:"max_iterations"`

	// EnableStreaming determines if the agent should stream responses.
	EnableStreaming bool `json:"enable_streaming"`

	// ThoughtGenerator is the generator for thoughts.
	ThoughtGenerator ThoughtGenerator

	// ActionSelector is the selector for actions.
	ActionSelector ActionSelector

	// ResponseGenerator is the generator for responses.
	ResponseGenerator ResponseGenerator

	// CycleManager is the manager for cycles.
	CycleManager CycleManager
}

// Agent is an agent that implements the ReAct paradigm.
type Agent struct {
	*agents.LLMAgent
	maxIterations        int
	reactMemory          Memory
	thoughtGenerator     ThoughtGenerator
	actionSelector       ActionSelector
	responseGenerator    ResponseGenerator
	cycleManager         CycleManager
	mu                   sync.RWMutex
	currentMaxIterations int
	config               AgentConfig
}

// NewAgent creates a new ReAct agent.
func NewAgent(config AgentConfig) (*Agent, error) {
	// Validate required components
	if err := validateReactAgentConfig(config); err != nil {
		return nil, err
	}

	// Set default values for optional fields
	config = setDefaultConfigValues(config)

	// Create LLM agent
	llmAgent, err := createLLMAgent(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create LLM agent: %w", err)
	}

	// Create or wrap ReactMemory
	reactMem := createReactMemory(config.Memory)

	return &Agent{
		LLMAgent:             llmAgent,
		maxIterations:        config.MaxIterations,
		reactMemory:          reactMem,
		thoughtGenerator:     config.ThoughtGenerator,
		actionSelector:       config.ActionSelector,
		responseGenerator:    config.ResponseGenerator,
		cycleManager:         config.CycleManager,
		currentMaxIterations: config.MaxIterations,
		config:               config,
	}, nil
}

// validateReactAgentConfig validates the configuration for a ReAct agent.
func validateReactAgentConfig(config AgentConfig) error {
	if config.Model == nil {
		return ErrModelRequired
	}
	return nil
}

// setDefaultConfigValues sets default values for optional configuration fields.
func setDefaultConfigValues(config AgentConfig) AgentConfig {
	if config.MaxIterations <= 0 {
		config.MaxIterations = 10
	}
	if config.SystemPrompt == "" {
		config.SystemPrompt = buildDefaultSystemPrompt(config.Tools)
	}
	if config.ThoughtGenerator == nil {
		thoughtPromptStrategy := NewDefaultThoughtPromptStrategy()
		config.ThoughtGenerator = NewLLMThoughtGenerator(
			config.Model,
			thoughtPromptStrategy,
			ThoughtFormatFree,
		)
	}
	if config.ActionSelector == nil {
		config.ActionSelector = NewLLMActionSelector(
			config.Model,
			NewDefaultActionPromptStrategy(),
		)
	}
	if config.ResponseGenerator == nil {
		config.ResponseGenerator = NewLLMResponseGenerator(
			config.Model,
			NewDefaultResponsePromptStrategy(true),
		)
	}
	if config.CycleManager == nil {
		config.CycleManager = NewInMemoryCycleManager()
	}
	if config.ThoughtFormat == "" {
		config.ThoughtFormat = ThoughtFormatFree
	}
	return config
}

// createLLMAgent creates an LLMAgent from the provided configuration.
func createLLMAgent(config AgentConfig) (*agents.LLMAgent, error) {
	llmConfig := agents.LLMAgentConfig{
		Name:               config.Name,
		Description:        config.Description,
		Model:              config.Model,
		Memory:             config.Memory,
		MaxHistoryMessages: config.MaxHistoryMessages,
		SystemPrompt:       config.SystemPrompt,
		Tools:              config.Tools,
		EnableStreaming:    config.EnableStreaming,
	}

	return agents.NewLLMAgent(llmConfig)
}

// createReactMemory creates or wraps a ReactMemory from the provided Memory.
func createReactMemory(mem memory.Memory) Memory {
	// If memory already implements ReactMemory, use it directly
	if rm, ok := mem.(Memory); ok {
		return rm
	}

	// If memory is provided but doesn't implement ReactMemory, wrap it
	if mem != nil {
		return NewReactMemoryWrapper(mem)
	}

	// Create a new base ReactMemory if none is provided
	return NewBaseReactMemory()
}

// GetModel returns the model of the agent.
func (a *Agent) GetModel() model.Model {
	return a.LLMAgent.GetModel()
}

// Tools returns the tools available to the agent.
func (a *Agent) Tools() []tool.Tool {
	return a.config.Tools
}

// GetTools returns the tools available to the agent (alias for Tools() for IReActAgent interface).
func (a *Agent) GetTools() []tool.Tool {
	return a.Tools()
}

// Run executes the ReAct agent.
func (a *Agent) Run(ctx context.Context, msg *message.Message) (*message.Message, error) {
	a.mu.Lock()
	a.currentMaxIterations = a.maxIterations
	a.mu.Unlock()

	log.Infof("Starting ReAct agent execution")

	// Get tools
	tools := a.Tools()
	if len(tools) == 0 {
		log.Errorf("No tools available")
		return nil, fmt.Errorf("no tools available")
	}

	// Initialize vars
	var latestThought *Thought
	var finalResponse *message.Message

	// Main agent loop - continue until we reach max cycles or encounter a terminating condition
	for i := 0; i < a.MaxIterations(); i++ {
		log.Debugf("Starting cycle %d of %d", i+1, a.MaxIterations())

		// Process a single cycle
		thought, response, shouldBreak, err := a.processSingleCycle(ctx, msg, tools, i)
		if err != nil {
			return nil, err
		}

		latestThought = thought

		if response != nil {
			finalResponse = response
		}

		if shouldBreak {
			break
		}
	}

	// If we have a final response, return it
	if finalResponse != nil {
		return finalResponse, nil
	}

	// If we reached this point without a response, generate one from the last thought or cycles
	if latestThought != nil {
		return message.NewAssistantMessage(latestThought.Content), nil
	}

	// Fallback to generating a response from all cycles
	return a.generateResponseFromCycles(ctx)
}

// processSingleCycle handles a single cycle of the ReAct agent.
// Returns the thought, a possible response message, a flag indicating if the loop should break,
// and any error that occurred.
func (a *Agent) processSingleCycle(
	ctx context.Context,
	msg *message.Message,
	tools []tool.Tool,
	cycleIndex int,
) (*Thought, *message.Message, bool, error) {
	// Get updated cycle count
	cycles, err := a.GetHistory(ctx)
	if err != nil {
		log.Warnf("Failed to get cycle history: %v", err)
	}

	// Generate thought
	log.Debugf("Generating thought (cycle %d)", cycleIndex+1)
	thought, err := a.thoughtGenerator.Generate(ctx, []*message.Message{msg}, cycles, tools)
	if err != nil {
		log.Errorf("Failed to generate thought: %v", err)
		return nil, nil, false, fmt.Errorf("failed to generate thought: %w", err)
	}

	// Record the thought and start new cycle
	log.Debugf("Recording thought and starting new cycle")
	if err = a.cycleManager.StartCycle(ctx, thought); err != nil {
		log.Errorf("Failed to start cycle: %v", err)
		return nil, nil, false, fmt.Errorf("failed to start cycle: %w", err)
	}
	defer a.cycleManager.EndCycle(ctx)

	// Check if thought contains a final answer directly
	if containsFinalAnswer(thought) {
		log.Infof("Thought contains final answer, returning it directly")
		finalResponse := message.NewAssistantMessage(extractFinalAnswer(thought.Content))
		return thought, finalResponse, true, nil
	}

	// Select multiple actions
	log.Debugf("Selecting actions for thought: %s", thought.ID)
	actions, err := a.actionSelector.Select(ctx, thought, tools)
	if err != nil {
		log.Errorf("Failed to select actions: %v", err)
		return nil, nil, false, fmt.Errorf("failed to select actions: %w", err)
	}

	// Record the actions
	if err := a.cycleManager.RecordActions(ctx, actions); err != nil {
		log.Errorf("Failed to record actions: %v", err)
		return nil, nil, false, fmt.Errorf("failed to record actions: %w", err)
	}

	// Handle final_answer actions if present
	for _, action := range actions {
		if action.ToolName == "final_answer" {
			return a.handleFinalAnswerAction(ctx, thought, action)
		}
	}

	// Execute all tools in parallel
	_, err = a.executeMultipleToolActions(ctx, actions)
	if err != nil {
		return thought, nil, false, err
	}
	return thought, nil, false, nil
}

// executeMultipleToolActions executes multiple tools in parallel.
// Returns the collected observations and any error that occurred.
func (a *Agent) executeMultipleToolActions(ctx context.Context, actions []*Action) ([]*CycleObservation, error) {
	if len(actions) == 0 {
		return nil, nil
	}

	// If only one action, use the existing single tool execution
	if len(actions) == 1 {
		obs, err := a.executeToolAction(ctx, actions[0])
		if err != nil {
			return nil, err
		}
		return []*CycleObservation{obs}, nil
	}

	// Execute multiple tools in parallel
	log.Infof("Executing %d tools in parallel", len(actions))

	// Create wait group to synchronize goroutines
	var wg sync.WaitGroup
	observations := make([]*CycleObservation, len(actions))
	errors := make([]error, len(actions))

	// Launch a goroutine for each tool
	for i, action := range actions {
		wg.Add(1)
		go func(idx int, act *Action) {
			defer wg.Done()

			// Find the tool
			selectedTool, found := a.findTool(act.ToolName)
			if !found {
				log.Errorf("Tool '%s' not found", act.ToolName)

				// Create error observation
				observations[idx] = &CycleObservation{
					ID:       fmt.Sprintf("obs-%d", time.Now().UnixNano()),
					ActionID: act.ID,
					ToolOutput: map[string]interface{}{
						"error": fmt.Sprintf("tool %s not found", act.ToolName),
					},
					IsError:   true,
					Timestamp: time.Now().Unix(),
				}
				return
			}

			// Execute the tool
			log.Debugf("Executing tool '%s' with input: %v", selectedTool.Name(), act.ToolInput)
			result, err := selectedTool.Execute(ctx, act.ToolInput)

			// Create observation based on result or error
			if err != nil {
				// Tool execution resulted in an error
				log.Warnf("Tool execution failed: %v", err)
				observations[idx] = &CycleObservation{
					ID:       fmt.Sprintf("obs-%d", time.Now().UnixNano()),
					ActionID: act.ID,
					ToolOutput: map[string]interface{}{
						"error": err.Error(),
					},
					IsError:   true,
					Timestamp: time.Now().Unix(),
				}
				errors[idx] = err
			} else {
				// Tool executed successfully
				observations[idx] = &CycleObservation{
					ID:       fmt.Sprintf("obs-%d", time.Now().UnixNano()),
					ActionID: act.ID,
					ToolOutput: map[string]interface{}{
						"output": result.Output,
					},
					IsError:   false,
					Timestamp: time.Now().Unix(),
				}
			}
		}(i, action)
	}

	// Wait for all tool executions to complete
	wg.Wait()

	// Record all observations at once
	if err := a.cycleManager.RecordObservations(ctx, observations); err != nil {
		log.Errorf("Failed to record observations: %v", err)
		return nil, fmt.Errorf("failed to record observations: %w", err)
	}

	// Check if any errors occurred during execution
	var firstError error
	for _, err := range errors {
		if err != nil {
			firstError = err
			break
		}
	}

	return observations, firstError
}

// handleFinalAnswerAction processes a final_answer action.
// Returns the thought, response message, whether to break the loop, and any error.
func (a *Agent) handleFinalAnswerAction(
	ctx context.Context,
	thought *Thought,
	action *Action,
) (*Thought, *message.Message, bool, error) {
	content, ok := action.ToolInput["content"].(string)
	if !ok {
		content = "I've completed my analysis."
	}
	log.Infof("Final answer action detected, returning response to user")
	finalResponse := message.NewAssistantMessage(content)

	// Create a successful observation for the final answer
	observation := &CycleObservation{
		ID:       fmt.Sprintf("obs-%d", time.Now().UnixNano()),
		ActionID: action.ID,
		ToolOutput: map[string]interface{}{
			"output": content,
		},
		IsError:   false,
		Timestamp: time.Now().Unix(),
	}

	// Record the observation
	if err := a.cycleManager.RecordObservations(ctx, []*CycleObservation{observation}); err != nil {
		log.Warnf("Failed to record final answer observation: %v", err)
	}

	// End the cycle
	if _, err := a.cycleManager.EndCycle(ctx); err != nil {
		log.Warnf("Failed to end cycle after final answer: %v", err)
	}

	return thought, finalResponse, true, nil
}

// executeToolAction finds and executes a tool based on the given action.
// Returns an observation and any error that occurred.
func (a *Agent) executeToolAction(ctx context.Context, action *Action) (*CycleObservation, error) {
	// Find the tool
	selectedTool, found := a.findTool(action.ToolName)
	if !found {
		log.Errorf("Tool '%s' not found", action.ToolName)

		// Create error observation
		observation := &CycleObservation{
			ID:       fmt.Sprintf("obs-%d", time.Now().UnixNano()),
			ActionID: action.ID,
			ToolOutput: map[string]interface{}{
				"error": fmt.Sprintf("tool %s not found", action.ToolName),
			},
			IsError:   true,
			Timestamp: time.Now().Unix(),
		}

		// Record the observation
		if err := a.cycleManager.RecordObservations(ctx, []*CycleObservation{observation}); err != nil {
			log.Errorf("Failed to record observation: %v", err)
			return nil, fmt.Errorf("failed to record observation: %w", err)
		}

		// End the cycle
		if _, err := a.cycleManager.EndCycle(ctx); err != nil {
			log.Errorf("Failed to end cycle: %v", err)
			return nil, fmt.Errorf("failed to end cycle: %w", err)
		}

		return observation, nil
	}

	// Execute the tool
	log.Debugf("Executing tool '%s' with input: %v", selectedTool.Name(), action.ToolInput)
	result, err := selectedTool.Execute(ctx, action.ToolInput)

	// Create observation based on result or error
	var observation *CycleObservation
	if err != nil {
		// Tool execution resulted in an error
		log.Warnf("Tool execution failed: %v", err)
		observation = &CycleObservation{
			ID:       fmt.Sprintf("obs-%d", time.Now().UnixNano()),
			ActionID: action.ID,
			ToolOutput: map[string]interface{}{
				"error": err.Error(),
			},
			IsError:   true,
			Timestamp: time.Now().Unix(),
		}
	} else {
		// Tool executed successfully
		observation = &CycleObservation{
			ID:       fmt.Sprintf("obs-%d", time.Now().UnixNano()),
			ActionID: action.ID,
			ToolOutput: map[string]interface{}{
				"output": result.Output,
			},
			IsError:   false,
			Timestamp: time.Now().Unix(),
		}
	}

	// Record the observation
	log.Debugf("Recording observation")
	if err := a.cycleManager.RecordObservations(ctx, []*CycleObservation{observation}); err != nil {
		log.Errorf("Failed to record observation: %v", err)
		return nil, fmt.Errorf("failed to record observation: %w", err)
	}

	// End the current cycle
	log.Debugf("Ending current cycle")
	if _, err := a.cycleManager.EndCycle(ctx); err != nil {
		log.Errorf("Failed to end cycle: %v", err)
		return nil, fmt.Errorf("failed to end cycle: %w", err)
	}

	return observation, nil
}

// Helper functions for detecting final answers

// containsFinalAnswer checks if thought content contains explicit final answer markers.
func containsFinalAnswer(thought *Thought) bool {
	if len(thought.SuggestedActions) > 0 && thought.SuggestedActions[0].ToolName == "final_answer" {
		return true
	}

	lowerContent := strings.ToLower(thought.Content)

	// Common final answer patterns
	patterns := []string{
		"final answer:",
		"my final answer is",
		"in conclusion,",
		"to summarize,",
		"i've completed my analysis",
		"the answer is",
	}

	for _, pattern := range patterns {
		if strings.Contains(lowerContent, pattern) {
			return true
		}
	}

	return false
}

// extractFinalAnswer attempts to extract the final answer from thought content.
func extractFinalAnswer(content string) string {
	lowerContent := strings.ToLower(content)

	// Common start markers for the final answer section
	startMarkers := []string{
		"final answer:",
		"my final answer is",
		"in conclusion,",
		"to summarize,",
	}

	// Try to find and extract content after any of the markers
	for _, marker := range startMarkers {
		if idx := strings.Index(lowerContent, marker); idx != -1 {
			markerEnd := idx + len(marker)
			if markerEnd < len(content) {
				return strings.TrimSpace(content[markerEnd:])
			}
		}
	}

	// If no markers found, return the last paragraph which often contains the conclusion
	paragraphs := strings.Split(content, "\n\n")
	if len(paragraphs) > 0 {
		return strings.TrimSpace(paragraphs[len(paragraphs)-1])
	}

	// Fallback to returning the whole thought
	return content
}

// generateResponseFromContent generates a response from the given content.
func (a *Agent) generateResponseFromContent(ctx context.Context, content string) (*message.Message, error) {
	// Extract the final answer part if present
	answer := content
	if idx := strings.Index(strings.ToLower(content), "final answer:"); idx != -1 {
		answer = strings.TrimSpace(content[idx+13:]) // 13 is length of "final answer:"
	} else if idx := strings.Index(strings.ToLower(content), "answer:"); idx != -1 {
		answer = strings.TrimSpace(content[idx+7:]) // 7 is length of "answer:"
	}

	// Create and return the response message
	return message.NewAssistantMessage(answer), nil
}

// generateResponseFromCycles generates a response using all the available cycles.
func (a *Agent) generateResponseFromCycles(ctx context.Context) (*message.Message, error) {
	// Get all cycles
	cycles, err := a.cycleManager.GetHistory(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get cycles: %w", err)
	}

	if len(cycles) == 0 {
		return message.NewAssistantMessage("I wasn't able to process your request effectively. Could you provide more details?"), nil
	}

	// Use the response generator if available
	if a.responseGenerator != nil {
		// Create empty history for the ResponseGenerator - it's expected but not used in our implementation
		emptyHistory := []*message.Message{}
		return a.responseGenerator.Generate(ctx, "", emptyHistory, cycles)
	}

	// Fallback: Summarize last thought or combine all observations
	lastCycle := cycles[len(cycles)-1]
	if lastCycle.Thought != nil {
		return a.generateResponseFromContent(ctx, lastCycle.Thought.Content)
	}

	// Create a summary of all observations if no final thought is available
	return a.generateSummaryFromObservations(cycles)
}

// generateSummaryFromObservations creates a summary response from all observations in the cycles.
func (a *Agent) generateSummaryFromObservations(cycles []*Cycle) (*message.Message, error) {
	var summary strings.Builder
	summary.WriteString("Based on my analysis:\n\n")

	for i, cycle := range cycles {
		for idx, observation := range cycle.Observations {
			action := cycle.Actions[idx]
			content := a.extractContentFromObservation(observation)

			if action != nil {
				summary.WriteString(fmt.Sprintf("%d. Observation %d for %s: %s\n",
					i+1, idx+1, action.ToolName, content))
			} else {
				summary.WriteString(fmt.Sprintf("%d. Observation %d: %s\n",
					i+1, idx+1, content))
			}
		}
	}

	summary.WriteString("\nThis is what I've been able to determine from the available information.")
	return message.NewAssistantMessage(summary.String()), nil
}

// extractContentFromObservation extracts the content from an observation.
func (a *Agent) extractContentFromObservation(observation *CycleObservation) string {
	if observation.IsError {
		if errMsg, ok := observation.ToolOutput["error"]; ok {
			return fmt.Sprintf("%v", errMsg)
		}
		return "An error occurred"
	}

	if output, ok := observation.ToolOutput["output"]; ok {
		return fmt.Sprintf("%v", output)
	}
	return "Tool executed successfully"
}

// RunAsync processes the given message asynchronously and returns a channel of events.
func (a *Agent) RunAsync(ctx context.Context, msg *message.Message) (<-chan *event.Event, error) {
	eventCh := make(chan *event.Event)

	// Reset the current max iterations
	a.mu.Lock()
	a.currentMaxIterations = a.maxIterations
	a.mu.Unlock()

	// Register tools with model if it supports function calling
	a.registerToolsWithModel()

	// Get current cycles for thought generation
	cycles, err := a.cycleManager.GetHistory(ctx)
	if err != nil {
		log.Infof("Warning: failed to get cycle history: %v", err)
		// Continue with empty history if retrieval fails
		cycles = []*Cycle{}
	}

	go func() {
		defer close(eventCh)
		a.runAsyncLoop(ctx, msg, cycles, eventCh)
	}()

	return eventCh, nil
}

// registerToolsWithModel registers tools with the model if it supports tool calls.
func (a *Agent) registerToolsWithModel() {
	if toolModel, ok := a.GetModel().(model.ToolCallSupportingModel); ok && toolModel.SupportsToolCalls() {
		var toolDefs []*tool.ToolDefinition
		for _, t := range a.Tools() {
			toolDefs = append(toolDefs, t.GetDefinition())
		}

		if err := toolModel.RegisterTools(toolDefs); err != nil {
			log.Infof("Warning: failed to register tools with model: %v", err)
		}
	}
}

// runAsyncLoop is the main loop for asynchronous reasoning cycles.
func (a *Agent) runAsyncLoop(
	ctx context.Context,
	msg *message.Message,
	cycles []*Cycle,
	eventCh chan<- *event.Event,
) {
	// Begin the reasoning cycle
	for i := 0; i < a.MaxIterations(); i++ {
		// Check if the context has been canceled
		if ctx.Err() != nil {
			eventCh <- event.NewErrorEvent(ctx.Err(), 500)
			return
		}

		// Process a single async cycle
		shouldContinue, err := a.processSingleAsyncCycle(ctx, msg, eventCh)
		if err != nil {
			eventCh <- event.NewErrorEvent(err, 500)
			return
		}

		if !shouldContinue {
			return
		}
	}

	// If we've reached the maximum number of iterations, generate a response
	// based on all the information we've gathered
	finalResp, err := a.generateResponseFromCycles(ctx)
	if err != nil {
		eventCh <- event.NewErrorEvent(fmt.Errorf("failed to generate response from cycles: %w", err), 500)
		return
	}
	eventCh <- event.NewMessageEvent(finalResp)
}

// processSingleAsyncCycle processes a single cycle in the async execution mode.
// Returns whether to continue with more cycles and any error that occurred.
func (a *Agent) processSingleAsyncCycle(
	ctx context.Context,
	msg *message.Message,
	eventCh chan<- *event.Event,
) (bool, error) {
	// Generate a thought based on the input
	userMsgs := []*message.Message{msg}
	cycles, err := a.GetHistory(ctx)
	if err != nil {
		return false, fmt.Errorf("failed to get cycle history: %w", err)
	}

	thought, err := a.thoughtGenerator.Generate(ctx, userMsgs, cycles, a.Tools())
	if err != nil {
		return false, fmt.Errorf("failed to generate thought: %w", err)
	}

	// Emit a thought event - use custom event for thinking
	eventCh <- event.NewCustomEvent("thinking", thought.Content)

	// Record the thought
	if err = a.cycleManager.StartCycle(ctx, thought); err != nil {
		return false, fmt.Errorf("failed to start cycle: %w", err)
	}
	defer a.cycleManager.EndCycle(ctx)

	// Update cycles
	cycle, err := a.cycleManager.CurrentCycle(ctx)
	if err != nil {
		return false, fmt.Errorf("failed to get current cycle: %w", err)
	}
	cycles = append(cycles, cycle)

	// If the thought contains a final answer, stop and return it
	if containsFinalAnswer(thought) {
		return a.handleAsyncFinalThought(ctx, thought, eventCh)
	}

	// Select actions for the thought
	log.Debugf("Selecting actions for thought: %s", thought.ID)
	actions, err := a.actionSelector.Select(ctx, thought, a.Tools())
	if err != nil {
		return a.handleAsyncActionError(ctx, thought, err, eventCh)
	}

	// Record the actions
	if err := a.cycleManager.RecordActions(ctx, actions); err != nil {
		log.Errorf("Failed to record actions: %v", err)
		return false, fmt.Errorf("failed to record actions: %w", err)
	}

	// Emit events for each action
	for _, action := range actions {
		a.emitActionEvent(action, eventCh)

		// Special handling for final_answer action
		if action.ToolName == "final_answer" {
			return a.handleAsyncFinalAnswerAction(ctx, action, eventCh)
		}
	}

	// Execute all tools in parallel
	observations, err := a.executeAsyncMultipleTools(ctx, actions, eventCh)
	if err != nil {
		// If an error occurred but we still have observations, continue
		if len(observations) > 0 {
			return true, nil
		}
		return false, err
	}

	return true, nil
}

// emitActionEvent emits an event for an action.
func (a *Agent) emitActionEvent(action *Action, eventCh chan<- *event.Event) {
	// Emit an action event - use custom event for tool usage
	actionJSON, _ := json.Marshal(action.ToolInput)
	toolEvent := event.NewEvent(event.TypeTool, nil)
	toolEvent.SetMetadata("tool_name", action.ToolName)
	toolEvent.SetMetadata("input", string(actionJSON))
	eventCh <- toolEvent
}

// executeAsyncMultipleTools executes multiple tools in parallel asynchronously.
// It emits events for each observation and returns all observations and any error.
func (a *Agent) executeAsyncMultipleTools(
	ctx context.Context,
	actions []*Action,
	eventCh chan<- *event.Event,
) ([]*CycleObservation, error) {
	if len(actions) == 0 {
		return nil, nil
	}

	// If only one action, execute it directly
	if len(actions) == 1 {
		action := actions[0]
		selectedTool, found := a.findTool(action.ToolName)
		if !found {
			log.Errorf("Tool '%s' not found", action.ToolName)

			// Create error observation
			observationContent := fmt.Sprintf("Error: tool %s not found", action.ToolName)
			observation := &CycleObservation{
				ID:       fmt.Sprintf("obs-%d", time.Now().UnixNano()),
				ActionID: action.ID,
				ToolOutput: map[string]interface{}{
					"error": observationContent,
				},
				IsError:   true,
				Timestamp: time.Now().Unix(),
			}

			// Emit an observation event
			observationEvent := event.NewCustomEvent("observation", observationContent)
			eventCh <- observationEvent

			// Record the observation
			if err := a.cycleManager.RecordObservations(ctx, []*CycleObservation{observation}); err != nil {
				return []*CycleObservation{observation}, fmt.Errorf("failed to record observation: %w", err)
			}

			return []*CycleObservation{observation}, nil
		}

		// Execute the tool and handle result
		result, err := selectedTool.Execute(ctx, action.ToolInput)

		var observation *CycleObservation
		var observationContent string

		if err != nil {
			// Create error observation
			observationContent = fmt.Sprintf("Error: %v", err)
			observation = &CycleObservation{
				ID:       fmt.Sprintf("obs-%d", time.Now().UnixNano()),
				ActionID: action.ID,
				ToolOutput: map[string]interface{}{
					"error": observationContent,
				},
				IsError:   true,
				Timestamp: time.Now().Unix(),
			}
		} else {
			// Create success observation
			observationContent = result.String()
			observation = &CycleObservation{
				ID:       fmt.Sprintf("obs-%d", time.Now().UnixNano()),
				ActionID: action.ID,
				ToolOutput: map[string]interface{}{
					"output": observationContent,
				},
				IsError:   false,
				Timestamp: time.Now().Unix(),
			}
		}

		// Emit an observation event
		observationEvent := event.NewCustomEvent("observation", observationContent)
		eventCh <- observationEvent

		// Record the observation
		if recordErr := a.cycleManager.RecordObservations(ctx, []*CycleObservation{observation}); recordErr != nil {
			return []*CycleObservation{observation}, fmt.Errorf("failed to record observation: %w", recordErr)
		}

		return []*CycleObservation{observation}, err
	}

	// Execute multiple tools in parallel
	log.Infof("Executing %d tools in parallel asynchronously", len(actions))

	// Create wait group to synchronize goroutines
	var wg sync.WaitGroup
	observations := make([]*CycleObservation, len(actions))
	errors := make([]error, len(actions))

	// Launch a goroutine for each tool
	for i, action := range actions {
		wg.Add(1)
		go func(idx int, act *Action) {
			defer wg.Done()

			// Find the tool
			selectedTool, found := a.findTool(act.ToolName)
			if !found {
				log.Errorf("Tool '%s' not found", act.ToolName)

				// Create error observation
				observationContent := fmt.Sprintf("Error: tool %s not found", act.ToolName)
				observation := &CycleObservation{
					ID:       fmt.Sprintf("obs-%d", time.Now().UnixNano()),
					ActionID: act.ID,
					ToolOutput: map[string]interface{}{
						"error": observationContent,
					},
					IsError:   true,
					Timestamp: time.Now().Unix(),
				}

				// Emit an observation event
				observationEvent := event.NewCustomEvent("observation", observationContent)
				eventCh <- observationEvent

				observations[idx] = observation
				return
			}

			// Execute the tool
			log.Debugf("Executing tool '%s' with input: %v", selectedTool.Name(), act.ToolInput)
			result, err := selectedTool.Execute(ctx, act.ToolInput)

			// Create observation based on result or error
			var observationContent string
			if err != nil {
				// Tool execution resulted in an error
				log.Warnf("Tool execution failed: %v", err)
				observationContent = fmt.Sprintf("Error: %v", err)
				observations[idx] = &CycleObservation{
					ID:       fmt.Sprintf("obs-%d", time.Now().UnixNano()),
					ActionID: act.ID,
					ToolOutput: map[string]interface{}{
						"error": observationContent,
					},
					IsError:   true,
					Timestamp: time.Now().Unix(),
				}
				errors[idx] = err
			} else {
				// Tool executed successfully
				observationContent = result.String()
				observations[idx] = &CycleObservation{
					ID:       fmt.Sprintf("obs-%d", time.Now().UnixNano()),
					ActionID: act.ID,
					ToolOutput: map[string]interface{}{
						"output": observationContent,
					},
					IsError:   false,
					Timestamp: time.Now().Unix(),
				}
			}

			// Emit an observation event
			observationEvent := event.NewCustomEvent("observation", observationContent)
			eventCh <- observationEvent
		}(i, action)
	}

	// Wait for all tool executions to complete
	wg.Wait()

	// Record all observations at once
	if err := a.cycleManager.RecordObservations(ctx, observations); err != nil {
		log.Errorf("Failed to record observations: %v", err)
		return observations, fmt.Errorf("failed to record observations: %w", err)
	}

	// Check if any errors occurred during execution
	var firstError error
	for _, err := range errors {
		if err != nil {
			firstError = err
			break
		}
	}

	return observations, firstError
}

// handleFinalAnswerAction processes a final_answer action.
// Returns the thought, response message, whether to break the loop, and any error.
func (a *Agent) handleAsyncFinalAnswerAction(
	ctx context.Context,
	action *Action,
	eventCh chan<- *event.Event,
) (bool, error) {
	content, ok := action.ToolInput["content"].(string)
	if !ok {
		content = "I've completed my analysis."
	}
	log.Infof("Final answer action detected, returning response to user")
	finalResponse := message.NewAssistantMessage(content)
	eventCh <- event.NewMessageEvent(finalResponse)

	// Create a successful observation for the final answer
	observation := &CycleObservation{
		ID:       fmt.Sprintf("obs-%d", time.Now().UnixNano()),
		ActionID: action.ID,
		ToolOutput: map[string]interface{}{
			"output": content,
		},
		IsError:   false,
		Timestamp: time.Now().Unix(),
	}

	// Record the observation
	if err := a.cycleManager.RecordObservations(ctx, []*CycleObservation{observation}); err != nil {
		log.Warnf("Failed to record final answer observation: %v", err)
	}

	// End the cycle
	if _, err := a.cycleManager.EndCycle(ctx); err != nil {
		log.Warnf("Failed to end cycle after final answer: %v", err)
	}

	return false, nil
}

// handleAsyncFinalThought handles a thought that contains a final answer.
// Returns whether to continue execution and any error.
func (a *Agent) handleAsyncFinalThought(
	ctx context.Context,
	thought *Thought,
	eventCh chan<- *event.Event,
) (bool, error) {
	answer := extractFinalAnswer(thought.Content)
	// Generate a final response
	finalResp, err := a.generateResponseFromContent(ctx, answer)
	if err != nil {
		return false, fmt.Errorf("failed to generate final response: %w", err)
	}
	eventCh <- event.NewMessageEvent(finalResp)
	return false, nil // Stop processing more cycles
}

// handleAsyncActionError handles errors that occur when selecting an action.
// Returns whether to continue execution and any error.
func (a *Agent) handleAsyncActionError(
	ctx context.Context,
	thought *Thought,
	actionErr error,
	eventCh chan<- *event.Event,
) (bool, error) {
	// If we fail to select an action, we'll generate a response based on the thought
	finalResp, genErr := a.generateResponseFromContent(ctx, fmt.Sprintf(
		"I tried to analyze this but faced some difficulties: %v. Based on what I've understood so far: %s",
		actionErr, thought.Content))
	if genErr != nil {
		return false, fmt.Errorf("failed to generate error response: %w", genErr)
	}
	eventCh <- event.NewMessageEvent(finalResp)
	return false, nil // Stop processing more cycles
}

// MaxIterations returns the maximum number of ReAct cycles.
func (a *Agent) MaxIterations() int {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.currentMaxIterations
}

// SetMaxIterations sets the maximum number of ReAct cycles.
func (a *Agent) SetMaxIterations(maxIterations int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if maxIterations > 0 {
		a.currentMaxIterations = maxIterations
	}
}

// EndCycle ends the current cycle via the cycle manager.
func (a *Agent) EndCycle(ctx context.Context) (*Cycle, error) {
	return a.cycleManager.EndCycle(ctx)
}

// GetHistory retrieves cycle history from the cycle manager.
func (a *Agent) GetHistory(ctx context.Context) ([]*Cycle, error) {
	return a.cycleManager.GetHistory(ctx)
}

// CurrentCycle gets the current cycle from the cycle manager.
func (a *Agent) CurrentCycle(ctx context.Context) (*Cycle, error) {
	return a.cycleManager.CurrentCycle(ctx)
}

// Ensure ReActAgent implements IReActAgent.
var _ IReActAgent = (*Agent)(nil)

// RunReActCycle executes a single Thought-Action-Observation cycle.
// It takes the current conversation history and returns the updated history
// including the new thought, action, and observation.
func (a *Agent) RunReActCycle(ctx context.Context, history []*message.Message) (*Cycle, []*message.Message, error) {
	// Get current cycles for thought generation
	cycles, err := a.cycleManager.GetHistory(ctx)
	if err != nil {
		return nil, history, fmt.Errorf("failed to get cycle history: %w", err)
	}

	// Generate and record thought
	cycle, updatedHistory, shouldReturn, err := a.generateAndRecordThought(ctx, history, cycles)
	if shouldReturn || err != nil {
		return cycle, updatedHistory, err
	}

	// Select and record actions
	actions, updatedHistory, shouldReturn, err := a.selectAndRecordAction(ctx, cycle, history)
	if shouldReturn || err != nil {
		return cycle, updatedHistory, err
	}

	// Execute actions and record observations
	return a.executeActionsAndRecordObservations(ctx, actions, history)
}

// executeActionsAndRecordObservations executes multiple actions and records their observations.
// Returns the updated cycle, updated history, and any error.
func (a *Agent) executeActionsAndRecordObservations(
	ctx context.Context,
	actions []*Action,
	history []*message.Message,
) (*Cycle, []*message.Message, error) {
	// If there are no actions, just return
	if len(actions) == 0 {
		return nil, history, fmt.Errorf("no actions to execute")
	}

	// Execute multiple actions in parallel
	observations, err := a.executeMultipleToolActions(ctx, actions)
	if err != nil {
		// End the cycle before returning error
		a.cycleManager.EndCycle(ctx)
		return nil, history, fmt.Errorf("failed to execute actions: %w", err)
	}

	// End the current cycle
	updatedCycle, endErr := a.cycleManager.EndCycle(ctx)
	if endErr != nil {
		return nil, history, fmt.Errorf("failed to end cycle: %w", endErr)
	}

	// Add each observation to history as messages
	updatedHistory := history
	for i, obs := range observations {
		action := actions[i]
		actionName := action.ToolName

		// If action name wasn't found, use a generic one
		if actionName == "" {
			actionName = "tool"
		}

		// Create tool message
		toolMsg := message.NewToolMessage(actionName)

		// Set content based on observation
		if obs.IsError {
			if errContent, ok := obs.ToolOutput["error"].(string); ok {
				toolMsg.Content = errContent
			} else {
				toolMsg.Content = fmt.Sprintf("%v", obs.ToolOutput["error"])
			}
		} else {
			if output, ok := obs.ToolOutput["output"].(string); ok {
				toolMsg.Content = output
			} else {
				toolMsg.Content = fmt.Sprintf("%v", obs.ToolOutput["output"])
			}
		}
		updatedHistory = append(updatedHistory, toolMsg)
	}
	return updatedCycle, updatedHistory, nil
}

// generateAndRecordThought generates a thought and records it in a new cycle.
// Returns the cycle, updated history, whether to return early, and any error.
func (a *Agent) generateAndRecordThought(
	ctx context.Context,
	history []*message.Message,
	cycles []*Cycle,
) (*Cycle, []*message.Message, bool, error) {
	// Find the last message to use for thought generation
	lastMsg := findLatestUserMessage(history)
	if lastMsg == nil && len(history) > 0 {
		lastMsg = history[len(history)-1]
	}

	// Generate a thought based on the input
	thought, err := a.thoughtGenerator.Generate(ctx, history, cycles, a.Tools())
	if err != nil {
		return nil, history, false, fmt.Errorf("failed to generate thought: %w", err)
	}

	// Record the thought and start new cycle
	err = a.cycleManager.StartCycle(ctx, thought)
	if err != nil {
		return nil, history, false, fmt.Errorf("failed to start cycle: %w", err)
	}

	// Get the current cycle after starting it
	cycle, err := a.cycleManager.CurrentCycle(ctx)
	if err != nil {
		return nil, history, false, fmt.Errorf("failed to get current cycle: %w", err)
	}

	// If the thought contains a final answer, stop and return it
	if containsFinalAnswer(thought) {
		// Add the thought to history as a message
		respMsg := message.NewAssistantMessage(extractFinalAnswer(thought.Content))
		updatedHistory := append(history, respMsg)

		// End the cycle before returning
		if _, endErr := a.cycleManager.EndCycle(ctx); endErr != nil {
			log.Warnf("Failed to end cycle for final answer: %v", endErr)
			// Continue anyway as we have the response
		}
		return cycle, updatedHistory, true, nil
	}
	return cycle, history, false, nil
}

// selectAndRecordAction selects an action based on the thought and records it.
// Returns the action, updated history, whether to return early, and any error.
func (a *Agent) selectAndRecordAction(
	ctx context.Context,
	cycle *Cycle,
	history []*message.Message,
) ([]*Action, []*message.Message, bool, error) {
	// Select actions based on the thought
	actions, err := a.actionSelector.Select(ctx, cycle.Thought, a.Tools())
	if err != nil {
		// If we fail to select actions, create a response with error info
		errorMsg := fmt.Sprintf("Failed to select actions: %v", err)
		respMsg := message.NewAssistantMessage(errorMsg)
		updatedHistory := append(history, respMsg)

		// End the cycle before returning
		if _, endErr := a.cycleManager.EndCycle(ctx); endErr != nil {
			log.Warnf("Failed to end cycle after action selection error: %v", endErr)
		}

		return nil, updatedHistory, true, nil
	}

	// Record the actions
	if err := a.cycleManager.RecordActions(ctx, actions); err != nil {
		// End the cycle before returning error
		a.cycleManager.EndCycle(ctx)
		return nil, history, false, fmt.Errorf("failed to record actions: %w", err)
	}

	return actions, history, false, nil
}

// createObservationFromToolExecution executes a tool and creates an observation.
// Returns the observation, observation content string, and any error.
func (a *Agent) createObservationFromToolExecution(
	ctx context.Context,
	tool tool.Tool,
	action *Action,
) (*CycleObservation, string, error) {
	// Execute the tool
	result, err := tool.Execute(ctx, action.ToolInput)
	var observationContent string

	// Create observation based on result or error
	var observation *CycleObservation
	if err != nil {
		// If the tool execution fails, record the error
		observationContent = fmt.Sprintf("Error: %v", err)
		observation = &CycleObservation{
			ID:       fmt.Sprintf("obs-%d", time.Now().UnixNano()),
			ActionID: action.ID,
			ToolOutput: map[string]interface{}{
				"error": observationContent,
			},
			IsError:   true,
			Timestamp: time.Now().Unix(),
		}
	} else {
		observationContent = result.String()
		observation = &CycleObservation{
			ID:       fmt.Sprintf("obs-%d", time.Now().UnixNano()),
			ActionID: action.ID,
			ToolOutput: map[string]interface{}{
				"output": observationContent,
			},
			Timestamp: time.Now().Unix(),
		}
	}
	// Record the observation
	if recordErr := a.cycleManager.RecordObservations(ctx, []*CycleObservation{observation}); recordErr != nil {
		return nil, "", fmt.Errorf("failed to record observation: %w", recordErr)
	}
	return observation, observationContent, err
}

// findTool finds a tool by name.
func (a *Agent) findTool(name string) (tool.Tool, bool) {
	for _, t := range a.Tools() {
		if t.Name() == name {
			return t, true
		}
	}
	return nil, false
}

// buildDefaultSystemPrompt creates a default system prompt for the ReAct agent.
func buildDefaultSystemPrompt(tools []tool.Tool) string {
	var prompt strings.Builder
	prompt.WriteString("You are a helpful AI assistant that can use tools to solve problems. ")
	prompt.WriteString("Think carefully and reason step-by-step. ")
	prompt.WriteString("For complex tasks, break them down into smaller steps.\n\n")

	// Add available tools
	prompt.WriteString("Available tools:\n")
	for _, t := range tools {
		prompt.WriteString(fmt.Sprintf("- %s: %s\n", t.Name(), t.Description()))
	}

	prompt.WriteString("\nWhen you have a final answer, start with 'Final Answer:'\n")

	return prompt.String()
}

// findLatestUserMessage finds the most recent user message in a list of messages.
func findLatestUserMessage(messages []*message.Message) *message.Message {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == message.RoleUser {
			return messages[i]
		}
	}
	return nil
}

// RecordActions records multiple actions via the cycle manager.
func (a *Agent) RecordActions(ctx context.Context, actions []*Action) error {
	return a.cycleManager.RecordActions(ctx, actions)
}

// RecordObservations records multiple observations via the cycle manager.
func (a *Agent) RecordObservations(ctx context.Context, observations []*CycleObservation) error {
	return a.cycleManager.RecordObservations(ctx, observations)
}
