//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package agent

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
	coreagent "trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

func TestDynamicAgentModelProfileDeclarationIsOptIn(t *testing.T) {
	withoutProfiles := NewDynamicTool().Declaration()
	require.NotContains(t, withoutProfiles.InputSchema.Properties, fieldModel)
	require.Contains(t, withoutProfiles.Description, "cannot select arbitrary agents, models")
	require.NotContains(t, withoutProfiles.Description, "'model'")

	fast := &dynRecordingModel{name: "provider-fast"}
	deep := &dynRecordingModel{name: "provider-deep"}
	withProfiles := NewDynamicTool(
		WithAgentModelProfile(" fast ", " Low-latency drafting. ", fast),
		WithAgentModelProfile("deep", "Careful review.", deep),
	).Declaration()

	modelSchema := withProfiles.InputSchema.Properties[fieldModel]
	require.NotNil(t, modelSchema)
	require.Equal(t, "string", modelSchema.Type)
	require.Equal(t, []any{"fast", "deep"}, modelSchema.Enum)
	require.Contains(t, modelSchema.Description, "fast: Low-latency drafting.")
	require.Contains(t, modelSchema.Description, "deep: Careful review.")
	require.Contains(t, modelSchema.Description, "Omit to keep")
	require.NotContains(t, modelSchema.Description, "provider-fast")
	require.NotContains(t, modelSchema.Description, "provider-deep")
	require.Contains(t, withProfiles.Description, "host-authorized model profile")
	require.Contains(t, withProfiles.Description, "'model'")
	require.Equal(t, []string{fieldRequest}, withProfiles.InputSchema.Required)
}

func TestDynamicAgentModelProfileValidation(t *testing.T) {
	valid := &dynRecordingModel{name: "valid"}
	tests := []struct {
		name    string
		opts    []Option
		message string
	}{
		{
			name: "empty name",
			opts: []Option{WithAgentModelProfile(" ", "description", valid)},
			message: "Invalid Dynamic AgentTool configuration: " +
				"agent model profile name is required",
		},
		{
			name: "empty description",
			opts: []Option{WithAgentModelProfile("fast", " ", valid)},
			message: "Invalid Dynamic AgentTool configuration: " +
				"agent model profile \"fast\" description is required",
		},
		{
			name: "nil model",
			opts: []Option{WithAgentModelProfile("fast", "description", nil)},
			message: "Invalid Dynamic AgentTool configuration: " +
				"agent model profile \"fast\" model is required",
		},
		{
			name: "typed nil model",
			opts: []Option{WithAgentModelProfile(
				"fast",
				"description",
				(*dynRecordingModel)(nil),
			)},
			message: "Invalid Dynamic AgentTool configuration: " +
				"agent model profile \"fast\" model is required",
		},
		{
			name: "duplicate normalized name",
			opts: []Option{
				WithAgentModelProfile("fast", "one", valid),
				WithAgentModelProfile(" fast ", "two", valid),
			},
			message: "Invalid Dynamic AgentTool configuration: " +
				"duplicate agent model profile \"fast\"",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.PanicsWithValue(t, tt.message, func() {
				NewDynamicTool(tt.opts...)
			})
		})
	}
}

func TestDynamicAgentModelProfileOverridesTemplateForOneCall(t *testing.T) {
	parentModel := &dynRecordingModel{name: "parent", response: "parent"}
	parentOverride := &dynRecordingModel{name: "parent-override", response: "override"}
	templateModel := &dynRecordingModel{name: "template", response: "template"}
	fastModel := &dynRecordingModel{name: "provider-fast", response: "fast"}

	parentAgent := llmagent.New("parent", llmagent.WithModel(parentModel))
	templateAgent := llmagent.New("worker", llmagent.WithModel(templateModel))
	dynamicTool := NewDynamicTool(
		WithTemplateAgent(templateAgent),
		WithAgentModelProfile("fast", "Low-latency work.", fastModel),
	)
	parent := newDynamicModelProfileParent(parentAgent, "selected-profile")
	parent.RunOptions.Model = parentOverride

	got, err := dynamicTool.Call(
		coreagent.NewInvocationContext(context.Background(), parent),
		[]byte(`{"request":"draft","model":"fast"}`),
	)
	require.NoError(t, err)
	require.Equal(t, "fast", got)
	require.Len(t, fastModel.snapshot(), 1)
	require.Empty(t, templateModel.snapshot())
	require.Empty(t, parentOverride.snapshot())
	require.Empty(t, parentModel.snapshot())
}

func TestDynamicAgentModelProfileComposesWithToolSelection(t *testing.T) {
	templateModel := &dynRecordingModel{name: "template", response: "template"}
	profileModel := &dynRecordingModel{name: "provider-fast", response: "fast"}
	templateAgent := llmagent.New("worker", llmagent.WithModel(templateModel))
	dynamicTool := NewDynamicTool(
		WithTemplateAgent(templateAgent),
		WithCapabilityTools(stubTools("read_file", "search_code")),
		WithAgentModelProfile("fast", "Low-latency work.", profileModel),
	)
	parentAgent := llmagent.New(
		"parent",
		llmagent.WithModel(&dynRecordingModel{name: "parent"}),
	)
	parent := newDynamicModelProfileParent(parentAgent, "profile-and-tools")

	got, err := dynamicTool.Call(
		coreagent.NewInvocationContext(context.Background(), parent),
		[]byte(`{
			"request":"find the declaration",
			"instruction":"Act as a focused code explorer.",
			"tools":["search_code"],
			"model":"fast"
		}`),
	)
	require.NoError(t, err)
	require.Equal(t, "fast", got)
	require.Equal(t, [][]string{{"search_code"}}, profileModel.snapshot())
	require.Empty(t, templateModel.snapshot())
}

func TestDynamicAgentModelProfileOmissionPreservesTemplateDefault(t *testing.T) {
	templateDefault := &dynRecordingModel{name: "template-default", response: "default"}
	templateHiddenAlternative := &dynRecordingModel{name: "template-hidden", response: "hidden"}
	profileModel := &dynRecordingModel{name: "provider-fast", response: "fast"}
	templateAgent := llmagent.New(
		"worker",
		llmagent.WithModel(templateDefault),
		llmagent.WithModels(map[string]model.Model{
			"internal-alternative": templateHiddenAlternative,
		}),
	)
	dynamicTool := NewDynamicTool(
		WithTemplateAgent(templateAgent),
		WithAgentModelProfile("fast", "Low-latency work.", profileModel),
	)

	modelSchema := dynamicTool.Declaration().InputSchema.Properties[fieldModel]
	require.Equal(t, []any{"fast"}, modelSchema.Enum)
	require.NotContains(t, modelSchema.Description, "internal-alternative")

	parentAgent := llmagent.New(
		"parent",
		llmagent.WithModel(&dynRecordingModel{name: "parent"}),
	)
	parent := newDynamicModelProfileParent(parentAgent, "default-profile")
	got, err := dynamicTool.Call(
		coreagent.NewInvocationContext(context.Background(), parent),
		[]byte(`{"request":"use the default"}`),
	)
	require.NoError(t, err)
	require.Equal(t, "default", got)
	require.Len(t, templateDefault.snapshot(), 1)
	require.Empty(t, templateHiddenAlternative.snapshot())
	require.Empty(t, profileModel.snapshot())
}

func TestDynamicAgentModelProfileOverridesInheritedModelWithoutTemplate(t *testing.T) {
	parentDefault := &dynRecordingModel{name: "parent-default", response: "default"}
	parentOverride := &dynRecordingModel{name: "parent-override", response: "override"}
	profileModel := &dynRecordingModel{name: "provider-deep", response: "deep"}
	parentAgent := llmagent.New("parent", llmagent.WithModel(parentDefault))
	dynamicTool := NewDynamicTool(
		WithAgentModelProfile("deep", "Careful reasoning.", profileModel),
	)

	selectedParent := newDynamicModelProfileParent(parentAgent, "selected")
	selectedParent.RunOptions.Model = parentOverride
	got, err := dynamicTool.Call(
		coreagent.NewInvocationContext(context.Background(), selectedParent),
		[]byte(`{"request":"review","model":"deep"}`),
	)
	require.NoError(t, err)
	require.Equal(t, "deep", got)
	require.Len(t, profileModel.snapshot(), 1)
	require.Empty(t, parentOverride.snapshot())

	omittedParent := newDynamicModelProfileParent(parentAgent, "omitted")
	omittedParent.RunOptions.Model = parentOverride
	got, err = dynamicTool.Call(
		coreagent.NewInvocationContext(context.Background(), omittedParent),
		[]byte(`{"request":"review"}`),
	)
	require.NoError(t, err)
	require.Equal(t, "override", got)
	require.Len(t, parentOverride.snapshot(), 1)
	require.Empty(t, parentDefault.snapshot())
}

func TestDynamicAgentModelProfileOverridesInheritedModelNameWithoutTemplate(
	t *testing.T,
) {
	parentDefault := &dynRecordingModel{name: "parent-default", response: "default"}
	parentNamed := &dynRecordingModel{name: "parent-named", response: "named"}
	profileModel := &dynRecordingModel{name: "provider-deep", response: "deep"}
	parentAgent := llmagent.New(
		"parent",
		llmagent.WithModel(parentDefault),
		llmagent.WithModels(map[string]model.Model{"named": parentNamed}),
	)
	dynamicTool := NewDynamicTool(
		WithAgentModelProfile("deep", "Careful reasoning.", profileModel),
	)

	selectedParent := newDynamicModelProfileParent(parentAgent, "selected-name")
	selectedParent.RunOptions.ModelName = "named"
	got, err := dynamicTool.Call(
		coreagent.NewInvocationContext(context.Background(), selectedParent),
		[]byte(`{"request":"review","model":"deep"}`),
	)
	require.NoError(t, err)
	require.Equal(t, "deep", got)
	require.Len(t, profileModel.snapshot(), 1)
	require.Empty(t, parentNamed.snapshot())

	omittedParent := newDynamicModelProfileParent(parentAgent, "omitted-name")
	omittedParent.RunOptions.ModelName = "named"
	got, err = dynamicTool.Call(
		coreagent.NewInvocationContext(context.Background(), omittedParent),
		[]byte(`{"request":"review"}`),
	)
	require.NoError(t, err)
	require.Equal(t, "named", got)
	require.Len(t, parentNamed.snapshot(), 1)
}

func TestDynamicAgentModelProfileOverridesInheritedRunModelSelector(t *testing.T) {
	parentDefault := &dynRecordingModel{name: "parent-default", response: "default"}
	selectorModel := &dynRecordingModel{name: "selector", response: "selector"}
	profileModel := &dynRecordingModel{name: "provider-fast", response: "fast"}
	parentAgent := llmagent.New("parent", llmagent.WithModel(parentDefault))
	dynamicTool := NewDynamicTool(
		WithAgentModelProfile("fast", "Low-latency work.", profileModel),
	)
	var selectorCalls atomic.Int32
	selector := func(
		context.Context,
		*coreagent.Invocation,
	) (model.Model, error) {
		selectorCalls.Add(1)
		return selectorModel, nil
	}

	selectedParent := newDynamicModelProfileParent(parentAgent, "selected-selector")
	selectedParent.RunOptions.ModelSelector = selector
	got, err := dynamicTool.Call(
		coreagent.NewInvocationContext(context.Background(), selectedParent),
		[]byte(`{"request":"draft","model":"fast"}`),
	)
	require.NoError(t, err)
	require.Equal(t, "fast", got)
	require.Zero(t, selectorCalls.Load())
	require.Len(t, profileModel.snapshot(), 1)
	require.Empty(t, selectorModel.snapshot())

	omittedParent := newDynamicModelProfileParent(parentAgent, "omitted-selector")
	omittedParent.RunOptions.ModelSelector = selector
	got, err = dynamicTool.Call(
		coreagent.NewInvocationContext(context.Background(), omittedParent),
		[]byte(`{"request":"draft"}`),
	)
	require.NoError(t, err)
	require.Equal(t, "selector", got)
	require.Equal(t, int32(1), selectorCalls.Load())
	require.Len(t, selectorModel.snapshot(), 1)
}

func TestDynamicAgentModelProfileOverridesTemplateModelSelector(t *testing.T) {
	templateDefault := &dynRecordingModel{name: "template-default", response: "default"}
	templateSelected := &dynRecordingModel{name: "template-selector", response: "selector"}
	profileModel := &dynRecordingModel{name: "provider-fast", response: "fast"}
	var selectorCalls atomic.Int32
	templateAgent := llmagent.New(
		"worker",
		llmagent.WithModel(templateDefault),
		llmagent.WithModelSelector(func(
			context.Context,
			*coreagent.Invocation,
		) (model.Model, error) {
			selectorCalls.Add(1)
			return templateSelected, nil
		}),
	)
	dynamicTool := NewDynamicTool(
		WithTemplateAgent(templateAgent),
		WithAgentModelProfile("fast", "Low-latency work.", profileModel),
	)
	parentAgent := llmagent.New(
		"parent",
		llmagent.WithModel(&dynRecordingModel{name: "parent"}),
	)

	selectedParent := newDynamicModelProfileParent(parentAgent, "selected-template-selector")
	got, err := dynamicTool.Call(
		coreagent.NewInvocationContext(context.Background(), selectedParent),
		[]byte(`{"request":"draft","model":"fast"}`),
	)
	require.NoError(t, err)
	require.Equal(t, "fast", got)
	require.Zero(t, selectorCalls.Load())
	require.Len(t, profileModel.snapshot(), 1)
	require.Empty(t, templateSelected.snapshot())

	omittedParent := newDynamicModelProfileParent(parentAgent, "omitted-template-selector")
	got, err = dynamicTool.Call(
		coreagent.NewInvocationContext(context.Background(), omittedParent),
		[]byte(`{"request":"draft"}`),
	)
	require.NoError(t, err)
	require.Equal(t, "selector", got)
	require.Equal(t, int32(1), selectorCalls.Load())
	require.Len(t, templateSelected.snapshot(), 1)
}

func TestDynamicAgentModelProfileClearsInheritedProviderRequestOptions(
	t *testing.T,
) {
	parentModel := &dynRecordingModel{name: "parent", response: "parent"}
	profileModel := &dynRecordingModel{name: "profile", response: "profile"}
	parentAgent := llmagent.New("parent", llmagent.WithModel(parentModel))
	dynamicTool := NewDynamicTool(
		WithAgentModelProfile("deep", "Careful reasoning.", profileModel),
	)
	parent := newDynamicModelProfileParent(parentAgent, "provider-options")
	parent.RunOptions.ModelContextWindow = 4096
	parent.RunOptions.ModelRequestExtraFields = map[string]any{
		"model":           "unapproved-provider-model",
		"provider_option": "parent",
	}
	parent.RunOptions.ModelRequestHeaders = map[string]string{
		"X-Provider-Route": "parent",
	}

	_, child, _, err := dynamicTool.buildDynamicSubInvocation(
		coreagent.NewInvocationContext(context.Background(), parent),
		[]byte(`{"request":"review","model":"deep"}`),
	)
	require.NoError(t, err)
	require.Zero(t, child.RunOptions.ModelContextWindow)
	require.Nil(t, child.RunOptions.ModelRequestExtraFields)
	require.Nil(t, child.RunOptions.ModelRequestHeaders)
}

func TestDynamicAgentUnknownModelProfileFailsClosed(t *testing.T) {
	templateModel := &dynRecordingModel{name: "template", response: "template"}
	fastModel := &dynRecordingModel{name: "fast", response: "fast"}
	deepModel := &dynRecordingModel{name: "deep", response: "deep"}
	templateAgent := llmagent.New("worker", llmagent.WithModel(templateModel))
	dynamicTool := NewDynamicTool(
		WithTemplateAgent(templateAgent),
		WithAgentModelProfile("fast", "Fast work.", fastModel),
		WithAgentModelProfile("deep", "Deep work.", deepModel),
	)
	parentAgent := llmagent.New(
		"parent",
		llmagent.WithModel(&dynRecordingModel{name: "parent"}),
	)
	parent := newDynamicModelProfileParent(parentAgent, "unknown")

	_, err := dynamicTool.Call(
		coreagent.NewInvocationContext(context.Background(), parent),
		[]byte(`{"request":"work","model":"provider-model-id"}`),
	)
	require.Error(t, err)
	require.Contains(t, err.Error(), `unknown agent model profile "provider-model-id"`)
	require.Contains(t, err.Error(), "available: fast, deep")
	require.Empty(t, templateModel.snapshot())
	require.Empty(t, fastModel.snapshot())
	require.Empty(t, deepModel.snapshot())
}

func TestDynamicAgentModelProfilesAreInvocationScopedUnderConcurrency(t *testing.T) {
	const callsPerProfile = 12
	templateModel := &dynRecordingModel{name: "template", response: "template"}
	fastModel := &dynRecordingModel{name: "fast", response: "fast"}
	deepModel := &dynRecordingModel{name: "deep", response: "deep"}
	templateAgent := llmagent.New("worker", llmagent.WithModel(templateModel))
	dynamicTool := NewDynamicTool(
		WithTemplateAgent(templateAgent),
		WithAgentModelProfile("fast", "Fast work.", fastModel),
		WithAgentModelProfile("deep", "Deep work.", deepModel),
	)
	parentAgent := llmagent.New(
		"parent",
		llmagent.WithModel(&dynRecordingModel{name: "parent"}),
	)

	type callResult struct {
		want string
		got  any
		err  error
	}
	results := make(chan callResult, callsPerProfile*2)
	var wg sync.WaitGroup
	for i := 0; i < callsPerProfile*2; i++ {
		alias := "fast"
		if i%2 == 1 {
			alias = "deep"
		}
		wg.Add(1)
		go func(index int, profile string) {
			defer wg.Done()
			parent := newDynamicModelProfileParent(
				parentAgent,
				fmt.Sprintf("concurrent-%d", index),
			)
			got, err := dynamicTool.Call(
				coreagent.NewInvocationContext(context.Background(), parent),
				[]byte(fmt.Sprintf(
					`{"request":"work","model":%q}`,
					profile,
				)),
			)
			results <- callResult{want: profile, got: got, err: err}
		}(i, alias)
	}
	wg.Wait()
	close(results)
	for result := range results {
		require.NoError(t, result.err)
		require.Equal(t, result.want, result.got)
	}
	require.Len(t, fastModel.snapshot(), callsPerProfile)
	require.Len(t, deepModel.snapshot(), callsPerProfile)
	require.Empty(t, templateModel.snapshot())

	// Profile selection must not mutate the shared template. A later call that
	// omits model still uses the template default.
	parent := newDynamicModelProfileParent(parentAgent, "after-concurrency")
	got, err := dynamicTool.Call(
		coreagent.NewInvocationContext(context.Background(), parent),
		[]byte(`{"request":"default"}`),
	)
	require.NoError(t, err)
	require.Equal(t, "template", got)
	require.Len(t, templateModel.snapshot(), 1)
}

func TestDynamicAgentModelArgumentIgnoredWhenProfilesAreNotConfigured(t *testing.T) {
	dynamicTool := NewDynamicTool()
	spec := dynamicTool.parseDynamicArgs([]byte(
		`{"request":"work","model":"provider-model-id"}`,
	))
	require.Equal(t, "work", spec.request)
	require.Empty(t, spec.model)
	require.NotContains(
		t,
		dynamicTool.Declaration().InputSchema.Properties,
		fieldModel,
	)
}

func newDynamicModelProfileParent(
	parentAgent coreagent.Agent,
	sessionID string,
) *coreagent.Invocation {
	return coreagent.NewInvocation(
		coreagent.WithInvocationAgent(parentAgent),
		coreagent.WithInvocationSession(
			session.NewSession("app", "user", strings.TrimSpace(sessionID)),
		),
		coreagent.WithInvocationEventFilterKey("parent"),
	)
}
