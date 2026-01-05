package llmtoolselector

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

type testTool struct {
	decl tool.Declaration
}

func (t *testTool) Declaration() *tool.Declaration { return &t.decl }

type staticSelectionModel struct {
	content string
}

func (m *staticSelectionModel) GenerateContent(ctx context.Context, req *model.Request) (<-chan *model.Response, error) {
	ch := make(chan *model.Response, 1)
	ch <- &model.Response{
		ID:     "sel",
		Object: model.ObjectTypeChatCompletion,
		Choices: []model.Choice{
			{
				Index: 0,
				Message: model.Message{
					Role:    model.RoleAssistant,
					Content: m.content,
				},
			},
		},
		Done: true,
	}
	close(ch)
	return ch, nil
}

func (m *staticSelectionModel) Info() model.Info { return model.Info{Name: "static"} }

func TestLLMToolSelector_NoTools_NoOp(t *testing.T) {
	mw := New(WithModel(&staticSelectionModel{content: `{"tools":[]}`})) // model unused
	cb := mw.Callback()

	req := &model.Request{Messages: []model.Message{model.NewUserMessage("hi")}}
	_, err := cb(context.Background(), &model.BeforeModelArgs{Request: req})
	require.NoError(t, err)
	require.Nil(t, req.Tools)
}

func TestLLMToolSelector_AlwaysIncludeMissing_Error(t *testing.T) {
	mw := New(
		WithModel(&staticSelectionModel{content: `{"tools":["calculator"]}`}),
		WithAlwaysInclude("missing_tool"),
	)
	cb := mw.Callback()

	req := &model.Request{
		Messages: []model.Message{model.NewUserMessage("hi")},
		Tools: map[string]tool.Tool{
			"calculator": &testTool{decl: tool.Declaration{Name: "calculator", Description: "calc"}},
		},
	}
	_, err := cb(context.Background(), &model.BeforeModelArgs{Request: req})
	require.Error(t, err)
}

func TestLLMToolSelector_InvalidSelection_Error(t *testing.T) {
	mw := New(WithModel(&staticSelectionModel{content: `{"tools":["nope"]}`})) // invalid tool
	cb := mw.Callback()

	req := &model.Request{
		Messages: []model.Message{model.NewUserMessage("hi")},
		Tools: map[string]tool.Tool{
			"calculator": &testTool{decl: tool.Declaration{Name: "calculator", Description: "calc"}},
		},
	}
	_, err := cb(context.Background(), &model.BeforeModelArgs{Request: req})
	require.Error(t, err)
}

func TestLLMToolSelector_MaxToolsAndAlwaysInclude(t *testing.T) {
	mw := New(
		WithModel(&staticSelectionModel{content: `{"tools":["calculator"]}`}),
		WithMaxTools(1),
		WithAlwaysInclude("current_time"),
	)
	cb := mw.Callback()

	req := &model.Request{
		Messages: []model.Message{model.NewUserMessage("what time is it and also 2+2")},
		Tools: map[string]tool.Tool{
			"calculator":   &testTool{decl: tool.Declaration{Name: "calculator", Description: "calc"}},
			"current_time": &testTool{decl: tool.Declaration{Name: "current_time", Description: "time"}},
		},
	}

	_, err := cb(context.Background(), &model.BeforeModelArgs{Request: req})
	require.NoError(t, err)

	// max_tools=1 keeps only the first selected, and always_include is appended.
	require.Len(t, req.Tools, 2)
	require.NotNil(t, req.Tools["calculator"])
	require.NotNil(t, req.Tools["current_time"])
}

func TestLLMToolSelector_NoSelectableTools_NoOp(t *testing.T) {
	mw := New(
		WithModel(&staticSelectionModel{content: `{"tools":["calculator"]}`}),
		WithAlwaysInclude("calculator"),
	)
	cb := mw.Callback()

	req := &model.Request{
		Messages: []model.Message{model.NewUserMessage("2+2")},
		Tools: map[string]tool.Tool{
			"calculator": &testTool{decl: tool.Declaration{Name: "calculator", Description: "calc"}},
		},
	}

	_, err := cb(context.Background(), &model.BeforeModelArgs{Request: req})
	require.NoError(t, err)
	require.Len(t, req.Tools, 1)
	require.NotNil(t, req.Tools["calculator"])
}
