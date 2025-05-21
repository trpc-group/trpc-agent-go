// Package react defines the interfaces and core components for ReAct agents.
package react

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/message"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// StreamingLLMThoughtGenerator generates streaming thoughts using an LLM.
type StreamingLLMThoughtGenerator struct {
	model     model.StreamingModel
	prompting ThoughtPromptStrategy
	format    ThoughtFormat
}

// NewStreamingLLMThoughtGenerator creates a new streaming LLM-based thought generator.
func NewStreamingLLMThoughtGenerator(
	model model.StreamingModel,
	strategy ThoughtPromptStrategy,
	format ThoughtFormat,
) *StreamingLLMThoughtGenerator {
	return &StreamingLLMThoughtGenerator{
		model:     model,
		prompting: strategy,
		format:    format,
	}
}

// Generate implements ThoughtGenerator.Generate as a wrapper around streaming generation.
func (g *StreamingLLMThoughtGenerator) Generate(
	ctx context.Context,
	messages []*message.Message,
	history []*Cycle,
	tools []tool.Tool,
) (*Thought, error) {
	// Call streaming version and collect the final result
	thoughtCh, err := g.GenerateStream(ctx, messages, history, tools)
	if err != nil {
		return nil, err
	}

	var finalThought *Thought
	for thought := range thoughtCh {
		finalThought = thought
	}

	if finalThought == nil {
		return nil, fmt.Errorf("no thought was generated")
	}
	return finalThought, nil
}

// GenerateStream streams thought generation using an LLM.
func (g *StreamingLLMThoughtGenerator) GenerateStream(
	ctx context.Context,
	messages []*message.Message,
	history []*Cycle,
	tools []tool.Tool,
) (<-chan *Thought, error) {
	if g.model == nil {
		log.Errorf("STREAMING DIAGNOSIS - StreamingLLMThoughtGenerator has nil model!")
		return nil, fmt.Errorf("streaming model is required for thought generation")
	}

	// Find the last user message if any
	var msg *message.Message
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == message.RoleUser {
			msg = messages[i]
			break
		}
	}
	if msg == nil && len(messages) > 0 {
		msg = messages[len(messages)-1]
	}
	if msg == nil {
		log.Errorf("STREAMING DIAGNOSIS - No message found for thought generation")
		return nil, fmt.Errorf("no message found for thought generation")
	}

	log.Debugf("StreamingLLMThoughtGenerator: Generating thoughts for message: %s", msg.Content)

	// Build the prompt for thought generation
	promptText := ""
	if g.prompting != nil {
		promptText = g.prompting.BuildThoughtPrompt(msg, history, tools, g.format)
	} else {
		promptText = fmt.Sprintf("Think about how to respond to this: %s", msg.Content)
	}
	log.Debugf("### Streaming thought prompt ###\n%s\n### End of streaming thought prompt ###", promptText)

	// Create a user message with the prompt
	userMsg := message.NewUserMessage(promptText)

	// Generate the thought stream using the model
	opts := model.DefaultOptions()
	log.Debugf("STREAMING DIAGNOSIS - Calling model.GenerateStreamWithMessages with prompt length: %d", len(promptText))
	responseCh, err := g.model.GenerateStreamWithMessages(ctx, []*message.Message{userMsg}, opts)
	if err != nil {
		log.Errorf("STREAMING DIAGNOSIS - Model streaming failed: %v", err)
		return nil, fmt.Errorf("streaming thought generation failed: %w", err)
	}

	// Create a channel for streaming thoughts
	thoughtCh := make(chan *Thought)

	// Create unique thought ID for this streaming session
	thoughtID := fmt.Sprintf("thought-%d", time.Now().UnixNano())

	// Process the stream in a goroutine
	go func() {
		defer close(thoughtCh)
		var contentBuffer strings.Builder
		chunkCount := 0
		for response := range responseCh {
			chunkCount++
			// Check for completion or errors
			if response.FinishReason == "error" {
				log.Errorf("STREAMING DIAGNOSIS - Error in streaming thought generation: %s", response.Text)
				return
			}
			var thought *Thought

			// Handle content chunks
			if response.Text != "" {
				contentBuffer.WriteString(response.Text)
				thought = &Thought{
					ID:        thoughtID,
					Content:   response.Text,
					Type:      "reasoning",
					Timestamp: time.Now().Unix(),
				}
			}

			// Handle tool calls in the stream
			if len(response.ToolCalls) > 0 {
				log.Debugf("StreamingLLMThoughtGenerator: Processing %d tool calls", len(response.ToolCalls))
				thought = &Thought{
					ID:               thoughtID,
					Content:          response.Text,
					Type:             "reasoning",
					SuggestedActions: make([]*Action, 0, len(response.ToolCalls)),
					Timestamp:        time.Now().Unix(),
				}

				// Add tool calls as suggested actions
				for _, toolCall := range response.ToolCalls {
					// Parse arguments if possible
					var toolInput map[string]interface{}
					if err := json.Unmarshal([]byte(toolCall.Function.Arguments), &toolInput); err == nil {
						actionID := fmt.Sprintf("action-%d", time.Now().UnixNano())
						thought.SuggestedActions = append(thought.SuggestedActions, &Action{
							ID:        actionID,
							ThoughtID: thoughtID,
							ToolName:  toolCall.Function.Name,
							ToolInput: toolInput,
							RawArgs:   toolCall.Function.Arguments,
							Timestamp: time.Now().Unix(),
						})
					} else {
						log.Warnf("StreamingLLMThoughtGenerator: Failed to parse tool arguments: %v", err)
					}
				}
			}
			if thought != nil {
				select {
				case thoughtCh <- thought:
				case <-ctx.Done():
					log.Errorf("STREAMING DIAGNOSIS - Context cancelled while sending final thought")
				}
			}
			// Handle completion
			if response.FinishReason == "stop" {
				return
			}
		}
	}()
	return thoughtCh, nil
}

// Ensure StreamingLLMThoughtGenerator implements StreamingThoughtGenerator
var _ StreamingThoughtGenerator = (*StreamingLLMThoughtGenerator)(nil)
