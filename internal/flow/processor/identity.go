//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
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

	addNameToInstruction bool
}

// Option is a function that can be used to configure the identity request processor.
type Option func(*IdentityRequestProcessor)

// WithAddNameToInstruction adds the agent name to the instruction if true.
func WithAddNameToInstruction(addNameToInstruction bool) Option {
	return func(p *IdentityRequestProcessor) {
		p.addNameToInstruction = addNameToInstruction
	}
}

// NewIdentityRequestProcessor creates a new identity request processor.
func NewIdentityRequestProcessor(agentName, description string, opts ...Option) *IdentityRequestProcessor {
	p := &IdentityRequestProcessor{
		AgentName:   agentName,
		Description: description,
	}
	for _, opt := range opts {
		opt(p)
	}
	return p
}

// ProcessRequest implements the flow.RequestProcessor interface.
// It adds agent identity information to the request if provided.
func (p *IdentityRequestProcessor) ProcessRequest(
	ctx context.Context,
	invocation *agent.Invocation,
	req *model.Request,
	ch chan<- *event.Event,
) {
	if invocation == nil {
		return
	}

	if req == nil {
		log.ErrorfContext(
			ctx,
			"Identity request processor: request is nil",
		)
		return
	}

	// Get agent name.
	agentName := invocation.AgentName
	log.DebugfContext(
		ctx,
		"Identity request processor: processing request for agent %s",
		agentName,
	)

	// Initialize messages slice if nil.
	if req.Messages == nil {
		req.Messages = make([]model.Message, 0)
	}

	// Create identity message if we have name or description.
	var identityContent string
	if p.addNameToInstruction && p.AgentName != "" {
		identityContent = "You are " + p.AgentName + ". "
	}
	if p.Description != "" {
		identityContent += p.Description
	}

	if identityContent != "" {
		// Find existing system message or create new one
		systemMsgIndex := findSystemMessageIndex(req.Messages)
		if systemMsgIndex >= 0 {
			// There's already a system message, check if it contains identity
			if !containsIdentity(req.Messages[systemMsgIndex].Content, identityContent) {
				// Prepend identity to existing system message
				req.Messages[systemMsgIndex].Content = identityContent + "\n\n" + req.Messages[systemMsgIndex].Content
				log.DebugContext(
					ctx,
					"Identity request processor: prepended identity to "+
						"existing system message",
				)
			}
		} else {
			// No existing system message, create new one
			identityMsg := model.NewSystemMessage(identityContent)
			req.Messages = append([]model.Message{identityMsg}, req.Messages...)
			log.DebugContext(
				ctx,
				"Identity request processor: added identity message",
			)
		}
	}

	log.DebugContext(ctx, "Identity request processor: sent preprocessing event")

	if err := agent.EmitEvent(ctx, invocation, ch, event.New(
		invocation.InvocationID,
		invocation.AgentName,
		event.WithObject(model.ObjectTypePreprocessingIdentity),
	)); err != nil {
		log.DebugContext(
			ctx,
			"Identity request processor: context cancelled",
		)
	}
}

// containsIdentity checks if the given content already contains the identity.
func containsIdentity(content, identity string) bool {
	return strings.Contains(content, identity)
}
