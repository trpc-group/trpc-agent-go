//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package gwclient provides an in-process client for the gateway HTTP
// handler.
package gwclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

const (
	headerContentType = "Content-Type"
	contentTypeJSON   = "application/json"

	methodPost = "POST"
)

// Client invokes the gateway handler without a network hop.
type Client struct {
	handler      http.Handler
	messagesPath string
	cancelPath   string
}

// New creates a new client for the gateway messages endpoint.
func New(
	handler http.Handler,
	messagesPath string,
	cancelPath string,
) (*Client, error) {
	if handler == nil {
		return nil, errors.New("gwclient: nil handler")
	}
	if messagesPath == "" {
		return nil, errors.New("gwclient: empty messages path")
	}
	return &Client{
		handler:      handler,
		messagesPath: messagesPath,
		cancelPath:   cancelPath,
	}, nil
}

// MessageRequest matches the gateway /messages JSON payload.
type MessageRequest struct {
	Channel   string `json:"channel,omitempty"`
	From      string `json:"from,omitempty"`
	Thread    string `json:"thread,omitempty"`
	MessageID string `json:"message_id,omitempty"`
	Text      string `json:"text,omitempty"`

	UserID    string `json:"user_id,omitempty"`
	SessionID string `json:"session_id,omitempty"`
	RequestID string `json:"request_id,omitempty"`
}

// APIError matches gateway error payloads.
type APIError struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

// MessageResponse matches the gateway /messages response JSON.
type MessageResponse struct {
	SessionID string    `json:"session_id,omitempty"`
	RequestID string    `json:"request_id,omitempty"`
	Reply     string    `json:"reply,omitempty"`
	Ignored   bool      `json:"ignored,omitempty"`
	Error     *APIError `json:"error,omitempty"`

	StatusCode int `json:"-"`
}

// SendMessage sends one message to the gateway handler.
func (c *Client) SendMessage(
	ctx context.Context,
	req MessageRequest,
) (MessageResponse, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return MessageResponse{}, fmt.Errorf(
			"gwclient: marshal request: %w", err,
		)
	}

	httpReq, err := http.NewRequestWithContext(
		ctx,
		methodPost,
		c.messagesPath,
		bytes.NewReader(body),
	)
	if err != nil {
		return MessageResponse{}, fmt.Errorf(
			"gwclient: new request: %w", err,
		)
	}
	httpReq.Header.Set(headerContentType, contentTypeJSON)

	rr := newResponseRecorder()
	c.handler.ServeHTTP(rr, httpReq)

	rsp := MessageResponse{StatusCode: rr.Code()}
	if err := json.Unmarshal(rr.BodyBytes(), &rsp); err != nil {
		return rsp, fmt.Errorf(
			"gwclient: unmarshal response: %w", err,
		)
	}

	if rsp.StatusCode != http.StatusOK {
		if rsp.Error == nil {
			return rsp, fmt.Errorf(
				"gwclient: status %d", rsp.StatusCode,
			)
		}
		return rsp, fmt.Errorf(
			"gwclient: status %d: %s: %s",
			rsp.StatusCode,
			rsp.Error.Type,
			rsp.Error.Message,
		)
	}
	return rsp, nil
}

type cancelRequest struct {
	RequestID string `json:"request_id,omitempty"`
}

type cancelResponse struct {
	Canceled bool `json:"canceled"`
}

type errorResponse struct {
	Error *APIError `json:"error,omitempty"`
}

// Cancel attempts to cancel a request by request_id.
func (c *Client) Cancel(
	ctx context.Context,
	requestID string,
) (bool, error) {
	if strings.TrimSpace(c.cancelPath) == "" {
		return false, errors.New("gwclient: empty cancel path")
	}
	if strings.TrimSpace(requestID) == "" {
		return false, errors.New("gwclient: empty request id")
	}

	body, err := json.Marshal(cancelRequest{RequestID: requestID})
	if err != nil {
		return false, fmt.Errorf("gwclient: marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(
		ctx,
		methodPost,
		c.cancelPath,
		bytes.NewReader(body),
	)
	if err != nil {
		return false, fmt.Errorf("gwclient: new request: %w", err)
	}
	httpReq.Header.Set(headerContentType, contentTypeJSON)

	rr := newResponseRecorder()
	c.handler.ServeHTTP(rr, httpReq)

	if rr.Code() != http.StatusOK {
		var rsp errorResponse
		_ = json.Unmarshal(rr.BodyBytes(), &rsp)
		if rsp.Error == nil {
			return false, fmt.Errorf("gwclient: status %d", rr.Code())
		}
		return false, fmt.Errorf(
			"gwclient: status %d: %s: %s",
			rr.Code(),
			rsp.Error.Type,
			rsp.Error.Message,
		)
	}

	var rsp cancelResponse
	if err := json.Unmarshal(rr.BodyBytes(), &rsp); err != nil {
		return false, fmt.Errorf("gwclient: unmarshal response: %w", err)
	}
	return rsp.Canceled, nil
}

type responseRecorder struct {
	header http.Header
	code   int
	body   bytes.Buffer
}

func newResponseRecorder() *responseRecorder {
	return &responseRecorder{
		header: make(http.Header),
		code:   http.StatusOK,
	}
}

func (r *responseRecorder) Header() http.Header {
	return r.header
}

func (r *responseRecorder) WriteHeader(statusCode int) {
	r.code = statusCode
}

func (r *responseRecorder) Write(b []byte) (int, error) {
	return r.body.Write(b)
}

func (r *responseRecorder) Code() int {
	return r.code
}

func (r *responseRecorder) BodyBytes() []byte {
	return r.body.Bytes()
}
