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
