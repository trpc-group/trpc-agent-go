//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package hedge

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func TestNewReturnsErrorWithoutCandidates(t *testing.T) {
	llm, err := New()
	require.Error(t, err)
	assert.EqualError(t, err, "hedge: at least one candidate model is required")
	assert.Nil(t, llm)
}

func TestNewReturnsErrorWithNilCandidate(t *testing.T) {
	llm, err := New(WithCandidates(nil))
	require.Error(t, err)
	assert.EqualError(t, err, "hedge: candidate model at index 0 is nil")
	assert.Nil(t, llm)
}

func TestNewReturnsErrorForInvalidDelays(t *testing.T) {
	primary := &scriptedIterModel{name: "primary"}
	backup := &scriptedIterModel{name: "backup"}
	llm, err := New(
		WithCandidates(primary, backup),
		WithDelay(-time.Millisecond),
	)
	require.Error(t, err)
	assert.EqualError(t, err, "hedge: delay cannot be negative")
	assert.Nil(t, llm)
	llm, err = New(
		WithCandidates(primary, backup),
		WithDelays(10*time.Millisecond, 20*time.Millisecond),
	)
	require.Error(t, err)
	assert.EqualError(t, err, "hedge: expected 1 explicit delays, got 2")
	assert.Nil(t, llm)
	llm, err = New(
		WithCandidates(primary, backup),
		WithDelays(),
	)
	require.Error(t, err)
	assert.EqualError(t, err, "hedge: expected 1 explicit delays, got 0")
	assert.Nil(t, llm)
	llm, err = New(
		WithCandidates(primary, backup),
		WithDelays(-time.Millisecond),
	)
	require.Error(t, err)
	assert.EqualError(t, err, "hedge: delay at index 0 cannot be negative")
	assert.Nil(t, llm)
	llm, err = New(
		WithCandidates(primary, backup, &scriptedIterModel{name: "third"}),
		WithDelays(20*time.Millisecond, 10*time.Millisecond),
	)
	require.Error(t, err)
	assert.EqualError(t, err, "hedge: delays must be non-decreasing")
	assert.Nil(t, llm)
}

func TestInfoUsesConfiguredName(t *testing.T) {
	llm, err := New(
		WithCandidates(&scriptedIterModel{name: "primary"}),
		WithName("hedge-model"),
	)
	require.NoError(t, err)
	assert.Equal(t, "hedge-model", llm.Info().Name)
}

func TestCloneRequestDeepCopiesSerializableFields(t *testing.T) {
	maxTokens := 128
	toolImpl := &stubTool{name: "lookup"}
	request := &model.Request{
		Messages: []model.Message{
			model.NewSystemMessage("system"),
			model.NewUserMessage("user"),
		},
		GenerationConfig: model.GenerationConfig{
			Stream:    true,
			MaxTokens: &maxTokens,
		},
		StructuredOutput: &model.StructuredOutput{
			Type: model.StructuredOutputJSONSchema,
			JSONSchema: &model.JSONSchemaConfig{
				Name:   "answer",
				Strict: true,
				Schema: map[string]any{
					"type": "object",
				},
			},
		},
		ExtraFields: map[string]any{
			"prompt_cache_key": "cache-1",
			"metadata": map[string]any{
				"session_id": "session-1",
			},
		},
		Tools: map[string]tool.Tool{
			"lookup": toolImpl,
		},
	}
	cloned, err := cloneRequest(request)
	require.NoError(t, err)
	require.NotNil(t, cloned)
	require.NotSame(t, request, cloned)
	require.NotSame(t, request.StructuredOutput, cloned.StructuredOutput)
	require.NotSame(t, request.StructuredOutput.JSONSchema, cloned.StructuredOutput.JSONSchema)
	cloned.Messages[1].Content = "changed"
	cloned.StructuredOutput.JSONSchema.Name = "changed"
	cloned.StructuredOutput.JSONSchema.Schema["type"] = "array"
	cloned.ExtraFields["prompt_cache_key"] = "changed"
	clonedMetadata := cloned.ExtraFields["metadata"].(map[string]any)
	clonedMetadata["session_id"] = "changed"
	cloned.Tools["other"] = stubTool{name: "other"}
	assert.Equal(t, "user", request.Messages[1].Content)
	assert.Equal(t, "cache-1", request.ExtraFields["prompt_cache_key"])
	metadata := request.ExtraFields["metadata"].(map[string]any)
	assert.Equal(t, "session-1", metadata["session_id"])
	assert.Equal(t, "answer", request.StructuredOutput.JSONSchema.Name)
	assert.Equal(t, "object", request.StructuredOutput.JSONSchema.Schema["type"])
	assert.Len(t, request.Tools, 1)
	assert.Same(t, toolImpl, cloned.Tools["lookup"])
}

func TestGenerateContentUsesNonIterCandidate(t *testing.T) {
	llm, err := New(WithCandidates(&scriptedChannelModel{
		name: "channel-primary",
		responses: []*model.Response{
			assistantResponse("hello from channel", true),
		},
	}))
	require.NoError(t, err)
	responses := collectChannelResponses(t, llm, &model.Request{
		Messages: []model.Message{model.NewUserMessage("hello")},
	})
	require.Len(t, responses, 1)
	assert.Equal(t, "hello from channel", responses[0].Choices[0].Message.Content)
}

func TestGenerateContentIterReturnsErrorForNilRequest(t *testing.T) {
	llm, err := New(WithCandidates(&scriptedIterModel{name: "primary"}))
	require.NoError(t, err)
	iterModel, ok := llm.(model.IterModel)
	require.True(t, ok)
	seq, err := iterModel.GenerateContentIter(context.Background(), nil)
	require.Error(t, err)
	assert.EqualError(t, err, "request cannot be nil")
	assert.Nil(t, seq)
}

func TestGenerateContentStopsWhenContextCanceled(t *testing.T) {
	llm, err := New(WithCandidates(&scriptedIterModel{
		name:       "primary",
		startDelay: 50 * time.Millisecond,
		responses: []*model.Response{
			assistantResponse("late response", true),
		},
	}))
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	responseChan, err := llm.GenerateContent(ctx, &model.Request{
		Messages: []model.Message{model.NewUserMessage("hello")},
	})
	require.NoError(t, err)
	cancel()
	assert.Empty(t, collectResponsesFromChannel(responseChan))
}

func TestHedgeDelayLaunchesNextCandidateAfterTimer(t *testing.T) {
	primaryStarted := make(chan struct{})
	primaryStopped := make(chan struct{})
	backupStarted := make(chan struct{})
	primary := &scriptedIterModel{
		name:      "primary",
		started:   primaryStarted,
		stopped:   primaryStopped,
		holdAfter: true,
	}
	backup := &scriptedIterModel{
		name:    "backup",
		started: backupStarted,
		responses: []*model.Response{
			assistantResponse("backup wins", true),
		},
	}
	llm, err := New(
		WithCandidates(primary, backup),
		WithDelay(80*time.Millisecond),
	)
	require.NoError(t, err)
	responseChan, err := llm.GenerateContent(context.Background(), &model.Request{
		Messages: []model.Message{model.NewUserMessage("hello")},
	})
	require.NoError(t, err)
	waitForClosed(t, primaryStarted, 100*time.Millisecond)
	assertNotClosed(t, backupStarted, 40*time.Millisecond)
	waitForClosed(t, backupStarted, 300*time.Millisecond)
	responses := collectResponsesFromChannel(responseChan)
	require.Len(t, responses, 1)
	assert.Equal(t, "backup wins", responses[0].Choices[0].Message.Content)
	waitForClosed(t, primaryStopped, 100*time.Millisecond)
}

func TestExplicitHedgeDelaysLaunchCandidatesAtConfiguredOffsets(t *testing.T) {
	primaryStarted := make(chan struct{})
	primaryStopped := make(chan struct{})
	secondStarted := make(chan struct{})
	secondStopped := make(chan struct{})
	thirdStarted := make(chan struct{})
	primary := &scriptedIterModel{
		name:      "primary",
		started:   primaryStarted,
		stopped:   primaryStopped,
		holdAfter: true,
	}
	second := &scriptedIterModel{
		name:      "second",
		started:   secondStarted,
		stopped:   secondStopped,
		holdAfter: true,
	}
	third := &scriptedIterModel{
		name:    "third",
		started: thirdStarted,
		responses: []*model.Response{
			assistantResponse("third wins", true),
		},
	}
	llm, err := New(
		WithCandidates(primary, second, third),
		WithDelays(80*time.Millisecond, 220*time.Millisecond),
	)
	require.NoError(t, err)
	responseChan, err := llm.GenerateContent(context.Background(), &model.Request{
		Messages: []model.Message{model.NewUserMessage("hello")},
	})
	require.NoError(t, err)
	waitForClosed(t, primaryStarted, 100*time.Millisecond)
	assertNotClosed(t, secondStarted, 40*time.Millisecond)
	waitForClosed(t, secondStarted, 250*time.Millisecond)
	assertNotClosed(t, thirdStarted, 80*time.Millisecond)
	waitForClosed(t, thirdStarted, 400*time.Millisecond)
	responses := collectResponsesFromChannel(responseChan)
	require.Len(t, responses, 1)
	assert.Equal(t, "third wins", responses[0].Choices[0].Message.Content)
	waitForClosed(t, primaryStopped, 100*time.Millisecond)
	waitForClosed(t, secondStopped, 100*time.Millisecond)
}

func TestHedgeLaunchesNextCandidateImmediatelyWhenAllActiveAttemptsFail(t *testing.T) {
	backupStarted := make(chan struct{})
	primary := &scriptedIterModel{
		name:       "primary",
		startupErr: errors.New("primary failed"),
	}
	backup := &scriptedIterModel{
		name:    "backup",
		started: backupStarted,
		responses: []*model.Response{
			assistantResponse("backup wins", true),
		},
	}
	llm, err := New(
		WithCandidates(primary, backup),
		WithDelay(500*time.Millisecond),
	)
	require.NoError(t, err)
	responseChan, err := llm.GenerateContent(context.Background(), &model.Request{
		Messages: []model.Message{model.NewUserMessage("hello")},
	})
	require.NoError(t, err)
	waitForClosed(t, backupStarted, 100*time.Millisecond)
	responses := collectResponsesFromChannel(responseChan)
	require.Len(t, responses, 1)
	assert.Equal(t, "backup wins", responses[0].Choices[0].Message.Content)
}

func TestMeaningfulWinnerIgnoresEmptyPrelude(t *testing.T) {
	primaryStopped := make(chan struct{})
	primary := &scriptedIterModel{
		name:    "primary",
		stopped: primaryStopped,
		responses: []*model.Response{
			{ID: "prelude", Model: "primary", Done: false},
		},
		holdAfter: true,
	}
	backup := &scriptedIterModel{
		name: "backup",
		responses: []*model.Response{
			assistantResponse("backup wins", true),
		},
	}
	llm, err := New(
		WithCandidates(primary, backup),
		WithDelay(20*time.Millisecond),
	)
	require.NoError(t, err)
	responses := collectChannelResponses(t, llm, &model.Request{
		Messages: []model.Message{model.NewUserMessage("hello")},
	})
	require.Len(t, responses, 1)
	assert.Equal(t, "backup wins", responses[0].Choices[0].Message.Content)
	waitForClosed(t, primaryStopped, 100*time.Millisecond)
}

func TestWinnerErrorIsForwardedAndLosersAreCanceled(t *testing.T) {
	backupStopped := make(chan struct{})
	primary := &scriptedIterModel{
		name: "primary",
		responses: []*model.Response{
			partialResponse("hello"),
			errorResponse("primary stream failed", model.ErrorTypeStreamError),
		},
	}
	backup := &scriptedIterModel{
		name:      "backup",
		stopped:   backupStopped,
		holdAfter: true,
	}
	llm, err := New(
		WithCandidates(primary, backup),
		WithDelay(0),
	)
	require.NoError(t, err)
	responses := collectChannelResponses(t, llm, &model.Request{
		Messages: []model.Message{model.NewUserMessage("hello")},
	})
	require.Len(t, responses, 2)
	assert.Equal(t, "hello", responses[0].Choices[0].Delta.Content)
	require.NotNil(t, responses[1].Error)
	assert.Equal(t, "primary stream failed", responses[1].Error.Message)
	waitForClosed(t, backupStopped, 100*time.Millisecond)
}

func TestAllCandidatesFailReturnsAggregatedFailureResponse(t *testing.T) {
	primary := &scriptedIterModel{
		name:       "primary",
		startupErr: errors.New("dial tcp timeout"),
	}
	backup := &scriptedIterModel{
		name: "backup",
		responses: []*model.Response{
			errorResponse("rate limit", model.ErrorTypeAPIError),
		},
	}
	llm, err := New(
		WithCandidates(primary, backup),
		WithDelay(0),
	)
	require.NoError(t, err)
	responses := collectChannelResponses(t, llm, &model.Request{
		Messages: []model.Message{model.NewUserMessage("hello")},
	})
	require.Len(t, responses, 1)
	require.NotNil(t, responses[0].Error)
	assert.Contains(t, responses[0].Error.Message, `candidate model "primary" failed before winner selection: dial tcp timeout`)
	assert.Contains(t, responses[0].Error.Message, `candidate model "backup" failed before winner selection: rate limit`)
	assert.Equal(t, model.ErrorTypeAPIError, responses[0].Error.Type)
	assert.True(t, responses[0].Done)
}

func TestRequestIsClonedPerCandidate(t *testing.T) {
	request := &model.Request{
		Messages: []model.Message{model.NewUserMessage("original")},
	}
	primary := &scriptedIterModel{
		name: "primary",
		prepare: func(req *model.Request) {
			req.Messages[0].Content = "mutated"
		},
		startupErr: errors.New("primary failed"),
	}
	backup := &scriptedIterModel{
		name: "backup",
		responsesForRequest: func(req *model.Request) []*model.Response {
			return []*model.Response{assistantResponse(req.Messages[0].Content, true)}
		},
	}
	llm, err := New(
		WithCandidates(primary, backup),
		WithDelay(500*time.Millisecond),
	)
	require.NoError(t, err)
	responses := collectChannelResponses(t, llm, request)
	require.Len(t, responses, 1)
	assert.Equal(t, "original", responses[0].Choices[0].Message.Content)
	assert.Equal(t, "original", request.Messages[0].Content)
}

func TestBuildFailureMessageIncludesWinnerSelectionWording(t *testing.T) {
	message := buildFailureMessage([]failureRecord{
		{candidate: "primary", message: "primary failed"},
		{message: "fallback preparation failed"},
	})
	assert.True(t, strings.Contains(message, `candidate model "primary" failed before winner selection: primary failed`))
	assert.True(t, strings.Contains(message, "fallback preparation failed"))
}

func TestFailureHelpersPreserveFailureRecords(t *testing.T) {
	records := []failureRecord{
		{candidate: "primary", message: "primary failed", errType: model.ErrorTypeAPIError},
	}
	err := newFailureError(records)
	require.EqualError(t, err, `candidate model "primary" failed before winner selection: primary failed`)
	assert.Equal(t, records, failuresFromError(err, nil))
	fallback := failuresFromError(errors.New("prepare failed"), nil)
	require.Len(t, fallback, 1)
	assert.Equal(t, "prepare failed", fallback[0].message)
	assert.Equal(t, "", fallback[0].candidate)
}

func TestBuildFailureResponseUsesDefaultErrorType(t *testing.T) {
	response := buildFailureResponse([]failureRecord{{candidate: "primary", message: "primary failed"}})
	require.NotNil(t, response.Error)
	assert.Equal(t, model.ErrorTypeAPIError, response.Error.Type)
}

func TestLaunchAttemptRecordsCloneFailure(t *testing.T) {
	runCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	candidate := &scriptedIterModel{name: "primary"}
	run := &hedgeRun{
		hedge: &hedgeModel{
			candidates: []model.Model{candidate},
		},
		request: &model.Request{
			StructuredOutput: &model.StructuredOutput{
				Type: model.StructuredOutputJSONSchema,
				JSONSchema: &model.JSONSchemaConfig{
					Name: "answer",
					Schema: map[string]any{
						"unsupported": func() {},
					},
				},
			},
		},
		yield:       func(*model.Response) bool { return true },
		ctx:         runCtx,
		cancel:      cancel,
		attempts:    make([]*attempt, 1),
		failures:    make([]failureRecord, 0, 1),
		eventChan:   make(chan attemptEvent, 1),
		winnerIndex: -1,
	}
	run.launchAttempt(0)
	require.Len(t, run.failures, 1)
	assert.Equal(t, "primary", run.failures[0].candidate)
	assert.Contains(t, run.failures[0].message, "marshal request")
	assert.Equal(t, 0, run.activeCount)
}

func TestSequenceForCandidateReturnsErrorForNilResults(t *testing.T) {
	request := &model.Request{
		Messages: []model.Message{model.NewUserMessage("hello")},
	}
	seq, err := sequenceForCandidate(context.Background(), &nilIterModel{name: "iter-primary"}, request)
	require.Error(t, err)
	assert.EqualError(t, err, `candidate model "iter-primary" returned nil response sequence`)
	assert.Nil(t, seq)
	seq, err = sequenceForCandidate(context.Background(), &nilChannelModel{name: "channel-primary"}, request)
	require.Error(t, err)
	assert.EqualError(t, err, `candidate model "channel-primary" returned nil response channel`)
	assert.Nil(t, seq)
}

func TestSequenceForCandidateStopsWhenContextCanceledForChannelModel(t *testing.T) {
	request := &model.Request{
		Messages: []model.Message{model.NewUserMessage("hello")},
	}
	ctx, cancel := context.WithCancel(context.Background())
	seq, err := sequenceForCandidate(ctx, &blockingChannelModel{name: "blocking-primary"}, request)
	require.NoError(t, err)
	done := make(chan struct{})
	go func() {
		defer close(done)
		seq(func(*model.Response) bool {
			return true
		})
	}()
	cancel()
	waitForClosed(t, done, 100*time.Millisecond)
}

func TestCloneRequestReturnsMarshalError(t *testing.T) {
	request := &model.Request{
		StructuredOutput: &model.StructuredOutput{
			Type: model.StructuredOutputJSONSchema,
			JSONSchema: &model.JSONSchemaConfig{
				Name: "answer",
				Schema: map[string]any{
					"unsupported": func() {},
				},
			},
		},
	}
	cloned, err := cloneRequest(request)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "marshal request")
	assert.Nil(t, cloned)
}

func TestResponseErrorMessageBranches(t *testing.T) {
	code := "bad-request"
	param := "temperature"
	assert.Equal(t, "unknown response error", responseErrorMessage(nil))
	assert.Equal(t, "rate limit", responseErrorMessage(&model.ResponseError{Message: "rate limit"}))
	assert.Equal(t, model.ErrorTypeAPIError, responseErrorMessage(&model.ResponseError{Type: model.ErrorTypeAPIError}))
	assert.Equal(t, code, responseErrorMessage(&model.ResponseError{Code: &code}))
	assert.Equal(t, param, responseErrorMessage(&model.ResponseError{Param: &param}))
	assert.Equal(t, "unknown response error", responseErrorMessage(&model.ResponseError{}))
}

func TestSendAttemptEventReturnsFalseWhenContextCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	assert.False(t, sendAttemptEvent(ctx, make(chan attemptEvent), attemptEvent{}))
}

func TestWaitReturnsForContextAndTimer(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	run := &hedgeRun{
		ctx:         ctx,
		cancel:      func() {},
		winnerIndex: -1,
	}
	assert.True(t, run.wait())
	run = &hedgeRun{
		ctx:         context.Background(),
		cancel:      func() {},
		winnerIndex: -1,
	}
	run.launchTimer = time.NewTimer(0)
	run.launchTimerChan = run.launchTimer.C
	assert.False(t, run.wait())
	assert.Nil(t, run.launchTimer)
	assert.Nil(t, run.launchTimerChan)
}

func TestHandleEventStateTransitions(t *testing.T) {
	t.Run("select winner and cancel losers", func(t *testing.T) {
		loserCanceled := false
		run := &hedgeRun{
			yield: func(*model.Response) bool {
				return true
			},
			cancel: func() {},
			attempts: []*attempt{
				{index: 0, cancel: func() {}},
				{index: 1, cancel: func() { loserCanceled = true }},
			},
			winnerIndex: -1,
		}
		done := run.handleEvent(attemptEvent{
			index:    0,
			response: assistantResponse("winner", true),
		})
		assert.False(t, done)
		assert.Equal(t, 0, run.winnerIndex)
		assert.True(t, loserCanceled)
	})
	t.Run("record pre-winner failure", func(t *testing.T) {
		run := &hedgeRun{
			yield: func(*model.Response) bool {
				return true
			},
			cancel:      func() {},
			activeCount: 1,
			failures:    make([]failureRecord, 0, 1),
			winnerIndex: -1,
		}
		done := run.handleEvent(attemptEvent{
			index:   0,
			failure: &failureRecord{candidate: "primary", message: "primary failed"},
		})
		assert.False(t, done)
		assert.Equal(t, 0, run.activeCount)
		require.Len(t, run.failures, 1)
		assert.Equal(t, "primary failed", run.failures[0].message)
	})
	t.Run("stop when winner stream is rejected", func(t *testing.T) {
		canceled := false
		run := &hedgeRun{
			yield: func(*model.Response) bool {
				return false
			},
			cancel:      func() { canceled = true },
			winnerIndex: 0,
		}
		done := run.handleEvent(attemptEvent{
			index:    0,
			response: partialResponse("winner"),
		})
		assert.True(t, done)
		assert.True(t, canceled)
	})
}

func TestAdvanceProcessesQueuedWinnerBeforeLaunchingDueCandidates(t *testing.T) {
	runCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	run := &hedgeRun{
		hedge: &hedgeModel{
			candidates: []model.Model{
				&scriptedIterModel{name: "primary"},
				&scriptedIterModel{name: "backup"},
			},
			launchOffsets: []time.Duration{0, 0},
		},
		request: &model.Request{
			Messages: []model.Message{model.NewUserMessage("hello")},
		},
		yield:           func(*model.Response) bool { return true },
		ctx:             runCtx,
		cancel:          cancel,
		attempts:        make([]*attempt, 2),
		failures:        make([]failureRecord, 0, 2),
		eventChan:       make(chan attemptEvent, 1),
		nextLaunchIndex: 1,
		winnerIndex:     -1,
	}
	run.eventChan <- attemptEvent{
		index:    0,
		response: assistantResponse("winner", true),
	}
	done := run.advance(time.Now())
	assert.False(t, done)
	assert.Equal(t, 0, run.winnerIndex)
	assert.Nil(t, run.attempts[1])
	assert.Equal(t, 1, run.nextLaunchIndex)
}

type scriptedIterModel struct {
	name                string
	startupErr          error
	responses           []*model.Response
	responsesForRequest func(*model.Request) []*model.Response
	prepare             func(*model.Request)
	started             chan struct{}
	stopped             chan struct{}
	startDelay          time.Duration
	holdAfter           bool
}

func (m *scriptedIterModel) GenerateContent(
	ctx context.Context,
	request *model.Request,
) (<-chan *model.Response, error) {
	ch := make(chan *model.Response)
	close(ch)
	return ch, nil
}

func (m *scriptedIterModel) GenerateContentIter(
	ctx context.Context,
	request *model.Request,
) (model.Seq[*model.Response], error) {
	if m.prepare != nil {
		m.prepare(request)
	}
	signalClosed(m.started)
	if m.startupErr != nil {
		return nil, m.startupErr
	}
	responses := m.responses
	if m.responsesForRequest != nil {
		responses = m.responsesForRequest(request)
	}
	return func(yield func(*model.Response) bool) {
		defer signalClosed(m.stopped)
		if m.startDelay > 0 {
			select {
			case <-time.After(m.startDelay):
			case <-ctx.Done():
				return
			}
		}
		for _, response := range responses {
			if !yield(response) {
				return
			}
		}
		if !m.holdAfter {
			return
		}
		<-ctx.Done()
	}, nil
}

func (m *scriptedIterModel) Info() model.Info {
	return model.Info{Name: m.name}
}

type nilIterModel struct {
	name string
}

func (m *nilIterModel) GenerateContent(
	ctx context.Context,
	request *model.Request,
) (<-chan *model.Response, error) {
	ch := make(chan *model.Response)
	close(ch)
	return ch, nil
}

func (m *nilIterModel) GenerateContentIter(
	ctx context.Context,
	request *model.Request,
) (model.Seq[*model.Response], error) {
	return nil, nil
}

func (m *nilIterModel) Info() model.Info {
	return model.Info{Name: m.name}
}

type scriptedChannelModel struct {
	name                string
	responses           []*model.Response
	responsesForRequest func(*model.Request) []*model.Response
	err                 error
	prepare             func(*model.Request)
	started             chan struct{}
	stopped             chan struct{}
	holdAfter           bool
}

func (m *scriptedChannelModel) GenerateContent(
	ctx context.Context,
	request *model.Request,
) (<-chan *model.Response, error) {
	if m.prepare != nil {
		m.prepare(request)
	}
	signalClosed(m.started)
	if m.err != nil {
		return nil, m.err
	}
	responses := m.responses
	if m.responsesForRequest != nil {
		responses = m.responsesForRequest(request)
	}
	ch := make(chan *model.Response, len(responses))
	go func() {
		defer close(ch)
		defer signalClosed(m.stopped)
		for _, response := range responses {
			select {
			case ch <- response:
			case <-ctx.Done():
				return
			}
		}
		if !m.holdAfter {
			return
		}
		<-ctx.Done()
	}()
	return ch, nil
}

func (m *scriptedChannelModel) Info() model.Info {
	return model.Info{Name: m.name}
}

type nilChannelModel struct {
	name string
}

func (m *nilChannelModel) GenerateContent(
	ctx context.Context,
	request *model.Request,
) (<-chan *model.Response, error) {
	return nil, nil
}

func (m *nilChannelModel) Info() model.Info {
	return model.Info{Name: m.name}
}

type blockingChannelModel struct {
	name string
}

func (m *blockingChannelModel) GenerateContent(
	ctx context.Context,
	request *model.Request,
) (<-chan *model.Response, error) {
	return make(chan *model.Response), nil
}

func (m *blockingChannelModel) Info() model.Info {
	return model.Info{Name: m.name}
}

type stubTool struct {
	name string
}

func (t stubTool) Declaration() *tool.Declaration {
	return &tool.Declaration{Name: t.name}
}

func assistantResponse(content string, done bool) *model.Response {
	return &model.Response{
		Model: "assistant-model",
		Choices: []model.Choice{
			{
				Message: model.Message{
					Role:    model.RoleAssistant,
					Content: content,
				},
			},
		},
		Done: done,
	}
}

func partialResponse(content string) *model.Response {
	return &model.Response{
		Model: "assistant-model",
		Choices: []model.Choice{
			{
				Delta: model.Message{
					Role:    model.RoleAssistant,
					Content: content,
				},
			},
		},
		IsPartial: true,
	}
}

func errorResponse(message string, errType string) *model.Response {
	return &model.Response{
		Error: &model.ResponseError{
			Message: message,
			Type:    errType,
		},
		Model: "assistant-model",
		Done:  true,
	}
}

func collectChannelResponses(
	t *testing.T,
	llm model.Model,
	request *model.Request,
) []*model.Response {
	t.Helper()
	responseChan, err := llm.GenerateContent(context.Background(), request)
	require.NoError(t, err)
	return collectResponsesFromChannel(responseChan)
}

func collectResponsesFromChannel(responseChan <-chan *model.Response) []*model.Response {
	responses := make([]*model.Response, 0)
	for response := range responseChan {
		responses = append(responses, response)
	}
	return responses
}

func waitForClosed(t *testing.T, signal <-chan struct{}, timeout time.Duration) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(timeout):
		t.Fatalf("timed out waiting for signal after %v", timeout)
	}
}

func assertNotClosed(t *testing.T, signal <-chan struct{}, timeout time.Duration) {
	t.Helper()
	select {
	case <-signal:
		t.Fatalf("signal closed unexpectedly within %v", timeout)
	case <-time.After(timeout):
	}
}

func signalClosed(signal chan struct{}) {
	if signal == nil {
		return
	}
	select {
	case <-signal:
	default:
		close(signal)
	}
}
