//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package gateway

import (
	"context"
	"net/http"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/gwproto"
)

// ProcessMessage processes a gateway message request without an HTTP hop.
//
// It returns a JSON-serializable response payload and the HTTP-like status
// code that the /messages endpoint would use.
func (s *Server) ProcessMessage(
	ctx context.Context,
	req gwproto.MessageRequest,
) (gwproto.MessageResponse, int) {
	if ctx == nil {
		ctx = context.Background()
	}
	if s == nil {
		return gwproto.MessageResponse{
			Error: &gwproto.APIError{
				Type:    errTypeInternal,
				Message: "nil server",
			},
		}, http.StatusInternalServerError
	}

	userMsg, mentionText, err := s.normalizeUserMessage(ctx, req)
	if err != nil {
		return gwproto.MessageResponse{
			Error: &gwproto.APIError{
				Type:    errTypeInvalidRequest,
				Message: err.Error(),
			},
		}, http.StatusBadRequest
	}

	msg := inboundFromRequest(req, mentionText)

	userID := strings.TrimSpace(req.UserID)
	if userID == "" {
		userID = msg.From
	}
	if userID == "" {
		return gwproto.MessageResponse{
			Error: &gwproto.APIError{
				Type:    errTypeInvalidRequest,
				Message: "missing user_id or from",
			},
		}, http.StatusBadRequest
	}

	if !s.isUserAllowed(userID) {
		return gwproto.MessageResponse{
			Error: &gwproto.APIError{
				Type:    errTypeUnauthorized,
				Message: "user is not allowed",
			},
		}, http.StatusForbidden
	}

	if s.requireMention && msg.Thread != "" {
		if !containsAny(msg.Text, s.mentionPatterns) {
			return gwproto.MessageResponse{
				Ignored: true,
			}, http.StatusOK
		}
	}

	sessionID := strings.TrimSpace(req.SessionID)
	if sessionID == "" {
		sessionIDFunc := s.sessionIDFunc
		if sessionIDFunc == nil {
			sessionIDFunc = DefaultSessionID
		}

		var err error
		sessionID, err = sessionIDFunc(msg)
		if err != nil {
			return gwproto.MessageResponse{
				Error: &gwproto.APIError{
					Type:    errTypeInvalidRequest,
					Message: err.Error(),
				},
			}, http.StatusBadRequest
		}
	}

	requestID := strings.TrimSpace(req.RequestID)
	reply, resolvedRequestID, err := s.run(
		ctx,
		userID,
		sessionID,
		requestID,
		userMsg,
	)
	if err != nil {
		log.WarnfContext(ctx, "gateway: run failed: %v", err)
		return gwproto.MessageResponse{
			Error: &gwproto.APIError{
				Type:    errTypeInternal,
				Message: err.Error(),
			},
		}, http.StatusInternalServerError
	}

	return gwproto.MessageResponse{
		SessionID: sessionID,
		RequestID: resolvedRequestID,
		Reply:     reply,
	}, http.StatusOK
}

// CancelRequest cancels a running request by its request ID.
//
// It returns the canceled flag, an optional gateway-style API error, and the
// HTTP-like status code that the /cancel endpoint would use.
func (s *Server) CancelRequest(
	ctx context.Context,
	requestID string,
) (bool, *gwproto.APIError, int) {
	if ctx == nil {
		ctx = context.Background()
	}
	if s == nil {
		return false, &gwproto.APIError{
			Type:    errTypeInternal,
			Message: "nil server",
		}, http.StatusInternalServerError
	}
	if s.managed == nil {
		return false, &gwproto.APIError{
			Type:    errTypeUnsupported,
			Message: "runner does not support cancel",
		}, http.StatusNotImplemented
	}
	requestID = strings.TrimSpace(requestID)
	if requestID == "" {
		return false, &gwproto.APIError{
			Type:    errTypeInvalidRequest,
			Message: "missing request_id",
		}, http.StatusBadRequest
	}
	return s.managed.Cancel(requestID), nil, http.StatusOK
}
