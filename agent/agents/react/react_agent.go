// Package react provides the ReAct agent implementation.
package react

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent/agents"
	"trpc.group/trpc-go/trpc-agent-go/event"
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

// ThoughtFormat represents the format for reasoning traces.
type ThoughtFormat string

const (
	// FormatMarkdown indicates reasoning traces should be formatted as markdown.
	FormatMarkdown ThoughtFormat = "markdown"

	// FormatPlaintext indicates reasoning traces should be formatted as plaintext.
	FormatPlaintext ThoughtFormat = "plaintext"

	// FormatJSON indicates reasoning traces should be formatted as JSON.
	FormatJSON ThoughtFormat = "json"
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

// ReactStep represents a single step in the ReAct thought process.
type ReactStep struct {
	// Thought is the reasoning trace.
	Thought string `json:"thought,omitempty"`

	// Action is the name of the tool to execute.
	Action string `json:"action,omitempty"`

	// ActionParams are the parameters to pass to the tool.
	ActionParams map[string]interface{} `json:"action_params,omitempty"`

	// Observation is the result of executing the action.
	Observation *Observation `json:"observation,omitempty"`
}

// ReactMemory extends the base memory to store ReAct-specific information.
type ReactMemory interface {
	memory.Memory

	// StoreStep stores a ReAct step.
	StoreStep(ctx context.Context, step *ReactStep) error

	// RetrieveSteps retrieves all ReAct steps.
	RetrieveSteps(ctx context.Context) ([]*ReactStep, error)

	// LastStep retrieves the most recent ReAct step.
	LastStep(ctx context.Context) (*ReactStep, error)
}

// ReActAgentConfig contains configuration for a ReAct agent.
type ReActAgentConfig struct {
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

// ReActAgent is an agent that implements the ReAct paradigm.
type ReActAgent struct {
	*agents.LLMAgent
	thoughtFormat        ThoughtFormat
	maxIterations        int
	reactMemory          ReactMemory
	thoughtGenerator     ThoughtGenerator
	actionSelector       ActionSelector
	responseGenerator    ResponseGenerator
	cycleManager         CycleManager
	mu                   sync.RWMutex
	currentMaxIterations int
	config               ReActAgentConfig
}

// NewReActAgent creates a new ReAct agent.
func NewReActAgent(config ReActAgentConfig) (*ReActAgent, error) {
	if config.Model == nil {
		return nil, ErrModelRequired
	}

	if len(config.Tools) == 0 {
		return nil, ErrNoToolsProvided
	}

	if config.ThoughtGenerator == nil {
		return nil, ErrThoughtGeneratorRequired
	}

	if config.ActionSelector == nil {
		return nil, ErrActionSelectorRequired
	}

	if config.ResponseGenerator == nil {
		return nil, ErrResponseGeneratorRequired
	}

	if config.CycleManager == nil {
		return nil, ErrCycleManagerRequired
	}

	// Set default max iterations if not specified
	if config.MaxIterations <= 0 {
		config.MaxIterations = 10
	}

	// Set default thought format if not specified
	if config.ThoughtFormat == "" {
		config.ThoughtFormat = FormatMarkdown
	}

	// Create default system prompt if not provided
	if config.SystemPrompt == "" {
		config.SystemPrompt = buildDefaultSystemPrompt(config.Tools)
	}

	// Create LLM agent
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

	llmAgent, err := agents.NewLLMAgent(llmConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create LLM agent: %w", err)
	}

	// Create ReactMemory wrapper if needed
	var reactMem ReactMemory
	if rm, ok := config.Memory.(ReactMemory); ok {
		reactMem = rm
	} else if config.Memory != nil {
		// Wrap the provided memory with ReactMemory capabilities
		reactMem = NewReactMemoryWrapper(config.Memory)
	} else {
		// Create a new base ReactMemory
		reactMem = NewBaseReactMemory()
	}

	return &ReActAgent{
		LLMAgent:             llmAgent,
		thoughtFormat:        config.ThoughtFormat,
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

// Run processes the given message using the ReAct paradigm and returns a response.
func (a *ReActAgent) Run(ctx context.Context, msg *message.Message) (*message.Message, error) {
	if err := a.LLMAgent.GetMemory().Store(ctx, msg); err != nil {
		return nil, fmt.Errorf("failed to store initial message: %w", err)
	}

	history, err := a.LLMAgent.GetMemory().Retrieve(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to retrieve history: %w", err)
	}

	var finalAnswerMsg *message.Message

	for i := 0; i < a.MaxIterations(); i++ {
		currentCycle, updatedHistory, cycleErr := a.RunReActCycle(ctx, history)
		history = updatedHistory // Update history regardless of cycle error for context.

		if cycleErr != nil {
			// Attempt to generate a response even if a cycle had an error.
			fmt.Printf("Warning: ReAct cycle %d failed: %v\n", i+1, cycleErr)
			allCycles, _ := a.cycleManager.GetHistory(ctx)
			finalAnswerMsg, err = a.responseGenerator.Generate(ctx, msg.Content, history, allCycles)
			if err != nil {
				return nil, fmt.Errorf("failed to generate response after cycle error: %w (original cycle error: %v)", err, cycleErr)
			}
			break // Exit loop after attempting recovery response.
		}

		if currentCycle == nil || currentCycle.Thought == nil {
			// Should not happen if RunReActCycle is implemented correctly.
			fmt.Printf("Warning: ReAct cycle %d returned nil cycle or thought\n", i+1)
			allCycles, _ := a.cycleManager.GetHistory(ctx)
			finalAnswerMsg, err = a.responseGenerator.Generate(ctx, msg.Content, history, allCycles)
			if err != nil {
				return nil, fmt.Errorf("failed to generate response after nil cycle/thought: %w", err)
			}
			break
		}

		// Check if the thought indicates a final answer.
		if a.isFinalAnswer(currentCycle.Thought) {
			allCycles, _ := a.cycleManager.GetHistory(ctx)
			finalAnswerMsg, err = a.responseGenerator.Generate(ctx, msg.Content, history, allCycles)
			if err != nil {
				return nil, fmt.Errorf("failed to generate final response: %w", err)
			}
			break
		}
	}

	if finalAnswerMsg == nil { // Max iterations reached.
		fmt.Printf("Warning: ReAct agent reached max iterations (%d)\n", a.MaxIterations())
		allCycles, _ := a.cycleManager.GetHistory(ctx)
		finalAnswerMsg, err = a.responseGenerator.Generate(ctx, msg.Content, history, allCycles)
		if err != nil {
			return nil, fmt.Errorf("failed to generate response after max iterations: %w", err)
		}
	}

	if err := a.LLMAgent.GetMemory().Store(ctx, finalAnswerMsg); err != nil {
		fmt.Printf("Warning: failed to store final response in memory: %v\n", err)
	}
	return finalAnswerMsg, nil
}

// RunAsync processes the given message asynchronously using the ReAct paradigm.
func (a *ReActAgent) RunAsync(ctx context.Context, msg *message.Message) (<-chan *event.Event, error) {
	eventCh := make(chan *event.Event, 10)
	go func() {
		defer close(eventCh)
		response, err := a.Run(ctx, msg)
		if err != nil {
			eventCh <- event.NewErrorEvent(err, 0)
			return
		}
		eventCh <- event.NewMessageEvent(response)
	}()
	return eventCh, nil
}

// RunReActCycle executes a single Thought-Action-Observation cycle.
func (a *ReActAgent) RunReActCycle(ctx context.Context, history []*message.Message) (*Cycle, []*message.Message, error) {
	currentHistory := make([]*message.Message, len(history))
	copy(currentHistory, history)

	// 1. Generate Thought
	previousCycles, err := a.cycleManager.GetHistory(ctx)
	if err != nil {
		// Log this but don't make it fatal for thought generation if manager is just for persistence.
		fmt.Printf("Warning: failed to get previous cycles from manager: %v\n", err)
	}
	thought, err := a.thoughtGenerator.Generate(ctx, currentHistory, previousCycles)
	if err != nil {
		return nil, currentHistory, fmt.Errorf("thought generation failed: %w", err)
	}
	if thought == nil { // Should not happen if generator is well-behaved.
		return nil, currentHistory, fmt.Errorf("thought generator returned nil thought")
	}

	if err := a.cycleManager.StartCycle(ctx, thought); err != nil {
		fmt.Printf("Warning: failed to start cycle with manager: %v\n", err)
	}

	// Represent thought as a message for history. The role can be assistant or a special ReAct role.
	// Using Assistant for now, assuming the LLM generates the thought as part of its response.
	thoughtMsg := message.NewAssistantMessage(fmt.Sprintf("Thought: %s", thought.Content))
	currentHistory = append(currentHistory, thoughtMsg)

	var currentAction *Action
	var currentCycleObservation *CycleObservation

	// 2. Select Action (if thought doesn't indicate a final answer)
	if !a.isFinalAnswer(thought) {
		toolSet := a.LLMAgent.GetToolSet()
		var tools []tool.Tool
		if toolSet != nil {
			tools = toolSet.List()
		}

		selectedAction, err := a.actionSelector.Select(ctx, thought, tools)
		if err != nil {
			fmt.Printf("Warning: action selection failed: %v. Proceeding without action.\n", err)
		} else if selectedAction != nil && selectedAction.ToolName != "" {
			currentAction = selectedAction
			if err := a.cycleManager.RecordAction(ctx, currentAction); err != nil {
				fmt.Printf("Warning: failed to record action with manager: %v\n", err)
			}

			// Represent action for history
			actionMsgContent := fmt.Sprintf("Action: %s\nAction Input: %v", currentAction.ToolName, currentAction.ToolInput)
			actionMsg := message.NewAssistantMessage(actionMsgContent) // Action is taken by assistant.
			currentHistory = append(currentHistory, actionMsg)

			// 3. Execute Action & Get Observation
			var toolResult *tool.Result
			var toolErr error
			if toolSet == nil {
				toolErr = fmt.Errorf("no toolset available in LLMAgent")
			} else {
				toolToExecute, ok := toolSet.Get(currentAction.ToolName)
				if !ok {
					toolErr = fmt.Errorf("tool '%s' not found in toolset", currentAction.ToolName)
				} else {
					toolResult, toolErr = toolToExecute.Execute(ctx, currentAction.ToolInput)
				}
			}

			observationOutput := make(map[string]interface{})
			var observationIsError bool

			if toolErr != nil {
				observationOutput["error"] = toolErr.Error()
				observationIsError = true
			} else if toolResult != nil {
				observationOutput["output"] = toolResult.Output
				if toolResult.Metadata != nil {
					observationOutput["metadata"] = toolResult.Metadata
				}
			} else {
				// This case (nil error, nil result) should ideally not happen from ExecuteTool.
				observationOutput["output"] = "Tool executed successfully but returned no output."
			}

			currentCycleObservation = &CycleObservation{
				ID:         fmt.Sprintf("obs-%d-%s", time.Now().UnixNano(), currentAction.ID), // More unique ID.
				ActionID:   currentAction.ID,
				ToolOutput: observationOutput,
				IsError:    observationIsError,
				Timestamp:  time.Now().Unix(),
			}
			if err := a.cycleManager.RecordObservation(ctx, currentCycleObservation); err != nil {
				fmt.Printf("Warning: failed to record observation with manager: %v\n", err)
			}

			// Represent observation for history. Observation comes from the environment/tool.
			obsMsgContent := ""
			if observationIsError {
				obsMsgContent = fmt.Sprintf("Observation: Error - %v", observationOutput["error"])
			} else {
				obsMsgContent = fmt.Sprintf("Observation: %v", observationOutput["output"])
			}
			// Use RoleUser for observations as they are external inputs to the LLM for the next step.
			obsMsg := message.NewUserMessage(obsMsgContent)
			currentHistory = append(currentHistory, obsMsg)
		}
	} // End if !a.isFinalAnswer(thought)

	// 4. Complete Cycle
	completedCycle, err := a.cycleManager.EndCycle(ctx)
	if err != nil {
		fmt.Printf("Warning: failed to end cycle with manager: %v. Using locally constructed cycle.\n", err)
		// Ensure a cycle object is returned even if manager fails.
		var cycleID string
		if completedCycle != nil && completedCycle.ID != "" {
			cycleID = completedCycle.ID
		} else if thought != nil && thought.ID != "" {
			cycleID = "cycle-for-" + thought.ID
		} else {
			cycleID = fmt.Sprintf("cycle-%d", time.Now().UnixNano())
		}
		completedCycle = &Cycle{
			ID:          cycleID,
			Thought:     thought,
			Action:      currentAction,
			Observation: currentCycleObservation,
		}
	} else if completedCycle == nil { // Manager ended cycle without error but returned nil.
		fmt.Printf("Warning: cycle manager ended cycle with nil. Using locally constructed cycle.\n")
		completedCycle = &Cycle{
			ID:          fmt.Sprintf("cycle-%d", time.Now().UnixNano()),
			Thought:     thought,
			Action:      currentAction,
			Observation: currentCycleObservation,
		}
	}

	return completedCycle, currentHistory, nil
}

// isFinalAnswer checks if the thought contains a final answer indication.
func (a *ReActAgent) isFinalAnswer(thought *Thought) bool {
	if thought == nil {
		return false
	}
	// This logic needs to be robust. The LLM should be prompted to use "Final Answer:".
	return strings.Contains(thought.Content, "Final Answer:")
}

// buildDefaultSystemPrompt builds a default system prompt for the ReAct agent.
func buildDefaultSystemPrompt(tools []tool.Tool) string {
	prompt := "You are a ReAct agent that uses tools to solve problems. You MUST follow this process:\n\n"

	prompt += "1. Think through the problem carefully step by step (Thought)\n"
	prompt += "2. Select an appropriate tool from the available tools (Action)\n"
	prompt += "3. Call the tool with the correct parameters (Action Input)\n"
	prompt += "4. Observe the result of the tool call (Observation)\n"
	prompt += "5. Continue this Thought/Action/Observation cycle until you can provide a complete solution\n"
	prompt += "6. Once you have all necessary information from tool calls, provide your Final Answer\n\n"

	prompt += "Available tools:\n"
	for _, t := range tools {
		prompt += fmt.Sprintf("- %s: %s\n", t.Name(), t.Description())
	}

	prompt += "\nCRITICAL RULES:\n"
	prompt += "- ALWAYS execute at least one appropriate tool before providing a Final Answer\n"
	prompt += "- NEVER skip directly to a Final Answer without using tools when appropriate\n"
	prompt += "- Extract relevant information from the user's query to pass to tools\n"
	prompt += "- Do not ask the user for information that was already provided in their query\n"
	prompt += "- Format your actions precisely as shown in the examples below\n\n"

	prompt += `FORMAT YOUR RESPONSES EXACTLY LIKE THIS:

Thought: [Your reasoning about what needs to be done and which tool to use]
Action: [Tool name]
Action Input: {"parameter1": "value1", "parameter2": "value2"}
Observation: [This will be the result from the tool]
... (repeat Thought/Action/Observation as needed)
Thought: I now have enough information to answer the question.
Final Answer: [Your complete response to the user's request]

EXAMPLES:

Example 1 - Using a calculator:
User: What is the square root of 144?
Thought: I need to calculate the square root of 144. I can use the calculator tool for this.
Action: calculator
Action Input: {"operation": "sqrt", "a": 144}
Observation: 12
Thought: I now have the answer to the user's question.
Final Answer: The square root of 144 is 12.

Example 2 - Analyzing text:
User: Analyze the sentiment of "I really love this product!"
Thought: I need to analyze the sentiment of the text provided. I should use the text_analysis tool with the sentiment analysis type.
Action: text_analysis
Action Input: {"text": "I really love this product!", "analysis_type": "sentiment"}
Observation: {"sentiment": "Positive", "positive_words": 1, "negative_words": 0}
Thought: I now have the sentiment analysis result.
Final Answer: The text "I really love this product!" has a positive sentiment.

Begin!
`
	return prompt
}

// MaxIterations returns the maximum number of ReAct cycles.
func (a *ReActAgent) MaxIterations() int {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.currentMaxIterations
}

// SetMaxIterations sets the maximum number of ReAct cycles.
func (a *ReActAgent) SetMaxIterations(maxIterations int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if maxIterations > 0 {
		a.currentMaxIterations = maxIterations
	}
}

// RecordAction records an action via the cycle manager.
func (a *ReActAgent) RecordAction(ctx context.Context, action *Action) error {
	return a.cycleManager.RecordAction(ctx, action)
}

// RecordObservation records an observation via the cycle manager.
func (a *ReActAgent) RecordObservation(ctx context.Context, observation *CycleObservation) error {
	return a.cycleManager.RecordObservation(ctx, observation)
}

// EndCycle ends the current cycle via the cycle manager.
func (a *ReActAgent) EndCycle(ctx context.Context) (*Cycle, error) {
	return a.cycleManager.EndCycle(ctx)
}

// GetHistory retrieves cycle history from the cycle manager.
func (a *ReActAgent) GetHistory(ctx context.Context) ([]*Cycle, error) {
	return a.cycleManager.GetHistory(ctx)
}

// CurrentCycle gets the current cycle from the cycle manager.
func (a *ReActAgent) CurrentCycle(ctx context.Context) (*Cycle, error) {
	return a.cycleManager.CurrentCycle(ctx)
}

// Ensure ReActAgent implements IReActAgent.
var _ IReActAgent = (*ReActAgent)(nil)
