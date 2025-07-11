package processor

import (
	"context"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

// InstructionRequestProcessor implements instruction processing logic.
type InstructionRequestProcessor struct {
	// Instruction is the instruction to add to requests.
	Instruction string
	// SystemPrompt is the system prompt to add to requests.
	SystemPrompt string
}

// NewInstructionRequestProcessor creates a new instruction request processor.
func NewInstructionRequestProcessor(instruction, systemPrompt string) *InstructionRequestProcessor {
	return &InstructionRequestProcessor{
		Instruction:  instruction,
		SystemPrompt: systemPrompt,
	}
}

// ProcessRequest implements the flow.RequestProcessor interface.
// It adds instruction content and system prompt to the request if provided.
func (p *InstructionRequestProcessor) ProcessRequest(
	ctx context.Context,
	invocation *agent.Invocation,
	req *model.Request,
	ch chan<- *event.Event,
) {
	if req == nil {
		log.Errorf("Instruction request processor: request is nil")
		return
	}

	log.Debugf("Instruction request processor: processing request for agent %s", invocation.AgentName)

	// Initialize messages slice if nil.
	if req.Messages == nil {
		req.Messages = make([]model.Message, 0)
	}

	// Add system prompt if specified and not already present.
	if p.SystemPrompt != "" && !hasInstructionSystemMessage(req.Messages, p.SystemPrompt) {
		systemMsg := model.NewSystemMessage(p.SystemPrompt)
		req.Messages = append([]model.Message{systemMsg}, req.Messages...)
		log.Debugf("Instruction request processor: added system prompt")
	}

	// Add instruction as a system message if not already present.
	if p.Instruction != "" && !hasInstructionSystemMessage(req.Messages, p.Instruction) {
		instructionMsg := model.NewSystemMessage(p.Instruction)
		req.Messages = append([]model.Message{instructionMsg}, req.Messages...)
		log.Debugf("Instruction request processor: added instruction message")
	}

	// Send a preprocessing event.
	if invocation != nil {
		evt := event.New(invocation.InvocationID, invocation.AgentName)
		evt.Object = model.ObjectTypePreprocessingInstruction

		select {
		case ch <- evt:
			log.Debugf("Instruction request processor: sent preprocessing event")
		case <-ctx.Done():
			log.Debugf("Instruction request processor: context cancelled")
		}
	}
}

// hasInstructionSystemMessage checks if there's already a system message with the given content in the messages.
func hasInstructionSystemMessage(messages []model.Message, content string) bool {
	for _, msg := range messages {
		if msg.Role == model.RoleSystem && msg.Content == content {
			return true
		}
	}
	return false
}
