package processor

import (
	"context"

	"trpc.group/trpc-go/trpc-agent-go/core/agent"
	"trpc.group/trpc-go/trpc-agent-go/core/event"
	"trpc.group/trpc-go/trpc-agent-go/core/model"
	"trpc.group/trpc-go/trpc-agent-go/log"
)

// ContentRequestProcessor implements content processing logic.
type ContentRequestProcessor struct {
}

// NewContentRequestProcessor creates a new content request processor.
func NewContentRequestProcessor() *ContentRequestProcessor {
	return &ContentRequestProcessor{}
}

// ProcessRequest implements the flow.RequestProcessor interface.
// It handles adding messages from the invocation to the request.
func (p *ContentRequestProcessor) ProcessRequest(
	ctx context.Context,
	invocation *agent.Invocation,
	req *model.Request,
	ch chan<- *event.Event,
) {
	if req == nil {
		log.Errorf("Content request processor: request is nil")
		return
	}

	log.Debugf("Content request processor: processing request for agent %s", invocation.AgentName)

	// Initialize messages slice if nil.
	if req.Messages == nil {
		req.Messages = make([]model.Message, 0)
	}

	// Add user message from invocation if provided.
	if invocation.Message.Content != "" {
		if len(req.Messages) == 0 {
			// If no messages exist, add the invocation message.
			req.Messages = append(req.Messages, invocation.Message)
			log.Debugf("Content request processor: added initial message from invocation")
		} else if invocation.Message.Role == model.RoleUser {
			// Add as the last message if it's a user message.
			req.Messages = append(req.Messages, invocation.Message)
			log.Debugf("Content request processor: appended user message from invocation")
		}
	}

	// Send a preprocessing event.
	if invocation != nil {
		evt := event.New(invocation.InvocationID, invocation.AgentName)
		evt.Object = "preprocessing.content"

		select {
		case ch <- evt:
			log.Debugf("Content request processor: sent preprocessing event")
		case <-ctx.Done():
			log.Debugf("Content request processor: context cancelled")
		}
	}
} 