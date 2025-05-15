package agents

import (
	"context"
	"errors"
	"fmt"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/message"
)

var (
	// ErrNoAgentsSpecified is returned when a Sequential agent is initialized without any agents.
	ErrNoAgentsSpecified = errors.New("no agents specified for Sequential agent")
)

// SequentialAgentConfig contains configuration for a Sequential agent.
type SequentialAgentConfig struct {
	// Base configuration for the agent.
	agent.BaseAgentConfig

	// Agents to execute in sequence.
	Agents []agent.Agent

	// ContinueOnError determines whether to continue execution if an agent returns an error.
	ContinueOnError bool

	// PreProcess is an optional function that pre-processes the message before passing it to each agent.
	PreProcess func(index int, agent agent.Agent, msg *message.Message) *message.Message

	// PostProcess is an optional function that post-processes the response from each agent.
	PostProcess func(index int, agent agent.Agent, msg *message.Message) *message.Message

	// IncludeIntermediateResults determines whether to include intermediate results in metadata.
	IncludeIntermediateResults bool
}

// SequentialAgent is an agent that executes multiple agents in sequence.
type SequentialAgent struct {
	*agent.BaseAgent
	agents                     []agent.Agent
	continueOnError            bool
	preProcess                 func(index int, agent agent.Agent, msg *message.Message) *message.Message
	postProcess                func(index int, agent agent.Agent, msg *message.Message) *message.Message
	includeIntermediateResults bool
}

// NewSequentialAgent creates a new Sequential agent.
func NewSequentialAgent(config SequentialAgentConfig) (*SequentialAgent, error) {
	if len(config.Agents) == 0 {
		return nil, ErrNoAgentsSpecified
	}

	return &SequentialAgent{
		BaseAgent:                  agent.NewBaseAgent(config.BaseAgentConfig),
		agents:                     config.Agents,
		continueOnError:            config.ContinueOnError,
		preProcess:                 config.PreProcess,
		postProcess:                config.PostProcess,
		includeIntermediateResults: config.IncludeIntermediateResults,
	}, nil
}

// Run executes multiple agents in sequence.
func (a *SequentialAgent) Run(ctx context.Context, msg *message.Message) (*message.Message, error) {
	currentMsg := msg
	var lastError error
	intermediateResults := make([]*message.Message, 0, len(a.agents))

	for i, agent := range a.agents {
		// Pre-process message if function is provided
		if a.preProcess != nil {
			currentMsg = a.preProcess(i, agent, currentMsg)
		}

		// Execute agent
		response, err := agent.Run(ctx, currentMsg)

		// Store the error but continue if configured to do so
		if err != nil {
			lastError = fmt.Errorf("error in agent %d (%s): %w", i, agent.Name(), err)
			if !a.continueOnError {
				break
			}
		}

		// If response is nil but we're continuing on error, skip to next agent
		if response == nil {
			continue
		}

		// Post-process response if function is provided
		if a.postProcess != nil {
			response = a.postProcess(i, agent, response)
		}

		// Store the intermediate result
		if a.includeIntermediateResults {
			intermediateResults = append(intermediateResults, response)
		}

		// Use the response as the input for the next agent
		currentMsg = response
	}

	// If we encountered an error and didn't continue, return nil and the error
	if lastError != nil && !a.continueOnError {
		return nil, lastError
	}

	// If we have a message to return, add metadata with intermediate results
	if currentMsg != nil && a.includeIntermediateResults {
		currentMsg.SetMetadata("intermediate_results", intermediateResults)
	}

	return currentMsg, lastError
}

// RunAsync executes multiple agents in sequence asynchronously.
func (a *SequentialAgent) RunAsync(ctx context.Context, msg *message.Message) (<-chan *event.Event, error) {
	// Create a channel for events
	eventCh := make(chan *event.Event, 10)

	// Run in a goroutine
	go func() {
		defer close(eventCh)

		currentMsg := msg
		var lastError error
		intermediateResults := make([]*message.Message, 0, len(a.agents))

		for i, ag := range a.agents {
			// Send agent start event
			eventCh <- event.NewAgentStartEvent(ag.Name(), i)

			// Pre-process message if function is provided
			if a.preProcess != nil {
				currentMsg = a.preProcess(i, ag, currentMsg)
			}

			// Execute agent asynchronously
			innerEventCh, err := ag.RunAsync(ctx, currentMsg)
			if err != nil {
				lastError = fmt.Errorf("error starting agent %d (%s): %w", i, ag.Name(), err)
				eventCh <- event.NewErrorEvent(lastError, 500)
				if !a.continueOnError {
					return
				}
				continue
			}

			// Forward events from inner agent
			var response *message.Message
			for innerEvent := range innerEventCh {
				// Forward the event
				eventCh <- innerEvent

				// Capture message events for the next agent
				if innerEvent.Type == event.TypeMessage {
					if msg, ok := innerEvent.GetMetadata("message"); ok {
						if msgObj, ok := msg.(*message.Message); ok {
							response = msgObj
						}
					}
				}

				// Capture error events
				if innerEvent.Type == event.TypeError {
					if errStr, ok := innerEvent.GetMetadata("error"); ok {
						if errString, ok := errStr.(string); ok {
							lastError = errors.New(errString)
							if !a.continueOnError {
								return
							}
						}
					}
				}
			}

			// Send agent end event
			eventCh <- event.NewAgentEndEvent(ag.Name(), i)

			// If no response was received and we're not continuing on error, stop
			if response == nil {
				if !a.continueOnError {
					eventCh <- event.NewErrorEvent(errors.New("no response received from agent"), 500)
					return
				}
				continue
			}

			// Post-process response if function is provided
			if a.postProcess != nil {
				response = a.postProcess(i, ag, response)
			}

			// Store the intermediate result
			if a.includeIntermediateResults {
				intermediateResults = append(intermediateResults, response)
			}

			// Use the response as the input for the next agent
			currentMsg = response
		}

		// If we have a message to return, add metadata with intermediate results
		if currentMsg != nil {
			if a.includeIntermediateResults {
				currentMsg.SetMetadata("intermediate_results", intermediateResults)
			}

			// Send the final message event
			eventCh <- event.NewMessageEvent(currentMsg)
		}

		// If there was an error at any point, send it
		if lastError != nil {
			eventCh <- event.NewErrorEvent(lastError, 500)
		}
	}()

	return eventCh, nil
}
