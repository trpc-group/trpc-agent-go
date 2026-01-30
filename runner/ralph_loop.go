//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package runner

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/internal/state/appender"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

const (
	defaultRalphMaxIterations = 10

	defaultPromiseTagOpen  = "<promise>"
	defaultPromiseTagClose = "</promise>"

	defaultRalphEventBuffer = 256

	ralphVerifyShell = "bash"
	ralphVerifyFlag  = "-lc"
)

var errRalphLoopMissingStopCondition = errors.New(
	"ralph loop: missing completion promise, verify command, and verifiers",
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
// Verifiers are only called after an agent iteration completes.
// All configured verifiers must pass before Ralph Loop allows the run to
// stop.
type Verifier interface {
	Verify(
		ctx context.Context,
		invocation *agent.Invocation,
		lastEvent *event.Event,
	) (VerifyResult, error)
}

// RalphLoopConfig controls a runner-level Ralph Loop mode.
//
// Ralph Loop is an "outer loop": instead of trusting the Large Language Model
// (LLM) to decide when it is done, the runner keeps iterating until a
// verifiable completion condition is met (or max iterations is reached).
type RalphLoopConfig struct {
	// MaxIterations is the maximum number of agent iterations to run.
	// When <= 0, a safe default is used.
	MaxIterations int

	// CompletionPromise is an optional stop signal.
	//
	// When set, the loop stops only if the agent output contains:
	//   <promise>CompletionPromise</promise>
	//
	// The tag strings are configurable via PromiseTagOpen/PromiseTagClose.
	CompletionPromise string
	PromiseTagOpen    string
	PromiseTagClose   string

	// VerifyCommand is an optional command to run after each iteration.
	// The loop stops only if the command exits with code 0.
	VerifyCommand string
	VerifyWorkDir string
	VerifyTimeout time.Duration
	VerifyEnv     map[string]string
	VerifyRunner  RalphLoopCommandRunner

	// Verifiers is an optional list of additional completion checks.
	// All verifiers must pass.
	Verifiers []Verifier
}

// RalphLoopCommandRunner executes VerifyCommand.
type RalphLoopCommandRunner interface {
	Run(
		ctx context.Context,
		spec RalphLoopCommandSpec,
	) (RalphLoopCommandResult, error)
}

// RalphLoopCommandSpec describes a command execution.
type RalphLoopCommandSpec struct {
	Command string
	WorkDir string
	Env     map[string]string
	Timeout time.Duration
}

// RalphLoopCommandResult captures command output and exit status.
type RalphLoopCommandResult struct {
	Stdout   string
	Stderr   string
	ExitCode int
	Duration time.Duration
	TimedOut bool
}

// WithRalphLoop enables Ralph Loop mode on a Runner.
//
// It wraps the selected agent so the run will keep iterating until:
//   - CompletionPromise is detected (when set), AND
//   - VerifyCommand exits with code 0 (when set), AND
//   - All Verifiers pass (when set), OR
//   - MaxIterations is reached.
func WithRalphLoop(cfg RalphLoopConfig) Option {
	return func(opts *Options) {
		copied := cfg
		opts.ralphLoop = &copied
	}
}

func wrapAgentsWithRalphLoop(
	agents map[string]agent.Agent,
	cfg RalphLoopConfig,
) {
	if len(agents) == 0 {
		return
	}
	for name, ag := range agents {
		if ag == nil {
			continue
		}
		agents[name] = wrapAgentWithRalphLoop(ag, cfg)
	}
}

func wrapAgentWithRalphLoop(
	ag agent.Agent,
	cfg RalphLoopConfig,
) agent.Agent {
	if ag == nil {
		return nil
	}
	if _, ok := ag.(*ralphLoopAgent); ok {
		return ag
	}
	return &ralphLoopAgent{
		inner: ag,
		cfg:   normalizeRalphLoopConfig(cfg),
	}
}

type ralphLoopAgent struct {
	inner agent.Agent
	cfg   RalphLoopConfig
}

func (a *ralphLoopAgent) Info() agent.Info {
	if a == nil || a.inner == nil {
		return agent.Info{}
	}
	info := a.inner.Info()
	if info.Description == "" {
		info.Description = "Ralph loop enabled"
	} else {
		info.Description = info.Description + " (ralph loop)"
	}
	return info
}

func (a *ralphLoopAgent) Tools() []tool.Tool {
	if a == nil || a.inner == nil {
		return nil
	}
	return a.inner.Tools()
}

func (a *ralphLoopAgent) SubAgents() []agent.Agent {
	if a == nil || a.inner == nil {
		return nil
	}
	return a.inner.SubAgents()
}

func (a *ralphLoopAgent) FindSubAgent(name string) agent.Agent {
	if a == nil || a.inner == nil {
		return nil
	}
	return a.inner.FindSubAgent(name)
}

func (a *ralphLoopAgent) Run(
	ctx context.Context,
	invocation *agent.Invocation,
) (<-chan *event.Event, error) {
	if a == nil || a.inner == nil {
		return nil, errors.New("ralph loop: inner agent is nil")
	}
	if invocation == nil {
		return nil, errors.New("ralph loop: invocation is nil")
	}
	if err := validateRalphLoopConfig(a.cfg); err != nil {
		return nil, err
	}

	out := make(chan *event.Event, defaultRalphEventBuffer)
	runCtx := agent.CloneContext(ctx)
	go a.runLoop(runCtx, invocation, out)
	return out, nil
}

func (a *ralphLoopAgent) runLoop(
	ctx context.Context,
	base *agent.Invocation,
	out chan<- *event.Event,
) {
	defer close(out)

	max := a.cfg.MaxIterations
	for iter := 1; iter <= max; iter++ {
		if err := agent.CheckContextCancelled(ctx); err != nil {
			return
		}

		innerInv := a.newInnerInvocation(base)
		innerCtx := agent.NewInvocationContext(ctx, innerInv)
		events, err := agent.RunWithPlugins(innerCtx, innerInv, a.inner)
		if err != nil {
			a.emitStopError(ctx, base, out, err.Error())
			return
		}

		lastFull := a.forwardEvents(ctx, events, out)
		done, feedback, verifyErr := a.verifyIteration(
			ctx,
			base,
			lastFull,
		)
		if verifyErr != nil {
			a.emitStopError(ctx, base, out, verifyErr.Error())
			return
		}
		if done {
			return
		}
		if err := a.appendFeedback(ctx, base, iter, feedback); err != nil {
			a.emitStopError(ctx, base, out, err.Error())
			return
		}
	}

	a.emitStopError(
		ctx,
		base,
		out,
		fmt.Sprintf(
			"ralph loop: max iterations (%d) reached",
			max,
		),
	)
}

func (a *ralphLoopAgent) newInnerInvocation(
	base *agent.Invocation,
) *agent.Invocation {
	if base == nil {
		return nil
	}
	return base.Clone(
		agent.WithInvocationAgent(a.inner),
		agent.WithInvocationBranch(base.Branch),
	)
}

func (a *ralphLoopAgent) forwardEvents(
	ctx context.Context,
	events <-chan *event.Event,
	out chan<- *event.Event,
) *event.Event {
	var lastFull *event.Event
	for evt := range events {
		if evt != nil && evt.Response != nil && !evt.Response.IsPartial {
			lastFull = evt
		}
		if err := event.EmitEvent(ctx, out, evt); err != nil {
			return lastFull
		}
	}
	return lastFull
}

func (a *ralphLoopAgent) verifyIteration(
	ctx context.Context,
	base *agent.Invocation,
	lastFull *event.Event,
) (bool, string, error) {
	promiseOK := a.promiseSatisfied(lastFull)
	cmdOK, cmdReport := a.commandSatisfied(ctx)

	verifiersOK, verifierReport, err := a.verifiersSatisfied(
		ctx,
		base,
		lastFull,
	)
	if err != nil {
		return false, "", err
	}

	wantPromise := strings.TrimSpace(a.cfg.CompletionPromise) != ""
	wantCmd := strings.TrimSpace(a.cfg.VerifyCommand) != ""
	wantVerifiers := hasNonNilVerifier(a.cfg.Verifiers)

	complete := (!wantPromise || promiseOK) &&
		(!wantCmd || cmdOK) &&
		(!wantVerifiers || verifiersOK)
	if complete {
		return true, "", nil
	}

	var parts []string
	if wantVerifiers && !verifiersOK {
		parts = append(parts, verifierReport)
	}
	if wantCmd && !cmdOK {
		parts = append(parts, cmdReport)
	}
	if wantPromise && !promiseOK {
		parts = append(parts, a.missingPromiseReport())
	}
	return false, strings.TrimSpace(strings.Join(parts, "\n\n")), nil
}

func (a *ralphLoopAgent) verifiersSatisfied(
	ctx context.Context,
	base *agent.Invocation,
	lastFull *event.Event,
) (bool, string, error) {
	if !hasNonNilVerifier(a.cfg.Verifiers) {
		return true, "", nil
	}

	ok := true
	var feedback []string
	for _, v := range a.cfg.Verifiers {
		if v == nil {
			continue
		}
		res, err := v.Verify(ctx, base, lastFull)
		if err != nil {
			return false, "", err
		}
		if res.Passed {
			continue
		}
		ok = false
		if strings.TrimSpace(res.Feedback) == "" {
			continue
		}
		feedback = append(feedback, res.Feedback)
	}
	if ok {
		return true, "", nil
	}
	return false, strings.TrimSpace(strings.Join(feedback, "\n\n")), nil
}

func hasNonNilVerifier(verifiers []Verifier) bool {
	for _, v := range verifiers {
		if v != nil {
			return true
		}
	}
	return false
}

func (a *ralphLoopAgent) promiseSatisfied(lastFull *event.Event) bool {
	expected := normalizePromiseText(a.cfg.CompletionPromise)
	if expected == "" {
		return true
	}
	open := a.cfg.PromiseTagOpen
	closeTag := a.cfg.PromiseTagClose
	promise, ok := firstTagText(lastFull, open, closeTag)
	if !ok {
		return false
	}
	return normalizePromiseText(promise) == expected
}

func (a *ralphLoopAgent) missingPromiseReport() string {
	open := a.cfg.PromiseTagOpen
	closeTag := a.cfg.PromiseTagClose
	want := a.cfg.CompletionPromise
	return fmt.Sprintf(
		"Completion promise not detected. To stop, output:\n%s%s%s",
		open,
		want,
		closeTag,
	)
}

func (a *ralphLoopAgent) commandSatisfied(
	ctx context.Context,
) (bool, string) {
	cmd := strings.TrimSpace(a.cfg.VerifyCommand)
	if cmd == "" {
		return true, ""
	}

	runner := a.cfg.VerifyRunner
	if runner == nil {
		runner = hostRalphLoopRunner{}
	}
	res, err := runner.Run(ctx, RalphLoopCommandSpec{
		Command: cmd,
		WorkDir: a.cfg.VerifyWorkDir,
		Env:     a.cfg.VerifyEnv,
		Timeout: a.cfg.VerifyTimeout,
	})
	if err != nil {
		return false, fmt.Sprintf(
			"Verify command failed: %s\nError: %s",
			cmd,
			err.Error(),
		)
	}
	if res.ExitCode == 0 && !res.TimedOut {
		return true, ""
	}
	return false, formatCommandFailure(cmd, res)
}

func (a *ralphLoopAgent) appendFeedback(
	ctx context.Context,
	base *agent.Invocation,
	iter int,
	feedback string,
) error {
	if base == nil || base.Session == nil {
		return nil
	}
	msg := strings.TrimSpace(feedback)
	if msg == "" {
		msg = "Ralph loop: verification failed, please continue."
	}
	if iter > 0 {
		msg = fmt.Sprintf(
			"Ralph iteration %d/%d:\n%s",
			iter,
			a.cfg.MaxIterations,
			msg,
		)
	}

	evt := event.NewResponseEvent(
		base.InvocationID,
		authorUser,
		&model.Response{
			Done: false,
			Choices: []model.Choice{{
				Index: 0,
				Message: model.Message{
					Role:    model.RoleUser,
					Content: msg,
				},
			}},
		},
	)
	agent.InjectIntoEvent(base, evt)

	attached, err := appender.Invoke(ctx, base, evt)
	if err != nil {
		return err
	}
	if !attached {
		return errors.New("ralph loop: session appender not attached")
	}
	return nil
}

func (a *ralphLoopAgent) emitStopError(
	ctx context.Context,
	base *agent.Invocation,
	out chan<- *event.Event,
	message string,
) {
	if base == nil {
		return
	}
	evt := event.NewErrorEvent(
		base.InvocationID,
		base.AgentName,
		agent.ErrorTypeStopAgentError,
		message,
	)
	_ = agent.EmitEvent(ctx, base, out, evt)
}

func validateRalphLoopConfig(cfg RalphLoopConfig) error {
	hasPromise := strings.TrimSpace(cfg.CompletionPromise) != ""
	hasCmd := strings.TrimSpace(cfg.VerifyCommand) != ""
	hasVerifiers := hasNonNilVerifier(cfg.Verifiers)
	if !hasPromise && !hasCmd && !hasVerifiers {
		return errRalphLoopMissingStopCondition
	}
	return nil
}

func normalizeRalphLoopConfig(cfg RalphLoopConfig) RalphLoopConfig {
	if cfg.MaxIterations <= 0 {
		cfg.MaxIterations = defaultRalphMaxIterations
	}
	if strings.TrimSpace(cfg.PromiseTagOpen) == "" {
		cfg.PromiseTagOpen = defaultPromiseTagOpen
	}
	if strings.TrimSpace(cfg.PromiseTagClose) == "" {
		cfg.PromiseTagClose = defaultPromiseTagClose
	}
	return cfg
}

func firstTagText(
	evt *event.Event,
	open string,
	closeTag string,
) (string, bool) {
	if evt == nil {
		return "", false
	}
	for _, choice := range evt.Choices {
		msg := choice.Message
		if msg.Role != model.RoleAssistant {
			continue
		}
		text := msg.Content
		if text == "" && len(msg.ContentParts) > 0 {
			text = textFromContentParts(msg.ContentParts)
		}
		tagText, ok := firstTagTextInString(text, open, closeTag)
		if ok {
			return tagText, true
		}
	}
	return "", false
}

func firstTagTextInString(
	text string,
	open string,
	closeTag string,
) (string, bool) {
	if open == "" || closeTag == "" {
		return "", false
	}
	start := strings.Index(text, open)
	if start < 0 {
		return "", false
	}
	start += len(open)
	end := strings.Index(text[start:], closeTag)
	if end < 0 {
		return "", false
	}
	return text[start : start+end], true
}

func textFromContentParts(parts []model.ContentPart) string {
	var b strings.Builder
	for _, part := range parts {
		if part.Type != model.ContentTypeText || part.Text == nil {
			continue
		}
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		b.WriteString(*part.Text)
	}
	return b.String()
}

func normalizePromiseText(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

func formatCommandFailure(
	cmd string,
	res RalphLoopCommandResult,
) string {
	var b strings.Builder
	b.WriteString("Verify command failed:\n")
	b.WriteString(cmd)
	b.WriteString("\nExit code: ")
	b.WriteString(fmt.Sprintf("%d", res.ExitCode))
	if res.TimedOut {
		b.WriteString(" (timed out)")
	}
	if strings.TrimSpace(res.Stdout) != "" {
		b.WriteString("\n\nStdout:\n")
		b.WriteString(res.Stdout)
	}
	if strings.TrimSpace(res.Stderr) != "" {
		b.WriteString("\n\nStderr:\n")
		b.WriteString(res.Stderr)
	}
	return strings.TrimSpace(b.String())
}

type hostRalphLoopRunner struct{}

func (hostRalphLoopRunner) Run(
	ctx context.Context,
	spec RalphLoopCommandSpec,
) (RalphLoopCommandResult, error) {
	if strings.TrimSpace(spec.Command) == "" {
		return RalphLoopCommandResult{}, errors.New("empty command")
	}
	runCtx := ctx
	var cancel context.CancelFunc
	if spec.Timeout > 0 {
		runCtx, cancel = context.WithTimeout(ctx, spec.Timeout)
		defer cancel()
	}

	//nolint:gosec // VerifyCommand is explicitly provided by the caller.
	cmd := exec.CommandContext(
		runCtx,
		ralphVerifyShell,
		ralphVerifyFlag,
		spec.Command,
	)
	cmd.Dir = strings.TrimSpace(spec.WorkDir)
	cmd.Env = mergeEnv(spec.Env)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	started := time.Now()
	err := cmd.Run()
	dur := time.Since(started)

	res := RalphLoopCommandResult{
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
		ExitCode: 0,
		Duration: dur,
		TimedOut: runCtx.Err() == context.DeadlineExceeded,
	}
	if err == nil {
		return res, nil
	}

	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		res.ExitCode = exitErr.ExitCode()
		return res, nil
	}
	return RalphLoopCommandResult{}, err
}

func mergeEnv(overrides map[string]string) []string {
	env := os.Environ()
	if len(overrides) == 0 {
		return env
	}
	merged := make(map[string]string, len(env)+len(overrides))
	for _, kv := range env {
		key, val, ok := strings.Cut(kv, "=")
		if !ok || key == "" {
			continue
		}
		merged[key] = val
	}
	for k, v := range overrides {
		merged[k] = v
	}
	out := make([]string, 0, len(merged))
	for k, v := range merged {
		out = append(out, k+"="+v)
	}
	return out
}
