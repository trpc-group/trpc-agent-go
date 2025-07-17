//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.
// All rights reserved.
//
// If you have downloaded a copy of the tRPC source code from Tencent,
// please note that tRPC source code is licensed under the  Apache 2.0 License,
// A copy of the Apache 2.0 License is included in this file.
//
//

package schema

import (
	"encoding/json"
	"testing"
)

func TestADKSession_MarshalJSON(t *testing.T) {
	session := ADKSession{
		AppName:    "test-app",
		UserID:     "test-user",
		ID:         "test-session-id",
		CreateTime: 1234567890,
		UpdateTime: 1234567891,
		State:      map[string][]byte{"key1": []byte("value1")},
		Events:     []map[string]interface{}{},
	}

	data, err := json.Marshal(session)
	if err != nil {
		t.Fatalf("failed to marshal session: %v", err)
	}

	var unmarshaled ADKSession
	if err := json.Unmarshal(data, &unmarshaled); err != nil {
		t.Fatalf("failed to unmarshal session: %v", err)
	}

	if unmarshaled.AppName != "test-app" {
		t.Errorf("expected AppName 'test-app', got '%s'", unmarshaled.AppName)
	}

	if unmarshaled.UserID != "test-user" {
		t.Errorf("expected UserID 'test-user', got '%s'", unmarshaled.UserID)
	}

	if unmarshaled.ID != "test-session-id" {
		t.Errorf("expected ID 'test-session-id', got '%s'", unmarshaled.ID)
	}

	if unmarshaled.CreateTime != 1234567890 {
		t.Errorf("expected CreateTime 1234567890, got %d", unmarshaled.CreateTime)
	}

	if unmarshaled.UpdateTime != 1234567891 {
		t.Errorf("expected UpdateTime 1234567891, got %d", unmarshaled.UpdateTime)
	}
}

func TestContent_MarshalJSON(t *testing.T) {
	content := Content{
		Role: "user",
		Parts: []Part{
			{Text: "Hello, world!"},
			{
				FunctionCall: &FunctionCall{
					Name: "test_function",
					Args: map[string]interface{}{
						"param1": "value1",
						"param2": 42,
					},
				},
			},
		},
	}

	data, err := json.Marshal(content)
	if err != nil {
		t.Fatalf("failed to marshal content: %v", err)
	}

	var unmarshaled Content
	if err := json.Unmarshal(data, &unmarshaled); err != nil {
		t.Fatalf("failed to unmarshal content: %v", err)
	}

	if unmarshaled.Role != "user" {
		t.Errorf("expected Role 'user', got '%s'", unmarshaled.Role)
	}

	if len(unmarshaled.Parts) != 2 {
		t.Errorf("expected 2 parts, got %d", len(unmarshaled.Parts))
	}

	if unmarshaled.Parts[0].Text != "Hello, world!" {
		t.Errorf("expected first part text 'Hello, world!', got '%s'", unmarshaled.Parts[0].Text)
	}

	if unmarshaled.Parts[1].FunctionCall == nil {
		t.Error("expected second part to have function call")
	}

	if unmarshaled.Parts[1].FunctionCall.Name != "test_function" {
		t.Errorf("expected function name 'test_function', got '%s'", unmarshaled.Parts[1].FunctionCall.Name)
	}
}

func TestAgentRunRequest_MarshalJSON(t *testing.T) {
	request := AgentRunRequest{
		AppName:   "test-app",
		UserID:    "test-user",
		SessionID: "test-session",
		NewMessage: Content{
			Role: "user",
			Parts: []Part{
				{Text: "Hello, world!"},
			},
		},
		Streaming: true,
	}

	data, err := json.Marshal(request)
	if err != nil {
		t.Fatalf("failed to marshal request: %v", err)
	}

	var unmarshaled AgentRunRequest
	if err := json.Unmarshal(data, &unmarshaled); err != nil {
		t.Fatalf("failed to unmarshal request: %v", err)
	}

	if unmarshaled.AppName != "test-app" {
		t.Errorf("expected AppName 'test-app', got '%s'", unmarshaled.AppName)
	}

	if unmarshaled.UserID != "test-user" {
		t.Errorf("expected UserID 'test-user', got '%s'", unmarshaled.UserID)
	}

	if unmarshaled.SessionID != "test-session" {
		t.Errorf("expected SessionID 'test-session', got '%s'", unmarshaled.SessionID)
	}

	if !unmarshaled.Streaming {
		t.Error("expected Streaming to be true")
	}

	if unmarshaled.NewMessage.Role != "user" {
		t.Errorf("expected message role 'user', got '%s'", unmarshaled.NewMessage.Role)
	}
}

func TestFunctionCall_MarshalJSON(t *testing.T) {
	functionCall := FunctionCall{
		Name: "test_function",
		Args: map[string]interface{}{
			"param1": "value1",
			"param2": 42,
			"param3": true,
		},
	}

	data, err := json.Marshal(functionCall)
	if err != nil {
		t.Fatalf("failed to marshal function call: %v", err)
	}

	var unmarshaled FunctionCall
	if err := json.Unmarshal(data, &unmarshaled); err != nil {
		t.Fatalf("failed to unmarshal function call: %v", err)
	}

	if unmarshaled.Name != "test_function" {
		t.Errorf("expected Name 'test_function', got '%s'", unmarshaled.Name)
	}

	if len(unmarshaled.Args) != 3 {
		t.Errorf("expected 3 args, got %d", len(unmarshaled.Args))
	}

	if unmarshaled.Args["param1"] != "value1" {
		t.Errorf("expected param1 'value1', got '%v'", unmarshaled.Args["param1"])
	}

	if unmarshaled.Args["param2"] != float64(42) {
		t.Errorf("expected param2 42, got %v", unmarshaled.Args["param2"])
	}

	if unmarshaled.Args["param3"] != true {
		t.Errorf("expected param3 true, got %v", unmarshaled.Args["param3"])
	}
}

func TestFunctionResponse_MarshalJSON(t *testing.T) {
	functionResponse := FunctionResponse{
		Name:     "test_function",
		Response: "test response",
		ID:       "test-id",
	}

	data, err := json.Marshal(functionResponse)
	if err != nil {
		t.Fatalf("failed to marshal function response: %v", err)
	}

	var unmarshaled FunctionResponse
	if err := json.Unmarshal(data, &unmarshaled); err != nil {
		t.Fatalf("failed to unmarshal function response: %v", err)
	}

	if unmarshaled.Name != "test_function" {
		t.Errorf("expected Name 'test_function', got '%s'", unmarshaled.Name)
	}

	if unmarshaled.Response != "test response" {
		t.Errorf("expected Response 'test response', got '%v'", unmarshaled.Response)
	}

	if unmarshaled.ID != "test-id" {
		t.Errorf("expected ID 'test-id', got '%s'", unmarshaled.ID)
	}
}
