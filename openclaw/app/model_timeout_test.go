//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package app

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/model"
)

func TestNewModelTimeoutModel_DisabledReturnsOriginal(t *testing.T) {
	t.Parallel()

	underlying := &timeoutImmediateModel{}
	require.Same(t, underlying, newModelTimeoutModel(underlying, 0))
	require.Nil(t, newModelTimeoutModel(nil, time.Second))
}

func TestModelTimeoutModel_StopsBlockedCreation(t *testing.T) {
	t.Parallel()

	underlying := newBlockingCreationModel()
	wrapped := newModelTimeoutModel(underlying, 10*time.Millisecond)
	defer close(underlying.release)

	ch, err := wrapped.GenerateContent(context.Background(), &model.Request{})
	require.NoError(t, err)

	resp := readTimeoutResponse(t, ch)
	require.Equal(t, model.ErrorTypeCancelled, resp.Error.Type)
	require.Contains(t, resp.Error.Message, "model request timeout")
}

func TestModelTimeoutModel_ForwardsImmediateResponse(t *testing.T) {
	t.Parallel()

	wrapped := newModelTimeoutModel(
		&timeoutImmediateModel{},
		time.Second,
	)

	ch, err := wrapped.GenerateContent(context.Background(), &model.Request{})
	require.NoError(t, err)

	resp := readResponse(t, ch)
	require.True(t, resp.Done)
	require.Nil(t, resp.Error)
}

func TestModelTimeoutModel_ForwardsInfo(t *testing.T) {
	t.Parallel()

	wrapped := newModelTimeoutModel(
		&timeoutImmediateModel{},
		time.Second,
	)

	require.Equal(t, "timeout-immediate", wrapped.Info().Name)
}

func TestModelTimeoutModel_ReturnsCreationError(t *testing.T) {
	t.Parallel()

	wrapped := newModelTimeoutModel(
		&timeoutErrorModel{err: errTimeoutModel},
		time.Second,
	)

	ch, err := wrapped.GenerateContent(context.Background(), &model.Request{})
	require.ErrorIs(t, err, errTimeoutModel)
	require.Nil(t, ch)
}

func TestModelTimeoutModel_ReportsContextCancellation(t *testing.T) {
	t.Parallel()

	underlying := newBlockingCreationModel()
	wrapped := newModelTimeoutModel(underlying, time.Second)
	defer close(underlying.release)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	ch, err := wrapped.GenerateContent(ctx, &model.Request{})
	require.NoError(t, err)

	resp := readTimeoutResponse(t, ch)
	require.Contains(t, resp.Error.Message, "model request canceled")
}

func TestModelTimeoutModel_StopsBlockedStream(t *testing.T) {
	t.Parallel()

	underlying := newBlockingStreamModel()
	wrapped := newModelTimeoutModel(underlying, 10*time.Millisecond)
	defer close(underlying.release)

	ch, err := wrapped.GenerateContent(context.Background(), &model.Request{})
	require.NoError(t, err)

	resp := readTimeoutResponse(t, ch)
	require.Equal(t, model.ErrorTypeCancelled, resp.Error.Type)
	require.Contains(t, resp.Error.Message, "model request timeout")
}

func TestModelTimeoutModel_DeliversTimeoutAfterQueuedResponses(t *testing.T) {
	t.Parallel()

	underlying := newQueuedThenBlockingStreamModel()
	wrapped := newModelTimeoutModel(underlying, 10*time.Millisecond)
	defer close(underlying.release)

	ch, err := wrapped.GenerateContent(context.Background(), &model.Request{})
	require.NoError(t, err)

	time.Sleep(50 * time.Millisecond)
	resp := readResponse(t, ch)
	require.Equal(t, "partial-1", resp.Choices[0].Message.Content)

	resp = readResponse(t, ch)
	require.Equal(t, "partial-2", resp.Choices[0].Message.Content)

	timeout := readTimeoutResponse(t, ch)
	require.Equal(t, model.ErrorTypeCancelled, timeout.Error.Type)
	require.Contains(t, timeout.Error.Message, "model request timeout")
}

func TestModelTimeoutIterModel_StopsBlockedCreation(t *testing.T) {
	t.Parallel()

	underlying := newBlockingIterCreationModel()
	wrapped := newModelTimeoutModel(underlying, 10*time.Millisecond)
	iter, ok := wrapped.(model.IterModel)
	require.True(t, ok)
	defer close(underlying.release)

	seq, err := iter.GenerateContentIter(
		context.Background(),
		&model.Request{},
	)
	require.NoError(t, err)

	var resp *model.Response
	seq(func(r *model.Response) bool {
		resp = r
		return false
	})
	require.NotNil(t, resp)
	require.Equal(t, model.ErrorTypeCancelled, resp.Error.Type)
}

func TestModelTimeoutIterModel_ForwardsImmediateSeq(t *testing.T) {
	t.Parallel()

	wrapped := newModelTimeoutModel(
		&timeoutImmediateIterModel{},
		time.Second,
	)
	iter, ok := wrapped.(model.IterModel)
	require.True(t, ok)

	seq, err := iter.GenerateContentIter(
		context.Background(),
		&model.Request{},
	)
	require.NoError(t, err)

	var responses []*model.Response
	seq(func(r *model.Response) bool {
		responses = append(responses, r)
		return true
	})
	require.Len(t, responses, 1)
	require.True(t, responses[0].Done)
}

func TestModelTimeoutIterModel_ReturnsCreationError(t *testing.T) {
	t.Parallel()

	wrapped := newModelTimeoutModel(
		&timeoutErrorIterModel{err: errTimeoutModel},
		time.Second,
	)
	iter, ok := wrapped.(model.IterModel)
	require.True(t, ok)

	seq, err := iter.GenerateContentIter(
		context.Background(),
		&model.Request{},
	)
	require.ErrorIs(t, err, errTimeoutModel)
	require.Nil(t, seq)
}

func TestModelTimeoutIterModel_StopsBlockedSeq(t *testing.T) {
	t.Parallel()

	underlying := newBlockingIterSeqModel()
	wrapped := newModelTimeoutModel(underlying, 10*time.Millisecond)
	iter, ok := wrapped.(model.IterModel)
	require.True(t, ok)
	defer close(underlying.release)

	seq, err := iter.GenerateContentIter(
		context.Background(),
		&model.Request{},
	)
	require.NoError(t, err)

	var resp *model.Response
	seq(func(r *model.Response) bool {
		resp = r
		return false
	})
	require.NotNil(t, resp)
	require.Equal(t, model.ErrorTypeCancelled, resp.Error.Type)
}

func readResponse(
	t *testing.T,
	ch <-chan *model.Response,
) *model.Response {
	t.Helper()

	select {
	case resp, ok := <-ch:
		require.True(t, ok)
		require.NotNil(t, resp)
		return resp
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for model response")
	}
	return nil
}

func readTimeoutResponse(
	t *testing.T,
	ch <-chan *model.Response,
) *model.Response {
	t.Helper()

	select {
	case resp, ok := <-ch:
		require.True(t, ok)
		require.NotNil(t, resp)
		require.NotNil(t, resp.Error)
		return resp
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for model timeout response")
	}
	return nil
}

type timeoutImmediateModel struct{}

func (m *timeoutImmediateModel) GenerateContent(
	context.Context,
	*model.Request,
) (<-chan *model.Response, error) {
	ch := make(chan *model.Response, 1)
	ch <- &model.Response{Done: true}
	close(ch)
	return ch, nil
}

func (m *timeoutImmediateModel) Info() model.Info {
	return model.Info{Name: "timeout-immediate"}
}

var errTimeoutModel = errors.New("timeout model failed")

type timeoutErrorModel struct {
	timeoutImmediateModel
	err error
}

func (m *timeoutErrorModel) GenerateContent(
	context.Context,
	*model.Request,
) (<-chan *model.Response, error) {
	return nil, m.err
}

type timeoutImmediateIterModel struct {
	timeoutImmediateModel
}

func (m *timeoutImmediateIterModel) GenerateContentIter(
	context.Context,
	*model.Request,
) (model.Seq[*model.Response], error) {
	return func(yield func(*model.Response) bool) {
		yield(&model.Response{Done: true})
	}, nil
}

type timeoutErrorIterModel struct {
	timeoutImmediateModel
	err error
}

func (m *timeoutErrorIterModel) GenerateContentIter(
	context.Context,
	*model.Request,
) (model.Seq[*model.Response], error) {
	return nil, m.err
}

type blockingCreationModel struct {
	timeoutImmediateModel
	release chan struct{}
}

func newBlockingCreationModel() *blockingCreationModel {
	return &blockingCreationModel{release: make(chan struct{})}
}

func (m *blockingCreationModel) GenerateContent(
	context.Context,
	*model.Request,
) (<-chan *model.Response, error) {
	<-m.release
	ch := make(chan *model.Response)
	close(ch)
	return ch, nil
}

type blockingStreamModel struct {
	timeoutImmediateModel
	release chan struct{}
}

func newBlockingStreamModel() *blockingStreamModel {
	return &blockingStreamModel{release: make(chan struct{})}
}

func (m *blockingStreamModel) GenerateContent(
	context.Context,
	*model.Request,
) (<-chan *model.Response, error) {
	ch := make(chan *model.Response)
	go func() {
		defer close(ch)
		<-m.release
	}()
	return ch, nil
}

type queuedThenBlockingStreamModel struct {
	timeoutImmediateModel
	release chan struct{}
}

func newQueuedThenBlockingStreamModel() *queuedThenBlockingStreamModel {
	return &queuedThenBlockingStreamModel{release: make(chan struct{})}
}

func (m *queuedThenBlockingStreamModel) GenerateContent(
	context.Context,
	*model.Request,
) (<-chan *model.Response, error) {
	ch := make(chan *model.Response, 2)
	ch <- &model.Response{
		Choices: []model.Choice{{
			Message: model.NewAssistantMessage("partial-1"),
		}},
	}
	ch <- &model.Response{
		Choices: []model.Choice{{
			Message: model.NewAssistantMessage("partial-2"),
		}},
	}
	go func() {
		defer close(ch)
		<-m.release
	}()
	return ch, nil
}

type blockingIterCreationModel struct {
	blockingCreationModel
}

func newBlockingIterCreationModel() *blockingIterCreationModel {
	return &blockingIterCreationModel{
		blockingCreationModel: *newBlockingCreationModel(),
	}
}

func (m *blockingIterCreationModel) GenerateContentIter(
	context.Context,
	*model.Request,
) (model.Seq[*model.Response], error) {
	<-m.release
	return func(func(*model.Response) bool) {}, nil
}

type blockingIterSeqModel struct {
	timeoutImmediateModel
	release chan struct{}
}

func newBlockingIterSeqModel() *blockingIterSeqModel {
	return &blockingIterSeqModel{release: make(chan struct{})}
}

func (m *blockingIterSeqModel) GenerateContentIter(
	context.Context,
	*model.Request,
) (model.Seq[*model.Response], error) {
	return func(func(*model.Response) bool) {
		<-m.release
	}, nil
}
