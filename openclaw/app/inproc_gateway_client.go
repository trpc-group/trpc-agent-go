//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"trpc.group/trpc-go/trpc-agent-go/openclaw/gwclient"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/internal/gateway"
)

const (
	gwclientStatusErrFmt = "gwclient: status %d"

	gwclientStatusAPIErrorFmt = "gwclient: status %d: %s: %s"

	errNilGatewayServer = "gateway client: nil server"
)

type inProcGatewayClient struct {
	srv *gateway.Server
}

func newInProcGatewayClient(srv *gateway.Server) *inProcGatewayClient {
	return &inProcGatewayClient{srv: srv}
}

func (c *inProcGatewayClient) SendMessage(
	ctx context.Context,
	req gwclient.MessageRequest,
) (gwclient.MessageResponse, error) {
	if c == nil || c.srv == nil {
		return gwclient.MessageResponse{}, errors.New(errNilGatewayServer)
	}

	rsp, status := c.srv.ProcessMessage(ctx, req)
	out := gwclient.MessageResponse{
		SessionID:  rsp.SessionID,
		RequestID:  rsp.RequestID,
		Reply:      rsp.Reply,
		Ignored:    rsp.Ignored,
		Error:      rsp.Error,
		StatusCode: status,
	}
	if err := errorForGWStatus(status, out.Error); err != nil {
		return out, err
	}
	return out, nil
}

func (c *inProcGatewayClient) Cancel(
	ctx context.Context,
	requestID string,
) (bool, error) {
	if c == nil || c.srv == nil {
		return false, errors.New(errNilGatewayServer)
	}

	canceled, apiErr, status := c.srv.CancelRequest(ctx, requestID)
	if err := errorForGWStatus(status, apiErr); err != nil {
		return false, err
	}
	return canceled, nil
}

func errorForGWStatus(status int, apiErr *gwclient.APIError) error {
	if status == http.StatusOK {
		return nil
	}
	if apiErr == nil {
		return fmt.Errorf(gwclientStatusErrFmt, status)
	}
	return fmt.Errorf(
		gwclientStatusAPIErrorFmt,
		status,
		apiErr.Type,
		apiErr.Message,
	)
}
