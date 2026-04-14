//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package hedge provides a model.Model wrapper that launches hedge requests
// across candidates and commits the first meaningful response.
package hedge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

type hedgeModel struct {
	candidates    []model.Model
	name          string
	launchOffsets []time.Duration
}

type attempt struct {
	index     int
	candidate model.Model
	cancel    context.CancelFunc
}

type attemptEvent struct {
	index    int
	response *model.Response
	failure  *failureRecord
	finished bool
}

type hedgeRun struct {
	hedge           *hedgeModel
	request         *model.Request
	yield           func(*model.Response) bool
	ctx             context.Context
	cancel          context.CancelFunc
	attempts        []*attempt
	failures        []failureRecord
	eventChan       chan attemptEvent
	start           time.Time
	nextLaunchIndex int
	activeCount     int
	winnerIndex     int
	launchTimer     *time.Timer
	launchTimerChan <-chan time.Time
}

// New creates a hedge model wrapper.
func New(opt ...Option) (model.Model, error) {
	opts := newOptions(opt...)
	if len(opts.candidates) == 0 {
		return nil, errors.New("hedge: at least one candidate model is required")
	}
	candidates := make([]model.Model, 0, len(opts.candidates))
	for i, candidate := range opts.candidates {
		if candidate == nil {
			return nil, fmt.Errorf("hedge: candidate model at index %d is nil", i)
		}
		candidates = append(candidates, candidate)
	}
	launchOffsets, err := resolveLaunchOffsets(len(candidates), &opts)
	if err != nil {
		return nil, err
	}
	name := opts.name
	if name == "" {
		name = candidates[0].Info().Name
	}
	return &hedgeModel{
		candidates:    candidates,
		name:          name,
		launchOffsets: launchOffsets,
	}, nil
}

// Info returns the logical hedge model info.
func (m *hedgeModel) Info() model.Info {
	return model.Info{Name: m.name}
}

// GenerateContent implements the model.Model interface.
func (m *hedgeModel) GenerateContent(
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
func (m *hedgeModel) GenerateContentIter(
	ctx context.Context,
	request *model.Request,
) (model.Seq[*model.Response], error) {
	if request == nil {
		return nil, errors.New("request cannot be nil")
	}
	return func(yield func(*model.Response) bool) {
		m.runHedge(ctx, request, yield)
	}, nil
}

func (m *hedgeModel) runHedge(
	ctx context.Context,
	request *model.Request,
	yield func(*model.Response) bool,
) {
	run := newHedgeRun(ctx, m, request, yield)
	defer run.close()
	run.run()
}

func newHedgeRun(
	ctx context.Context,
	hedge *hedgeModel,
	request *model.Request,
	yield func(*model.Response) bool,
) *hedgeRun {
	runCtx, cancel := context.WithCancel(ctx)
	return &hedgeRun{
		hedge:       hedge,
		request:     request,
		yield:       yield,
		ctx:         runCtx,
		cancel:      cancel,
		attempts:    make([]*attempt, len(hedge.candidates)),
		failures:    make([]failureRecord, 0, len(hedge.candidates)),
		eventChan:   make(chan attemptEvent, len(hedge.candidates)),
		start:       time.Now(),
		winnerIndex: -1,
	}
}

func (r *hedgeRun) close() {
	r.stopLaunchTimer()
	r.cancel()
}

func (r *hedgeRun) run() {
	r.launchAttempt(0)
	r.nextLaunchIndex = 1
	for {
		if r.advance(time.Now()) {
			return
		}
		if r.wait() {
			return
		}
	}
}

func (r *hedgeRun) advance(now time.Time) bool {
	if r.drainReadyEvents() {
		return true
	}
	r.launchReadyAttempts(now)
	if r.drainReadyEvents() {
		return true
	}
	if r.winnerIndex == -1 && r.activeCount == 0 && r.nextLaunchIndex >= len(r.hedge.candidates) {
		if len(r.failures) == 0 {
			return true
		}
		r.yield(buildFailureResponse(r.failures))
		return true
	}
	r.updateLaunchTimer(now)
	return false
}

func (r *hedgeRun) drainReadyEvents() bool {
	for {
		select {
		case event := <-r.eventChan:
			if r.handleEvent(event) {
				return true
			}
		default:
			return false
		}
	}
}

func (r *hedgeRun) wait() bool {
	select {
	case <-r.ctx.Done():
		r.stopLaunchTimer()
		return true
	case <-r.launchTimerChan:
		r.stopLaunchTimer()
		return false
	case event := <-r.eventChan:
		return r.handleEvent(event)
	}
}

func (r *hedgeRun) handleEvent(event attemptEvent) bool {
	if r.winnerIndex == -1 {
		if event.response != nil {
			if !isWinningResponse(event.response) {
				return false
			}
			r.winnerIndex = event.index
			r.cancelLosers(event.index)
			r.stopLaunchTimer()
			if r.yield(event.response) {
				return false
			}
			r.cancel()
			return true
		}
		if event.failure != nil {
			r.activeCount--
			r.failures = append(r.failures, *event.failure)
			return false
		}
		if event.finished {
			r.activeCount--
		}
		return false
	}
	if event.index != r.winnerIndex {
		return false
	}
	if event.response != nil {
		if r.yield(event.response) {
			return false
		}
		r.cancel()
		return true
	}
	return event.failure != nil || event.finished
}

func (r *hedgeRun) launchReadyAttempts(now time.Time) {
	if r.winnerIndex != -1 {
		return
	}
	r.launchDueAttempts(now)
	for r.activeCount == 0 && r.nextLaunchIndex < len(r.hedge.candidates) {
		r.launchAttempt(r.nextLaunchIndex)
		r.nextLaunchIndex++
		r.launchDueAttempts(time.Now())
	}
}

func (r *hedgeRun) launchDueAttempts(now time.Time) {
	elapsed := now.Sub(r.start)
	for r.nextLaunchIndex < len(r.hedge.launchOffsets) &&
		r.hedge.launchOffsets[r.nextLaunchIndex] <= elapsed {
		r.launchAttempt(r.nextLaunchIndex)
		r.nextLaunchIndex++
	}
}

func (r *hedgeRun) launchAttempt(index int) {
	candidate := r.hedge.candidates[index]
	candidateRequest, err := cloneRequest(r.request)
	if err != nil {
		r.failures = appendFailure(
			r.failures,
			candidate.Info().Name,
			err.Error(),
			"",
		)
		return
	}
	attemptCtx, attemptCancel := context.WithCancel(r.ctx)
	seq, err := sequenceForCandidate(
		attemptCtx,
		candidate,
		candidateRequest,
	)
	if err != nil {
		attemptCancel()
		r.failures = appendFailure(
			r.failures,
			candidate.Info().Name,
			err.Error(),
			"",
		)
		return
	}
	activeAttempt := &attempt{
		index:     index,
		candidate: candidate,
		cancel:    attemptCancel,
	}
	r.attempts[index] = activeAttempt
	r.activeCount++
	go runAttempt(attemptCtx, activeAttempt, seq, r.eventChan)
}

func (r *hedgeRun) updateLaunchTimer(now time.Time) {
	r.stopLaunchTimer()
	if r.winnerIndex != -1 || r.nextLaunchIndex >= len(r.hedge.launchOffsets) {
		return
	}
	delay := r.hedge.launchOffsets[r.nextLaunchIndex] - now.Sub(r.start)
	if delay < 0 {
		delay = 0
	}
	r.launchTimer = time.NewTimer(delay)
	r.launchTimerChan = r.launchTimer.C
}

func (r *hedgeRun) stopLaunchTimer() {
	if r.launchTimer == nil {
		return
	}
	if !r.launchTimer.Stop() {
		select {
		case <-r.launchTimer.C:
		default:
		}
	}
	r.launchTimer = nil
	r.launchTimerChan = nil
}

func (r *hedgeRun) cancelLosers(winner int) {
	for i, activeAttempt := range r.attempts {
		if activeAttempt == nil || i == winner {
			continue
		}
		activeAttempt.cancel()
	}
}

func runAttempt(
	ctx context.Context,
	activeAttempt *attempt,
	seq model.Seq[*model.Response],
	eventChan chan<- attemptEvent,
) {
	sawMeaningful := false
	var terminalFailure *failureRecord
	seq(func(resp *model.Response) bool {
		if resp == nil {
			return true
		}
		cloned := resp.Clone()
		if isWinningResponse(cloned) {
			sawMeaningful = true
		}
		if !sendAttemptEvent(ctx, eventChan, attemptEvent{
			index:    activeAttempt.index,
			response: cloned,
		}) {
			return false
		}
		if !hasHedgeResponseError(cloned) {
			return true
		}
		terminalFailure = &failureRecord{
			candidate: activeAttempt.candidate.Info().Name,
			message:   responseErrorMessage(cloned.Error),
			errType:   cloned.Error.Type,
		}
		return false
	})
	if ctx.Err() != nil {
		return
	}
	if terminalFailure != nil {
		sendAttemptEvent(ctx, eventChan, attemptEvent{
			index:   activeAttempt.index,
			failure: terminalFailure,
		})
		return
	}
	if !sawMeaningful {
		sendAttemptEvent(ctx, eventChan, attemptEvent{
			index: activeAttempt.index,
			failure: &failureRecord{
				candidate: activeAttempt.candidate.Info().Name,
				message:   completedWithoutMeaningfulResponse,
			},
		})
		return
	}
	sendAttemptEvent(ctx, eventChan, attemptEvent{
		index:    activeAttempt.index,
		finished: true,
	})
}

func sendAttemptEvent(
	ctx context.Context,
	eventChan chan<- attemptEvent,
	event attemptEvent,
) bool {
	select {
	case eventChan <- event:
		return true
	case <-ctx.Done():
		return false
	}
}

func resolveLaunchOffsets(candidateCount int, opts *options) ([]time.Duration, error) {
	launchOffsets := make([]time.Duration, candidateCount)
	if opts.delays != nil {
		if len(opts.delays) != candidateCount-1 {
			return nil, fmt.Errorf(
				"hedge: expected %d explicit delays, got %d",
				candidateCount-1,
				len(opts.delays),
			)
		}
		prev := time.Duration(0)
		for i, delay := range opts.delays {
			if delay < 0 {
				return nil, fmt.Errorf("hedge: delay at index %d cannot be negative", i)
			}
			if i > 0 && delay < prev {
				return nil, fmt.Errorf("hedge: delays must be non-decreasing")
			}
			launchOffsets[i+1] = delay
			prev = delay
		}
		return launchOffsets, nil
	}
	delay := opts.delay
	if delay < 0 {
		return nil, errors.New("hedge: delay cannot be negative")
	}
	for i := 1; i < candidateCount; i++ {
		launchOffsets[i] = time.Duration(i) * delay
	}
	return launchOffsets, nil
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
		for {
			select {
			case <-ctx.Done():
				return
			case response, ok := <-responseChan:
				if !ok {
					return
				}
				if !yield(response) {
					return
				}
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
	if len(request.Tools) > 0 {
		cloned.Tools = make(map[string]tool.Tool, len(request.Tools))
		for name, toolImpl := range request.Tools {
			cloned.Tools[name] = toolImpl
		}
	}
	return &cloned, nil
}

func hasHedgeResponseError(response *model.Response) bool {
	if response == nil || response.Error == nil {
		return false
	}
	return response.Error.Message != "" ||
		response.Error.Type != "" ||
		response.Error.Param != nil ||
		response.Error.Code != nil
}

func isWinningResponse(response *model.Response) bool {
	return response != nil &&
		!hasHedgeResponseError(response) &&
		response.IsValidContent()
}

func responseErrorMessage(responseError *model.ResponseError) string {
	if responseError == nil {
		return "unknown response error"
	}
	if responseError.Message != "" {
		return responseError.Message
	}
	if responseError.Type != "" {
		return responseError.Type
	}
	if responseError.Code != nil && *responseError.Code != "" {
		return *responseError.Code
	}
	if responseError.Param != nil && *responseError.Param != "" {
		return *responseError.Param
	}
	return "unknown response error"
}
