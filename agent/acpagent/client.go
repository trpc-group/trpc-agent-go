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

	acpsdk "github.com/coder/acp-go-sdk"
)

// protocolClient implements unsupported ACP client callbacks conservatively until
// the corresponding capability phase is implemented.
type protocolClient struct{}

var _ acpsdk.Client = (*protocolClient)(nil)

func (protocolClient) ReadTextFile(
	context.Context,
	acpsdk.ReadTextFileRequest,
) (acpsdk.ReadTextFileResponse, error) {
	return acpsdk.ReadTextFileResponse{}, acpsdk.NewMethodNotFound(acpsdk.ClientMethodFsReadTextFile)
}

func (protocolClient) WriteTextFile(
	context.Context,
	acpsdk.WriteTextFileRequest,
) (acpsdk.WriteTextFileResponse, error) {
	return acpsdk.WriteTextFileResponse{}, acpsdk.NewMethodNotFound(acpsdk.ClientMethodFsWriteTextFile)
}

func (protocolClient) RequestPermission(
	context.Context,
	acpsdk.RequestPermissionRequest,
) (acpsdk.RequestPermissionResponse, error) {
	return acpsdk.RequestPermissionResponse{
		Outcome: acpsdk.NewRequestPermissionOutcomeCancelled(),
	}, nil
}

func (protocolClient) SessionUpdate(context.Context, acpsdk.SessionNotification) error {
	return nil
}

func (protocolClient) CreateTerminal(
	context.Context,
	acpsdk.CreateTerminalRequest,
) (acpsdk.CreateTerminalResponse, error) {
	return acpsdk.CreateTerminalResponse{}, acpsdk.NewMethodNotFound(acpsdk.ClientMethodTerminalCreate)
}

func (protocolClient) KillTerminal(
	context.Context,
	acpsdk.KillTerminalRequest,
) (acpsdk.KillTerminalResponse, error) {
	return acpsdk.KillTerminalResponse{}, acpsdk.NewMethodNotFound(acpsdk.ClientMethodTerminalKill)
}

func (protocolClient) TerminalOutput(
	context.Context,
	acpsdk.TerminalOutputRequest,
) (acpsdk.TerminalOutputResponse, error) {
	return acpsdk.TerminalOutputResponse{}, acpsdk.NewMethodNotFound(acpsdk.ClientMethodTerminalOutput)
}

func (protocolClient) ReleaseTerminal(
	context.Context,
	acpsdk.ReleaseTerminalRequest,
) (acpsdk.ReleaseTerminalResponse, error) {
	return acpsdk.ReleaseTerminalResponse{}, acpsdk.NewMethodNotFound(acpsdk.ClientMethodTerminalRelease)
}

func (protocolClient) WaitForTerminalExit(
	context.Context,
	acpsdk.WaitForTerminalExitRequest,
) (acpsdk.WaitForTerminalExitResponse, error) {
	return acpsdk.WaitForTerminalExitResponse{}, acpsdk.NewMethodNotFound(acpsdk.ClientMethodTerminalWaitForExit)
}
