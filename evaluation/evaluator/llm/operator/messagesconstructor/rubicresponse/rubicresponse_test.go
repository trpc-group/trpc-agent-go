package rubicresponse

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/genai"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/llm"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

func TestConstructMessagesIncludesAllFields(t *testing.T) {
	constructor := New()
	actual := &evalset.Invocation{
		UserContent: &genai.Content{Parts: []*genai.Part{{Text: "hello"}}},
		FinalResponse: &genai.Content{Parts: []*genai.Part{
			{Text: "final"},
		}},
		IntermediateData: &evalset.IntermediateData{
			ToolUses: []*genai.FunctionCall{{Name: "call"}},
		},
	}
	evalMetric := &metric.EvalMetric{
		Criterion: &criterion.Criterion{
			LLMJudge: &llm.LLMCriterion{
				Rubrics: []*llm.Rubric{{ID: "1", Content: &llm.RubricContent{Text: "rubric text"}}},
			},
		},
	}

	messages, err := constructor.ConstructMessages(context.Background(), actual, nil, evalMetric)
	require.NoError(t, err)
	require.Len(t, messages, 1)
	assert.Equal(t, model.RoleUser, messages[0].Role)
	assert.Contains(t, messages[0].Content, "hello")
	assert.Contains(t, messages[0].Content, "final")
	assert.Contains(t, messages[0].Content, "rubric text")
	assert.Contains(t, messages[0].Content, "call")
}

func TestConstructMessagesIntermediateDataError(t *testing.T) {
	constructor := New()
	actual := &evalset.Invocation{
		UserContent: &genai.Content{Parts: []*genai.Part{{Text: "hello"}}},
		FinalResponse: &genai.Content{Parts: []*genai.Part{
			{Text: "final"},
		}},
		IntermediateData: &evalset.IntermediateData{
			IntermediateResponses: [][]any{{make(chan int)}},
		},
	}
	evalMetric := &metric.EvalMetric{
		Criterion: &criterion.Criterion{
			LLMJudge: &llm.LLMCriterion{
				Rubrics: []*llm.Rubric{},
			},
		},
	}

	_, err := constructor.ConstructMessages(context.Background(), actual, nil, evalMetric)
	require.Error(t, err)
}
