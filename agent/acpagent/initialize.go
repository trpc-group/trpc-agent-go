//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package acpagent

import (
	"context"
	"errors"
	"fmt"

	acpsdk "github.com/coder/acp-go-sdk"
)

const supportedProtocolVersion = acpsdk.ProtocolVersion(1)

var errUnsupportedProtocolVersion = errors.New("acpagent: unsupported protocol version")

type acpAgentDescriptor struct {
	agentCapabilities acpsdk.AgentCapabilities
	authMethods       []acpsdk.AuthMethod
	agentInfo         *acpsdk.Implementation
}

func newInitializeRequest(terminalAuth bool) acpsdk.InitializeRequest {
	return acpsdk.InitializeRequest{
		ProtocolVersion: supportedProtocolVersion,
		ClientCapabilities: acpsdk.ClientCapabilities{
			Auth: acpsdk.AuthCapabilities{
				Terminal: terminalAuth,
			},
			Fs:       acpsdk.FileSystemCapabilities{},
			Terminal: false,
		},
	}
}

func (m *connectionManager) initialize(
	ctx context.Context,
	process *processConnection,
) error {
	if ctx == nil {
		ctx = context.Background()
	}
	response, err := process.connection.Initialize(
		ctx,
		newInitializeRequest(m.terminalAuth),
	)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			err = errors.Join(err, ctxErr)
		}
		return fmt.Errorf("acpagent: initialize connection: %w", err)
	}
	if response.ProtocolVersion != supportedProtocolVersion {
		return fmt.Errorf(
			"%w: got %d, want %d",
			errUnsupportedProtocolVersion,
			response.ProtocolVersion,
			supportedProtocolVersion,
		)
	}
	process.agentDescriptor = captureACPAgentDescriptor(response)
	return nil
}

func captureACPAgentDescriptor(
	response acpsdk.InitializeResponse,
) acpAgentDescriptor {
	descriptor := acpAgentDescriptor{
		agentCapabilities: response.AgentCapabilities,
		authMethods:       append([]acpsdk.AuthMethod(nil), response.AuthMethods...),
	}
	if response.AgentInfo != nil {
		agentInfo := *response.AgentInfo
		descriptor.agentInfo = &agentInfo
	}
	return descriptor
}
