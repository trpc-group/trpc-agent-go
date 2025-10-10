//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package processor

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

func Test_mergeFunctionResponseEvents_FiltersAndPreservesToolIDs(t *testing.T) {
	p := NewContentRequestProcessor()

	evt1 := event.Event{
		Author: "assistant",
		Response: &model.Response{
			Choices: []model.Choice{
				{
					Message: model.Message{
						Role:    model.RoleTool,
						ToolID:  "tool_a",
						Content: "A ok",
					},
				},
				{
					// Should be filtered out (no ToolID)
					Message: model.Message{
						Role:    model.RoleTool,
						Content: "missing id",
					},
				},
			},
		},
	}
	evt2 := event.Event{
		Author: "assistant",
		Response: &model.Response{
			Choices: []model.Choice{
				{
					// Should be filtered out (empty content)
					Message: model.Message{
						Role:   model.RoleTool,
						ToolID: "tool_b",
					},
				},
				{
					Message: model.Message{
						Role:    model.RoleTool,
						ToolID:  "tool_b",
						Content: "B ok",
					},
				},
			},
		},
	}
	evt3 := event.Event{
		Author: "assistant",
		Response: &model.Response{
			Choices: []model.Choice{
				{
					Message: model.Message{
						Role:    model.RoleTool,
						ToolID:  "tool_c",
						Content: "C ok",
					},
				},
			},
		},
	}

	merged := p.mergeFunctionResponseEvents([]event.Event{evt1, evt2, evt3})
	assert.NotNil(t, merged.Response, "merged response should not be nil")
	assert.Len(t, merged.Response.Choices, 3, "only 3 valid tool result choices should remain")
	gotIDs := merged.GetToolResultIDs()
	assert.ElementsMatch(t, []string{"tool_a", "tool_b", "tool_c"}, gotIDs)
	contents := []string{
		merged.Response.Choices[0].Message.Content,
		merged.Response.Choices[1].Message.Content,
		merged.Response.Choices[2].Message.Content,
	}
	assert.ElementsMatch(t, []string{"A ok", "B ok", "C ok"}, contents)
}

func Test_rearrangeLatestFuncResp_MergesBetweenCallAndLatest(t *testing.T) {
	p := NewContentRequestProcessor()

	toolCall := event.Event{
		Author: "assistant",
		Response: &model.Response{
			Choices: []model.Choice{
				{
					Message: model.Message{
						Role: model.RoleAssistant,
						ToolCalls: []model.ToolCall{
							{ID: "a", Function: model.FunctionDefinitionParam{Name: "calc"}},
							{ID: "b", Function: model.FunctionDefinitionParam{Name: "calc"}},
						},
					},
				},
			},
		},
	}
	unrelated1 := event.Event{
		Author: "assistant",
		Response: &model.Response{
			Choices: []model.Choice{
				{Message: model.Message{Role: model.RoleAssistant, Content: "thinking..."}},
			},
		},
	}
	respA := event.Event{
		Author: "assistant",
		Response: &model.Response{
			Choices: []model.Choice{
				{Message: model.Message{Role: model.RoleTool, ToolID: "a", Content: "A=1"}},
			},
		},
	}
	unrelated2 := event.Event{
		Author: "assistant",
		Response: &model.Response{
			Choices: []model.Choice{
				{Message: model.Message{Role: model.RoleAssistant, Content: "more..."}},
			},
		},
	}
	// Latest event is a tool result for "b"
	respB := event.Event{
		Author: "assistant",
		Response: &model.Response{
			Choices: []model.Choice{
				{Message: model.Message{Role: model.RoleTool, ToolID: "b", Content: "B=2"}},
			},
		},
	}

	events := []event.Event{toolCall, unrelated1, respA, unrelated2, respB}
	out := p.rearrangeLatestFuncResp(events)

	// Expect: [toolCall, merged(tool results for a,b)], unrelated events removed in between for latest rearrangement
	assert.Len(t, out, 2)
	assert.True(t, out[0].IsToolCallResponse(), "first should remain the tool call")
	assert.True(t, out[1].IsToolResultResponse(), "second should be merged tool result")
	assert.ElementsMatch(t, []string{"b"}, out[1].GetToolResultIDs())
	assert.Len(t, out[1].Response.Choices, 1, "merged choices should contain latest event results only")
}

func Test_rearrangeLatestFuncResp_NoMatchingCall_ReturnsOriginal(t *testing.T) {
	p := NewContentRequestProcessor()

	// Latest is a tool result, but there is no preceding matching tool call for that ID
	respX := event.Event{
		Author: "assistant",
		Response: &model.Response{
			Choices: []model.Choice{
				{Message: model.Message{Role: model.RoleTool, ToolID: "x", Content: "X=9"}},
			},
		},
	}
	plain := event.Event{
		Author: "assistant",
		Response: &model.Response{
			Choices: []model.Choice{
				{Message: model.Message{Role: model.RoleAssistant, Content: "msg"}},
			},
		},
	}
	out := p.rearrangeLatestFuncResp([]event.Event{plain, respX})
	assert.Equal(t, 2, len(out))
	assert.Equal(t, "msg", out[0].Choices[0].Message.Content)
	assert.Equal(t, "x", out[1].GetToolResultIDs()[0])
}

func Test_rearrangeLatestFuncResp_LatestNotToolResult_ReturnsOriginal(t *testing.T) {
	p := NewContentRequestProcessor()

	plain1 := event.Event{
		Author: "assistant",
		Response: &model.Response{
			Choices: []model.Choice{{Message: model.Message{Role: model.RoleAssistant, Content: "m1"}}},
		},
	}
	plain2 := event.Event{
		Author: "assistant",
		Response: &model.Response{
			Choices: []model.Choice{{Message: model.Message{Role: model.RoleAssistant, Content: "m2"}}},
		},
	}
	out := p.rearrangeLatestFuncResp([]event.Event{plain1, plain2})
	assert.Equal(t, 2, len(out))
	assert.Equal(t, "m1", out[0].Choices[0].Message.Content)
	assert.Equal(t, "m2", out[1].Choices[0].Message.Content)
}

func Test_rearrangeAsyncFuncRespHist_MergesSeparateResponseEvents(t *testing.T) {
	p := NewContentRequestProcessor()

	call := event.Event{
		Author: "assistant",
		Response: &model.Response{
			Choices: []model.Choice{
				{
					Message: model.Message{
						Role: model.RoleAssistant,
						ToolCalls: []model.ToolCall{
							{ID: "t1", Function: model.FunctionDefinitionParam{Name: "calc"}},
							{ID: "t2", Function: model.FunctionDefinitionParam{Name: "calc"}},
						},
					},
				},
			},
		},
	}
	resp1 := event.Event{
		Author: "assistant",
		Response: &model.Response{
			Choices: []model.Choice{
				{Message: model.Message{Role: model.RoleTool, ToolID: "t1", Content: "r1"}},
			},
		},
	}
	resp2 := event.Event{
		Author: "assistant",
		Response: &model.Response{
			Choices: []model.Choice{
				{Message: model.Message{Role: model.RoleTool, ToolID: "t2", Content: "r2"}},
			},
		},
	}

	out := p.rearrangeAsyncFuncRespHist([]event.Event{call, resp1, resp2})
	assert.Len(t, out, 2, "call + merged response")
	assert.True(t, out[0].IsToolCallResponse())
	assert.True(t, out[1].IsToolResultResponse())
	assert.ElementsMatch(t, []string{"t1", "t2"}, out[1].GetToolResultIDs())
	assert.Len(t, out[1].Response.Choices, 2)
}
