//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package agent

import (
	"reflect"

	"trpc.group/trpc-go/trpc-agent-go/artifact"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

// InvocationOptions is the options for the Invocation.
type InvocationOptions func(*Invocation)

// WithInvocationID set invocation id for the Invocation.
func WithInvocationID(id string) InvocationOptions {
	return func(inv *Invocation) {
		inv.InvocationID = id
	}
}

// WithInvocationAgent set agent for the Invocation.
func WithInvocationAgent(agent Agent) InvocationOptions {
	return func(inv *Invocation) {
		inv.Agent = agent
		inv.AgentName = agent.Info().Name
	}
}

// WithInvocationBranch set branch for the Invocation.
func WithInvocationBranch(branch string) InvocationOptions {
	return func(inv *Invocation) {
		inv.Branch = branch
	}
}

// WithInvocationEndInvocation set endInvocation for the Invocation.
func WithInvocationEndInvocation(endInvocation bool) InvocationOptions {
	return func(inv *Invocation) {
		inv.EndInvocation = endInvocation
	}
}

// WithInvocationSession set session for the Invocation.
func WithInvocationSession(session *session.Session) InvocationOptions {
	return func(inv *Invocation) {
		inv.Session = session
	}
}

// WithInvocationModel set model for the Invocation.
func WithInvocationModel(model model.Model) InvocationOptions {
	return func(inv *Invocation) {
		inv.Model = model
	}
}

// WithInvocationMessage set message for the Invocation.
func WithInvocationMessage(message model.Message) InvocationOptions {
	return func(inv *Invocation) {
		inv.Message = message
	}
}

// WithInvocationRunOptions set runOptions for the Invocation.
func WithInvocationRunOptions(runOptions RunOptions) InvocationOptions {
	return func(inv *Invocation) {
		inv.RunOptions = runOptions
	}
}

// WithInvocationTransferInfo set transferInfo for the Invocation.
func WithInvocationTransferInfo(transferInfo *TransferInfo) InvocationOptions {
	return func(inv *Invocation) {
		inv.TransferInfo = transferInfo
	}
}

// WithInvocationStructuredOutput set structuredOutput for the Invocation.
func WithInvocationStructuredOutput(structuredOutput *model.StructuredOutput) InvocationOptions {
	return func(inv *Invocation) {
		inv.StructuredOutput = structuredOutput
	}
}

// WithInvocationStructuredOutputType set structuredOutputType for the Invocation.
func WithInvocationStructuredOutputType(structuredOutputType reflect.Type) InvocationOptions {
	return func(inv *Invocation) {
		inv.StructuredOutputType = structuredOutputType
	}
}

// WithInvocationMemoryService set memoryService for the Invocation.
func WithInvocationMemoryService(memoryService memory.Service) InvocationOptions {
	return func(inv *Invocation) {
		inv.MemoryService = memoryService
	}
}

// WithInvocationArtifactService set artifactService for the Invocation.
func WithInvocationArtifactService(artifactService artifact.Service) InvocationOptions {
	return func(inv *Invocation) {
		inv.ArtifactService = artifactService
	}
}

// WithInvocationEventFilterKey set eventFilterKey for the Invocation.
func WithInvocationEventFilterKey(key string) InvocationOptions {
	return func(inv *Invocation) {
		inv.eventFilterKey = key
	}
}
