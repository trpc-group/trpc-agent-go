//
// Tencent is pleased to support the open source community by making tRPC available.
//
// Copyright (C) 2025 Tencent.
// All rights reserved.
//
// If you have downloaded a copy of the tRPC source code from Tencent,
// please note that tRPC source code is licensed under the  Apache 2.0 License,
// A copy of the Apache 2.0 License is included in this file.
//
//

// Package schema defines JSON schema structs used by the CLI HTTP server.
// These types are internal – they are not intended to be imported by other
// packages. They only exist to facilitate request/response marshalling.
package schema

// ADKSession mirrors the structure expected by ADK Web UI for a session.
// Field names follow the camel-case convention required by the UI.
type ADKSession struct {
	AppName    string                   `json:"appName"`
	UserID     string                   `json:"userId"`
	ID         string                   `json:"id"`
	CreateTime int64                    `json:"createTime"`
	UpdateTime int64                    `json:"updateTime"`
	State      map[string][]byte        `json:"state"`
	Events     []map[string]interface{} `json:"events"`
}

// -----------------------------------------------------------------------------
// Incoming request payloads ----------------------------------------------------
// -----------------------------------------------------------------------------

// Part represents a single message segment used by ADK Web.
type Part struct {
	Text             string            `json:"text,omitempty"`
	InlineData       *InlineData       `json:"inlineData,omitempty"`
	FunctionCall     *FunctionCall     `json:"functionCall,omitempty"`
	FunctionResponse *FunctionResponse `json:"functionResponse,omitempty"`
}

// InlineData encapsulates binary data (image/audio/video/file).
type InlineData struct {
	Data        string `json:"data"`
	MimeType    string `json:"mimeType"`
	DisplayName string `json:"displayName,omitempty"`
}

// FunctionCall matches GenAI functionCall part.
type FunctionCall struct {
	Name string                 `json:"name"`
	Args map[string]interface{} `json:"args,omitempty"`
}

// FunctionResponse matches GenAI functionResponse part.
type FunctionResponse struct {
	Name     string      `json:"name"`
	Response interface{} `json:"response"`
	ID       string      `json:"id,omitempty"`
}

// Content matches the GenAI content contract used by ADK Web.
type Content struct {
	Role  string `json:"role"`
	Parts []Part `json:"parts"`
}

// AgentRunRequest mirrors the FastAPI schema used by ADK Web.
// Field names are camel-case to match JSON directly.
type AgentRunRequest struct {
	AppName    string  `json:"appName"`
	UserID     string  `json:"userId"`
	SessionID  string  `json:"sessionId"`
	NewMessage Content `json:"newMessage"`
	Streaming  bool    `json:"streaming"`
}
