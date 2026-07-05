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
	"fmt"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/model"
)

type modelTimeoutResult struct {
	ch  <-chan *model.Response
	err error
}

type modelTimeoutIterResult struct {
	seq model.Seq[*model.Response]
	err error
}

func newModelTimeoutModel(
	m model.Model,
	timeout time.Duration,
) model.Model {
	if m == nil || timeout <= 0 {
		return m
	}
	wrapped := &modelTimeoutModel{
		model:   m,
		timeout: timeout,
	}
	if iter, ok := m.(model.IterModel); ok {
		return &modelTimeoutIterModel{
			modelTimeoutModel: wrapped,
			iter:              iter,
		}
	}
	return wrapped
}

type modelTimeoutModel struct {
	model   model.Model
	timeout time.Duration
}

func (m *modelTimeoutModel) GenerateContent(
	ctx context.Context,
	req *model.Request,
) (<-chan *model.Response, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	callCtx, cancel := context.WithTimeout(ctx, m.timeout)
	resultCh := make(chan modelTimeoutResult, 1)
	go func() {
		ch, err := m.model.GenerateContent(callCtx, req)
		resultCh <- modelTimeoutResult{ch: ch, err: err}
	}()

	select {
	case result := <-resultCh:
		if result.err != nil {
			cancel()
			return nil, result.err
		}
		return m.forwardResponses(callCtx, cancel, result.ch), nil
	case <-callCtx.Done():
		cancel()
		return singleTimeoutResponse(m.timeout, callCtx.Err()), nil
	}
}

func (m *modelTimeoutModel) forwardResponses(
	ctx context.Context,
	cancel context.CancelFunc,
	ch <-chan *model.Response,
) <-chan *model.Response {
	out := make(chan *model.Response, 1)
	go func() {
		defer close(out)
		defer cancel()
		for {
			select {
			case rsp, ok := <-ch:
				if !ok {
					return
				}
				select {
				case out <- rsp:
				case <-ctx.Done():
					sendTimeoutResponse(out, m.timeout, ctx.Err())
					return
				}
			case <-ctx.Done():
				sendTimeoutResponse(out, m.timeout, ctx.Err())
				return
			}
		}
	}()
	return out
}

func (m *modelTimeoutModel) Info() model.Info {
	return m.model.Info()
}

type modelTimeoutIterModel struct {
	*modelTimeoutModel
	iter model.IterModel
}

func (m *modelTimeoutIterModel) GenerateContentIter(
	ctx context.Context,
	req *model.Request,
) (model.Seq[*model.Response], error) {
	if ctx == nil {
		ctx = context.Background()
	}
	callCtx, cancel := context.WithTimeout(ctx, m.timeout)
	resultCh := make(chan modelTimeoutIterResult, 1)
	go func() {
		seq, err := m.iter.GenerateContentIter(callCtx, req)
		resultCh <- modelTimeoutIterResult{seq: seq, err: err}
	}()

	select {
	case result := <-resultCh:
		if result.err != nil {
			cancel()
			return nil, result.err
		}
		return m.forwardSeq(callCtx, cancel, result.seq), nil
	case <-callCtx.Done():
		cancel()
		return timeoutSeq(m.timeout, callCtx.Err()), nil
	}
}

func (m *modelTimeoutIterModel) forwardSeq(
	ctx context.Context,
	cancel context.CancelFunc,
	seq model.Seq[*model.Response],
) model.Seq[*model.Response] {
	return func(yield func(*model.Response) bool) {
		defer cancel()
		ch := make(chan *model.Response)
		go func() {
			defer close(ch)
			seq(func(rsp *model.Response) bool {
				select {
				case ch <- rsp:
					return true
				case <-ctx.Done():
					return false
				}
			})
		}()
		for {
			select {
			case rsp, ok := <-ch:
				if !ok {
					return
				}
				if !yield(rsp) {
					return
				}
			case <-ctx.Done():
				yield(timeoutResponse(m.timeout, ctx.Err()))
				return
			}
		}
	}
}

func timeoutSeq(
	timeout time.Duration,
	err error,
) model.Seq[*model.Response] {
	return func(yield func(*model.Response) bool) {
		yield(timeoutResponse(timeout, err))
	}
}

func singleTimeoutResponse(
	timeout time.Duration,
	err error,
) <-chan *model.Response {
	ch := make(chan *model.Response, 1)
	ch <- timeoutResponse(timeout, err)
	close(ch)
	return ch
}

func sendTimeoutResponse(
	ch chan<- *model.Response,
	timeout time.Duration,
	err error,
) {
	select {
	case ch <- timeoutResponse(timeout, err):
	default:
	}
}

func timeoutResponse(
	timeout time.Duration,
	err error,
) *model.Response {
	message := fmt.Sprintf("model request timeout after %s", timeout)
	if err != nil && err != context.DeadlineExceeded {
		message = fmt.Sprintf("model request canceled: %v", err)
	}
	return &model.Response{
		Object: model.ObjectTypeError,
		Error: &model.ResponseError{
			Message: message,
			Type:    model.ErrorTypeCancelled,
		},
		Timestamp: time.Now(),
		Done:      true,
	}
}
