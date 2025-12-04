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
            "parts": [
              {
                "text": "Hello agent."
              }
            ]
          },
          "finalResponse": {
            "role": "assistant",
            "parts": [
              {
                "text": "Greetings, user."
              }
            ]
          },
          "intermediateData": {
            "toolUses": [
              {
                "name": "calculator",
                "args": {
                  "operation": "add",
                  "a": 1,
                  "b": 2
                }
              }
            ],
            "toolResponses": [
              {
                "name": "calculator",
                "response": {
                  "result": 3
                }
              }
            ],
            "intermediateResponses": [
              [
                "assistant",
                [
                  {
                    "text": "Let me compute that."
                  }
                ]
              ]
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
	assert.Equal(t, "user", firstInvocation.UserContent.Role)
	assert.Len(t, firstInvocation.UserContent.Parts, 1)
	assert.Equal(t, "Hello agent.", firstInvocation.UserContent.Parts[0].Text)
	assert.Equal(t, "assistant", firstInvocation.FinalResponse.Role)
	assert.Len(t, firstInvocation.FinalResponse.Parts, 1)
	assert.Equal(t, "Greetings, user.", firstInvocation.FinalResponse.Parts[0].Text)
	assert.NotNil(t, firstInvocation.CreationTimestamp)
	assert.WithinDuration(t, time.Unix(1700000100, 0).UTC(), firstInvocation.CreationTimestamp.Time, time.Nanosecond)

	assert.NotNil(t, firstInvocation.IntermediateData)
	assert.Len(t, firstInvocation.IntermediateData.ToolUses, 1)
	assert.Equal(t, "calculator", firstInvocation.IntermediateData.ToolUses[0].Name)
	assert.Equal(t, map[string]any{"operation": "add", "a": float64(1), "b": float64(2)},
		firstInvocation.IntermediateData.ToolUses[0].Args)

	assert.Len(t, firstInvocation.IntermediateData.ToolResponses, 1)
	expectedToolResponse := map[string]any{"result": float64(3)}
	assert.Equal(t, "calculator", firstInvocation.IntermediateData.ToolResponses[0].Name)
	assert.Equal(t, expectedToolResponse, firstInvocation.IntermediateData.ToolResponses[0].Response)

	expectedIntermediateResponses := [][]any{
		{
			"assistant",
			[]any{
				map[string]any{"text": "Let me compute that."},
			},
		},
	}
	assert.Equal(t, expectedIntermediateResponses, firstInvocation.IntermediateData.IntermediateResponses)

	encoded, err := json.Marshal(evalSet)
	assert.NoError(t, err)
	assert.JSONEq(t, jsonData, string(encoded))
}
