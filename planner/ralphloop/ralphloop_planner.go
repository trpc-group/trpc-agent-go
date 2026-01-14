//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package ralphloop implements a Ralph Loop planner.
//
// Ralph Loop is an "outer loop" idea: instead of trusting a Large Language
// Model (LLM) to decide when it is done, the framework keeps iterating until
// an external, machine-checkable completion condition is met.
//
// This planner runs inside LLMAgent's flow. When the model tries to stop
// (Response.Done == true), it can override that decision by setting Done back
// to false so the internal llmflow loop continues with another LLM call.
package ralphloop

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/planner"
)

const (
	defaultMaxIterations = 10

	defaultPromiseTagOpen  = "<promise>"
	defaultPromiseTagClose = "</promise>"

	stateKeyIteration = "planner:ralphloop:iteration"
	stateKeyFeedback  = "planner:ralphloop:pending_feedback"
)

var errMissingStopCondition = errors.New(
	"ralph loop planner: missing completion promise and verifiers",
)

// VerifyResult describes one verifier outcome.
type VerifyResult struct {
	// Passed reports whether the completion condition is satisfied.
	Passed bool
	// Feedback is a human-readable failure message for the next iteration.
	// It is ignored when Passed is true.
	Feedback string
}

// Verifier checks whether the task is complete.
//
// Verifiers are only called when the model tries to stop (Response.Done is
// true and the response is not a tool-call response). All configured
// verifiers must pass before the planner allows the flow to stop.
type Verifier interface {
	Verify(
		ctx context.Context,
		invocation *agent.Invocation,
		response *model.Response,
	) (VerifyResult, error)
}

// Config configures a RalphLoop planner.
type Config struct {
	// MaxIterations is the maximum number of times the planner is allowed
	// to override Done=false and force another LLM call.
	//
	// When <= 0, a safe default is used.
	MaxIterations int

	// CompletionPromise is an optional stop signal.
	//
	// When set, the flow is allowed to stop only if the assistant output
	// contains:
	//   <promise>CompletionPromise</promise>
	//
	// The tag strings are configurable via PromiseTagOpen/PromiseTagClose.
	CompletionPromise string
	PromiseTagOpen    string
	PromiseTagClose   string

	// Verifiers is an optional list of additional completion checks.
	// All verifiers must pass.
	Verifiers []Verifier
}

// Planner is a planner-based implementation of Ralph Loop.
type Planner struct {
	cfg Config
}

var _ planner.Planner = (*Planner)(nil)

// New constructs a Planner after validating and normalizing cfg.
func New(cfg Config) (*Planner, error) {
	normalized := normalizeConfig(cfg)
	if err := validateConfig(normalized); err != nil {
		return nil, err
	}
	return &Planner{cfg: normalized}, nil
}

// MustNew is like New but panics on invalid configuration.
func MustNew(cfg Config) *Planner {
	p, err := New(cfg)
	if err != nil {
		panic(err)
	}
	return p
}

// BuildPlanningInstruction injects any pending Ralph feedback and returns the
// static instruction describing Ralph Loop behavior.
func (p *Planner) BuildPlanningInstruction(
	ctx context.Context,
	invocation *agent.Invocation,
	llmRequest *model.Request,
) string {
	if invocation == nil || llmRequest == nil {
		return ""
	}

	p.injectPendingFeedback(invocation, llmRequest)
	return p.buildInstruction()
}

// ProcessPlanningResponse enforces Ralph Loop by overriding Done when the
// completion conditions are not met.
func (p *Planner) ProcessPlanningResponse(
	ctx context.Context,
	invocation *agent.Invocation,
	response *model.Response,
) *model.Response {
	if invocation == nil || response == nil {
		return nil
	}
	if !response.Done {
		return nil
	}
	if response.IsToolCallResponse() {
		return nil
	}

	complete, feedback, err := p.verify(ctx, invocation, response)
	if err != nil {
		return errorResponse(
			response,
			model.ErrorTypeFlowError,
			err.Error(),
		)
	}
	if complete {
		return nil
	}

	iter := incrementIteration(invocation)
	if iter > p.cfg.MaxIterations {
		msg := fmt.Sprintf(
			"ralph loop planner: max iterations (%d) reached",
			p.cfg.MaxIterations,
		)
		if feedback != "" {
			msg = msg + ": " + feedback
		}
		return errorResponse(response, model.ErrorTypeFlowError, msg)
	}

	p.setPendingFeedback(invocation, formatFeedback(
		iter,
		p.cfg.MaxIterations,
		feedback,
	))

	processed := *response
	processed.Done = false
	return &processed
}

func (p *Planner) verify(
	ctx context.Context,
	invocation *agent.Invocation,
	response *model.Response,
) (complete bool, feedback string, err error) {
	promiseOK, promiseFeedback := p.promiseSatisfied(response)

	verifiersOK := true
	var verifierFeedback []string
	for _, v := range p.cfg.Verifiers {
		if v == nil {
			continue
		}
		res, verifyErr := v.Verify(ctx, invocation, response)
		if verifyErr != nil {
			return false, "", verifyErr
		}
		if res.Passed {
			continue
		}
		verifiersOK = false
		if strings.TrimSpace(res.Feedback) != "" {
			verifierFeedback = append(verifierFeedback, res.Feedback)
		}
	}

	if promiseOK && verifiersOK {
		return true, "", nil
	}

	var parts []string
	if !verifiersOK {
		parts = append(parts, strings.Join(verifierFeedback, "\n\n"))
	}
	if !promiseOK && promiseFeedback != "" {
		parts = append(parts, promiseFeedback)
	}
	return false, strings.TrimSpace(strings.Join(parts, "\n\n")), nil
}

func (p *Planner) promiseSatisfied(
	response *model.Response,
) (bool, string) {
	want := normalizePromise(p.cfg.CompletionPromise)
	if want == "" {
		return true, ""
	}
	got, ok := firstPromiseText(
		response,
		p.cfg.PromiseTagOpen,
		p.cfg.PromiseTagClose,
	)
	if !ok {
		return false, missingPromiseFeedback(
			p.cfg.PromiseTagOpen,
			p.cfg.PromiseTagClose,
			p.cfg.CompletionPromise,
		)
	}
	if normalizePromise(got) != want {
		return false, missingPromiseFeedback(
			p.cfg.PromiseTagOpen,
			p.cfg.PromiseTagClose,
			p.cfg.CompletionPromise,
		)
	}
	return true, ""
}

func (p *Planner) setPendingFeedback(inv *agent.Invocation, msg string) {
	if inv == nil {
		return
	}
	if strings.TrimSpace(msg) == "" {
		return
	}
	inv.SetState(stateKeyFeedback, msg)
}

func (p *Planner) injectPendingFeedback(
	inv *agent.Invocation,
	req *model.Request,
) {
	if inv == nil || req == nil {
		return
	}
	raw, ok := agent.GetStateValue[string](inv, stateKeyFeedback)
	if !ok {
		return
	}
	feedback := strings.TrimSpace(raw)
	if feedback == "" {
		return
	}
	req.Messages = append([]model.Message{
		model.NewSystemMessage(feedback),
	}, req.Messages...)
	inv.SetState(stateKeyFeedback, "")
}

func (p *Planner) buildInstruction() string {
	open := p.cfg.PromiseTagOpen
	closeTag := p.cfg.PromiseTagClose
	want := p.cfg.CompletionPromise

	lines := []string{
		"You are running in Ralph Loop mode.",
		"",
		"Ralph Loop is an outer loop: if the completion criteria are not met,",
		"the framework will call you again and you must continue working.",
		"",
		"Rules:",
		"- Only output the completion promise when you are truly done.",
		"- If you see a system message that starts with \"Ralph Loop\" then it",
		"  contains verification feedback from the previous attempt.",
	}
	if strings.TrimSpace(want) != "" {
		lines = append(lines,
			"",
			"Completion promise:",
			fmt.Sprintf("%s%s%s", open, want, closeTag),
		)
	}
	return strings.Join(lines, "\n")
}

func normalizeConfig(cfg Config) Config {
	if cfg.MaxIterations <= 0 {
		cfg.MaxIterations = defaultMaxIterations
	}
	if strings.TrimSpace(cfg.PromiseTagOpen) == "" {
		cfg.PromiseTagOpen = defaultPromiseTagOpen
	}
	if strings.TrimSpace(cfg.PromiseTagClose) == "" {
		cfg.PromiseTagClose = defaultPromiseTagClose
	}
	return cfg
}

func validateConfig(cfg Config) error {
	hasPromise := strings.TrimSpace(cfg.CompletionPromise) != ""
	hasVerifiers := len(cfg.Verifiers) != 0
	if !hasPromise && !hasVerifiers {
		return errMissingStopCondition
	}
	return nil
}

func incrementIteration(inv *agent.Invocation) int {
	if inv == nil {
		return 0
	}
	cur, _ := agent.GetStateValue[int](inv, stateKeyIteration)
	next := cur + 1
	inv.SetState(stateKeyIteration, next)
	return next
}

func formatFeedback(iter int, max int, body string) string {
	const defaultBody = "Verification failed. Continue iterating."

	msg := strings.TrimSpace(body)
	if msg == "" {
		msg = defaultBody
	}
	return strings.Join([]string{
		fmt.Sprintf("Ralph Loop iteration %d/%d:", iter, max),
		msg,
	}, "\n")
}

func missingPromiseFeedback(open, closeTag, promise string) string {
	return strings.Join([]string{
		"Completion promise not detected.",
		"To stop, output exactly:",
		fmt.Sprintf("%s%s%s", open, promise, closeTag),
	}, "\n")
}

func firstPromiseText(
	response *model.Response,
	open string,
	closeTag string,
) (string, bool) {
	if response == nil || open == "" || closeTag == "" {
		return "", false
	}
	for _, choice := range response.Choices {
		msg := choice.Message
		if msg.Role != model.RoleAssistant {
			continue
		}
		text := assistantText(msg)
		if text == "" {
			continue
		}
		got, ok := firstTagText(text, open, closeTag)
		if ok {
			return got, true
		}
	}
	return "", false
}

func assistantText(msg model.Message) string {
	if msg.Content != "" {
		return msg.Content
	}
	if len(msg.ContentParts) == 0 {
		return ""
	}
	var b strings.Builder
	for _, part := range msg.ContentParts {
		if part.Type != model.ContentTypeText || part.Text == nil {
			continue
		}
		b.WriteString(*part.Text)
	}
	return b.String()
}

func firstTagText(text, open, closeTag string) (string, bool) {
	start := strings.Index(text, open)
	if start == -1 {
		return "", false
	}
	start += len(open)
	end := strings.Index(text[start:], closeTag)
	if end == -1 {
		return "", false
	}
	return text[start : start+end], true
}

func normalizePromise(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

func errorResponse(
	base *model.Response,
	errType string,
	message string,
) *model.Response {
	rsp := &model.Response{
		Object: model.ObjectTypeError,
		Done:   true,
		Error: &model.ResponseError{
			Type:    errType,
			Message: message,
		},
	}
	if base == nil {
		return rsp
	}
	rsp.ID = base.ID
	rsp.Model = base.Model
	rsp.Created = base.Created
	rsp.SystemFingerprint = base.SystemFingerprint
	return rsp
}
