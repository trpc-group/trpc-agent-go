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

// protocolClient implements ACP client callbacks.
type protocolClient struct {
	activeTurns *activeTurnRegistry
	sessions    *acpSessionStore
}

var _ acpsdk.Client = (*protocolClient)(nil)

func (*protocolClient) ReadTextFile(
	ctx context.Context,
	_ acpsdk.ReadTextFileRequest,
) (acpsdk.ReadTextFileResponse, error) {
	logUnsupportedClientMethod(ctx, acpsdk.ClientMethodFsReadTextFile)
	return acpsdk.ReadTextFileResponse{}, acpsdk.NewMethodNotFound(acpsdk.ClientMethodFsReadTextFile)
}

func (*protocolClient) WriteTextFile(
	ctx context.Context,
	_ acpsdk.WriteTextFileRequest,
) (acpsdk.WriteTextFileResponse, error) {
	logUnsupportedClientMethod(ctx, acpsdk.ClientMethodFsWriteTextFile)
	return acpsdk.WriteTextFileResponse{}, acpsdk.NewMethodNotFound(acpsdk.ClientMethodFsWriteTextFile)
}

func (c *protocolClient) RequestPermission(
	ctx context.Context,
	request acpsdk.RequestPermissionRequest,
) (acpsdk.RequestPermissionResponse, error) {
	return c.checkPermission(ctx, request), nil
}

func (c *protocolClient) SessionUpdate(
	ctx context.Context,
	notification acpsdk.SessionNotification,
) error {
	if update := notification.Update.ConfigOptionUpdate; update != nil && c.sessions != nil {
		binding := c.sessions.lookupByID(notification.SessionId)
		if binding != nil {
			binding.replaceSessionConfig(update.ConfigOptions)
		} else {
			debugContext(
				ctx,
				"acpagent: session config update dropped without a session binding",
				"session_id", string(notification.SessionId),
			)
		}
	}
	turn, ok := c.activeTurns.get(notification.SessionId)
	if !ok {
		debugContext(
			ctx,
			"acpagent: session update dropped without an active turn",
			"session_id", string(notification.SessionId),
			"update_kind", sessionUpdateKind(notification.Update),
		)
		return nil
	}
	if err := turn.enqueue(notification); err != nil {
		invocationID := ""
		if turn.invocation != nil {
			invocationID = turn.invocation.InvocationID
		}
		warnContext(
			ctx,
			"acpagent: session update queue overflow",
			"session_id", string(notification.SessionId),
			"invocation_id", invocationID,
			"update_kind", sessionUpdateKind(notification.Update),
			"error", err,
		)
		return err
	}
	return nil
}

func (*protocolClient) CreateTerminal(
	ctx context.Context,
	_ acpsdk.CreateTerminalRequest,
) (acpsdk.CreateTerminalResponse, error) {
	logUnsupportedClientMethod(ctx, acpsdk.ClientMethodTerminalCreate)
	return acpsdk.CreateTerminalResponse{}, acpsdk.NewMethodNotFound(acpsdk.ClientMethodTerminalCreate)
}

func (*protocolClient) KillTerminal(
	ctx context.Context,
	_ acpsdk.KillTerminalRequest,
) (acpsdk.KillTerminalResponse, error) {
	logUnsupportedClientMethod(ctx, acpsdk.ClientMethodTerminalKill)
	return acpsdk.KillTerminalResponse{}, acpsdk.NewMethodNotFound(acpsdk.ClientMethodTerminalKill)
}

func (*protocolClient) TerminalOutput(
	ctx context.Context,
	_ acpsdk.TerminalOutputRequest,
) (acpsdk.TerminalOutputResponse, error) {
	logUnsupportedClientMethod(ctx, acpsdk.ClientMethodTerminalOutput)
	return acpsdk.TerminalOutputResponse{}, acpsdk.NewMethodNotFound(acpsdk.ClientMethodTerminalOutput)
}

func (*protocolClient) ReleaseTerminal(
	ctx context.Context,
	_ acpsdk.ReleaseTerminalRequest,
) (acpsdk.ReleaseTerminalResponse, error) {
	logUnsupportedClientMethod(ctx, acpsdk.ClientMethodTerminalRelease)
	return acpsdk.ReleaseTerminalResponse{}, acpsdk.NewMethodNotFound(acpsdk.ClientMethodTerminalRelease)
}

func (*protocolClient) WaitForTerminalExit(
	ctx context.Context,
	_ acpsdk.WaitForTerminalExitRequest,
) (acpsdk.WaitForTerminalExitResponse, error) {
	logUnsupportedClientMethod(ctx, acpsdk.ClientMethodTerminalWaitForExit)
	return acpsdk.WaitForTerminalExitResponse{}, acpsdk.NewMethodNotFound(acpsdk.ClientMethodTerminalWaitForExit)
}

func logUnsupportedClientMethod(ctx context.Context, method string) {
	warnContext(ctx, "acpagent: unsupported client method called", "method", method)
}
