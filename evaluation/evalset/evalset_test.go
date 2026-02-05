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
      "contextMessages": [
        {
          "role": "system",
          "content": "You are a helpful assistant."
        },
        {
          "role": "user",
          "content": "Previous user message."
        },
        {
          "role": "assistant",
          "content": "Previous assistant message."
        }
      ],
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
          "tools": [
            {
              "id": "use-1",
              "name": "calculator",
              "arguments": {
                "operation": "add",
                "a": 1,
                "b": 2
              },
              "result": {
                "result": 3
              }
            }
          ],
          "intermediateResponses": [
            {
              "role": "assistant",
              "content": "Let me compute that."
            }
          ],
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
	assert.Len(t, firstCase.ContextMessages, 3)
	assert.Equal(t, model.RoleSystem, firstCase.ContextMessages[0].Role)
	assert.Equal(t, "You are a helpful assistant.", firstCase.ContextMessages[0].Content)
	assert.Equal(t, model.RoleUser, firstCase.ContextMessages[1].Role)
	assert.Equal(t, "Previous user message.", firstCase.ContextMessages[1].Content)
	assert.Equal(t, model.RoleAssistant, firstCase.ContextMessages[2].Role)
	assert.Equal(t, "Previous assistant message.", firstCase.ContextMessages[2].Content)
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

	assert.Len(t, firstInvocation.Tools, 1)
	assert.Equal(t, "use-1", firstInvocation.Tools[0].ID)
	assert.Equal(t, "calculator", firstInvocation.Tools[0].Name)
	assert.Equal(t, map[string]any{"operation": "add", "a": float64(1), "b": float64(2)}, firstInvocation.Tools[0].Arguments)

	nestedArgs := map[string]any{
		"operation": "batch",
		"items": []any{
			map[string]any{"op": "add", "a": float64(1), "b": float64(2)},
			map[string]any{"op": "mul", "a": float64(3), "b": float64(4)},
		},
	}
	firstInvocation.Tools[0].Arguments = nestedArgs
	encoded, err := json.Marshal(evalSet)
	assert.NoError(t, err)
	var decoded EvalSet
	assert.NoError(t, json.Unmarshal(encoded, &decoded))
	assert.Len(t, decoded.EvalCases[0].Conversation[0].Tools, 1)
	assert.Equal(t, nestedArgs, decoded.EvalCases[0].Conversation[0].Tools[0].Arguments)

	expectedToolResponse := map[string]any{"result": float64(3)}
	assert.Equal(t, expectedToolResponse, firstInvocation.Tools[0].Result)

	expectedIntermediateResponses := []*model.Message{
		{
			Role:    "assistant",
			Content: "Let me compute that.",
		},
	}
	assert.Equal(t, expectedIntermediateResponses, firstInvocation.IntermediateResponses)

	firstInvocation.Tools[0].Arguments = map[string]any{
		"operation": "add",
		"a":         float64(1),
		"b":         float64(2),
	}
	encoded, err = json.Marshal(evalSet)
	assert.NoError(t, err)
	assert.JSONEq(t, jsonData, string(encoded))
}

func TestEvalSetJSONRoundTripWithActualConversation(t *testing.T) {
	jsonData := `{
  "evalSetId": "trace-set",
  "name": "Trace Set",
  "evalCases": [
    {
      "evalId": "case-trace",
      "evalMode": "trace",
      "conversation": [
        {
          "invocationId": "exp-1",
          "userContent": {
            "role": "user",
            "content": "hello"
          },
          "finalResponse": {
            "role": "assistant",
            "content": "expected"
          }
        }
      ],
      "actualConversation": [
        {
          "invocationId": "act-1",
          "userContent": {
            "role": "user",
            "content": "hello"
          },
          "finalResponse": {
            "role": "assistant",
            "content": "actual"
          }
        }
      ],
      "sessionInput": {
        "userId": "user-1"
      }
    }
  ]
}`

	var evalSet EvalSet
	err := json.Unmarshal([]byte(jsonData), &evalSet)
	assert.NoError(t, err)
	assert.Len(t, evalSet.EvalCases, 1)
	assert.Equal(t, EvalModeTrace, evalSet.EvalCases[0].EvalMode)
	assert.Len(t, evalSet.EvalCases[0].Conversation, 1)
	assert.Len(t, evalSet.EvalCases[0].ActualConversation, 1)
	assert.Equal(t, "act-1", evalSet.EvalCases[0].ActualConversation[0].InvocationID)
	assert.NotNil(t, evalSet.EvalCases[0].ActualConversation[0].FinalResponse)
	assert.Equal(t, "actual", evalSet.EvalCases[0].ActualConversation[0].FinalResponse.Content)

	encoded, err := json.Marshal(evalSet)
	assert.NoError(t, err)
	assert.JSONEq(t, jsonData, string(encoded))
}

func TestEvalSetNestedToolCallArgsFromJSON(t *testing.T) {
	const jsonData = `{
  "evalSetId": "nested-set",
  "name": "Nested Args",
  "evalCases": [
    {
      "evalId": "case-nested",
      "conversation": [
        {
          "invocationId": "invoke-nested",
          "userContent": {
            "role": "user",
            "content": "plan a calculation"
          },
          "finalResponse": {
            "role": "assistant",
            "content": "done"
          },
          "tools": [
            {
              "id": "call-1",
              "name": "planner",
              "arguments": {
                "steps": [
                  {
                    "op": "add",
                    "value": {
                      "a": 1,
                      "b": 2
                    }
                  },
                  {
                    "op": "chain",
                    "value": {
                      "inner": {
                        "op": "mul",
                        "params": [
                          3,
                          4
                        ]
                      }
                    }
                  }
                ]
              }
            }
          ]
        }
      ]
    }
  ]
}`

	var evalSet EvalSet
	err := json.Unmarshal([]byte(jsonData), &evalSet)
	assert.NoError(t, err)
	assert.Len(t, evalSet.EvalCases, 1)
	assert.Len(t, evalSet.EvalCases[0].Conversation, 1)
	assert.Len(t, evalSet.EvalCases[0].Conversation[0].Tools, 1)

	toolCall := evalSet.EvalCases[0].Conversation[0].Tools[0]
	expectedArgs := map[string]any{
		"steps": []any{
			map[string]any{
				"op": "add",
				"value": map[string]any{
					"a": float64(1),
					"b": float64(2),
				},
			},
			map[string]any{
				"op": "chain",
				"value": map[string]any{
					"inner": map[string]any{
						"op": "mul",
						"params": []any{
							float64(3),
							float64(4),
						},
					},
				},
			},
		},
	}
	assert.Equal(t, expectedArgs, toolCall.Arguments)

	encoded, marshalErr := json.Marshal(evalSet)
	assert.NoError(t, marshalErr)

	var decoded EvalSet
	assert.NoError(t, json.Unmarshal(encoded, &decoded))
	assert.Equal(t, expectedArgs, decoded.EvalCases[0].Conversation[0].Tools[0].Arguments)
	assert.JSONEq(t, jsonData, string(encoded))
}
