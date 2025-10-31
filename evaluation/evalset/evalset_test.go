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
  "eval_set_id": "test-set",
  "name": "Test Set",
  "description": "Complete eval set JSON for testing.",
  "eval_cases": [
    {
      "eval_id": "case-42",
      "conversation": [
        {
          "invocation_id": "invoke-1",
          "user_content": {
            "role": "user",
            "parts": [
              {
                "text": "Hello agent."
              }
            ]
          },
          "final_response": {
            "role": "assistant",
            "parts": [
              {
                "text": "Greetings, user."
              }
            ]
          },
          "intermediate_data": {
            "tool_uses": [
              {
                "name": "calculator",
                "args": {
                  "operation": "add",
                  "a": 1,
                  "b": 2
                }
              }
            ],
            "tool_responses": [
              {
                "name": "calculator",
                "response": {
                  "result": 3
                }
              }
            ],
            "intermediate_responses": [
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
          "creation_timestamp": 1700000100
        }
      ],
      "session_input": {
        "app_name": "demo-app",
        "user_id": "user-42",
        "state": {
          "language": "en",
          "isPremium": true
        }
      },
      "creation_timestamp": 1700000200
    }
  ],
  "creation_timestamp": 1700000000
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
	assert.Equal(t, map[string]interface{}{"language": "en", "isPremium": true}, firstCase.SessionInput.State)
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
	assert.Equal(t, map[string]interface{}{"operation": "add", "a": float64(1), "b": float64(2)},
		firstInvocation.IntermediateData.ToolUses[0].Args)

	assert.Len(t, firstInvocation.IntermediateData.ToolResponses, 1)
	expectedToolResponse := map[string]interface{}{"result": float64(3)}
	assert.Equal(t, "calculator", firstInvocation.IntermediateData.ToolResponses[0].Name)
	assert.Equal(t, expectedToolResponse, firstInvocation.IntermediateData.ToolResponses[0].Response)

	expectedIntermediateResponses := [][]interface{}{
		{
			"assistant",
			[]interface{}{
				map[string]interface{}{"text": "Let me compute that."},
			},
		},
	}
	assert.Equal(t, expectedIntermediateResponses, firstInvocation.IntermediateData.IntermediateResponses)

	encoded, err := json.Marshal(evalSet)
	assert.NoError(t, err)
	assert.JSONEq(t, jsonData, string(encoded))
}
