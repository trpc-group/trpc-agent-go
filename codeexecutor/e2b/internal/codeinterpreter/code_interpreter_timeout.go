//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package codeinterpreter

import (
	"context"
	"errors"
	"net"
	"sync"
	"time"
)

// resolveRequestTimeout applies the RunCodeOpts.RequestTimeout convention.
// A negative option disables the header deadline. Zero inherits the connection
// value. A non-positive result disables the deadline; ConnectionConfig.init
// normally replaces a zero connection value with DefaultRequestTimeout.
func resolveRequestTimeout(option, connection time.Duration) (time.Duration, bool) {
	if option < 0 {
		return 0, false
	}
	if option == 0 {
		option = connection
	}
	if option <= 0 {
		return 0, false
	}
	return option, true
}

// resolveExecutionTimeout applies the RunCodeOpts.Timeout convention.
// Zero selects DefaultTimeout seconds. A negative value disables the
// execution-stream deadline without disabling the header deadline.
func resolveExecutionTimeout(option time.Duration) (time.Duration, bool) {
	if option == 0 {
		option = DefaultTimeout * time.Second
	}
	if option <= 0 {
		return 0, false
	}
	return option, true
}

type timeoutPhase uint8

const (
	timeoutPhaseIdle timeoutPhase = iota
	timeoutPhaseRequest
	timeoutPhaseExecution
	timeoutPhaseDone
)

// codeExecTimer owns the header and execution deadlines for one RunCode call.
// The request context stays alive for the body, so the active deadline is
// switched by cancelling that context with a phase-specific cause. Callers
// and callbacks share mu so a timer that loses the phase switch cannot cancel.
type codeExecTimer struct {
	mu     sync.Mutex
	phase  timeoutPhase
	timer  *time.Timer
	cancel context.CancelCauseFunc
	fired  error
}

func newCodeExecTimer(cancel context.CancelCauseFunc) *codeExecTimer {
	return &codeExecTimer{cancel: cancel, phase: timeoutPhaseIdle}
}

// arm moves the deadline to phase. enabled false, or a non-positive duration,
// records the phase without starting a timer. arm returns false when a
// deadline already won the race; the recorded cause remains available.
func (p *codeExecTimer) arm(phase timeoutPhase, d time.Duration, enabled bool, cause error) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.phase == timeoutPhaseDone {
		return false
	}
	p.stopTimerLocked()
	p.phase = phase
	if !enabled || d <= 0 {
		return true
	}
	p.timer = time.AfterFunc(d, func() { p.fire(phase, cause) })
	return true
}

func (p *codeExecTimer) fire(phase timeoutPhase, cause error) {
	p.mu.Lock()
	if p.phase != phase {
		p.mu.Unlock()
		return
	}
	p.phase = timeoutPhaseDone
	p.timer = nil
	p.fired = cause
	cancel := p.cancel
	p.mu.Unlock()
	if cancel != nil {
		cancel(cause)
	}
}

// stop prevents a later callback from firing. It does not clear a cause that
// has already been recorded and does not cancel the stream.
func (p *codeExecTimer) stop() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.phase != timeoutPhaseDone {
		p.phase = timeoutPhaseDone
	}
	p.stopTimerLocked()
}

func (p *codeExecTimer) stopTimerLocked() {
	if p.timer == nil {
		return
	}
	p.timer.Stop()
	p.timer = nil
}

func (p *codeExecTimer) cause() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.fired
}

// classifyRunCodeError maps one failed header wait or stream read to a stable
// error. Our deadlines win only when they are the context's first cause.
// Caller cancellation and caller deadlines stay recognizable and are not
// returned as TimeoutError.
func classifyRunCodeError(caller, stream context.Context, timers *codeExecTimer, err error) error {
	if err == nil && stream != nil {
		err = stream.Err()
	}
	streamCause := context.Cause(stream)
	if timeoutErr := timeoutForCause(streamCause, err); timeoutErr != nil {
		return timeoutErr
	}
	if stream != nil && stream.Err() == nil {
		if timeoutErr := timeoutForCause(timers.cause(), err); timeoutErr != nil {
			return timeoutErr
		}
	}
	if caller != nil && caller.Err() != nil {
		return preserveCallerError(caller, err)
	}
	if timeoutErr := timeoutForCause(timers.cause(), err); timeoutErr != nil {
		return timeoutErr
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return runCodeRequestTimeout(err)
	}
	return err
}

func timeoutForCause(cause, trigger error) error {
	switch {
	case errors.Is(cause, errRequestTimeout):
		return runCodeRequestTimeout(firstNonNil(trigger, cause))
	case errors.Is(cause, errExecutionTimeout):
		return runCodeExecutionTimeout(firstNonNil(trigger, cause))
	default:
		return nil
	}
}

func firstNonNil(primary, fallback error) error {
	if primary != nil {
		return primary
	}
	return fallback
}

func preserveCallerError(caller context.Context, err error) error {
	callerErr := caller.Err()
	if callerErr == nil {
		return err
	}
	if err == nil || errors.Is(err, callerErr) {
		if err == nil {
			return callerErr
		}
		return err
	}
	return errors.Join(callerErr, err)
}
