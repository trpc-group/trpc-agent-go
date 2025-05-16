// Package react provides the ReAct agent implementation.
package react

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
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
	cycles, err := a.cycleManager.GetHistory(ctx)
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

	// Check if thought contains a final answer directly
	if containsFinalAnswer(thought) {
		log.Infof("Thought contains final answer, returning it directly")
		finalResponse := message.NewAssistantMessage(extractFinalAnswer(thought.Content))
		return thought, finalResponse, true, nil
	}

	// Select action
	log.Debugf("Selecting action for thought: %s", thought.ID)
	action, err := a.actionSelector.Select(ctx, thought, tools)
	if err != nil {
		log.Errorf("Failed to select action: %v", err)
		return nil, nil, false, fmt.Errorf("failed to select action: %w", err)
	}

	// Record the action
	if err := a.cycleManager.RecordAction(ctx, action); err != nil {
		log.Errorf("Failed to record action: %v", err)
		return nil, nil, false, fmt.Errorf("failed to record action: %w", err)
	}

	// Special handling for final_answer action
	if action.ToolName == "final_answer" {
		return a.handleFinalAnswerAction(ctx, thought, action)
	}

	// Find and execute the tool
	_, err = a.executeToolAction(ctx, action)
	if err != nil {
		return thought, nil, false, err
	}

	// Check for cycles or patterns that indicate we should stop
	updatedCycles, _ := a.cycleManager.GetHistory(ctx)
	if isRepeatingToolCalls(updatedCycles) {
		log.Infof("Detected repetitive actions, generating final answer")
		finalResponse, _ := a.generateFinalAnswerFromRepeatingCalls(ctx, updatedCycles)
		return thought, finalResponse, true, nil
	}

	return thought, nil, false, nil
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
	if err := a.cycleManager.RecordObservation(ctx, observation); err != nil {
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
		if err := a.cycleManager.RecordObservation(ctx, observation); err != nil {
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
	if err := a.cycleManager.RecordObservation(ctx, observation); err != nil {
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
	if thought.SuggestedAction != nil && thought.SuggestedAction.ToolName == "final_answer" {
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

// hasFinalAnswer checks if a thought contains a final answer.
func hasFinalAnswer(thought string) bool {
	thoughtLower := strings.ToLower(thought)

	return strings.Contains(thoughtLower, "final answer") ||
		strings.Contains(thoughtLower, "final response") ||
		strings.Contains(thoughtLower, "my answer is") ||
		(strings.Contains(thoughtLower, "answer:") && !strings.Contains(thoughtLower, "need more information"))
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
		if cycle.Observation != nil {
			content := a.extractContentFromObservation(cycle.Observation)

			if cycle.Action != nil {
				summary.WriteString(fmt.Sprintf("%d. For %s, I found: %s\n",
					i+1, cycle.Action.ToolName, content))
			} else {
				summary.WriteString(fmt.Sprintf("%d. I found: %s\n",
					i+1, content))
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

// isRepeatingToolCalls checks if the last 3 cycles are using the same tool with the same parameters.
func isRepeatingToolCalls(cycles []*Cycle) bool {
	if len(cycles) < 3 {
		return false
	}

	// Get the last 3 cycles
	lastThree := cycles[len(cycles)-3:]

	// Check if they all have actions
	for _, cycle := range lastThree {
		if cycle.Action == nil {
			return false
		}
	}

	// Check if they all use the same tool
	toolName := lastThree[0].Action.ToolName
	for _, cycle := range lastThree[1:] {
		if cycle.Action.ToolName != toolName {
			return false
		}
	}

	// Check if the parameters are similar
	firstInput := fmt.Sprintf("%v", lastThree[0].Action.ToolInput)
	for _, cycle := range lastThree[1:] {
		currentInput := fmt.Sprintf("%v", cycle.Action.ToolInput)
		if !stringsApproximatelyEqual(firstInput, currentInput, 0.8) {
			return false
		}
	}

	return true
}

// stringsApproximatelyEqual checks if two strings are approximately equal.
// It uses a simple approach based on string length and character similarity.
func stringsApproximatelyEqual(s1, s2 string, threshold float64) bool {
	// Simple implementation - just check if they're mostly the same length
	// and have mostly the same characters
	if len(s1) == 0 || len(s2) == 0 {
		return len(s1) == len(s2)
	}

	// If one string is much longer than the other, they're not similar
	maxLen := float64(max(len(s1), len(s2)))
	lenDiff := math.Abs(float64(len(s1) - len(s2)))
	if lenDiff/maxLen > (1 - threshold) {
		return false
	}

	// Count shared characters (simplified approach)
	s1Chars := make(map[rune]int)
	for _, c := range s1 {
		s1Chars[c]++
	}

	sharedChars := 0
	for _, c := range s2 {
		if s1Chars[c] > 0 {
			sharedChars++
			s1Chars[c]--
		}
	}

	similarity := float64(sharedChars) / float64(len(s2))
	return similarity >= threshold
}

// generateFinalAnswerFromRepeatingCalls generates a final answer when the agent is stuck in a loop.
func (a *Agent) generateFinalAnswerFromRepeatingCalls(ctx context.Context, cycles []*Cycle) (*message.Message, error) {
	// Find the last successful cycle
	lastSuccessfulCycle := a.findLastSuccessfulCycle(cycles)
	if lastSuccessfulCycle == nil {
		// No successful observations, generate a generic response
		return message.NewAssistantMessage("I've been trying to solve this but haven't been able to get a successful result."), nil
	}

	// Extract observation content
	observationStr := a.extractContentFromObservation(lastSuccessfulCycle.Observation)

	// Generate a tool-specific response
	finalAnswer := a.generateToolSpecificResponse(lastSuccessfulCycle.Action.ToolName, observationStr)

	return message.NewAssistantMessage(finalAnswer), nil
}

// findLastSuccessfulCycle finds the last cycle with a successful tool execution.
func (a *Agent) findLastSuccessfulCycle(cycles []*Cycle) *Cycle {
	for i := len(cycles) - 1; i >= 0; i-- {
		if cycles[i].Observation != nil && !cycles[i].Observation.IsError {
			return cycles[i]
		}
	}
	return nil
}

// generateToolSpecificResponse creates a response tailored to the specific tool that was used.
func (a *Agent) generateToolSpecificResponse(toolName string, observationStr string) string {
	switch toolName {
	case "calculator":
		return fmt.Sprintf("The result of the calculation is %s.", observationStr)
	default:
		return fmt.Sprintf("After analyzing the request, I used the %s tool and found that the result is: %s", toolName, observationStr)
	}
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
		shouldContinue, err := a.processSingleAsyncCycle(ctx, msg, cycles, eventCh)
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
	cycles []*Cycle,
	eventCh chan<- *event.Event,
) (bool, error) {
	// Generate a thought based on the input
	userMsgs := []*message.Message{msg}
	thought, err := a.thoughtGenerator.Generate(ctx, userMsgs, cycles, a.Tools())
	if err != nil {
		return false, fmt.Errorf("failed to generate thought: %w", err)
	}

	// Emit a thought event - use custom event for thinking
	eventCh <- event.NewCustomEvent("thinking", thought.Content)

	// Record the thought
	err = a.cycleManager.StartCycle(ctx, thought)
	if err != nil {
		return false, fmt.Errorf("failed to start cycle: %w", err)
	}

	// Update cycles
	cycle, err := a.cycleManager.CurrentCycle(ctx)
	if err != nil {
		return false, fmt.Errorf("failed to get current cycle: %w", err)
	}
	cycles = append(cycles, cycle)

	// If the thought contains a final answer, stop and return it
	if hasFinalAnswer(thought.Content) {
		return a.handleAsyncFinalThought(ctx, thought, eventCh)
	}

	// Check for repeated calculations - if we've done the same calculation repeatedly,
	// assume we have our answer and should stop
	if len(cycles) >= 3 && isRepeatingToolCalls(cycles) {
		return a.handleAsyncRepeatingTools(ctx, cycles, eventCh)
	}

	// Select an action based on the thought
	action, err := a.actionSelector.Select(ctx, thought, a.Tools())
	if err != nil {
		return a.handleAsyncActionError(ctx, thought, err, eventCh)
	}

	// Emit and record the action
	a.emitAndRecordAction(ctx, action, eventCh)

	// Execute the action
	selectedTool, found := a.findTool(action.ToolName)
	if !found {
		return false, fmt.Errorf("tool %s not found", action.ToolName)
	}

	// Execute the tool
	return a.executeAsyncTool(ctx, selectedTool, action, eventCh)
}

// handleAsyncFinalThought handles a thought that contains a final answer.
// Returns whether to continue execution and any error.
func (a *Agent) handleAsyncFinalThought(
	ctx context.Context,
	thought *Thought,
	eventCh chan<- *event.Event,
) (bool, error) {
	// Generate a final response
	finalResp, err := a.generateResponseFromContent(ctx, thought.Content)
	if err != nil {
		return false, fmt.Errorf("failed to generate final response: %w", err)
	}
	eventCh <- event.NewMessageEvent(finalResp)
	return false, nil // Stop processing more cycles
}

// handleAsyncRepeatingTools handles the case when the agent has repeated the same tool call multiple times.
// Returns whether to continue execution and any error.
func (a *Agent) handleAsyncRepeatingTools(
	ctx context.Context,
	cycles []*Cycle,
	eventCh chan<- *event.Event,
) (bool, error) {
	log.Infof("Detected repeating tool calls, stopping cycle and generating final answer")
	finalResp, err := a.generateFinalAnswerFromRepeatingCalls(ctx, cycles)
	if err != nil {
		return false, fmt.Errorf("failed to generate final answer: %w", err)
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

// emitAndRecordAction emits an action event and records the action.
func (a *Agent) emitAndRecordAction(
	ctx context.Context,
	action *Action,
	eventCh chan<- *event.Event,
) error {
	// Emit an action event - use custom event for tool usage
	actionJSON, _ := json.Marshal(action.ToolInput)
	toolEvent := event.NewEvent(event.TypeTool, nil)
	toolEvent.SetMetadata("tool_name", action.ToolName)
	toolEvent.SetMetadata("input", string(actionJSON))
	eventCh <- toolEvent

	// Record the action
	return a.cycleManager.RecordAction(ctx, action)
}

// executeAsyncTool executes a tool and handles the result asynchronously.
// Returns whether to continue execution and any error.
func (a *Agent) executeAsyncTool(
	ctx context.Context,
	selectedTool tool.Tool,
	action *Action,
	eventCh chan<- *event.Event,
) (bool, error) {
	result, err := selectedTool.Execute(ctx, action.ToolInput)
	if err != nil {
		return a.handleAsyncToolError(ctx, action, err, eventCh)
	}

	// Process successful tool execution
	return a.handleAsyncToolSuccess(ctx, action, result, eventCh)
}

// handleAsyncToolError handles errors that occur during tool execution.
// Returns whether to continue execution and any error.
func (a *Agent) handleAsyncToolError(
	ctx context.Context,
	action *Action,
	execErr error,
	eventCh chan<- *event.Event,
) (bool, error) {
	// If the tool execution fails, record the error and use it as the observation
	observationContent := fmt.Sprintf("Error: %v", execErr)
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
	if recordErr := a.cycleManager.RecordObservation(ctx, observation); recordErr != nil {
		return false, fmt.Errorf("failed to record observation: %w", recordErr)
	}

	// End the current cycle even after an error
	log.Debugf("Ending current cycle after tool error")
	if _, endErr := a.cycleManager.EndCycle(ctx); endErr != nil {
		return false, fmt.Errorf("failed to end cycle after tool error: %w", endErr)
	}

	// Generate a direct error response for specific error types
	if strings.Contains(execErr.Error(), "unsupported operation") {
		directResponse := fmt.Sprintf("The calculator doesn't support the '%s' operation. The calculator supports these operations: add, subtract, multiply, divide, sqrt, and power.", action.ToolInput["operation"])
		eventCh <- event.NewMessageEvent(message.NewAssistantMessage(directResponse))
		return false, nil // Stop processing more cycles
	}

	// Continue to next iteration for other types of errors
	return true, nil
}

// handleAsyncToolSuccess handles successful tool execution.
// Returns whether to continue execution and any error.
func (a *Agent) handleAsyncToolSuccess(
	ctx context.Context,
	action *Action,
	result *tool.Result,
	eventCh chan<- *event.Event,
) (bool, error) {
	// Create an observation from the result
	observationContent := result.String()
	observation := &CycleObservation{
		ID:       fmt.Sprintf("obs-%d", time.Now().UnixNano()),
		ActionID: action.ID,
		ToolOutput: map[string]interface{}{
			"output": observationContent,
		},
		Timestamp: time.Now().Unix(),
	}

	// Emit an observation event
	observationEvent := event.NewCustomEvent("observation", observationContent)
	eventCh <- observationEvent

	// Record the observation
	if err := a.cycleManager.RecordObservation(ctx, observation); err != nil {
		return false, fmt.Errorf("failed to record observation: %w", err)
	}

	// End the current cycle
	if _, err := a.cycleManager.EndCycle(ctx); err != nil {
		return false, fmt.Errorf("failed to end cycle: %w", err)
	}

	return true, nil // Continue processing more cycles
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

// RecordAction records an action via the cycle manager.
func (a *Agent) RecordAction(ctx context.Context, action *Action) error {
	return a.cycleManager.RecordAction(ctx, action)
}

// RecordObservation records an observation via the cycle manager.
func (a *Agent) RecordObservation(ctx context.Context, observation *CycleObservation) error {
	return a.cycleManager.RecordObservation(ctx, observation)
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

	// Select and record action
	action, updatedHistory, shouldReturn, err := a.selectAndRecordAction(ctx, cycle, history)
	if shouldReturn || err != nil {
		return cycle, updatedHistory, err
	}

	// Execute action and record observation
	return a.executeActionAndRecordObservation(ctx, action, history)
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
	if hasFinalAnswer(thought.Content) {
		// Add the thought to history as a message
		respMsg := message.NewAssistantMessage(thought.Content)
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
) (*Action, []*message.Message, bool, error) {
	// Select an action based on the thought
	action, err := a.actionSelector.Select(ctx, cycle.Thought, a.Tools())
	if err != nil {
		// If we fail to select an action, create a response with error info
		errorMsg := fmt.Sprintf("Failed to select action: %v", err)
		respMsg := message.NewAssistantMessage(errorMsg)
		updatedHistory := append(history, respMsg)

		// End the cycle before returning
		if _, endErr := a.cycleManager.EndCycle(ctx); endErr != nil {
			log.Warnf("Failed to end cycle after action selection error: %v", endErr)
		}

		return nil, updatedHistory, true, nil
	}

	// Record the action
	if err := a.cycleManager.RecordAction(ctx, action); err != nil {
		// End the cycle before returning error
		a.cycleManager.EndCycle(ctx)
		return nil, history, false, fmt.Errorf("failed to record action: %w", err)
	}

	return action, history, false, nil
}

// executeActionAndRecordObservation executes an action and records the observation.
// Returns the updated cycle, updated history, and any error.
func (a *Agent) executeActionAndRecordObservation(
	ctx context.Context,
	action *Action,
	history []*message.Message,
) (*Cycle, []*message.Message, error) {
	// Find the tool
	selectedTool, found := a.findTool(action.ToolName)
	if !found {
		// End the cycle before returning error
		a.cycleManager.EndCycle(ctx)
		return nil, history, fmt.Errorf("tool %s not found", action.ToolName)
	}

	// Execute the tool and create observation
	observation, observationContent, err := a.createObservationFromToolExecution(ctx, selectedTool, action)

	// Record the observation
	if recordErr := a.cycleManager.RecordObservation(ctx, observation); recordErr != nil {
		// End the cycle before returning error
		a.cycleManager.EndCycle(ctx)
		return nil, history, fmt.Errorf("failed to record observation: %w", recordErr)
	}

	// End the current cycle
	updatedCycle, endErr := a.cycleManager.EndCycle(ctx)
	if endErr != nil {
		return nil, history, fmt.Errorf("failed to end cycle: %w", endErr)
	}

	// Add the observation to history as a message
	toolMsg := message.NewToolMessage(action.ToolName)
	toolMsg.Content = observationContent
	updatedHistory := append(history, toolMsg)

	// Special handling for calculator errors
	if err != nil && strings.Contains(err.Error(), "unsupported operation") {
		// Add a helpful assistant message about the unsupported operation
		helpMsg := message.NewAssistantMessage(
			fmt.Sprintf("The calculator doesn't support the '%s' operation. The calculator supports these operations: add, subtract, multiply, divide, sqrt, and power.",
				action.ToolInput["operation"]))
		updatedHistory = append(updatedHistory, helpMsg)
	}

	return updatedCycle, updatedHistory, nil
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
