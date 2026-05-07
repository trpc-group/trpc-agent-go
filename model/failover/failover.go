//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package failover provides a model.Model wrapper that falls back across
// candidates before the first non-error chunk is emitted.
package failover

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"trpc.group/trpc-go/trpc-agent-go/internal/jsonmap"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// failoverModel wraps multiple candidate models and falls back before the
// first non-error chunk is observed.
type failoverModel struct {
	candidates []model.Model
}

// New creates a failover model wrapper.
func New(opts ...Option) (model.Model, error) {
	options := options{}
	for _, opt := range opts {
		opt(&options)
	}
	if len(options.candidates) == 0 {
		return nil, errors.New("failover: at least one candidate model is required")
	}
	candidates := make([]model.Model, 0, len(options.candidates))
	for i, candidate := range options.candidates {
		if candidate == nil {
			return nil, fmt.Errorf("failover: candidate model at index %d is nil", i)
		}
		candidates = append(candidates, candidate)
	}
	return &failoverModel{candidates: candidates}, nil
}

// Info returns the primary candidate model info.
func (m *failoverModel) Info() model.Info {
	return m.candidates[0].Info()
}

// GenerateContent implements the model.Model interface.
func (m *failoverModel) GenerateContent(
	ctx context.Context,
	request *model.Request,
) (<-chan *model.Response, error) {
	seq, err := m.GenerateContentIter(ctx, request)
	if err != nil {
		return nil, err
	}
	responseChan := make(chan *model.Response, 1)
	go func() {
		defer close(responseChan)
		seq(func(resp *model.Response) bool {
			if resp == nil {
				return true
			}
			select {
			case responseChan <- resp.Clone():
				return true
			case <-ctx.Done():
				return false
			}
		})
	}()
	return responseChan, nil
}

// GenerateContentIter implements the model.IterModel interface.
func (m *failoverModel) GenerateContentIter(
	ctx context.Context,
	request *model.Request,
) (model.Seq[*model.Response], error) {
	if request == nil {
		return nil, errors.New("request cannot be nil")
	}
	initialAttempt, err := m.prepareAttempt(ctx, request, 0, nil)
	if err != nil {
		return nil, err
	}
	return func(yield func(*model.Response) bool) {
		m.runAttempts(ctx, request, initialAttempt, yield)
	}, nil
}

type attempt struct {
	index     int
	candidate model.Model
	seq       model.Seq[*model.Response]
	failures  []failureRecord
	cancel    context.CancelFunc
}

func (m *failoverModel) prepareAttempt(
	ctx context.Context,
	request *model.Request,
	startIndex int,
	failures []failureRecord,
) (*attempt, error) {
	currentFailures := append([]failureRecord(nil), failures...)
	for i := startIndex; i < len(m.candidates); i++ {
		candidateRequest, err := cloneRequest(request)
		if err != nil {
			return nil, err
		}
		attemptCtx, cancel := context.WithCancel(ctx)
		seq, err := sequenceForCandidate(attemptCtx, m.candidates[i], candidateRequest)
		if err == nil {
			return &attempt{
				index:     i,
				candidate: m.candidates[i],
				seq:       seq,
				failures:  currentFailures,
				cancel:    cancel,
			}, nil
		}
		cancel()
		currentFailures = appendFailure(
			currentFailures,
			m.candidates[i].Info().Name,
			err.Error(),
			"",
		)
	}
	return nil, newFailureError(currentFailures)
}

func (m *failoverModel) runAttempts(
	ctx context.Context,
	request *model.Request,
	initialAttempt *attempt,
	yield func(*model.Response) bool,
) {
	currentAttempt := initialAttempt
	seenFirstNonErrorChunk := false
	for currentAttempt != nil {
		var nextAttempt *attempt
		currentAttempt.seq(func(resp *model.Response) bool {
			if resp == nil {
				return true
			}
			cloned := resp.Clone()
			if seenFirstNonErrorChunk {
				return yield(cloned)
			}
			if !hasFailoverResponseError(cloned) {
				seenFirstNonErrorChunk = true
				return yield(cloned)
			}
			failures := appendFailure(
				currentAttempt.failures,
				currentAttempt.candidate.Info().Name,
				cloned.Error.Message,
				cloned.Error.Type,
			)
			if currentAttempt.index == len(m.candidates)-1 {
				if len(currentAttempt.failures) == 0 {
					return yield(cloned)
				}
				yield(buildFailureResponse(failures))
				return false
			}
			preparedAttempt, err := m.prepareAttempt(
				ctx,
				request,
				currentAttempt.index+1,
				failures,
			)
			if err != nil {
				yield(buildFailureResponse(failuresFromError(err, failures)))
				return false
			}
			nextAttempt = preparedAttempt
			return false
		})
		if currentAttempt.cancel != nil {
			currentAttempt.cancel()
		}
		if nextAttempt == nil {
			return
		}
		currentAttempt = nextAttempt
	}
}

func sequenceForCandidate(
	ctx context.Context,
	candidate model.Model,
	request *model.Request,
) (model.Seq[*model.Response], error) {
	if iterModel, ok := candidate.(model.IterModel); ok {
		seq, err := iterModel.GenerateContentIter(ctx, request)
		if err != nil {
			return nil, err
		}
		if seq == nil {
			return nil, fmt.Errorf("candidate model %q returned nil response sequence", candidate.Info().Name)
		}
		return seq, nil
	}
	responseChan, err := candidate.GenerateContent(ctx, request)
	if err != nil {
		return nil, err
	}
	if responseChan == nil {
		return nil, fmt.Errorf("candidate model %q returned nil response channel", candidate.Info().Name)
	}
	return func(yield func(*model.Response) bool) {
		for response := range responseChan {
			if !yield(response) {
				return
			}
		}
	}, nil
}

func cloneRequest(request *model.Request) (*model.Request, error) {
	payload, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	var cloned model.Request
	if err := json.Unmarshal(payload, &cloned); err != nil {
		return nil, fmt.Errorf("unmarshal request: %w", err)
	}
	cloned.ExtraFields = jsonmap.Clone(request.ExtraFields)
	if len(request.Tools) > 0 {
		cloned.Tools = make(map[string]tool.Tool, len(request.Tools))
		for name, toolImpl := range request.Tools {
			cloned.Tools[name] = toolImpl
		}
	}
	return &cloned, nil
}

func hasFailoverResponseError(response *model.Response) bool {
	if response == nil || response.Error == nil {
		return false
	}
	return response.Error.Message != "" ||
		response.Error.Type != "" ||
		response.Error.Param != nil ||
		response.Error.Code != nil
}
