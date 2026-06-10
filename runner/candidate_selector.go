//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package runner

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"sync"

	"golang.org/x/sync/errgroup"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/graph"
	"trpc.group/trpc-go/trpc-agent-go/internal/state/appender"
	"trpc.group/trpc-go/trpc-agent-go/internal/state/barrier"
	"trpc.group/trpc-go/trpc-agent-go/internal/state/flush"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

const defaultCandidateAttempts = 2

// CandidateSelector selects the winning candidate from one candidate selection run.
type CandidateSelector interface {
	// Select chooses one candidate attempt as the winner.
	Select(ctx context.Context, req *CandidateSelectRequest) (int, error)
}

// CandidateSelectRequest contains the candidate attempts for one runner turn.
type CandidateSelectRequest struct {
	AppName   string
	UserID    string
	SessionID string
	Message   model.Message
	Attempts  []*CandidateAttempt
}

// CandidateAttempt contains the observable result of one candidate attempt.
type CandidateAttempt struct {
	// Index is the zero-based attempt index.
	Index int
	// InvocationID is the invocation ID used inside the isolated candidate attempt.
	InvocationID string
	// Events are the candidate attempt events before winner normalization.
	Events []*event.Event
	// FinalResponse is the last non-partial model response observed for the attempt.
	FinalResponse *model.Response
}

// candidateSelectOption configures the low-level candidate selector runner option.
type candidateSelectOption func(*candidateSelectOptions)

type candidateSelectOptions struct {
	attempts    int
	parallel    bool
	parallelism int
}

// WithCandidateAttempts sets how many candidate attempts are generated.
// Values less than or equal to one disable candidate selection.
func WithCandidateAttempts(attempts int) candidateSelectOption {
	return func(opts *candidateSelectOptions) {
		opts.attempts = attempts
	}
}

// WithCandidateAttemptParallelEnabled enables parallel candidate attempt execution.
func WithCandidateAttemptParallelEnabled(enabled bool) candidateSelectOption {
	return func(opts *candidateSelectOptions) {
		opts.parallel = enabled
	}
}

// WithCandidateAttemptParallelism sets the maximum parallel candidate attempts.
// Values less than or equal to zero use runtime.GOMAXPROCS when parallel execution is enabled.
func WithCandidateAttemptParallelism(parallelism int) candidateSelectOption {
	return func(opts *candidateSelectOptions) {
		opts.parallelism = parallelism
	}
}

func wrapAgentWithCandidateSelector(
	ag agent.Agent,
	selector CandidateSelector,
	opts candidateSelectOptions,
) agent.Agent {
	if ag == nil || selector == nil {
		return ag
	}
	if _, ok := ag.(*candidateSelectorAgent); ok {
		return ag
	}
	return &candidateSelectorAgent{
		inner:    ag,
		selector: selector,
		opts:     opts,
	}
}

type candidateSelectorAgent struct {
	inner    agent.Agent
	selector CandidateSelector
	opts     candidateSelectOptions
}

func (a *candidateSelectorAgent) Info() agent.Info {
	if a == nil || a.inner == nil {
		return agent.Info{}
	}
	return a.inner.Info()
}

func (a *candidateSelectorAgent) Tools() []tool.Tool {
	if a == nil || a.inner == nil {
		return nil
	}
	return a.inner.Tools()
}

func (a *candidateSelectorAgent) SubAgents() []agent.Agent {
	if a == nil || a.inner == nil {
		return nil
	}
	return a.inner.SubAgents()
}

func (a *candidateSelectorAgent) FindSubAgent(name string) agent.Agent {
	if a == nil || a.inner == nil {
		return nil
	}
	return a.inner.FindSubAgent(name)
}

func (a *candidateSelectorAgent) Run(
	ctx context.Context,
	invocation *agent.Invocation,
) (<-chan *event.Event, error) {
	if a == nil || a.inner == nil {
		return nil, errors.New("candidate selector: inner agent is nil")
	}
	if invocation == nil {
		return nil, errors.New("candidate selector: invocation is nil")
	}
	if reason := a.bypassReason(ctx, invocation); reason != "" {
		log.DebugfContext(ctx, "Candidate selector bypassed: %s.", reason)
		return a.inner.Run(ctx, invocation)
	}
	out := make(chan *event.Event, agent.GetEventChannelBufferSize(invocation))
	runCtx := agent.CloneContext(ctx)
	go a.run(runCtx, invocation, out)
	return out, nil
}

func (a *candidateSelectorAgent) run(
	ctx context.Context,
	base *agent.Invocation,
	out chan<- *event.Event,
) {
	defer close(out)
	attempts, attemptErrs := a.runAttempts(ctx, base)
	if len(attempts) == 0 {
		a.emitError(ctx, base, out, fmt.Sprintf(
			"candidate selector: all attempts failed: %v",
			errors.Join(attemptErrs...),
		))
		return
	}
	req := &CandidateSelectRequest{
		AppName:   base.Session.AppName,
		UserID:    base.Session.UserID,
		SessionID: base.Session.ID,
		Message:   base.Message,
		Attempts:  attempts,
	}
	winnerIndex, err := a.selector.Select(ctx, req)
	if err != nil {
		a.emitError(ctx, base, out, fmt.Sprintf("candidate selector: select: %v", err))
		return
	}
	winner := findCandidateAttempt(attempts, winnerIndex)
	if winner == nil {
		a.emitError(ctx, base, out, "candidate selector: winner is invalid")
		return
	}
	a.forwardWinner(ctx, base, out, winner)
}

func (a *candidateSelectorAgent) runAttempts(
	ctx context.Context,
	base *agent.Invocation,
) ([]*CandidateAttempt, []error) {
	if a.opts.parallel {
		return a.runAttemptsParallel(ctx, base)
	}
	return a.runAttemptsSequential(ctx, base)
}

func (a *candidateSelectorAgent) runAttemptsSequential(
	ctx context.Context,
	base *agent.Invocation,
) ([]*CandidateAttempt, []error) {
	attempts := make([]*CandidateAttempt, 0, a.opts.attempts)
	errs := make([]error, 0, a.opts.attempts)
	for i := 0; i < a.opts.attempts; i++ {
		attempt, err := a.runAttempt(ctx, base, i)
		if err != nil {
			errs = append(errs, fmt.Errorf("attempt %d: %w", i, err))
			continue
		}
		attempts = append(attempts, attempt)
	}
	return attempts, errs
}

func (a *candidateSelectorAgent) runAttemptsParallel(
	ctx context.Context,
	base *agent.Invocation,
) ([]*CandidateAttempt, []error) {
	type attemptResult struct {
		attempt *CandidateAttempt
		err     error
	}
	results := make([]attemptResult, a.opts.attempts)
	parallelism := a.opts.effectiveParallelism()
	var group errgroup.Group
	group.SetLimit(parallelism)
	for i := 0; i < a.opts.attempts; i++ {
		index := i
		attemptCtx := agent.CloneContext(ctx)
		group.Go(func() error {
			if err := attemptCtx.Err(); err != nil {
				results[index].err = err
				return nil
			}
			attempt, err := a.runAttempt(attemptCtx, base, index)
			results[index] = attemptResult{attempt: attempt, err: err}
			return nil
		})
	}
	attempts := make([]*CandidateAttempt, 0, a.opts.attempts)
	errs := make([]error, 0, a.opts.attempts)
	if err := group.Wait(); err != nil {
		errs = append(errs, err)
	}
	for i, result := range results {
		if result.err != nil {
			errs = append(errs, fmt.Errorf("attempt %d: %w", i, result.err))
			continue
		}
		if result.attempt != nil {
			attempts = append(attempts, result.attempt)
		}
	}
	return attempts, errs
}

func (opts candidateSelectOptions) effectiveParallelism() int {
	if opts.parallelism > 0 {
		return opts.parallelism
	}
	parallelism := runtime.GOMAXPROCS(0)
	if parallelism <= 0 {
		return 1
	}
	return parallelism
}

func (a *candidateSelectorAgent) runAttempt(
	ctx context.Context,
	base *agent.Invocation,
	index int,
) (*CandidateAttempt, error) {
	if base.Session == nil {
		return nil, errors.New("session is nil")
	}
	attemptSession := base.Session.Clone()
	attemptScope := newAttemptSessionService(base.SessionService, attemptSession)
	attemptService := attemptScope.Service()
	innerInv := a.newAttemptInvocation(base, attemptSession, attemptService)
	flushChan := make(chan *flush.FlushRequest)
	flush.Attach(ctx, innerInv, flushChan)
	defer flush.Clear(innerInv)
	recorder := newCandidateEventRecorder()
	appender.Attach(innerInv, func(ctx context.Context, evt *event.Event) error {
		if err := attemptScope.AppendEvent(ctx, attemptSession, evt); err != nil {
			return err
		}
		recorder.Add(evt)
		return notifyCandidateCompletion(ctx, innerInv, evt)
	})
	events, err := runCandidateInnerAgent(ctx, a.inner, innerInv)
	if err != nil {
		return nil, err
	}
	finalResponse, err := collectCandidateEvents(ctx, innerInv, attemptScope, attemptSession, recorder, events, flushChan)
	if err != nil {
		return nil, err
	}
	collected := recorder.Events()
	collected = appendCandidateStateUpdate(base, collected, attemptScope.DirectStateDelta())
	return &CandidateAttempt{
		Index:         index,
		InvocationID:  innerInv.InvocationID,
		Events:        collected,
		FinalResponse: finalResponse,
	}, nil
}

func runCandidateInnerAgent(
	ctx context.Context,
	inner agent.Agent,
	invocation *agent.Invocation,
) (<-chan *event.Event, error) {
	type runResult struct {
		events <-chan *event.Event
		err    error
	}
	resultCh := make(chan runResult, 1)
	go func() {
		events, err := agent.RunWithPlugins(
			agent.NewInvocationContext(ctx, invocation),
			invocation,
			inner,
		)
		resultCh <- runResult{events: events, err: err}
	}()
	select {
	case result := <-resultCh:
		return result.events, result.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (a *candidateSelectorAgent) bypassReason(
	ctx context.Context,
	invocation *agent.Invocation,
) string {
	if a.selector == nil || a.opts.attempts <= 1 {
		return "candidate selector is disabled"
	}
	if invocation.RunOptions.ExecutionTraceEnabled {
		return "execution trace is enabled"
	}
	if runOptionsResumeCheckpoint(invocation.RunOptions) {
		return "runtime state resumes a graph checkpoint"
	}
	return ""
}

func runOptionsResumeCheckpoint(opts agent.RunOptions) bool {
	if len(opts.RuntimeState) == 0 {
		return false
	}
	if _, ok := opts.RuntimeState[graph.CfgKeyCheckpointID]; ok {
		return true
	}
	configurable, ok := opts.RuntimeState[graph.CfgKeyConfigurable].(map[string]any)
	if !ok {
		return false
	}
	_, ok = configurable[graph.CfgKeyCheckpointID]
	return ok
}

func (a *candidateSelectorAgent) newAttemptInvocation(
	base *agent.Invocation,
	attemptSession *session.Session,
	attemptService session.Service,
) *agent.Invocation {
	opts := []agent.InvocationOptions{
		agent.WithInvocationAgent(a.inner),
		agent.WithInvocationBranch(base.Branch),
		agent.WithInvocationSession(attemptSession),
		agent.WithInvocationSessionService(attemptService),
		agent.WithInvocationMessage(base.Message),
		agent.WithInvocationRunOptions(base.RunOptions),
		agent.WithInvocationModel(base.Model),
		agent.WithInvocationStructuredOutput(base.StructuredOutput),
		agent.WithInvocationStructuredOutputType(base.StructuredOutputType),
		agent.WithInvocationMemoryService(newReadOnlyMemoryService(base.MemoryService)),
		agent.WithInvocationArtifactService(newReadOnlyArtifactService(base.ArtifactService)),
		agent.WithInvocationPlugins(base.Plugins),
		agent.WithInvocationEventFilterKey(base.GetEventFilterKey()),
	}
	if traceNodeID := agent.InvocationTraceNodeID(base); traceNodeID != "" {
		opts = append(opts, agent.WithInvocationTraceNodeID(traceNodeID))
	}
	innerInv := agent.NewInvocation(opts...)
	innerInv.MaxLLMCalls = base.MaxLLMCalls
	innerInv.MaxToolIterations = base.MaxToolIterations
	barrier.Enable(innerInv)
	return innerInv
}

func collectCandidateEvents(
	ctx context.Context,
	inv *agent.Invocation,
	attemptService *attemptSessionService,
	attemptSession *session.Session,
	recorder *candidateEventRecorder,
	events <-chan *event.Event,
	flushChan <-chan *flush.FlushRequest,
) (*model.Response, error) {
	var finalResponse *model.Response
	for {
		select {
		case evt, ok := <-events:
			if !ok {
				return finalResponse, nil
			}
			if err := attemptService.AppendEvent(ctx, attemptSession, evt); err != nil {
				return nil, err
			}
			if err := notifyCandidateCompletion(ctx, inv, evt); err != nil {
				return nil, err
			}
			recorder.Add(evt)
			if evt != nil && evt.Response != nil && !evt.Response.IsPartial {
				finalResponse = evt.Response
			}
		case req, ok := <-flushChan:
			if !ok {
				flushChan = nil
				continue
			}
			flushFinalResponse, err := flushCandidateEvents(ctx, inv, attemptService, attemptSession, recorder, events)
			if err != nil {
				return nil, err
			}
			if flushFinalResponse != nil {
				finalResponse = flushFinalResponse
			}
			if req != nil && req.ACK != nil {
				close(req.ACK)
			}
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

func flushCandidateEvents(
	ctx context.Context,
	inv *agent.Invocation,
	attemptService *attemptSessionService,
	attemptSession *session.Session,
	recorder *candidateEventRecorder,
	events <-chan *event.Event,
) (*model.Response, error) {
	var finalResponse *model.Response
	for {
		select {
		case evt, ok := <-events:
			if !ok {
				return finalResponse, nil
			}
			if err := attemptService.AppendEvent(ctx, attemptSession, evt); err != nil {
				return nil, err
			}
			if err := notifyCandidateCompletion(ctx, inv, evt); err != nil {
				return nil, err
			}
			recorder.Add(evt)
			if evt != nil && evt.Response != nil && !evt.Response.IsPartial {
				finalResponse = evt.Response
			}
		default:
			return finalResponse, nil
		}
	}
}

func appendCandidateStateUpdate(
	base *agent.Invocation,
	events []*event.Event,
	stateDelta session.StateMap,
) []*event.Event {
	if len(stateDelta) == 0 {
		return events
	}
	for i := len(events) - 1; i >= 0; i-- {
		if events[i] == nil {
			continue
		}
		mergeCandidateStateDeltaIntoEvent(events[i], stateDelta)
		return events
	}
	evt := event.New(
		base.InvocationID,
		base.AgentName,
		event.WithObject(model.ObjectTypeStateUpdate),
		event.WithStateDelta(cloneStateMap(stateDelta)),
	)
	agent.InjectIntoEvent(base, evt)
	return append(events, evt)
}

func mergeCandidateStateDeltaIntoEvent(evt *event.Event, stateDelta session.StateMap) {
	if evt.StateDelta == nil {
		evt.StateDelta = make(session.StateMap, len(stateDelta))
	}
	for key, value := range stateDelta {
		evt.StateDelta[key] = cloneBytes(value)
	}
}

func notifyCandidateCompletion(
	ctx context.Context,
	inv *agent.Invocation,
	evt *event.Event,
) error {
	if evt == nil || !evt.RequiresCompletion {
		return nil
	}
	return inv.NotifyCompletion(ctx, agent.GetAppendEventNoticeKey(evt.ID))
}

func findCandidateAttempt(
	attempts []*CandidateAttempt,
	winnerIndex int,
) *CandidateAttempt {
	for _, attempt := range attempts {
		if attempt != nil && attempt.Index == winnerIndex {
			return attempt
		}
	}
	return nil
}

func (a *candidateSelectorAgent) forwardWinner(
	ctx context.Context,
	base *agent.Invocation,
	out chan<- *event.Event,
	winner *CandidateAttempt,
) {
	for _, evt := range winner.Events {
		normalized := normalizeWinnerEvent(base, evt)
		if err := event.EmitEvent(ctx, out, normalized); err != nil {
			return
		}
	}
}

func normalizeWinnerEvent(base *agent.Invocation, evt *event.Event) *event.Event {
	if evt == nil {
		return nil
	}
	normalized := evt.Clone()
	normalized.ExecutionTrace = nil
	agent.InjectIntoEvent(base, normalized)
	return normalized
}

func (a *candidateSelectorAgent) emitError(
	ctx context.Context,
	base *agent.Invocation,
	out chan<- *event.Event,
	msg string,
) {
	evt := event.NewErrorEvent(
		base.InvocationID,
		base.AgentName,
		model.ErrorTypeRunError,
		msg,
	)
	agent.InjectIntoEvent(base, evt)
	if err := event.EmitEvent(ctx, out, evt); err != nil {
		log.ErrorfContext(ctx, "Candidate selector emit error event failed: %v.", err)
	}
}

type candidateEventRecorder struct {
	mu     sync.Mutex
	seen   map[string]bool
	events []*event.Event
}

func newCandidateEventRecorder() *candidateEventRecorder {
	return &candidateEventRecorder{
		seen:   make(map[string]bool),
		events: make([]*event.Event, 0),
	}
}

func (r *candidateEventRecorder) Add(evt *event.Event) {
	if evt == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if evt.ID != "" && r.seen[evt.ID] {
		return
	}
	if evt.ID != "" {
		r.seen[evt.ID] = true
	}
	r.events = append(r.events, evt)
}

func (r *candidateEventRecorder) Events() []*event.Event {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]*event.Event(nil), r.events...)
}
