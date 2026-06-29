//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package skill

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	itelemetry "trpc.group/trpc-go/trpc-agent-go/internal/telemetry"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/skill"
	semconvtrace "trpc.group/trpc-go/trpc-agent-go/telemetry/semconv/trace"
	telemetrytrace "trpc.group/trpc-go/trpc-agent-go/telemetry/trace"
)

type mockRepo struct {
	ok   map[string]bool
	sums []skill.Summary
}

func (m *mockRepo) Summaries() []skill.Summary { return m.sums }
func (m *mockRepo) Get(name string) (*skill.Skill, error) {
	if m.ok[name] {
		return &skill.Skill{Summary: skill.Summary{Name: name}}, nil
	}
	return nil, errors.New("not found")
}
func (m *mockRepo) Path(name string) (string, error) { return "", nil }

type contextPathRepo struct {
	name        string
	defaultDir  string
	contextDir  string
	contextBody string
}

func (r *contextPathRepo) Summaries() []skill.Summary {
	return []skill.Summary{{Name: r.name}}
}

func (r *contextPathRepo) Get(name string) (*skill.Skill, error) {
	if name != r.name {
		return nil, errors.New("not found")
	}
	return &skill.Skill{Summary: skill.Summary{Name: name}}, nil
}

func (r *contextPathRepo) Path(name string) (string, error) {
	if name != r.name {
		return "", errors.New("not found")
	}
	return r.defaultDir, nil
}

func (r *contextPathRepo) SummariesForContext(context.Context) []skill.Summary {
	return r.Summaries()
}

func (r *contextPathRepo) GetForContext(_ context.Context, name string) (*skill.Skill, error) {
	if name != r.name {
		return nil, errors.New("not found")
	}
	return &skill.Skill{
		Summary: skill.Summary{Name: name},
		Body:    r.contextBody,
	}, nil
}

func (r *contextPathRepo) PathForContext(_ context.Context, name string) (string, error) {
	if name != r.name {
		return "", errors.New("not found")
	}
	return r.contextDir, nil
}

func TestLoadTool_Call_ValidatesAndDelta(t *testing.T) {
	repo := &mockRepo{ok: map[string]bool{"calc": true}}
	lt := NewLoadTool(repo)
	inv := &agent.Invocation{
		AgentName: "tester",
		Session:   &session.Session{State: session.StateMap{}},
	}

	// include_all_docs path
	args := loadInput{Skill: "calc", IncludeAllDocs: true}
	b, _ := json.Marshal(args)
	res, err := lt.Call(context.Background(), b)
	require.NoError(t, err)
	require.Equal(t, "loaded: calc", res)

	delta := lt.StateDeltaForInvocation(inv, "call-1", b, nil)
	require.Equal(t, []byte("1"), delta[skill.LoadedKey("tester", "calc")])
	require.Equal(t, []byte("*"), delta[skill.DocsKey("tester", "calc")])
	require.Equal(
		t,
		`["calc"]`,
		string(delta[skill.LoadedOrderKey("tester")]),
	)
	inv.Session.State[skill.LoadedOrderKey("tester")] =
		delta[skill.LoadedOrderKey("tester")]

	// docs array path
	args = loadInput{Skill: "calc", Docs: []string{"A.md"}}
	b, _ = json.Marshal(args)
	delta = lt.StateDeltaForInvocation(inv, "call-2", b, nil)
	require.NotNil(t, delta[skill.DocsKey("tester", "calc")])
	require.Equal(
		t,
		`["calc"]`,
		string(delta[skill.LoadedOrderKey("tester")]),
	)

	// only loaded, no docs selection
	args = loadInput{Skill: "calc"}
	b, _ = json.Marshal(args)
	delta = lt.StateDeltaForInvocation(inv, "call-3", b, nil)
	require.Equal(t, []byte("1"), delta[skill.LoadedKey("tester", "calc")])
	_, ok := delta[skill.DocsKey("tester", "calc")]
	require.False(t, ok)
	require.Equal(
		t,
		`["calc"]`,
		string(delta[skill.LoadedOrderKey("tester")]),
	)
}

func TestLoadTool_StateDelta_TrimsSkillName(t *testing.T) {
	repo := &mockRepo{ok: map[string]bool{"calc": true}}
	lt := NewLoadTool(repo)
	inv := &agent.Invocation{
		AgentName: "tester",
		Session:   &session.Session{State: session.StateMap{}},
	}

	delta := lt.StateDeltaForInvocation(
		inv,
		"call-1",
		[]byte(`{"skill":" calc ","docs":["A.md"]}`),
		nil,
	)

	require.Equal(t, []byte("1"), delta[skill.LoadedKey("tester", "calc")])
	require.Equal(t, []byte(`["A.md"]`), delta[skill.DocsKey("tester", "calc")])
	require.Equal(t, `["calc"]`, string(delta[skill.LoadedOrderKey("tester")]))
}

func TestLoadTool_Call_Errors(t *testing.T) {
	lt := NewLoadTool(&mockRepo{ok: map[string]bool{}})

	// missing skill
	_, err := lt.Call(context.Background(), []byte(`{"skill":""}`))
	require.Error(t, err)

	// unknown skill
	_, err = lt.Call(context.Background(), []byte(`{"skill":"x"}`))
	require.Error(t, err)
}

func TestLoadTool_Declaration(t *testing.T) {
	lt := NewLoadTool(nil)
	d := lt.Declaration()
	require.Equal(t, "skill_load", d.Name)
	require.Equal(t, defaultLoadToolDescription, d.Description)
	require.NotNil(t, d.InputSchema)
	require.NotNil(t, d.OutputSchema)
}

func TestLoadTool_DeclarationOverride(t *testing.T) {
	const description = "Always load the matching skill first."

	lt := NewLoadToolWithOptions(
		nil,
		WithLoadToolDescription(description),
	)
	d := lt.Declaration()
	require.Equal(t, description, d.Description)
}

func TestLoadTool_StateDelta_InvalidArgs(t *testing.T) {
	lt := NewLoadTool(nil)
	// invalid json should return nil delta
	delta := lt.StateDelta("call-err", []byte("{"), nil)
	require.Nil(t, delta)
}

func TestLoadTool_Call_NoRepoSkipsValidation(t *testing.T) {
	lt := NewLoadTool(nil)
	// unknown skill is accepted when repo is nil
	out, err := lt.Call(context.Background(), []byte(
		`{"skill":"x"}`,
	))
	require.NoError(t, err)
	require.Equal(t, "loaded: x", out)
}

func TestAppendLoadedOrderStateDelta(t *testing.T) {
	delta := appendLoadedOrderStateDelta(nil, "tester", nil, "calc")
	require.Equal(
		t,
		`["calc"]`,
		string(delta[skill.LoadedOrderKey("tester")]),
	)

	inv := &agent.Invocation{
		AgentName: "tester",
		Session: &session.Session{
			State: session.StateMap{
				skill.LoadedOrderKey("tester"): []byte(`["a","b"]`),
			},
		},
	}
	delta = appendLoadedOrderStateDelta(
		inv,
		"tester",
		map[string][]byte{},
		"a",
	)
	require.Equal(
		t,
		`["b","a"]`,
		string(delta[skill.LoadedOrderKey("tester")]),
	)

	delta = appendLoadedOrderStateDelta(inv, "tester", nil, " ")
	require.Nil(t, delta)
}

func TestSkillNameEnum_SortsAndSkipsEmpty(t *testing.T) {
	repo := &mockRepo{
		ok: map[string]bool{},
		sums: []skill.Summary{
			{Name: "b"},
			{Name: ""},
			{Name: "a"},
		},
	}
	got := skillNameEnum(repo)
	require.Equal(t, []any{"a", "b"}, got)
}

func TestSkillNameEnum_TooManyValuesReturnsNil(t *testing.T) {
	repo := &mockRepo{
		ok:   map[string]bool{},
		sums: make([]skill.Summary, maxSkillEnumValues+1),
	}
	require.Nil(t, skillNameEnum(repo))
}

func TestSkillNameEnum_AllEmptyNamesReturnsNil(t *testing.T) {
	repo := &mockRepo{
		ok: map[string]bool{},
		sums: []skill.Summary{
			{Name: ""},
			{Name: ""},
		},
	}
	require.Nil(t, skillNameEnum(repo))
}

func TestLoadTool_Call_ContextAwareRepoHonorsFilter(t *testing.T) {
	base := &mockRepo{
		ok: map[string]bool{
			"alpha": true,
			"beta":  true,
		},
		sums: []skill.Summary{
			{Name: "alpha"},
			{Name: "beta"},
		},
	}
	repo := skill.NewFilteredRepository(
		base,
		func(ctx context.Context, summary skill.Summary) bool {
			userID, _ := agent.GetRuntimeStateValueFromContext[string](
				ctx,
				"user_id",
			)
			return userID == "user-a" && summary.Name == "alpha"
		},
	)
	lt := NewLoadTool(repo)
	ctx := agent.NewInvocationContext(
		context.Background(),
		agent.NewInvocation(agent.WithInvocationRunOptions(agent.RunOptions{
			RuntimeState: map[string]any{"user_id": "user-a"},
		})),
	)

	_, err := lt.Call(ctx, []byte(`{"skill":"alpha"}`))
	require.NoError(t, err)

	_, err = lt.Call(ctx, []byte(`{"skill":"beta"}`))
	require.ErrorContains(t, err, "unknown skill: beta")
}

func TestLoadTool_Call_EmitsInvokeSkillChildSpan(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	originalProvider := telemetrytrace.TracerProvider
	originalTracer := telemetrytrace.Tracer
	telemetrytrace.TracerProvider = provider
	telemetrytrace.Tracer = provider.Tracer("skill-load-test")
	t.Cleanup(func() {
		_ = provider.Shutdown(context.Background())
		telemetrytrace.TracerProvider = originalProvider
		telemetrytrace.Tracer = originalTracer
	})

	root := t.TempDir()
	skillDir := filepath.Join(root, "code_review")
	require.NoError(t, os.MkdirAll(skillDir, 0o755))
	skillContent := `---
name: code_review
description: Review code changes.
---
Use careful code review practices.`
	require.NoError(t, os.WriteFile(filepath.Join(skillDir, skill.SkillFile), []byte(skillContent), 0o644))
	repo, err := skill.NewFSRepository(root)
	require.NoError(t, err)
	lt := NewLoadTool(repo)
	inv := &agent.Invocation{
		AgentName: "review-agent",
		Session: &session.Session{
			ID:      "sess-1",
			AppName: "app-1",
			UserID:  "user-1",
		},
	}
	var ctx context.Context = agent.NewInvocationContext(context.Background(), inv)
	ctx, parent := telemetrytrace.Tracer.Start(ctx, itelemetry.NewExecuteToolSpanName("skill_load"))
	out, err := lt.Call(ctx, []byte(`{"skill":"code_review"}`))
	parent.End()
	require.NoError(t, err)
	require.Equal(t, "loaded: code_review", out)

	spans := recorder.Ended()
	require.Len(t, spans, 2)
	var invokeSpan, parentSpan sdktrace.ReadOnlySpan
	for _, sp := range spans {
		switch sp.Name() {
		case itelemetry.NewInvokeSkillSpanName("code_review"):
			invokeSpan = sp
		case itelemetry.NewExecuteToolSpanName("skill_load"):
			parentSpan = sp
		}
	}
	require.NotNil(t, parentSpan)
	require.NotNil(t, invokeSpan)
	require.Equal(t, parentSpan.SpanContext().SpanID(), invokeSpan.Parent().SpanID())
	requireSpanAttr(t, invokeSpan.Attributes(), semconvtrace.KeyGenAIOperationName, itelemetry.OperationInvokeSkill)
	requireSpanAttr(t, invokeSpan.Attributes(), semconvtrace.KeyGenAISkillName, "code_review")
	requireSpanAttr(t, invokeSpan.Attributes(), semconvtrace.KeyGenAIAgentID, "review-agent")
	requireSpanAttr(t, invokeSpan.Attributes(), semconvtrace.KeyGenAIUserID, "user-1")
	require.Empty(t, invokeSpan.Events())
	requestDetail := spanStringAttr(t, invokeSpan.Attributes(), semconvtrace.KeyGenAIInvokeSkillRequest)
	require.Contains(t, requestDetail, `"safe_path":"code_review/SKILL.md"`)
	require.NotContains(t, requestDetail, root)
	responseDetail := spanStringAttr(t, invokeSpan.Attributes(), semconvtrace.KeyGenAIInvokeSkillResponse)
	require.Contains(t, responseDetail, `"content_sha256"`)
	require.Contains(t, responseDetail, "Use careful code review practices.")
}

func TestLoadTool_Call_InvokeSkillUsesContextAwarePath(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	originalProvider := telemetrytrace.TracerProvider
	originalTracer := telemetrytrace.Tracer
	telemetrytrace.TracerProvider = provider
	telemetrytrace.Tracer = provider.Tracer("skill-load-test")
	t.Cleanup(func() {
		_ = provider.Shutdown(context.Background())
		telemetrytrace.TracerProvider = originalProvider
		telemetrytrace.Tracer = originalTracer
	})

	root := t.TempDir()
	defaultDir := filepath.Join(root, "default", "context_skill")
	contextDir := filepath.Join(root, "context", "context_skill")
	require.NoError(t, os.MkdirAll(defaultDir, 0o755))
	require.NoError(t, os.MkdirAll(contextDir, 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(defaultDir, skill.SkillFile),
		[]byte("default skill body"),
		0o644,
	))
	require.NoError(t, os.WriteFile(
		filepath.Join(contextDir, skill.SkillFile),
		[]byte("context skill body"),
		0o644,
	))
	repo := &contextPathRepo{
		name:        "context_skill",
		defaultDir:  defaultDir,
		contextDir:  contextDir,
		contextBody: "context skill body",
	}
	lt := NewLoadTool(repo)
	var ctx context.Context = agent.NewInvocationContext(
		context.Background(),
		&agent.Invocation{AgentName: "context-agent"},
	)
	ctx, parent := telemetrytrace.Tracer.Start(ctx, itelemetry.NewExecuteToolSpanName("skill_load"))
	out, err := lt.Call(ctx, []byte(`{"skill":"context_skill"}`))
	parent.End()
	require.NoError(t, err)
	require.Equal(t, "loaded: context_skill", out)

	var invokeSpan sdktrace.ReadOnlySpan
	for _, sp := range recorder.Ended() {
		if sp.Name() == itelemetry.NewInvokeSkillSpanName("context_skill") {
			invokeSpan = sp
			break
		}
	}
	require.NotNil(t, invokeSpan)
	requestDetail := spanStringAttr(t, invokeSpan.Attributes(), semconvtrace.KeyGenAIInvokeSkillRequest)
	responseDetail := spanStringAttr(t, invokeSpan.Attributes(), semconvtrace.KeyGenAIInvokeSkillResponse)
	require.Contains(t, requestDetail, `"safe_path":"context_skill/SKILL.md"`)
	require.Contains(t, requestDetail, sha256Hex(filepath.Join(contextDir, skill.SkillFile)))
	require.NotContains(t, requestDetail, sha256Hex(filepath.Join(defaultDir, skill.SkillFile)))
	require.Contains(t, responseDetail, sha256Hex("context skill body"))
	require.NotContains(t, responseDetail, sha256Hex("default skill body"))
}

func requireSpanAttr(t *testing.T, attrs []attribute.KeyValue, key, value string) {
	t.Helper()
	for _, attr := range attrs {
		if string(attr.Key) == key {
			require.Equal(t, value, attr.Value.AsString())
			return
		}
	}
	t.Fatalf("missing attribute %s", key)
}

func spanStringAttr(t *testing.T, attrs []attribute.KeyValue, key string) string {
	t.Helper()
	for _, attr := range attrs {
		if string(attr.Key) == key {
			return attr.Value.AsString()
		}
	}
	t.Fatalf("missing span attribute %s", key)
	return ""
}

func TestSkillNameEnum_ContextAwareRepoReturnsNil(t *testing.T) {
	repo := skill.NewFilteredRepository(
		&mockRepo{
			ok: map[string]bool{"alpha": true},
			sums: []skill.Summary{
				{Name: "alpha"},
			},
		},
		func(context.Context, skill.Summary) bool { return true },
	)
	require.Nil(t, skillNameEnum(repo))
}

func TestSkillNameEnum_NilFilterWrapperKeepsPlainRepoEnum(t *testing.T) {
	repo := skill.NewFilteredRepository(
		&mockRepo{
			ok: map[string]bool{"alpha": true},
			sums: []skill.Summary{
				{Name: "alpha"},
			},
		},
		nil,
	)
	require.Equal(t, []any{"alpha"}, skillNameEnum(repo))
}
