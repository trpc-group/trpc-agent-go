//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package dynamicworkflow

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/internal/state/appender"
	"trpc.group/trpc-go/trpc-agent-go/internal/surfacepatch"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/skill"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func TestAgentModelProfilesZeroProfilesPreservesPriorContract(t *testing.T) {
	templateModel := &recordingModel{name: "template", content: "from-template"}
	reviewer := llmagent.New("reviewer", llmagent.WithModel(templateModel))
	workflow, err := NewTool(scriptedRuntime{run: func(ctx context.Context, handler CallHandler) (Result, error) {
		raw, err := handler.HandleWorkflowCall(ctx, Call{
			ID: "agent-1", Kind: CallKindAgent,
			Args: json.RawMessage(`{"input":"review it"}`),
		})
		return Result{Value: raw}, err
	}}, []agent.Agent{reviewer})
	require.NoError(t, err)

	decl := workflow.Declaration()
	require.NotContains(t, decl.Description, "Host-authorized agent model profiles")
	require.NotContains(t, decl.Description, "model profile")
	codeDescription := decl.InputSchema.Properties["code"].Description
	require.NotContains(t, codeDescription, "model,")
	require.NotContains(t, codeDescription, agentModelProfileGuidance)
	require.Contains(t, decl.Description, "Registered templates fix model")

	parent := agent.NewInvocation(
		agent.WithInvocationAgent(&testAgent{name: "root"}),
		agent.WithInvocationSession(&session.Session{ID: "session-1", AppName: "app", UserID: "user"}),
	)
	appender.Attach(parent, func(context.Context, *event.Event) error { return nil })

	_, err = workflow.Call(agent.NewInvocationContext(context.Background(), parent), []byte(`{"code":"return None"}`))
	require.NoError(t, err)
	require.Equal(t, int32(1), templateModel.callCount())
}

func TestAgentModelProfileSelectionInvokesProfileModel(t *testing.T) {
	templateModel := &recordingModel{name: "template", content: "from-template"}
	fastModel := &recordingModel{name: "fast", content: "from-fast"}
	reviewer := llmagent.New("reviewer", llmagent.WithModel(templateModel))
	workflow, err := NewTool(scriptedRuntime{run: func(ctx context.Context, handler CallHandler) (Result, error) {
		raw, err := handler.HandleWorkflowCall(ctx, Call{
			ID: "agent-1", Kind: CallKindAgent,
			Args: json.RawMessage(`{
				"options": {"template": "reviewer", "model": "fast"},
				"input": "review it"
			}`),
		})
		return Result{Value: raw}, err
	}}, []agent.Agent{reviewer}, WithAgentModelProfile(
		"fast",
		"Low-latency drafting.",
		fastModel,
	))
	require.NoError(t, err)

	parent := agent.NewInvocation(
		agent.WithInvocationAgent(&testAgent{name: "root"}),
		agent.WithInvocationSession(&session.Session{ID: "session-1", AppName: "app", UserID: "user"}),
	)
	appender.Attach(parent, func(context.Context, *event.Event) error { return nil })

	value, err := workflow.Call(agent.NewInvocationContext(context.Background(), parent), []byte(`{"code":"return None"}`))
	require.NoError(t, err)
	result := value.(Result)
	require.JSONEq(t, `{"text":"from-fast","session_id":"session-1","history_key":"root/dynamic_workflow/<workflow>/agent-1","invocation_id":"<invocation-id>"}`, normalizeSingleAgentResult(t, result.Value))
	require.Equal(t, int32(0), templateModel.callCount())
	require.Equal(t, int32(1), fastModel.callCount())
}

func TestAgentModelProfileOverridesTemplateWithModelsForOneCall(t *testing.T) {
	templateDefault := &recordingModel{name: "template-default", content: "from-default"}
	templateAlternate := &recordingModel{name: "template-alternate", content: "from-alternate"}
	profileModel := &recordingModel{name: "profile", content: "from-profile"}
	reviewer := llmagent.New(
		"reviewer",
		llmagent.WithModel(templateDefault),
		llmagent.WithModels(map[string]model.Model{
			"default":   templateDefault,
			"alternate": templateAlternate,
		}),
	)
	require.NoError(t, reviewer.SetModelByName("alternate"))

	workflow, err := NewTool(scriptedRuntime{run: func(ctx context.Context, handler CallHandler) (Result, error) {
		profileRaw, err := handler.HandleWorkflowCall(ctx, Call{
			ID: "agent-profile", Kind: CallKindAgent,
			Args: json.RawMessage(`{
				"options": {"template": "reviewer", "model": "fast"},
				"input": "profile path"
			}`),
		})
		if err != nil {
			return Result{}, err
		}
		defaultRaw, err := handler.HandleWorkflowCall(ctx, Call{
			ID: "agent-default", Kind: CallKindAgent,
			Args: json.RawMessage(`{
				"options": {"template": "reviewer"},
				"input": "template path"
			}`),
		})
		if err != nil {
			return Result{}, err
		}
		combined, err := json.Marshal(map[string]json.RawMessage{
			"profile": profileRaw,
			"default": defaultRaw,
		})
		return Result{Value: combined}, err
	}}, []agent.Agent{reviewer}, WithAgentModelProfile(
		"fast",
		"Low-latency drafting.",
		profileModel,
	))
	require.NoError(t, err)

	parent := agent.NewInvocation(
		agent.WithInvocationAgent(&testAgent{name: "root"}),
		agent.WithInvocationSession(&session.Session{ID: "session-1", AppName: "app", UserID: "user"}),
	)
	appender.Attach(parent, func(context.Context, *event.Event) error { return nil })

	value, err := workflow.Call(
		agent.NewInvocationContext(context.Background(), parent),
		[]byte(`{"code":"return None"}`),
	)
	require.NoError(t, err)
	result := value.(Result)
	require.Contains(t, string(result.Value), `"text":"from-profile"`)
	require.Contains(t, string(result.Value), `"text":"from-alternate"`)
	require.Equal(t, int32(0), templateDefault.callCount())
	require.Equal(t, int32(1), templateAlternate.callCount())
	require.Equal(t, int32(1), profileModel.callCount())
}

func TestAgentModelProfilesConcurrentCallsDoNotCrossContaminate(t *testing.T) {
	templateModel := &recordingModel{name: "template", content: "from-template"}
	fastModel := &recordingModel{name: "fast", content: "from-fast"}
	deepModel := &recordingModel{name: "deep", content: "from-deep"}
	reviewer := llmagent.New("reviewer", llmagent.WithModel(templateModel))

	workflow, err := NewTool(scriptedRuntime{run: func(ctx context.Context, handler CallHandler) (Result, error) {
		var wg sync.WaitGroup
		var fastRaw, deepRaw json.RawMessage
		var fastErr, deepErr error
		wg.Add(2)
		go func() {
			defer wg.Done()
			fastRaw, fastErr = handler.HandleWorkflowCall(ctx, Call{
				ID: "agent-fast", Kind: CallKindAgent,
				Args: json.RawMessage(`{
					"options": {"template": "reviewer", "model": "fast", "instruction": "Draft."},
					"input": "fast path"
				}`),
			})
		}()
		go func() {
			defer wg.Done()
			deepRaw, deepErr = handler.HandleWorkflowCall(ctx, Call{
				ID: "agent-deep", Kind: CallKindAgent,
				Args: json.RawMessage(`{
					"options": {"template": "reviewer", "model": "deep", "instruction": "Review."},
					"input": "deep path"
				}`),
			})
		}()
		wg.Wait()
		if fastErr != nil {
			return Result{}, fastErr
		}
		if deepErr != nil {
			return Result{}, deepErr
		}
		combined, err := json.Marshal(map[string]json.RawMessage{
			"fast": fastRaw,
			"deep": deepRaw,
		})
		return Result{Value: combined}, err
	}}, []agent.Agent{reviewer},
		WithAgentModelProfile("fast", "Fast path.", fastModel),
		WithAgentModelProfile("deep", "Deep path.", deepModel),
	)
	require.NoError(t, err)

	parent := agent.NewInvocation(
		agent.WithInvocationAgent(&testAgent{name: "root"}),
		agent.WithInvocationSession(&session.Session{ID: "session-1", AppName: "app", UserID: "user"}),
	)
	appender.Attach(parent, func(context.Context, *event.Event) error { return nil })

	value, err := workflow.Call(agent.NewInvocationContext(context.Background(), parent), []byte(`{"code":"return None"}`))
	require.NoError(t, err)
	result := value.(Result)
	var got map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(result.Value, &got))
	require.Contains(t, string(got["fast"]), `"text":"from-fast"`)
	require.Contains(t, string(got["deep"]), `"text":"from-deep"`)
	require.Equal(t, int32(0), templateModel.callCount())
	require.Equal(t, int32(1), fastModel.callCount())
	require.Equal(t, int32(1), deepModel.callCount())

	// A later omitted-model call must still use the unchanged template model.
	followUp, err := NewTool(scriptedRuntime{run: func(ctx context.Context, handler CallHandler) (Result, error) {
		raw, err := handler.HandleWorkflowCall(ctx, Call{
			ID: "agent-default", Kind: CallKindAgent,
			Args: json.RawMessage(`{"input":"default path"}`),
		})
		return Result{Value: raw}, err
	}}, []agent.Agent{reviewer},
		WithAgentModelProfile("fast", "Fast path.", fastModel),
		WithAgentModelProfile("deep", "Deep path.", deepModel),
	)
	require.NoError(t, err)
	_, err = followUp.Call(agent.NewInvocationContext(context.Background(), parent), []byte(`{"code":"return None"}`))
	require.NoError(t, err)
	require.Equal(t, int32(1), templateModel.callCount())
}

func TestAgentModelProfileUnknownFailsBeforeChildExecution(t *testing.T) {
	var ran atomic.Bool
	reviewer := &testAgent{
		name: "reviewer",
		runFn: func(context.Context, *agent.Invocation) (<-chan *event.Event, error) {
			ran.Store(true)
			ch := make(chan *event.Event)
			close(ch)
			return ch, nil
		},
	}
	workflow, err := NewTool(scriptedRuntime{run: func(ctx context.Context, handler CallHandler) (Result, error) {
		_, err := handler.HandleWorkflowCall(ctx, Call{
			ID: "agent-1", Kind: CallKindAgent,
			Args: json.RawMessage(`{
				"options": {"template": "reviewer", "model": "missing"},
				"input": "review it"
			}`),
		})
		return Result{}, err
	}}, []agent.Agent{reviewer},
		WithAgentModelProfile("fast", "Fast path.", &recordingModel{name: "fast"}),
		WithAgentModelProfile("deep", "Deep path.", &recordingModel{name: "deep"}),
	)
	require.NoError(t, err)

	parent := agent.NewInvocation(
		agent.WithInvocationAgent(&testAgent{name: "root"}),
		agent.WithInvocationSession(&session.Session{ID: "session-1", AppName: "app", UserID: "user"}),
	)
	appender.Attach(parent, func(context.Context, *event.Event) error { return nil })

	_, err = workflow.Call(agent.NewInvocationContext(context.Background(), parent), []byte(`{"code":"return None"}`))
	require.ErrorContains(t, err, `unknown agent model profile "missing"`)
	require.ErrorContains(t, err, "available: fast, deep")
	require.False(t, ran.Load())
}

func TestAgentModelProfileRegistrationValidation(t *testing.T) {
	reviewer := &testAgent{name: "reviewer"}
	valid := &recordingModel{name: "fast"}

	_, err := NewTool(scriptedRuntime{}, []agent.Agent{reviewer}, WithAgentModelProfile(" ", "desc", valid))
	require.ErrorContains(t, err, "agent model profile name is required")

	_, err = NewTool(scriptedRuntime{}, []agent.Agent{reviewer}, WithAgentModelProfile("fast", " ", valid))
	require.ErrorContains(t, err, `agent model profile "fast" description is required`)

	_, err = NewTool(scriptedRuntime{}, []agent.Agent{reviewer}, WithAgentModelProfile("fast", "desc", nil))
	require.ErrorContains(t, err, `agent model profile "fast" model is required`)

	_, err = NewTool(scriptedRuntime{}, []agent.Agent{reviewer},
		WithAgentModelProfile("fast", "one", valid),
		WithAgentModelProfile(" fast ", "two", &recordingModel{name: "other"}),
	)
	require.ErrorContains(t, err, `duplicate agent model profile "fast"`)
}

func TestAgentModelProfileComposesWithInstructionToolsAndStructuredOutput(t *testing.T) {
	templateModel := &recordingModel{name: "template", content: `{"approved":false}`}
	fastModel := &structuredOutputCaptureModel{content: `{"approved":true}`}
	lookup := &testTool{name: "lookup"}
	unused := &testTool{name: "unused"}
	reviewer := llmagent.New(
		"reviewer",
		llmagent.WithModel(templateModel),
		llmagent.WithTools([]tool.Tool{lookup, unused}),
	)

	workflow, err := NewTool(scriptedRuntime{run: func(ctx context.Context, handler CallHandler) (Result, error) {
		raw, err := handler.HandleWorkflowCall(ctx, Call{
			ID: "agent-1", Kind: CallKindAgent,
			Args: json.RawMessage(`{
				"options": {
					"template": "reviewer",
					"model": "fast",
					"instruction": "Be strict.",
					"tools": ["lookup"],
					"structured_output": {
						"type": "object",
						"required": ["approved"],
						"properties": {"approved": {"type": "boolean"}}
					}
				},
				"input": "review it"
			}`),
		})
		return Result{Value: raw}, err
	}}, []agent.Agent{reviewer}, WithAgentModelProfile("fast", "Fast path.", fastModel))
	require.NoError(t, err)

	parent := agent.NewInvocation(
		agent.WithInvocationAgent(&testAgent{name: "root"}),
		agent.WithInvocationSession(&session.Session{ID: "session-1", AppName: "app", UserID: "user"}),
	)
	appender.Attach(parent, func(context.Context, *event.Event) error { return nil })

	value, err := workflow.Call(agent.NewInvocationContext(context.Background(), parent), []byte(`{"code":"return None"}`))
	require.NoError(t, err)
	result := value.(Result)
	require.Contains(t, string(result.Value), `"structured":{"approved":true}`)
	require.Equal(t, int32(0), templateModel.callCount())

	structuredOutput := fastModel.latestStructuredOutput()
	require.NotNil(t, structuredOutput)
	require.NotNil(t, structuredOutput.JSONSchema)
	require.Equal(t, "reviewer_output", structuredOutput.JSONSchema.Name)

	capture := &testAgent{
		name:  "reviewer",
		tools: []tool.Tool{lookup, unused},
		skills: &testSkillRepo{summaries: []skill.Summary{
			{Name: "risk"},
			{Name: "style"},
		}},
		response: "ok",
	}
	captureWorkflow, err := NewTool(scriptedRuntime{run: func(ctx context.Context, handler CallHandler) (Result, error) {
		raw, err := handler.HandleWorkflowCall(ctx, Call{
			ID: "agent-2", Kind: CallKindAgent,
			Args: json.RawMessage(`{
				"options": {
					"template": "reviewer",
					"model": "fast",
					"instruction": "Be strict.",
					"tools": ["lookup"],
					"skills": ["risk"]
				},
				"input": "review it"
			}`),
		})
		return Result{Value: raw}, err
	}}, []agent.Agent{capture}, WithAgentModelProfile("fast", "Fast path.", fastModel))
	require.NoError(t, err)
	_, err = captureWorkflow.Call(agent.NewInvocationContext(context.Background(), parent), []byte(`{"code":"return None"}`))
	require.NoError(t, err)

	child := capture.lastInvocation()
	require.NotNil(t, child)
	rootNode := agent.InvocationSurfaceRootNodeID(child)
	patch, ok := surfacepatch.PatchForNode(child.RunOptions.CustomAgentConfigs, rootNode)
	require.True(t, ok)
	instruction, ok := patch.Instruction()
	require.True(t, ok)
	require.Equal(t, "Be strict.", instruction)
	selectedModel, ok := patch.Model()
	require.True(t, ok)
	require.Equal(t, fastModel, selectedModel)
	selectedTools, ok := patch.Tools()
	require.True(t, ok)
	require.Equal(t, []string{"lookup"}, toolNames(selectedTools))
	selectedSkills, ok := patch.SkillRepository()
	require.True(t, ok)
	require.Equal(t, []string{"risk"}, skillNames(skill.SummariesForContext(context.Background(), selectedSkills)))
}

func TestAgentModelProfileDeclarationListsAliasesOnceInOrder(t *testing.T) {
	reviewer := &testAgent{name: "reviewer"}
	workflow, err := NewTool(scriptedRuntime{}, []agent.Agent{reviewer},
		WithAgentModelProfile("fast", "Fast drafting.", &recordingModel{name: "fast"}),
		WithAgentModelProfile("deep", "Deep review.", &recordingModel{name: "deep"}),
	)
	require.NoError(t, err)

	decl := workflow.Declaration()
	require.Contains(t, decl.Description, "Registered templates fix the default model")
	require.Equal(t, 1, strings.Count(decl.Description, "Host-authorized agent model profiles:"))
	require.Equal(t, 1, strings.Count(decl.Description, `- model "fast": Fast drafting.`))
	require.Equal(t, 1, strings.Count(decl.Description, `- model "deep": Deep review.`))
	require.NotContains(t, decl.Description, agentModelProfileGuidance)
	fastIdx := strings.Index(decl.Description, `- model "fast":`)
	deepIdx := strings.Index(decl.Description, `- model "deep":`)
	require.Greater(t, deepIdx, fastIdx)

	codeDescription := decl.InputSchema.Properties["code"].Description
	require.Contains(
		t,
		codeDescription,
		"template, instruction, model, instance_id, tools, skills, and structured_output",
	)
	require.Equal(t, 1, strings.Count(codeDescription, agentModelProfileGuidance))
	require.NotContains(t, codeDescription, `- model "fast":`)
	require.NotContains(t, codeDescription, `- model "deep":`)
}

func TestAgentModelProfileNormalizeAndHasOverrides(t *testing.T) {
	spec, err := decodeAgentOptions(json.RawMessage(`{
		"template": "reviewer",
		"model": " fast "
	}`))
	require.NoError(t, err)
	require.NoError(t, normalizeAgentSpec(&spec))
	require.Equal(t, "fast", spec.Model)
	require.True(t, agentSpecHasOverrides(spec))

	spec = AgentSpec{Template: "reviewer", Model: " "}
	require.NoError(t, normalizeAgentSpec(&spec))
	require.Empty(t, spec.Model)
	require.False(t, agentSpecHasOverrides(spec))
}

type recordingModel struct {
	name    string
	content string

	calls atomic.Int32
}

func (m *recordingModel) GenerateContent(
	_ context.Context,
	_ *model.Request,
) (<-chan *model.Response, error) {
	m.calls.Add(1)
	content := m.content
	if content == "" {
		content = m.name
	}
	responses := make(chan *model.Response, 1)
	responses <- &model.Response{
		ID:   "recording-" + m.name,
		Done: true,
		Choices: []model.Choice{{
			Index:   0,
			Message: model.NewAssistantMessage(content),
		}},
	}
	close(responses)
	return responses, nil
}

func (m *recordingModel) Info() model.Info {
	return model.Info{Name: m.name}
}

func (m *recordingModel) callCount() int32 {
	return m.calls.Load()
}
