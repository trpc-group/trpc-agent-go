package rubicresponse

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/model"
)

func TestScoreBasedOnResponseParsesBlocks(t *testing.T) {
	scorer := New()
	response := &model.Response{
		Choices: []model.Choice{
			{
				Message: model.Message{Content: `
ID: 1
Property: p1
Evidence: e1
Reason: r1
Verdict: yes

ID: 2
Property: p2
Evidence: e2
Reason: r2
Verdict: no
`},
			},
		},
	}

	result, err := scorer.ScoreBasedOnResponse(context.Background(), response, nil)
	require.NoError(t, err)
	require.Len(t, result.RubricScores, 2)
	assert.InDelta(t, 0.5, result.Score, 1e-9)
	assert.Equal(t, "1", result.RubricScores[0].ID)
	assert.Equal(t, "r1", result.RubricScores[0].Reason)
	assert.InDelta(t, 1.0, result.RubricScores[0].Score, 1e-9)
}

func TestScoreBasedOnResponseNoBlocks(t *testing.T) {
	scorer := New()
	_, err := scorer.ScoreBasedOnResponse(context.Background(), &model.Response{
		Choices: []model.Choice{{Message: model.Message{Content: "no rubric content"}}},
	}, nil)
	require.Error(t, err)
}
