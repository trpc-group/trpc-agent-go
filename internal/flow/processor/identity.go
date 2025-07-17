//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.
// All rights reserved.
//
// If you have downloaded a copy of the tRPC source code from Tencent,
// please note that tRPC source code is licensed under the  Apache 2.0 License,
// A copy of the Apache 2.0 License is included in this file.
//
//

package processor

import (
	"context"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

// IdentityRequestProcessor implements identity processing logic.
type IdentityRequestProcessor struct {
	// AgentName is the name of the agent.
	AgentName string
	// Description is the description of the agent.
	Description string
}

// NewIdentityRequestProcessor creates a new identity request processor.
func NewIdentityRequestProcessor(agentName, description string) *IdentityRequestProcessor {
	return &IdentityRequestProcessor{
		AgentName:   agentName,
		Description: description,
	}
}

// ProcessRequest implements the flow.RequestProcessor interface.
// It adds agent identity information to the request if provided.
func (p *IdentityRequestProcessor) ProcessRequest(
	ctx context.Context,
	invocation *agent.Invocation,
	req *model.Request,
	ch chan<- *event.Event,
) {
	if req == nil {
		log.Errorf("Identity request processor: request is nil")
		return
	}

	agentName := ""
	if invocation != nil {
		agentName = invocation.AgentName
	}
	log.Debugf("Identity request processor: processing request for agent %s", agentName)

	// Initialize messages slice if nil.
	if req.Messages == nil {
		req.Messages = make([]model.Message, 0)
	}

	// Create identity message if we have name or description.
	var identityContent string
	if p.AgentName != "" && p.Description != "" {
		identityContent = "You are " + p.AgentName + ". " + p.Description
	} else if p.AgentName != "" {
		identityContent = "You are " + p.AgentName + "."
	} else if p.Description != "" {
		identityContent = p.Description
	}

	if identityContent != "" {
		// Find existing system message or create new one
		systemMsgIndex := findSystemMessageIndex(req.Messages)
		if systemMsgIndex >= 0 {
			// There's already a system message, check if it contains identity
			if !containsIdentity(req.Messages[systemMsgIndex].Content, identityContent) {
				// Prepend identity to existing system message
				req.Messages[systemMsgIndex].Content = identityContent + "\n\n" + req.Messages[systemMsgIndex].Content
				log.Debugf("Identity request processor: prepended identity to existing system message")
			}
		} else {
			// No existing system message, create new one
			identityMsg := model.NewSystemMessage(identityContent)
			req.Messages = append([]model.Message{identityMsg}, req.Messages...)
			log.Debugf("Identity request processor: added identity message")
		}
	}

	// Send a preprocessing event.
	if invocation != nil {
		evt := event.New(invocation.InvocationID, invocation.AgentName)
		evt.Object = model.ObjectTypePreprocessingIdentity

		select {
		case ch <- evt:
			log.Debugf("Identity request processor: sent preprocessing event")
		case <-ctx.Done():
			log.Debugf("Identity request processor: context cancelled")
		}
	}
}

// containsIdentity checks if the given content already contains the identity.
func containsIdentity(content, identity string) bool {
	return strings.Contains(content, identity)
}
