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
	assert.ElementsMatch(t, []string{"a", "b"}, out[1].GetToolResultIDs())
	assert.Len(t, out[1].Response.Choices, 2, "merged choices should contain all matched results from the tool-call round")
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

func Test_rearrangeAsyncFuncRespHist_ReusedToolCallIDsStayInTheirRounds(t *testing.T) {
	p := NewContentRequestProcessor()

	firstCall := event.Event{
		Author: "assistant",
		Response: &model.Response{
			Choices: []model.Choice{
				{
					Message: model.Message{
						Role: model.RoleAssistant,
						ToolCalls: []model.ToolCall{
							{ID: "file_read:47", Function: model.FunctionDefinitionParam{Name: "file_read"}},
							{ID: "shell:49", Function: model.FunctionDefinitionParam{Name: "shell"}},
						},
					},
				},
			},
		},
	}
	firstFileResult := event.Event{
		Author: "assistant",
		Response: &model.Response{
			Choices: []model.Choice{
				{Message: model.Message{Role: model.RoleTool, ToolID: "file_read:47", Content: "file result"}},
			},
		},
	}
	firstShellResult := event.Event{
		Author: "assistant",
		Response: &model.Response{
			Choices: []model.Choice{
				{Message: model.Message{Role: model.RoleTool, ToolID: "shell:49", Content: "first shell result"}},
			},
		},
	}
	secondCall := event.Event{
		Author: "assistant",
		Response: &model.Response{
			Choices: []model.Choice{
				{
					Message: model.Message{
						Role: model.RoleAssistant,
						ToolCalls: []model.ToolCall{
							{ID: "shell:49", Function: model.FunctionDefinitionParam{Name: "shell"}},
						},
					},
				},
			},
		},
	}
	secondShellResult := event.Event{
		Author: "assistant",
		Response: &model.Response{
			Choices: []model.Choice{
				{Message: model.Message{Role: model.RoleTool, ToolID: "shell:49", Content: "second shell result"}},
			},
		},
	}

	out := p.rearrangeAsyncFuncRespHist([]event.Event{
		firstCall,
		firstFileResult,
		firstShellResult,
		secondCall,
		secondShellResult,
	})

	assert.Len(t, out, 4, "each call round should keep only its own tool result event")
	assert.True(t, out[0].IsToolCallResponse())
	assert.True(t, out[1].IsToolResultResponse())
	assert.ElementsMatch(t, []string{"file_read:47", "shell:49"}, out[1].GetToolResultIDs())
	assert.Contains(t, []string{
		out[1].Response.Choices[0].Message.Content,
		out[1].Response.Choices[1].Message.Content,
	}, "first shell result")
	assert.NotContains(t, []string{
		out[1].Response.Choices[0].Message.Content,
		out[1].Response.Choices[1].Message.Content,
	}, "second shell result")
	assert.True(t, out[2].IsToolCallResponse())
	assert.True(t, out[3].IsToolResultResponse())
	assert.Equal(t, []string{"shell:49"}, out[3].GetToolResultIDs())
	assert.Equal(t, "second shell result", out[3].Response.Choices[0].Message.Content)
}

func Test_rearrangeAsyncFuncRespHist_FiltersMixedResponseEventAcrossRounds(t *testing.T) {
	p := NewContentRequestProcessor()

	firstCall := event.Event{
		Author: "assistant",
		Response: &model.Response{
			Choices: []model.Choice{
				{
					Message: model.Message{
						Role: model.RoleAssistant,
						ToolCalls: []model.ToolCall{
							{ID: "call_first", Function: model.FunctionDefinitionParam{Name: "first_tool"}},
						},
					},
				},
			},
		},
	}
	secondCall := event.Event{
		Author: "assistant",
		Response: &model.Response{
			Choices: []model.Choice{
				{
					Message: model.Message{
						Role: model.RoleAssistant,
						ToolCalls: []model.ToolCall{
							{ID: "call_second", Function: model.FunctionDefinitionParam{Name: "second_tool"}},
						},
					},
				},
			},
		},
	}
	mixedResult := event.Event{
		Author: "assistant",
		Response: &model.Response{
			Choices: []model.Choice{
				{Message: model.Message{Role: model.RoleTool, ToolID: "call_first", Content: "first result"}},
				{Message: model.Message{Role: model.RoleTool, ToolID: "call_second", Content: "second result"}},
			},
		},
	}

	out := p.rearrangeAsyncFuncRespHist([]event.Event{firstCall, secondCall, mixedResult})

	assert.Len(t, out, 4, "mixed tool result event should be split across matching rounds")
	assert.True(t, out[0].IsToolCallResponse())
	assert.True(t, out[1].IsToolResultResponse())
	assert.Equal(t, []string{"call_first"}, out[1].GetToolResultIDs())
	assert.Len(t, out[1].Response.Choices, 1)
	assert.Equal(t, "first result", out[1].Response.Choices[0].Message.Content)
	assert.True(t, out[2].IsToolCallResponse())
	assert.True(t, out[3].IsToolResultResponse())
	assert.Equal(t, []string{"call_second"}, out[3].GetToolResultIDs())
	assert.Len(t, out[3].Response.Choices, 1)
	assert.Equal(t, "second result", out[3].Response.Choices[0].Message.Content)
}
