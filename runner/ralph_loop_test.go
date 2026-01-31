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
	"context"
	"errors"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	sessioninmemory "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

type scriptedAgent struct {
	name    string
	outputs []string
	calls   int32
}

func (a *scriptedAgent) Info() agent.Info {
	return agent.Info{Name: a.name}
}

func (a *scriptedAgent) SubAgents() []agent.Agent { return nil }

func (a *scriptedAgent) FindSubAgent(string) agent.Agent { return nil }

func (a *scriptedAgent) Tools() []tool.Tool { return nil }

func (a *scriptedAgent) Run(
	ctx context.Context,
	inv *agent.Invocation,
) (<-chan *event.Event, error) {
	ch := make(chan *event.Event, 1)
	go func() {
		defer close(ch)
		idx := int(atomic.AddInt32(&a.calls, 1) - 1)
		content := ""
		if idx >= 0 && idx < len(a.outputs) {
			content = a.outputs[idx]
		}
		evt := event.NewResponseEvent(
			inv.InvocationID,
			a.name,
			&model.Response{
				Done: true,
				Choices: []model.Choice{{
					Index: 0,
					Message: model.Message{
						Role:    model.RoleAssistant,
						Content: content,
					},
				}},
			},
		)
		agent.InjectIntoEvent(inv, evt)
		_ = event.EmitEvent(ctx, ch, evt)
	}()
	return ch, nil
}

type scriptedVerifyRunner struct {
	results []RalphLoopCommandResult
	calls   int32
}

func (r *scriptedVerifyRunner) Run(
	_ context.Context,
	_ RalphLoopCommandSpec,
) (RalphLoopCommandResult, error) {
	idx := int(atomic.AddInt32(&r.calls, 1) - 1)
	if idx >= 0 && idx < len(r.results) {
		return r.results[idx], nil
	}
	return RalphLoopCommandResult{}, nil
}

type scriptedVerifier struct {
	results []VerifyResult
	err     error
	calls   int32
}

func (v *scriptedVerifier) Verify(
	_ context.Context,
	_ *agent.Invocation,
	_ *event.Event,
) (VerifyResult, error) {
	atomic.AddInt32(&v.calls, 1)
	if v.err != nil {
		return VerifyResult{}, v.err
	}
	idx := int(atomic.LoadInt32(&v.calls) - 1)
	if idx >= 0 && idx < len(v.results) {
		return v.results[idx], nil
	}
	return VerifyResult{}, nil
}

func TestRalphLoop_PromiseStopsLoop(t *testing.T) {
	svc := sessioninmemory.NewSessionService()
	base := &scriptedAgent{
		name: "worker",
		outputs: []string{
			"not done",
			"<promise>DONE</promise>",
		},
	}
	r := NewRunner(
		"app",
		base,
		WithSessionService(svc),
		WithRalphLoop(RalphLoopConfig{
			MaxIterations:     5,
			CompletionPromise: "DONE",
		}),
	)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	ch, err := r.Run(
		ctx,
		"u",
		"s",
		model.Message{Role: model.RoleUser, Content: "task"},
	)
	require.NoError(t, err)

	assistant := 0
	completion := 0
	for e := range ch {
		if e == nil || e.Response == nil {
			continue
		}
		if e.IsRunnerCompletion() {
			completion++
			continue
		}
		if len(e.Choices) > 0 &&
			e.Choices[0].Message.Role == model.RoleAssistant {
			assistant++
		}
	}

	require.Equal(t, 2, assistant)
	require.Equal(t, 1, completion)
	require.Equal(t, int32(2), atomic.LoadInt32(&base.calls))

	sess := mustGetSession(t, svc, "app", "u", "s")
	require.Equal(t, 2, countUserMessages(sess))
}

func TestRalphLoop_MaxIterationsEmitsStopError(t *testing.T) {
	svc := sessioninmemory.NewSessionService()
	base := &scriptedAgent{
		name:    "worker",
		outputs: []string{"no", "no"},
	}
	r := NewRunner(
		"app",
		base,
		WithSessionService(svc),
		WithRalphLoop(RalphLoopConfig{
			MaxIterations:     2,
			CompletionPromise: "DONE",
		}),
	)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	ch, err := r.Run(
		ctx,
		"u",
		"s",
		model.Message{Role: model.RoleUser, Content: "task"},
	)
	require.NoError(t, err)

	stopErr := 0
	for e := range ch {
		if e == nil || e.Error == nil {
			continue
		}
		if e.Error.Type == agent.ErrorTypeStopAgentError {
			stopErr++
		}
	}
	require.Equal(t, 1, stopErr)
	require.Equal(t, int32(2), atomic.LoadInt32(&base.calls))
}

func TestRalphLoop_VerifyCommandBlocksCompletion(t *testing.T) {
	svc := sessioninmemory.NewSessionService()
	verify := &scriptedVerifyRunner{
		results: []RalphLoopCommandResult{
			{ExitCode: 1, Stderr: "fail"},
			{ExitCode: 0},
		},
	}
	base := &scriptedAgent{
		name: "worker",
		outputs: []string{
			"<promise>DONE</promise>",
			"<promise>DONE</promise>",
		},
	}
	r := NewRunner(
		"app",
		base,
		WithSessionService(svc),
		WithRalphLoop(RalphLoopConfig{
			MaxIterations:     5,
			CompletionPromise: "DONE",
			VerifyCommand:     "go test ./...",
			VerifyRunner:      verify,
		}),
	)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	ch, err := r.Run(
		ctx,
		"u",
		"s",
		model.Message{Role: model.RoleUser, Content: "task"},
	)
	require.NoError(t, err)

	completion := 0
	for e := range ch {
		if e != nil && e.IsRunnerCompletion() {
			completion++
		}
	}

	require.Equal(t, 1, completion)
	require.Equal(t, int32(2), atomic.LoadInt32(&base.calls))
	require.Equal(t, int32(2), atomic.LoadInt32(&verify.calls))

	sess := mustGetSession(t, svc, "app", "u", "s")
	require.Equal(t, 2, countUserMessages(sess))
}

func TestRalphLoop_VerifierStopsLoop(t *testing.T) {
	svc := sessioninmemory.NewSessionService()
	verify := &scriptedVerifier{
		results: []VerifyResult{
			{Passed: false, Feedback: "keep going"},
			{Passed: true},
		},
	}
	base := &scriptedAgent{
		name:    "worker",
		outputs: []string{"x", "y"},
	}
	r := NewRunner(
		"app",
		base,
		WithSessionService(svc),
		WithRalphLoop(RalphLoopConfig{
			MaxIterations: 5,
			Verifiers:     []Verifier{verify},
		}),
	)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	ch, err := r.Run(
		ctx,
		"u",
		"s",
		model.Message{Role: model.RoleUser, Content: "task"},
	)
	require.NoError(t, err)

	completion := 0
	for e := range ch {
		if e != nil && e.IsRunnerCompletion() {
			completion++
		}
	}
	require.Equal(t, 1, completion)
	require.Equal(t, int32(2), atomic.LoadInt32(&base.calls))
	require.Equal(t, int32(2), atomic.LoadInt32(&verify.calls))
}

func TestRalphLoop_VerifierErrorStops(t *testing.T) {
	svc := sessioninmemory.NewSessionService()
	verify := &scriptedVerifier{err: errors.New("boom")}
	base := &scriptedAgent{
		name:    "worker",
		outputs: []string{"x"},
	}
	r := NewRunner(
		"app",
		base,
		WithSessionService(svc),
		WithRalphLoop(RalphLoopConfig{
			MaxIterations: 2,
			Verifiers:     []Verifier{verify},
		}),
	)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	ch, err := r.Run(
		ctx,
		"u",
		"s",
		model.Message{Role: model.RoleUser, Content: "task"},
	)
	require.NoError(t, err)

	stopErr := 0
	for e := range ch {
		if e == nil || e.Error == nil {
			continue
		}
		if e.Error.Type == agent.ErrorTypeStopAgentError {
			stopErr++
		}
	}
	require.Equal(t, 1, stopErr)
}

func TestMergeEnv_Overrides(t *testing.T) {
	env := mergeEnv(map[string]string{"TRPC_AGENT_FOO": "bar"})
	found := false
	for _, kv := range env {
		if kv == "TRPC_AGENT_FOO=bar" {
			found = true
			break
		}
	}
	require.True(t, found)
}

func TestValidateRalphLoopConfig_RequiresStopCondition(t *testing.T) {
	err := validateRalphLoopConfig(RalphLoopConfig{})
	require.ErrorIs(t, err, errRalphLoopMissingStopCondition)

	err = validateRalphLoopConfig(RalphLoopConfig{Verifiers: []Verifier{nil}})
	require.ErrorIs(t, err, errRalphLoopMissingStopCondition)

	err = validateRalphLoopConfig(RalphLoopConfig{
		Verifiers: []Verifier{&scriptedVerifier{}},
	})
	require.NoError(t, err)
}

func TestNormalizeRalphLoopConfig_DefaultsApplied(t *testing.T) {
	cfg := normalizeRalphLoopConfig(RalphLoopConfig{})
	require.Equal(t, defaultRalphMaxIterations, cfg.MaxIterations)
	require.Equal(t, defaultPromiseTagOpen, cfg.PromiseTagOpen)
	require.Equal(t, defaultPromiseTagClose, cfg.PromiseTagClose)
}

func TestWrapAgentsWithRalphLoop_NoopOnEmptyMap(t *testing.T) {
	wrapAgentsWithRalphLoop(
		nil,
		RalphLoopConfig{CompletionPromise: "DONE"},
	)

	agents := map[string]agent.Agent{
		"nil": nil,
		"a":   &infoAgent{info: agent.Info{Name: "a"}},
	}
	wrapAgentsWithRalphLoop(agents, RalphLoopConfig{CompletionPromise: "DONE"})
	_, ok := agents["a"].(*ralphLoopAgent)
	require.True(t, ok)
	require.Nil(t, agents["nil"])
}

type toolDecl struct {
	name string
}

func (t toolDecl) Declaration() *tool.Declaration {
	return &tool.Declaration{Name: t.name}
}

type infoAgent struct {
	info       agent.Info
	tools      []tool.Tool
	subAgents  []agent.Agent
	subByName  map[string]agent.Agent
	runErr     error
	runContent string
}

func (a *infoAgent) Info() agent.Info { return a.info }

func (a *infoAgent) Tools() []tool.Tool { return a.tools }

func (a *infoAgent) SubAgents() []agent.Agent { return a.subAgents }

func (a *infoAgent) FindSubAgent(name string) agent.Agent {
	if a.subByName == nil {
		return nil
	}
	return a.subByName[name]
}

func (a *infoAgent) Run(
	ctx context.Context,
	inv *agent.Invocation,
) (<-chan *event.Event, error) {
	if a.runErr != nil {
		return nil, a.runErr
	}
	ch := make(chan *event.Event, 1)
	go func() {
		defer close(ch)
		evt := event.NewResponseEvent(
			inv.InvocationID,
			a.info.Name,
			&model.Response{
				Done: true,
				Choices: []model.Choice{{
					Index: 0,
					Message: model.Message{
						Role:    model.RoleAssistant,
						Content: a.runContent,
					},
				}},
			},
		)
		agent.InjectIntoEvent(inv, evt)
		_ = event.EmitEvent(ctx, ch, evt)
	}()
	return ch, nil
}

func TestWrapAgentWithRalphLoop_NilAndAlreadyWrapped(t *testing.T) {
	cfg := RalphLoopConfig{CompletionPromise: "DONE"}
	require.Nil(t, wrapAgentWithRalphLoop(nil, cfg))

	ag := &infoAgent{info: agent.Info{Name: "a"}}
	wrapped := wrapAgentWithRalphLoop(ag, cfg)
	_, ok := wrapped.(*ralphLoopAgent)
	require.True(t, ok)

	wrappedAgain := wrapAgentWithRalphLoop(wrapped, cfg)
	require.Same(t, wrapped, wrappedAgain)
}

func TestRalphLoopAgent_InfoAndDelegation(t *testing.T) {
	sub := &infoAgent{info: agent.Info{Name: "sub"}}
	ag := &infoAgent{
		info: agent.Info{Name: "a"},
		tools: []tool.Tool{
			toolDecl{name: "t"},
		},
		subAgents: []agent.Agent{sub},
		subByName: map[string]agent.Agent{"sub": sub},
	}

	wrapped := wrapAgentWithRalphLoop(
		ag,
		RalphLoopConfig{CompletionPromise: "DONE"},
	)
	rl, ok := wrapped.(*ralphLoopAgent)
	require.True(t, ok)

	info := rl.Info()
	require.Equal(t, "a", info.Name)
	require.Equal(t, "Ralph loop enabled", info.Description)

	require.Len(t, rl.Tools(), 1)
	require.Len(t, rl.SubAgents(), 1)
	require.Same(t, sub, rl.FindSubAgent("sub"))
	require.Nil(t, rl.FindSubAgent("missing"))
}

func TestRalphLoopAgent_DelegationNilSafety(t *testing.T) {
	var nilAgent *ralphLoopAgent
	require.Equal(t, agent.Info{}, nilAgent.Info())
	require.Nil(t, nilAgent.Tools())
	require.Nil(t, nilAgent.SubAgents())
	require.Nil(t, nilAgent.FindSubAgent("x"))

	innerNil := &ralphLoopAgent{}
	require.Equal(t, agent.Info{}, innerNil.Info())
	require.Nil(t, innerNil.Tools())
	require.Nil(t, innerNil.SubAgents())
	require.Nil(t, innerNil.FindSubAgent("x"))
}

func TestRalphLoopAgent_Info_AppendsDescription(t *testing.T) {
	ag := &infoAgent{
		info: agent.Info{
			Name:        "a",
			Description: "base",
		},
	}
	wrapped := wrapAgentWithRalphLoop(
		ag,
		RalphLoopConfig{CompletionPromise: "DONE"},
	)
	info := wrapped.Info()
	require.Equal(t, "base (ralph loop)", info.Description)
}

func TestFirstTagTextInString_EdgeCases(t *testing.T) {
	_, ok := firstTagTextInString("x", "", "</promise>")
	require.False(t, ok)

	_, ok = firstTagTextInString("x", "<promise>", "")
	require.False(t, ok)

	_, ok = firstTagTextInString("x", "<promise>", "</promise>")
	require.False(t, ok)

	_, ok = firstTagTextInString("<promise>", "<promise>", "</promise>")
	require.False(t, ok)
}

func TestFirstTagText_ContentParts(t *testing.T) {
	partText := "<promise>DONE</promise>"
	evt := &event.Event{
		Response: &model.Response{
			Choices: []model.Choice{{
				Message: model.Message{
					Role: model.RoleAssistant,
					ContentParts: []model.ContentPart{
						{
							Type: model.ContentTypeText,
							Text: &partText,
						},
					},
				},
			}},
		},
	}
	got, ok := firstTagText(evt, "<promise>", "</promise>")
	require.True(t, ok)
	require.Equal(t, "DONE", got)
}

func TestFirstTagText_SkipsNonAssistant(t *testing.T) {
	evt := &event.Event{
		Response: &model.Response{
			Choices: []model.Choice{
				{Message: model.Message{Role: model.RoleUser, Content: "x"}},
				{
					Message: model.Message{
						Role:    model.RoleAssistant,
						Content: "<promise>DONE</promise>",
					},
				},
			},
		},
	}
	got, ok := firstTagText(evt, "<promise>", "</promise>")
	require.True(t, ok)
	require.Equal(t, "DONE", got)
}

func TestTextFromContentParts_IgnoresNonText(t *testing.T) {
	a := "a"
	b := "b"
	out := textFromContentParts([]model.ContentPart{
		{Type: model.ContentTypeText},
		{Type: model.ContentTypeImage},
		{Type: model.ContentTypeText, Text: &a},
		{Type: model.ContentTypeText, Text: &b},
	})
	require.Equal(t, "a\nb", out)
}

func TestNormalizePromiseText_Whitespace(t *testing.T) {
	require.Equal(t, "a b", normalizePromiseText(" a \n b\t"))
	require.Equal(t, "", normalizePromiseText(" \n\t"))
}

func TestFormatCommandFailure_IncludesOutput(t *testing.T) {
	msg := formatCommandFailure("cmd", RalphLoopCommandResult{
		ExitCode: 7,
		Stdout:   "out",
		Stderr:   "err",
		TimedOut: true,
	})
	require.Contains(t, msg, "Exit code: 7")
	require.Contains(t, msg, "Stdout:")
	require.Contains(t, msg, "out")
	require.Contains(t, msg, "Stderr:")
	require.Contains(t, msg, "err")
	require.Contains(t, msg, "timed out")
}

func TestHostRalphLoopRunner_Run(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("host runner uses bash")
	}

	r := hostRalphLoopRunner{}
	okRes, err := r.Run(context.Background(), RalphLoopCommandSpec{
		Command: "echo hello",
	})
	require.NoError(t, err)
	require.Equal(t, 0, okRes.ExitCode)
	require.Contains(t, okRes.Stdout, "hello")

	failRes, err := r.Run(context.Background(), RalphLoopCommandSpec{
		Command: "exit 7",
	})
	require.NoError(t, err)
	require.Equal(t, 7, failRes.ExitCode)

	_, err = r.Run(context.Background(), RalphLoopCommandSpec{})
	require.Error(t, err)
}

func TestHostRalphLoopRunner_RunTimeout(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("host runner uses bash")
	}

	r := hostRalphLoopRunner{}
	res, err := r.Run(context.Background(), RalphLoopCommandSpec{
		Command: "sleep 2",
		Timeout: 50 * time.Millisecond,
	})
	require.NoError(t, err)
	require.True(t, res.TimedOut)
}

func TestRalphLoop_RunRejectsNilInvocation(t *testing.T) {
	ag := &ralphLoopAgent{
		inner: &infoAgent{info: agent.Info{Name: "a"}},
		cfg: normalizeRalphLoopConfig(
			RalphLoopConfig{CompletionPromise: "DONE"},
		),
	}
	_, err := ag.Run(context.Background(), nil)
	require.Error(t, err)
}

func TestRalphLoop_RunRejectsNilInner(t *testing.T) {
	ag := &ralphLoopAgent{
		cfg: normalizeRalphLoopConfig(
			RalphLoopConfig{CompletionPromise: "DONE"},
		),
	}
	_, err := ag.Run(context.Background(), &agent.Invocation{})
	require.Error(t, err)
}

func TestRalphLoop_InnerRunErrorStops(t *testing.T) {
	svc := sessioninmemory.NewSessionService()
	base := &infoAgent{
		info:   agent.Info{Name: "worker"},
		runErr: errors.New("boom"),
	}
	r := NewRunner(
		"app",
		base,
		WithSessionService(svc),
		WithRalphLoop(RalphLoopConfig{
			MaxIterations:     2,
			CompletionPromise: "DONE",
		}),
	)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	ch, err := r.Run(
		ctx,
		"u",
		"s",
		model.Message{Role: model.RoleUser, Content: "task"},
	)
	require.NoError(t, err)

	stopErr := 0
	for e := range ch {
		if e == nil || e.Error == nil {
			continue
		}
		if e.Error.Type == agent.ErrorTypeStopAgentError {
			stopErr++
		}
	}
	require.Equal(t, 1, stopErr)
}

func mustGetSession(
	t *testing.T,
	svc session.Service,
	app string,
	user string,
	sid string,
) *session.Session {
	t.Helper()
	sess, err := svc.GetSession(context.Background(), session.Key{
		AppName:   app,
		UserID:    user,
		SessionID: sid,
	})
	require.NoError(t, err)
	require.NotNil(t, sess)
	return sess
}

func countUserMessages(sess *session.Session) int {
	if sess == nil {
		return 0
	}
	cnt := 0
	for _, evt := range sess.Events {
		e := evt
		if e.IsUserMessage() {
			cnt++
		}
	}
	return cnt
}
