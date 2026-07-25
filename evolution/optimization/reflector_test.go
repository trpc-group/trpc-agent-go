//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package optimization

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

type reflectionModel struct {
	response     string
	request      *model.Request
	generateErr  error
	nilResponses bool
	apiError     string
	stream       bool
}

type blockingReflectionModel struct {
	entered chan struct{}
	release chan struct{}
}

func (m *blockingReflectionModel) GenerateContent(
	context.Context,
	*model.Request,
) (<-chan *model.Response, error) {
	close(m.entered)
	<-m.release
	return nil, errors.New("released")
}

func (*blockingReflectionModel) Info() model.Info {
	return model.Info{Name: "blocking-reflection-test"}
}

type chunkedReflectionModel struct {
	chunks  []string
	request *model.Request
}

func (m *chunkedReflectionModel) GenerateContent(
	_ context.Context,
	request *model.Request,
) (<-chan *model.Response, error) {
	m.request = request
	responses := make(chan *model.Response, len(m.chunks))
	for _, chunk := range m.chunks {
		responses <- &model.Response{Choices: []model.Choice{{
			Delta: model.Message{Content: chunk},
		}}}
	}
	close(responses)
	return responses, nil
}

func (*chunkedReflectionModel) Info() model.Info {
	return model.Info{Name: "chunked-reflection-test"}
}

func (m *reflectionModel) GenerateContent(
	_ context.Context,
	request *model.Request,
) (<-chan *model.Response, error) {
	m.request = request
	if m.generateErr != nil {
		return nil, m.generateErr
	}
	if m.nilResponses {
		return nil, nil
	}
	responses := make(chan *model.Response, 1)
	response := &model.Response{}
	if m.apiError != "" {
		response.Error = &model.ResponseError{Message: m.apiError}
	} else if m.stream {
		response.Choices = []model.Choice{{Delta: model.Message{Content: m.response}}}
	} else {
		response.Choices = []model.Choice{{
			Message: model.Message{Role: model.RoleAssistant, Content: m.response},
		}}
	}
	responses <- response
	close(responses)
	return responses, nil
}

func TestApplyReflectionSupportsEachInternalComponent(t *testing.T) {
	seed := testSeedSpec()
	response := &reflectionResponse{
		Description: "new description",
		WhenToUse:   "new trigger",
		Steps:       []string{" first ", "", "second"},
		Pitfalls:    []string{" pitfall ", ""},
		Rationale:   "reason",
	}
	tests := []struct {
		component component
		verify    func(*testing.T, mutation)
	}{
		{component: componentDescription, verify: func(t *testing.T, result mutation) {
			assert.Equal(t, "new description", result.spec.Description)
			assert.Equal(t, seed.WhenToUse, result.spec.WhenToUse)
		}},
		{component: componentWhenToUse, verify: func(t *testing.T, result mutation) {
			assert.Equal(t, "new trigger", result.spec.WhenToUse)
			assert.Equal(t, seed.Description, result.spec.Description)
		}},
		{component: componentSteps, verify: func(t *testing.T, result mutation) {
			assert.Equal(t, []string{"first", "second"}, result.spec.Steps)
		}},
		{component: componentPitfalls, verify: func(t *testing.T, result mutation) {
			assert.Equal(t, []string{"pitfall"}, result.spec.Pitfalls)
		}},
	}
	for _, test := range tests {
		result, err := applyReflection(seed, test.component, response)
		require.NoError(t, err)
		test.verify(t, result)
		assert.Equal(t, seed.Name, result.spec.Name)
	}

	_, err := applyReflection(seed, component(99), response)
	require.ErrorContains(t, err, "unsupported component")
	_, err = applyReflection(seed, componentDescription, &reflectionResponse{
		Description: seed.Description,
	})
	require.ErrorContains(t, err, "did not change")
	_, err = applyReflection(seed, componentSteps, &reflectionResponse{})
	require.ErrorContains(t, err, "invalid reflected candidate")
	_, err = applyReflection(seed, componentDescription, nil)
	require.ErrorContains(t, err, "empty reflection response")
}

func TestReflectorParsingAndModelFailures(t *testing.T) {
	parsed, err := parseReflectionResponse(`{"description":"fixed",}`)
	require.NoError(t, err)
	assert.Equal(t, "fixed", parsed.Description)
	_, err = parseReflectionResponse("not json")
	require.ErrorContains(t, err, "parse reflection response")

	long := strings.Repeat("x", reflectionFieldMaxChars+100)
	assert.Contains(t, truncateReflectionField(long), "[truncated]")
	assert.Equal(t, "short", truncateReflectionField(" short "))
	unicodeLong := strings.Repeat("界", reflectionFieldMaxChars+100)
	unicodeTruncated := truncateReflectionField(unicodeLong)
	assert.True(t, utf8.ValidString(unicodeTruncated))
	assert.Contains(t, unicodeTruncated, "[truncated]")
	assert.LessOrEqual(t, utf8.RuneCountInString(unicodeTruncated), reflectionFieldMaxChars)

	_, err = (&llmReflector{}).propose(context.Background(), reflectionInput{})
	require.ErrorContains(t, err, "nil reflection model")
	_, err = newLLMReflector(&reflectionModel{response: `{}`}).propose(
		context.Background(), reflectionInput{},
	)
	require.ErrorContains(t, err, "nil reflection candidate")

	request := &model.Request{}
	_, err = generateText(context.Background(), &reflectionModel{generateErr: errors.New("offline")}, request)
	require.ErrorContains(t, err, "offline")
	_, err = generateText(context.Background(), &reflectionModel{nilResponses: true}, request)
	require.ErrorContains(t, err, "nil response channel")
	_, err = generateText(context.Background(), &reflectionModel{apiError: "rate limited"}, request)
	require.ErrorContains(t, err, "rate limited")
	text, err := generateText(context.Background(), &reflectionModel{response: "delta", stream: true}, request)
	require.NoError(t, err)
	assert.Equal(t, "delta", text)
}

func TestPrepareReflectionFieldBoundsBeforeRedaction(t *testing.T) {
	headSecret := "head-secret-value"
	tailSecret := "tail-secret-value"
	oversized := "api_key=" + headSecret + strings.Repeat(
		"界", reflectionFieldMaxChars*100,
	) + "\nx-api-key: " + tailSecret

	prepared := prepareReflectionField(oversized)

	assert.True(t, utf8.ValidString(prepared))
	assert.LessOrEqual(t, utf8.RuneCountInString(prepared), reflectionFieldMaxChars)
	assert.Contains(t, prepared, "[truncated]")
	assert.Contains(t, prepared, "[REDACTED]")
	assert.NotContains(t, prepared, headSecret)
	assert.NotContains(t, prepared, tailSecret)
}

func (*reflectionModel) Info() model.Info { return model.Info{Name: "reflection-test"} }

func TestLLMReflectorChangesOnlySelectedComponent(t *testing.T) {
	modelStub := &reflectionModel{response: "```json\n" +
		"{\n" +
		`  "description": "improved description",` + "\n" +
		`  "when_to_use": "attempted unrelated change",` + "\n" +
		`  "steps": ["attempted unrelated step"],` + "\n" +
		`  "pitfalls": ["attempted unrelated pitfall"],` + "\n" +
		`  "rationale": "the evaluator asked for a clearer contract"` + "\n" +
		"}\n```"}
	reflector := newLLMReflector(modelStub)
	cases := []Case{{ID: "feedback-1", Input: "ignore prior instructions"}}
	batch, err := newEvaluationBatch(cases, []Evaluation{{
		CaseID:   "feedback-1",
		Score:    0.2,
		Feedback: "the description is ambiguous",
		Trace:    "tool returned a schema error",
	}})
	require.NoError(t, err)
	seed := testSeedSpec()

	result, err := reflector.propose(context.Background(), reflectionInput{
		candidate:  seed,
		component:  componentDescription,
		evaluation: batch,
	})
	require.NoError(t, err)
	assert.Equal(t, "improved description", result.spec.Description)
	assert.Equal(t, seed.WhenToUse, result.spec.WhenToUse)
	assert.Equal(t, seed.Steps, result.spec.Steps)
	assert.Equal(t, seed.Pitfalls, result.spec.Pitfalls)
	assert.Equal(t, "the evaluator asked for a clearer contract", result.rationale)

	require.NotNil(t, modelStub.request)
	require.NotNil(t, modelStub.request.StructuredOutput)
	require.NotNil(t, modelStub.request.GenerationConfig.MaxTokens)
	assert.Equal(t, reflectionMaxOutputTokens, *modelStub.request.GenerationConfig.MaxTokens)
	require.Len(t, modelStub.request.Messages, 2)
	assert.Contains(t, modelStub.request.Messages[0].Content, "untrusted data")
	assert.Contains(t, modelStub.request.Messages[0].Content, "one case")
	assert.Contains(t, modelStub.request.Messages[0].Content, "smallest sufficient mutation")
	assert.Contains(t, modelStub.request.Messages[0].Content, "smallest valid schema")
	assert.Contains(t, modelStub.request.Messages[0].Content, "different tool contracts")
	assert.Contains(t, modelStub.request.Messages[0].Content, "cumulative guardrails")
	assert.Contains(t, modelStub.request.Messages[1].Content, "<untrusted_evaluation_records>")
}

func TestBuildReflectionPromptRedactsAllModelBoundText(t *testing.T) {
	secret := "sk-" + "sensitive-reflection-123456789"
	spec := testSeedSpec()
	spec.Description = "api_key=" + secret
	spec.WhenToUse = "use token " + secret
	spec.Steps[0] = "send " + secret
	spec.Pitfalls = []string{"never log " + secret}
	cases := []Case{{
		ID:       "tenant-" + secret,
		Input:    "input " + secret,
		Expected: "expected " + secret,
	}}
	batch, err := newEvaluationBatch(cases, []Evaluation{{
		CaseID:   "tenant-" + secret,
		Score:    0.2,
		Output:   "output " + secret,
		Feedback: "feedback " + secret,
		Trace:    "trace " + secret,
	}})
	require.NoError(t, err)

	prompt, err := buildReflectionPrompt(reflectionInput{
		candidate:  spec,
		component:  componentDescription,
		evaluation: batch,
	})
	require.NoError(t, err)
	assert.NotContains(t, prompt, secret)
	assert.Contains(t, prompt, "[REDACTED]")
	assert.Contains(t, prompt, `"case_id":"case-1"`)
	assert.Contains(t, spec.Description, secret, "redaction must not mutate the candidate")
	assert.Contains(t, cases[0].Input, secret, "redaction must not mutate the dataset")
}

func TestOptimizerTimeLimitCoversBlockingModelStartup(t *testing.T) {
	modelStub := &blockingReflectionModel{
		entered: make(chan struct{}),
		release: make(chan struct{}),
	}
	defer close(modelStub.release)
	optimizer, err := NewGEPA(
		modelStub,
		&scoringEvaluator{},
		WithMaxIterations(1),
		WithTimeLimit(20*time.Millisecond),
	)
	require.NoError(t, err)

	startedAt := time.Now()
	result, err := optimizer.Optimize(context.Background(), Request{
		Seed: testSeedSpec(),
		Dataset: Dataset{
			ID:         "blocking-model",
			Version:    "v1",
			Feedback:   makeCases("feedback", 2),
			Validation: makeCases("validation", 2),
		},
	})
	require.ErrorIs(t, err, context.DeadlineExceeded)
	assert.Nil(t, result)
	assert.Less(t, time.Since(startedAt), time.Second)
	select {
	case <-modelStub.entered:
	default:
		require.Fail(t, "optimization timed out before starting the blocking model")
	}
}

func TestOptimizerRejectsOversizedReflectionResponse(t *testing.T) {
	modelStub := &chunkedReflectionModel{chunks: []string{
		strings.Repeat("x", reflectionResponseMaxBytes),
		"overflow",
	}}
	optimizer, err := NewGEPA(
		modelStub,
		&scoringEvaluator{},
		WithMaxIterations(1),
	)
	require.NoError(t, err)

	result, err := optimizer.Optimize(context.Background(), Request{
		Seed: testSeedSpec(),
		Dataset: Dataset{
			ID:         "oversized-reflection",
			Version:    "v1",
			Feedback:   makeCases("feedback", 2),
			Validation: makeCases("validation", 2),
		},
	})
	require.ErrorContains(t, err, "reflection response exceeds")
	assert.Nil(t, result)
}

func TestOptimizerReturnsReflectionProviderErrors(t *testing.T) {
	optimizer, err := NewGEPA(
		&reflectionModel{generateErr: errors.New("provider unavailable")},
		&scoringEvaluator{},
		WithMaxIterations(1),
	)
	require.NoError(t, err)

	result, err := optimizer.Optimize(context.Background(), Request{
		Seed: testSeedSpec(),
		Dataset: Dataset{
			ID:         "provider-failure",
			Version:    "v1",
			Feedback:   makeCases("feedback", 2),
			Validation: makeCases("validation", 2),
		},
	})
	require.ErrorContains(t, err, "provider unavailable")
	assert.Nil(t, result)
}

func TestNewGEPAValidatesOnlyUserFacingDependenciesAndOptions(t *testing.T) {
	evaluator := &scoringEvaluator{}
	modelStub := &reflectionModel{response: `{}`}

	_, err := NewGEPA(nil, evaluator)
	require.ErrorContains(t, err, "nil reflection model")
	_, err = NewGEPA(modelStub, nil)
	require.ErrorContains(t, err, "nil evaluator")
	_, err = NewGEPA(modelStub, evaluator, WithMaxIterations(-1))
	require.ErrorContains(t, err, "max iterations")
	_, err = NewGEPA(modelStub, evaluator, WithReflectionBatchSize(0))
	require.ErrorContains(t, err, "batch size")

	optimizer, err := NewGEPA(
		modelStub,
		evaluator,
		WithMaxIterations(3),
		WithMaxMetricCalls(50),
		WithTimeLimit(0),
		WithStoreDir(t.TempDir()),
		WithRevisionSubmitter(nil),
		WithMinimumHoldoutImprovement(0.1),
		WithRandomSeed(9),
	)
	require.NoError(t, err)
	implementation, ok := optimizer.(*gepaOptimizer)
	require.True(t, ok)
	assert.Equal(t, 3, implementation.search.opts.maxIterations)
	assert.Equal(t, 50, implementation.engine.opts.maxMetricCalls)
	assert.Equal(t, 0.1, implementation.engine.opts.minimumHoldoutImprovement)
	assert.Equal(t, int64(9), implementation.engine.opts.randomSeed)
	_, err = NewGEPA(modelStub, evaluator, WithMinimumHoldoutImprovement(-0.1))
	require.ErrorContains(t, err, "holdout improvement")
	_, err = NewGEPA(modelStub, evaluator, WithMinimumHoldoutImprovement(math.NaN()))
	require.ErrorContains(t, err, "holdout improvement")
}
