// Package react defines the interfaces and core components for ReAct agents.
package react

import (
	"context"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/message"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// Thought represents a reasoning step made by the ReAct agent.
// It contains the agent's internal monologue or reasoning process.
type Thought struct {
	ID        string     `json:"id"`                   // Unique identifier for the thought.
	Content   string     `json:"content"`              // The textual content of the reasoning trace.
	Type      string     `json:"type,omitempty"`       // The type of thought (e.g., reasoning, planning).
	PlanState *PlanState `json:"plan_state,omitempty"` // Current planning state for dynamic reasoning.
	Timestamp int64      `json:"timestamp"`            // Unix timestamp of when the thought occurred.
}

// Action represents an action taken by the ReAct agent based on its thought process.
// This typically involves calling a tool.
type Action struct {
	ID        string                 `json:"id"`         // Unique identifier for the action.
	ThoughtID string                 `json:"thought_id"` // ID of the thought that prompted this action.
	ToolName  string                 `json:"tool_name"`  // Name of the tool to be called.
	ToolInput map[string]interface{} `json:"tool_input"` // Input parameters for the tool.
	Timestamp int64                  `json:"timestamp"`  // Unix timestamp of when the action was initiated.
}

// Observation represents the outcome or result obtained from executing an action.
// This is typically the output from a tool call.
// Renamed to CycleObservation to avoid conflict with the Observation struct in react_agent.go
// which represents the tool feedback itself.
type CycleObservation struct {
	ID         string                 `json:"id"`          // Unique identifier for the observation.
	ActionID   string                 `json:"action_id"`   // ID of the action that produced this observation.
	ToolOutput map[string]interface{} `json:"tool_output"` // Output received from the tool.
	IsError    bool                   `json:"is_error"`    // Indicates if the tool execution resulted in an error.
	Timestamp  int64                  `json:"timestamp"`   // Unix timestamp of when the observation was received.
}

// Cycle represents a complete thought-action-observation cycle.
type Cycle struct {
	// ID is a unique identifier for the cycle.
	ID string `json:"id"`

	// Thought is the reasoning trace.
	Thought *Thought `json:"thought,omitempty"`

	// Action is the selected action to execute.
	Action *Action `json:"action,omitempty"`

	// Observation is the result of executing the action.
	Observation *CycleObservation `json:"observation,omitempty"`

	// StartTime is when the cycle started (Unix timestamp).
	StartTime int64 `json:"start_time"`

	// EndTime is when the cycle ended (Unix timestamp).
	EndTime int64 `json:"end_time,omitempty"`
}

// IReActAgent defines the interface for an agent that implements the ReAct framework.
// Renamed from ReActAgent to avoid conflict with the struct definition.
// It combines reasoning (thought) and acting (action) to solve problems.
type IReActAgent interface {
	agent.Agent // Embed the base Agent interface.

	// RunReActCycle executes a single Thought-Action-Observation cycle.
	// It takes the current conversation history and returns the updated history
	// including the new thought, action, and observation.
	RunReActCycle(ctx context.Context, history []*message.Message) (*Cycle, []*message.Message, error)

	// MaxIterations returns the maximum number of ReAct cycles allowed.
	MaxIterations() int

	// SetMaxIterations sets the maximum number of ReAct cycles.
	SetMaxIterations(maxIterations int)

	// RecordAction records the action taken within the current cycle.
	RecordAction(ctx context.Context, action *Action) error
	// RecordObservation records the observation received for the current action.
	RecordObservation(ctx context.Context, observation *CycleObservation) error
	// EndCycle completes the current ReAct cycle.
	EndCycle(ctx context.Context) (*Cycle, error)
	// GetHistory retrieves the history of all completed cycles.
	GetHistory(ctx context.Context) ([]*Cycle, error)
	// CurrentCycle returns the currently active cycle, if any.
	CurrentCycle(ctx context.Context) (*Cycle, error)
}

// CycleManager defines the interface for managing the ReAct cycles.
type CycleManager interface {
	// StartCycle initiates a new ReAct cycle.
	StartCycle(ctx context.Context, thought *Thought) error
	// RecordAction records the action taken within the current cycle.
	RecordAction(ctx context.Context, action *Action) error
	// RecordObservation records the observation received for the current action.
	RecordObservation(ctx context.Context, observation *CycleObservation) error
	// EndCycle completes the current ReAct cycle.
	EndCycle(ctx context.Context) (*Cycle, error)
	// GetHistory retrieves the history of all completed cycles.
	GetHistory(ctx context.Context) ([]*Cycle, error)
	// CurrentCycle returns the currently active cycle, if any.
	CurrentCycle(ctx context.Context) (*Cycle, error)
}

// ThoughtGenerator defines the interface for generating the next thought based
// on the current state and history.
type ThoughtGenerator interface {
	// Generate generates the next thought.
	Generate(ctx context.Context, history []*message.Message, previousCycles []*Cycle) (*Thought, error)
}

// ActionSelector defines the interface for selecting the next action based on
// the current thought.
type ActionSelector interface {
	// Select selects the action to take based on the thought.
	Select(ctx context.Context, thought *Thought, availableTools []tool.Tool) (*Action, error)
}

// ResponseGenerator defines the interface for generating the final response
// once the ReAct process concludes (either successfully or reaching max iterations).
type ResponseGenerator interface {
	// Generate generates the final response based on the goal and the ReAct history.
	Generate(ctx context.Context, goal string, history []*message.Message, cycles []*Cycle) (*message.Message, error)
}
