//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package evalset

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

func TestEvalSetJSONRoundTrip(t *testing.T) {
	jsonData := `{
  "evalSetId": "test-set",
  "name": "Test Set",
  "description": "Complete eval set JSON for testing.",
  "evalCases": [
    {
      "evalId": "case-42",
      "conversation": [
        {
          "invocationId": "invoke-1",
          "userContent": {
            "role": "user",
            "content": "Hello agent."
          },
          "finalResponse": {
            "role": "assistant",
            "content": "Greetings, user."
          },
          "intermediateData": {
            "toolCalls": [
              {
                "id": "use-1",
                "type": "function",
                "function": {
                  "name": "calculator",
                  "arguments": "{\"operation\":\"add\",\"a\":1,\"b\":2}"
                }
              }
            ],
            "toolResponses": [
              {
                "role": "tool",
                "tool_id": "use-1",
                "tool_name": "calculator",
                "content": "{\"result\":3}"
              }
            ],
            "intermediateResponses": [
              {
                "role": "assistant",
                "content": "Let me compute that."
              }
            ]
          },
          "creationTimestamp": 1700000100
        }
      ],
      "sessionInput": {
        "appName": "demo-app",
        "userId": "user-42",
        "state": {
          "language": "en",
          "isPremium": true
        }
      },
      "creationTimestamp": 1700000200
    }
  ],
  "creationTimestamp": 1700000000
}`

	var evalSet EvalSet
	err := json.Unmarshal([]byte(jsonData), &evalSet)
	assert.NoError(t, err)

	assert.Equal(t, "test-set", evalSet.EvalSetID)
	assert.Equal(t, "Test Set", evalSet.Name)
	assert.Equal(t, "Complete eval set JSON for testing.", evalSet.Description)
	assert.NotNil(t, evalSet.CreationTimestamp)
	assert.WithinDuration(t, time.Unix(1700000000, 0).UTC(), evalSet.CreationTimestamp.Time, time.Nanosecond)

	assert.Len(t, evalSet.EvalCases, 1)

	firstCase := evalSet.EvalCases[0]
	assert.Equal(t, "case-42", firstCase.EvalID)
	assert.NotNil(t, firstCase.SessionInput)
	assert.Equal(t, "demo-app", firstCase.SessionInput.AppName)
	assert.Equal(t, "user-42", firstCase.SessionInput.UserID)
	assert.Equal(t, map[string]any{"language": "en", "isPremium": true}, firstCase.SessionInput.State)
	assert.NotNil(t, firstCase.CreationTimestamp)
	assert.WithinDuration(t, time.Unix(1700000200, 0).UTC(), firstCase.CreationTimestamp.Time, time.Nanosecond)

	assert.Len(t, firstCase.Conversation, 1)
	firstInvocation := firstCase.Conversation[0]
	assert.Equal(t, "invoke-1", firstInvocation.InvocationID)
	assert.Equal(t, model.RoleUser, firstInvocation.UserContent.Role)
	assert.Equal(t, "Hello agent.", firstInvocation.UserContent.Content)
	assert.Equal(t, model.RoleAssistant, firstInvocation.FinalResponse.Role)
	assert.Equal(t, "Greetings, user.", firstInvocation.FinalResponse.Content)
	assert.NotNil(t, firstInvocation.CreationTimestamp)
	assert.WithinDuration(t, time.Unix(1700000100, 0).UTC(), firstInvocation.CreationTimestamp.Time, time.Nanosecond)

	assert.NotNil(t, firstInvocation.IntermediateData)
	assert.Len(t, firstInvocation.IntermediateData.ToolCalls, 1)
	assert.Equal(t, "calculator", firstInvocation.IntermediateData.ToolCalls[0].Function.Name)
	var args map[string]any
	assert.NoError(t, json.Unmarshal(firstInvocation.IntermediateData.ToolCalls[0].Function.Arguments, &args))
	assert.Equal(t, map[string]any{"operation": "add", "a": float64(1), "b": float64(2)}, args)

	assert.Len(t, firstInvocation.IntermediateData.ToolResponses, 1)
	expectedToolResponse := map[string]any{"result": float64(3)}
	assert.Equal(t, "use-1", firstInvocation.IntermediateData.ToolResponses[0].ToolID)
	assert.Equal(t, "calculator", firstInvocation.IntermediateData.ToolResponses[0].ToolName)
	var response map[string]any
	assert.NoError(t, json.Unmarshal([]byte(firstInvocation.IntermediateData.ToolResponses[0].Content), &response))
	assert.Equal(t, expectedToolResponse, response)

	expectedIntermediateResponses := []*model.Message{
		{
			Role:    "assistant",
			Content: "Let me compute that.",
		},
	}
	assert.Equal(t, expectedIntermediateResponses, firstInvocation.IntermediateData.IntermediateResponses)

	encoded, err := json.Marshal(evalSet)
	assert.NoError(t, err)
	assert.JSONEq(t, jsonData, string(encoded))
}
