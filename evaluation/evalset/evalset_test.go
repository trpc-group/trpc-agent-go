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
                  "arguments": {
                    "operation": "add",
                    "a": 1,
                    "b": 2
                  }
                }
              }
            ],
            "toolResponses": [
              {
                "role": "tool",
                "toolId": "use-1",
                "toolName": "calculator",
                "content": {
                  "result": 3
                }
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
	origArgs := append([]byte(nil), firstInvocation.IntermediateData.ToolCalls[0].Function.Arguments...)
	nestedArgs := map[string]any{
		"operation": "batch",
		"items": []any{
			map[string]any{"op": "add", "a": float64(1), "b": float64(2)},
			map[string]any{"op": "mul", "a": float64(3), "b": float64(4)},
		},
	}
	firstInvocation.IntermediateData.ToolCalls[0].Function.Arguments = mustMarshal(t, nestedArgs)
	encoded, err := json.Marshal(evalSet)
	assert.NoError(t, err)
	var decoded EvalSet
	assert.NoError(t, json.Unmarshal(encoded, &decoded))
	assert.Len(t, decoded.EvalCases[0].Conversation[0].IntermediateData.ToolCalls, 1)
	decodedArgs := map[string]any{}
	assert.NoError(t, json.Unmarshal(decoded.EvalCases[0].Conversation[0].IntermediateData.ToolCalls[0].Function.Arguments, &decodedArgs))
	assert.Equal(t, nestedArgs, decodedArgs)
	firstInvocation.IntermediateData.ToolCalls[0].Function.Arguments = origArgs

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

	encoded, err = json.Marshal(evalSet)
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
          "intermediateData": {
            "toolCalls": [
              {
                "id": "call-1",
                "type": "function",
                "function": {
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
              }
            ]
          }
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
	assert.Len(t, evalSet.EvalCases[0].Conversation[0].IntermediateData.ToolCalls, 1)

	toolCall := evalSet.EvalCases[0].Conversation[0].IntermediateData.ToolCalls[0]
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
	var args map[string]any
	assert.NoError(t, json.Unmarshal(toolCall.Function.Arguments, &args))
	assert.Equal(t, expectedArgs, args)

	encoded, marshalErr := json.Marshal(evalSet)
	assert.NoError(t, marshalErr)

	var decoded EvalSet
	assert.NoError(t, json.Unmarshal(encoded, &decoded))
	decodedArgs := map[string]any{}
	assert.NoError(t, json.Unmarshal(decoded.EvalCases[0].Conversation[0].IntermediateData.ToolCalls[0].Function.Arguments, &decodedArgs))
	assert.Equal(t, expectedArgs, decodedArgs)
	assert.JSONEq(t, jsonData, string(encoded))
}

func mustMarshal(t *testing.T, v any) []byte {
	t.Helper()
	data, err := json.Marshal(v)
	assert.NoError(t, err)
	return data
}
