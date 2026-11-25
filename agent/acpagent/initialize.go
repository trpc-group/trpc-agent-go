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
	"time"

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
	started := time.Now()
	debugContext(
		ctx,
		"acpagent: initializing connection",
		append(
			processLogFields(process),
			"protocol_version", supportedProtocolVersion,
			"terminal_auth", m.terminalAuth,
		)...,
	)
	response, err := process.connection.Initialize(
		ctx,
		newInitializeRequest(m.terminalAuth),
	)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			err = errors.Join(err, ctxErr)
		}
		err = fmt.Errorf("acpagent: initialize connection: %w", err)
		errorContext(
			ctx,
			"acpagent: initialize connection failed",
			append(processLogFields(process), "duration", time.Since(started), "error", err)...,
		)
		return err
	}
	if response.ProtocolVersion != supportedProtocolVersion {
		err := fmt.Errorf(
			"%w: got %d, want %d",
			errUnsupportedProtocolVersion,
			response.ProtocolVersion,
			supportedProtocolVersion,
		)
		errorContext(
			ctx,
			"acpagent: unsupported protocol version",
			append(processLogFields(process), "duration", time.Since(started), "error", err)...,
		)
		return err
	}
	process.agentDescriptor = captureACPAgentDescriptor(response)
	agentName := ""
	agentVersion := ""
	if process.agentDescriptor.agentInfo != nil {
		agentName = process.agentDescriptor.agentInfo.Name
		agentVersion = process.agentDescriptor.agentInfo.Version
	}
	infoContext(
		ctx,
		"acpagent: connection initialized",
		append(
			processLogFields(process),
			"protocol_version", response.ProtocolVersion,
			"agent_name", agentName,
			"agent_version", agentVersion,
			"auth_method_count", len(response.AuthMethods),
			"load_session", process.agentDescriptor.agentCapabilities.LoadSession,
			"resume_session", process.agentDescriptor.agentCapabilities.SessionCapabilities.Resume != nil,
			"close_session", process.agentDescriptor.agentCapabilities.SessionCapabilities.Close != nil,
			"duration", time.Since(started),
		)...,
	)
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
