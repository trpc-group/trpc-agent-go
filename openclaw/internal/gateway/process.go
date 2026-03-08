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
	"errors"
	"net/http"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/gwproto"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/internal/debugrecorder"
)

// ProcessMessage processes a gateway message request without an HTTP hop.
//
// It returns a JSON-serializable response payload and the HTTP-like status
// code that the /messages endpoint would use.
func (s *Server) ProcessMessage(
	ctx context.Context,
	req gwproto.MessageRequest,
) (rsp gwproto.MessageResponse, status int) {
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

	trace, created := s.ensureTrace(ctx, req)
	if created && trace != nil {
		ctx = debugrecorder.WithTrace(ctx, trace)
		startedAt := time.Now()
		defer func() {
			end := debugrecorder.TraceEnd{
				Duration: time.Since(startedAt),
			}
			switch {
			case rsp.Ignored:
				end.Status = "ignored"
			case status == http.StatusOK && rsp.Error == nil:
				end.Status = "ok"
			default:
				end.Status = "error"
			}
			if rsp.Error != nil {
				end.Error = rsp.Error.Message
			}
			_ = trace.Close(end)
		}()
	}

	if trace != nil {
		summary, err := debugrecorder.SummarizeRequest(trace, req)
		if err != nil {
			_ = trace.RecordError(err)
		}
		_ = trace.Record(debugrecorder.KindGatewayReq, summary)
	}

	userMsg, mentionText, err := s.normalizeUserMessage(ctx, req)
	if err != nil {
		if trace != nil {
			_ = trace.RecordError(err)
		}
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
		errMsg := "missing user_id or from"
		if trace != nil {
			_ = trace.RecordError(errString(errMsg))
		}
		return gwproto.MessageResponse{
			Error: &gwproto.APIError{
				Type:    errTypeInvalidRequest,
				Message: errMsg,
			},
		}, http.StatusBadRequest
	}

	if !s.isUserAllowed(userID) {
		errMsg := "user is not allowed"
		if trace != nil {
			_ = trace.RecordError(errString(errMsg))
		}
		return gwproto.MessageResponse{
			Error: &gwproto.APIError{
				Type:    errTypeUnauthorized,
				Message: errMsg,
			},
		}, http.StatusForbidden
	}

	if s.requireMention && msg.Thread != "" {
		if !containsAny(msg.Text, s.mentionPatterns) {
			if trace != nil {
				_ = trace.Record(
					debugrecorder.KindGatewayRsp,
					map[string]any{
						"ignored": true,
						"reason":  "missing mention",
					},
				)
			}
			return gwproto.MessageResponse{Ignored: true}, http.StatusOK
		}
	}

	sessionID := strings.TrimSpace(req.SessionID)
	if sessionID == "" {
		sessionIDFunc := s.sessionIDFunc
		if sessionIDFunc == nil {
			sessionIDFunc = DefaultSessionID
		}
		sessionID, err = sessionIDFunc(msg)
		if err != nil {
			if trace != nil {
				_ = trace.RecordError(err)
			}
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
		if errors.Is(err, errEmptyReplyValue) {
			reply = emptyReplyFallbackText
			rsp = gwproto.MessageResponse{
				SessionID: sessionID,
				RequestID: resolvedRequestID,
				Reply:     reply,
			}
			status = http.StatusOK
			if trace != nil {
				_ = trace.Record(
					debugrecorder.KindGatewayRsp,
					map[string]any{
						"status":     status,
						"session_id": sessionID,
						"request_id": resolvedRequestID,
						"reply":      reply,
						"warning":    err.Error(),
					},
				)
			}
			return rsp, status
		}
		log.WarnfContext(ctx, "gateway: run failed: %v", err)
		if trace != nil {
			_ = trace.RecordError(err)
			_ = trace.Record(
				debugrecorder.KindGatewayRsp,
				map[string]any{
					"status": http.StatusInternalServerError,
					"error":  err.Error(),
				},
			)
		}
		return gwproto.MessageResponse{
			Error: &gwproto.APIError{
				Type:    errTypeInternal,
				Message: err.Error(),
			},
		}, http.StatusInternalServerError
	}

	rsp = gwproto.MessageResponse{
		SessionID: sessionID,
		RequestID: resolvedRequestID,
		Reply:     reply,
	}
	status = http.StatusOK

	if trace != nil {
		_ = trace.Record(
			debugrecorder.KindGatewayRsp,
			map[string]any{
				"status":     status,
				"session_id": sessionID,
				"request_id": resolvedRequestID,
				"reply":      reply,
			},
		)
	}
	return rsp, status
}

// CancelRequest cancels an in-flight run by request ID.
//
// It returns (canceled=false, status=200) when no matching run exists.
func (s *Server) CancelRequest(
	ctx context.Context,
	requestID string,
) (canceled bool, apiErr *gwproto.APIError, status int) {
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

	rid := strings.TrimSpace(requestID)
	if rid == "" {
		return false, &gwproto.APIError{
			Type:    errTypeInvalidRequest,
			Message: "missing request_id",
		}, http.StatusBadRequest
	}

	return s.managed.Cancel(rid), nil, http.StatusOK
}

func (s *Server) ensureTrace(
	ctx context.Context,
	req gwproto.MessageRequest,
) (*debugrecorder.Trace, bool) {
	trace := debugrecorder.TraceFromContext(ctx)
	if trace != nil {
		return trace, false
	}
	if s == nil || s.recorder == nil {
		return nil, false
	}

	msg := inboundFromRequest(req, req.Text)
	userID := strings.TrimSpace(req.UserID)
	if userID == "" {
		userID = msg.From
	}

	sessionID := strings.TrimSpace(req.SessionID)
	if sessionID == "" {
		sessionIDFunc := s.sessionIDFunc
		if sessionIDFunc == nil {
			sessionIDFunc = DefaultSessionID
		}
		sid, err := sessionIDFunc(msg)
		if err == nil {
			sessionID = sid
		}
	}

	trace, err := s.recorder.Start(debugrecorder.TraceStart{
		Channel:   msg.Channel,
		UserID:    userID,
		SessionID: sessionID,
		Thread:    msg.Thread,
		MessageID: msg.MessageID,
		RequestID: strings.TrimSpace(req.RequestID),
		Source:    "gateway",
	})
	if err != nil {
		return nil, false
	}
	return trace, true
}

type errString string

func (e errString) Error() string { return string(e) }
