//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/gwclient"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/gwproto"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/internal/gateway"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/registry"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/runtimeprofile"
)

type catalogResponseModel struct {
	name  string
	reply string
}

var (
	registerEmptyNameModelOnce sync.Once
	registerEmptyNameModelErr  error
)

func (m *catalogResponseModel) GenerateContent(
	_ context.Context,
	_ *model.Request,
) (<-chan *model.Response, error) {
	ch := make(chan *model.Response, 1)
	ch <- &model.Response{
		Object: model.ObjectTypeChatCompletion,
		Model:  m.name,
		Choices: []model.Choice{{
			Message: model.NewAssistantMessage(m.reply),
		}},
		Done: true,
	}
	close(ch)
	return ch, nil
}

func (m *catalogResponseModel) Info() model.Info {
	return model.Info{Name: m.name}
}

func TestResolveModelCatalog_Injected(t *testing.T) {
	fast := &echoModel{name: "fast-model"}
	strong := &echoModel{name: "strong-model"}

	catalog, err := resolveModelCatalog(
		runOptions{
			ModelMode:     modeOpenAI,
			OpenAIVariant: "openai",
		},
		buildRuntimeOptions([]RuntimeOption{
			WithModelCatalog(ModelCatalog{
				Default: "fast",
				Models: map[string]model.Model{
					"fast":   fast,
					"strong": strong,
				},
				Metadata: map[string]ModelMetadata{
					"strong": {
						OpenAIVariant: "glm",
						BaseURL:       "https://open.bigmodel.cn/api/paas/v4",
					},
				},
			}),
		}),
	)
	require.NoError(t, err)
	require.Equal(t, "fast", catalog.defaultName)
	require.Equal(t, []string{"fast", "strong"}, catalog.modelNames())
	require.Equal(t, "fast-model", catalog.defaultModel().Info().Name)
	require.Equal(t, "openai", catalog.metadata["fast"].OpenAIVariant)
	require.Equal(t, "glm", catalog.metadata["strong"].OpenAIVariant)
	require.Equal(
		t,
		"https://open.bigmodel.cn/api/paas/v4",
		catalog.metadata["strong"].BaseURL,
	)
}

func TestResolveModelCatalog_InjectedValidation(t *testing.T) {
	tests := []struct {
		name    string
		catalog ModelCatalog
		wantErr string
	}{
		{
			name:    "missing default",
			catalog: ModelCatalog{Models: map[string]model.Model{"fast": &echoModel{}}},
			wantErr: "default is required",
		},
		{
			name:    "missing models",
			catalog: ModelCatalog{Default: "fast"},
			wantErr: "models are required",
		},
		{
			name: "unknown default",
			catalog: ModelCatalog{
				Default: "strong",
				Models:  map[string]model.Model{"fast": &echoModel{}},
			},
			wantErr: `default "strong" is not configured`,
		},
		{
			name: "nil model",
			catalog: ModelCatalog{
				Default: "fast",
				Models:  map[string]model.Model{"fast": nil},
			},
			wantErr: `entry "fast" is nil`,
		},
		{
			name: "empty alias",
			catalog: ModelCatalog{
				Default: "fast",
				Models:  map[string]model.Model{" ": &echoModel{}},
			},
			wantErr: "contains an empty alias",
		},
		{
			name: "duplicate trimmed alias",
			catalog: ModelCatalog{
				Default: "fast",
				Models: map[string]model.Model{
					"fast":   &echoModel{},
					" fast ": &echoModel{},
				},
			},
			wantErr: `duplicate alias "fast"`,
		},
		{
			name: "metadata without model",
			catalog: ModelCatalog{
				Default: "fast",
				Models:  map[string]model.Model{"fast": &echoModel{}},
				Metadata: map[string]ModelMetadata{
					"unknown": {OpenAIVariant: "glm"},
				},
			},
			wantErr: `metadata entry "unknown" has no matching model`,
		},
		{
			name: "invalid metadata variant",
			catalog: ModelCatalog{
				Default: "fast",
				Models:  map[string]model.Model{"fast": &echoModel{}},
				Metadata: map[string]ModelMetadata{
					"fast": {OpenAIVariant: "deepseekk"},
				},
			},
			wantErr: `metadata entry "fast": unsupported openai variant`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := resolveModelCatalog(
				runOptions{},
				buildRuntimeOptions([]RuntimeOption{
					WithModelCatalog(tt.catalog),
				}),
			)
			require.ErrorContains(t, err, tt.wantErr)
		})
	}
}

func TestResolveModelCatalog_LegacyAliasFallbacks(t *testing.T) {
	const emptyNameMode = "model_catalog_empty_name_test"
	registerEmptyNameModelOnce.Do(func() {
		registerEmptyNameModelErr = registry.RegisterModel(
			emptyNameMode,
			func(registry.ModelSpec) (model.Model, error) {
				return &echoModel{}, nil
			},
		)
	})
	require.NoError(t, registerEmptyNameModelErr)

	catalog, err := resolveModelCatalog(runOptions{
		ModelMode:   emptyNameMode,
		OpenAIModel: " configured-name ",
	}, runtimeOptions{})
	require.NoError(t, err)
	require.Equal(t, "configured-name", catalog.defaultName)
	require.False(t, catalog.explicit)

	catalog, err = resolveModelCatalog(
		runOptions{ModelMode: emptyNameMode},
		runtimeOptions{},
	)
	require.NoError(t, err)
	require.Equal(t, legacyDefaultModelAlias, catalog.defaultName)

	_, err = resolveModelCatalog(
		runOptions{ModelMode: "unsupported-catalog-model"},
		runtimeOptions{},
	)
	require.ErrorContains(t, err, "unsupported mode")
}

func TestResolveConfiguredModelCatalogValidation(t *testing.T) {
	tests := []struct {
		name    string
		opts    runOptions
		wantErr string
	}{
		{
			name: "missing default",
			opts: runOptions{
				ModelEntries: map[string]modelEntryConfig{
					"fast": {Mode: modelCatalogStringPtr(modeMock)},
				},
			},
			wantErr: "model.default is required",
		},
		{
			name: "empty alias",
			opts: runOptions{
				ModelDefault: "fast",
				ModelEntries: map[string]modelEntryConfig{
					" ": {Mode: modelCatalogStringPtr(modeMock)},
				},
			},
			wantErr: "contains an empty alias",
		},
		{
			name: "duplicate trimmed alias",
			opts: runOptions{
				ModelDefault: "fast",
				ModelEntries: map[string]modelEntryConfig{
					"fast":   {Mode: modelCatalogStringPtr(modeMock)},
					" fast ": {Mode: modelCatalogStringPtr(modeMock)},
				},
			},
			wantErr: `duplicate alias "fast"`,
		},
		{
			name: "invalid entry",
			opts: runOptions{
				ModelDefault: "fast",
				ModelEntries: map[string]modelEntryConfig{
					"fast": {
						Timeout: modelCatalogStringPtr("not-a-duration"),
					},
				},
			},
			wantErr: "model.models.fast.timeout",
		},
		{
			name: "model creation failure",
			opts: runOptions{
				ModelDefault: "fast",
				ModelEntries: map[string]modelEntryConfig{
					"fast": {
						Mode: modelCatalogStringPtr("unsupported-mode"),
					},
				},
			},
			wantErr: "model.models.fast: unsupported mode",
		},
		{
			name: "default absent",
			opts: runOptions{
				ModelDefault: "strong",
				ModelEntries: map[string]modelEntryConfig{
					"fast": {Mode: modelCatalogStringPtr(modeMock)},
				},
			},
			wantErr: `model.default "strong" is not present`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := resolveConfiguredModelCatalog(tt.opts)
			require.ErrorContains(t, err, tt.wantErr)
		})
	}
}

func TestNormalizeModelMetadataRejectsDuplicateTrimmedAlias(t *testing.T) {
	_, err := normalizeModelMetadata(
		map[string]ModelMetadata{
			"fast":   {},
			" fast ": {},
		},
		map[string]model.Model{"fast": &echoModel{}},
		ModelMetadata{},
	)
	require.ErrorContains(t, err, `duplicate alias "fast"`)
}

func TestModelRunOptionsForEntry(t *testing.T) {
	var configNode yaml.Node
	require.NoError(t, yaml.Unmarshal([]byte("setting: value"), &configNode))
	textOnly := true
	maxRetries := 3
	opts, err := modelRunOptionsForEntry(
		runOptions{DebugRecorderEnabled: true},
		"full",
		modelEntryConfig{
			Mode:            modelCatalogStringPtr(" mock "),
			Name:            modelCatalogStringPtr(" model-name "),
			BaseURL:         modelCatalogStringPtr(" https://example.com/v1 "),
			OpenAIVariant:   modelCatalogStringPtr(" openai "),
			TextOnlyContent: &textOnly,
			Timeout:         modelCatalogStringPtr("2s"),
			MaxRetries:      &maxRetries,
			Headers: map[string]string{
				" X-Test ": " value ",
			},
			Config: &rawYAMLNode{Node: &configNode},
		},
	)
	require.NoError(t, err)
	require.Equal(t, modeMock, opts.ModelMode)
	require.Equal(t, "model-name", opts.OpenAIModel)
	require.Equal(t, "https://example.com/v1", opts.OpenAIBaseURL)
	require.Equal(t, "openai", opts.OpenAIVariant)
	require.True(t, opts.OpenAITextOnlyMessageContent)
	require.Equal(t, 2*time.Second, opts.OpenAITimeout)
	require.Equal(t, 3, opts.OpenAIMaxRetries)
	require.True(t, opts.OpenAIMaxRetriesSet)
	require.True(t, opts.OpenAIUseVariantAPIKey)
	require.Equal(t, map[string]string{"X-Test": "value"}, opts.OpenAIHeaders)
	require.Same(t, &configNode, opts.ModelConfig)
	require.True(t, opts.DebugRecorderEnabled)

	_, err = modelRunOptionsForEntry(
		runOptions{},
		"negative-timeout",
		modelEntryConfig{Timeout: modelCatalogStringPtr("-1s")},
	)
	require.ErrorContains(t, err, "timeout must be >= 0")

	negativeRetries := -1
	_, err = modelRunOptionsForEntry(
		runOptions{},
		"negative-retries",
		modelEntryConfig{MaxRetries: &negativeRetries},
	)
	require.ErrorContains(t, err, "max_retries must be >= 0")
}

func TestResolveModelCatalog_InjectedCustomModeIgnoresOpenAIVariant(
	t *testing.T,
) {
	catalog, err := resolveModelCatalog(
		runOptions{},
		buildRuntimeOptions([]RuntimeOption{
			WithModelCatalog(ModelCatalog{
				Default: "custom",
				Models: map[string]model.Model{
					"custom": &echoModel{name: "custom-model"},
				},
				Metadata: map[string]ModelMetadata{
					"custom": {
						Mode:          modeMock,
						OpenAIVariant: "provider-specific",
					},
				},
			}),
		}),
	)
	require.NoError(t, err)
	require.Equal(t, modeMock, catalog.metadata["custom"].Mode)
	require.Equal(
		t,
		"provider-specific",
		catalog.metadata["custom"].OpenAIVariant,
	)
}

func TestResolveModelCatalog_InjectedMetadataCanClearInheritedBaseURL(
	t *testing.T,
) {
	t.Setenv("OPENAI_BASE_URL", "https://api.deepseek.com/v1")
	catalog, err := resolveModelCatalog(
		runOptions{
			ModelMode:     modeOpenAI,
			OpenAIVariant: openAIVariantAuto,
		},
		buildRuntimeOptions([]RuntimeOption{
			WithModelCatalog(ModelCatalog{
				Default: "openai",
				Models: map[string]model.Model{
					"openai": &echoModel{name: "openai-model"},
				},
				Metadata: map[string]ModelMetadata{
					"openai": {
						BaseURLSet:    true,
						OpenAIVariant: openAIVariantAuto,
					},
				},
			}),
		}),
	)
	require.NoError(t, err)
	require.Empty(t, catalog.metadata["openai"].BaseURL)

	selected := catalog.runOptionsForModel(runOptions{
		DeadlineFinalizationWindow: time.Minute,
	}, "openai")
	require.False(
		t,
		modelCallBudgetFinalRequestFromOptions(selected).DisableThinking,
	)
}

func TestModelMetadataFromRunOptions_UsesEnvironmentBaseURL(t *testing.T) {
	t.Setenv("OPENAI_BASE_URL", "https://api.deepseek.com/v1")
	metadata := modelMetadataFromRunOptions(runOptions{
		ModelMode:     modeOpenAI,
		OpenAIVariant: openAIVariantAuto,
	})
	require.Equal(
		t,
		"https://api.deepseek.com/v1",
		metadata.BaseURL,
	)

	cfg := modelCallBudgetFinalRequestFromOptions(runOptions{
		ModelMode:                  metadata.Mode,
		OpenAIBaseURL:              metadata.BaseURL,
		OpenAIVariant:              metadata.OpenAIVariant,
		DeadlineFinalizationWindow: time.Minute,
	})
	require.True(t, cfg.DisableThinking)
}

func TestParseRunOptions_ModelCatalog(t *testing.T) {
	cfgPath := writeTempConfig(t, `
model:
  default: strong
  generation_config:
    max_tokens: 256
  models:
    fast:
      mode: mock
      name: ignored
    strong:
      mode: mock
`)

	opts, err := parseRunOptions([]string{"-config", cfgPath})
	require.NoError(t, err)
	require.Equal(t, "strong", opts.ModelDefault)
	require.Len(t, opts.ModelEntries, 2)
	require.NotNil(t, opts.GenerationConfig)
	require.Equal(t, 256, *opts.GenerationConfig.MaxTokens)

	catalog, err := resolveModelCatalog(opts, runtimeOptions{})
	require.NoError(t, err)
	require.Equal(t, "strong", catalog.defaultName)
	require.Equal(t, []string{"fast", "strong"}, catalog.modelNames())
}

func TestResolveConfiguredModelCatalog_PreservesProviderMetadata(
	t *testing.T,
) {
	t.Setenv("OPENAI_API_KEY", "test-key")
	cfgPath := writeTempConfig(t, `
model:
  default: deepseek
  models:
    deepseek:
      mode: openai
      name: deepseek-chat
      openai_variant: deepseek
      base_url: https://api.deepseek.com/v1
    glm:
      mode: openai
      name: glm-4
      openai_variant: auto
      base_url: https://open.bigmodel.cn/api/paas/v4
`)

	opts, err := parseRunOptions([]string{"-config", cfgPath})
	require.NoError(t, err)
	catalog, err := resolveModelCatalog(opts, runtimeOptions{})
	require.NoError(t, err)
	require.Equal(t, "deepseek", catalog.defaultName)
	require.Equal(t, "deepseek", catalog.metadata["deepseek"].OpenAIVariant)
	require.Equal(
		t,
		"https://api.deepseek.com/v1",
		catalog.metadata["deepseek"].BaseURL,
	)
	require.Equal(t, "auto", catalog.metadata["glm"].OpenAIVariant)
	require.Equal(
		t,
		"https://open.bigmodel.cn/api/paas/v4",
		catalog.metadata["glm"].BaseURL,
	)
}

func TestConfiguredModelCatalogUsesVariantCredential(t *testing.T) {
	authorization := make(chan string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(
		w http.ResponseWriter,
		r *http.Request,
	) {
		authorization <- r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id":"chatcmpl-test",
			"object":"chat.completion",
			"created":123,
			"model":"deepseek-chat",
			"choices":[{
				"index":0,
				"message":{"role":"assistant","content":"ok"},
				"finish_reason":"stop"
			}]
		}`))
	}))
	defer server.Close()

	t.Setenv(openAIAPIKeyEnvName, "openai-key")
	t.Setenv(deepSeekAPIKeyEnvName, "deepseek-key")
	t.Setenv(openAIHeadersEnvName, "")
	entryOpts, err := modelRunOptionsForEntry(
		runOptions{},
		"deepseek",
		modelEntryConfig{
			Name:          modelCatalogStringPtr("deepseek-chat"),
			BaseURL:       modelCatalogStringPtr(server.URL),
			OpenAIVariant: modelCatalogStringPtr("deepseek"),
		},
	)
	require.NoError(t, err)
	require.True(t, entryOpts.OpenAIUseVariantAPIKey)
	mdl, err := modelFromOptions(entryOpts)
	require.NoError(t, err)

	ch, err := mdl.GenerateContent(context.Background(), &model.Request{
		Messages: []model.Message{model.NewUserMessage("hello")},
		GenerationConfig: model.GenerationConfig{
			Stream: false,
		},
	})
	require.NoError(t, err)
	for rsp := range ch {
		require.Nil(t, rsp.Error)
	}
	require.Equal(t, "Bearer deepseek-key", <-authorization)
}

func TestOpenAIAPIKeyFromOptionsSelectsVariantEnvironment(t *testing.T) {
	t.Setenv(openAIAPIKeyEnvName, "openai-key")
	t.Setenv(deepSeekAPIKeyEnvName, "deepseek-key")
	t.Setenv(qwenAPIKeyEnvName, "qwen-key")
	t.Setenv(miniMaxAPIKeyEnvName, "minimax-key")
	t.Setenv(kimiAPIKeyEnvName, "kimi-key")

	tests := []struct {
		name    string
		opts    runOptions
		wantKey string
	}{
		{
			name: "legacy keeps openai credential",
			opts: runOptions{
				ModelMode:     modeOpenAI,
				OpenAIVariant: "deepseek",
			},
			wantKey: "openai-key",
		},
		{
			name: "deepseek",
			opts: runOptions{
				ModelMode:              modeOpenAI,
				OpenAIVariant:          "deepseek",
				OpenAIUseVariantAPIKey: true,
			},
			wantKey: "deepseek-key",
		},
		{
			name: "qwen",
			opts: runOptions{
				ModelMode:              modeOpenAI,
				OpenAIVariant:          "qwen",
				OpenAIUseVariantAPIKey: true,
			},
			wantKey: "qwen-key",
		},
		{
			name: "minimax",
			opts: runOptions{
				ModelMode:              modeOpenAI,
				OpenAIVariant:          "minimax",
				OpenAIUseVariantAPIKey: true,
			},
			wantKey: "minimax-key",
		},
		{
			name: "kimi",
			opts: runOptions{
				ModelMode:              modeOpenAI,
				OpenAIVariant:          "kimi",
				OpenAIUseVariantAPIKey: true,
			},
			wantKey: "kimi-key",
		},
		{
			name: "auto infers deepseek",
			opts: runOptions{
				ModelMode:              modeOpenAI,
				OpenAIBaseURL:          "https://api.deepseek.com/v1",
				OpenAIVariant:          openAIVariantAuto,
				OpenAIUseVariantAPIKey: true,
			},
			wantKey: "deepseek-key",
		},
		{
			name: "openai variant",
			opts: runOptions{
				ModelMode:              modeOpenAI,
				OpenAIVariant:          "openai",
				OpenAIUseVariantAPIKey: true,
			},
			wantKey: "openai-key",
		},
		{
			name: "glm uses openai credential",
			opts: runOptions{
				ModelMode:              modeOpenAI,
				OpenAIVariant:          "glm",
				OpenAIUseVariantAPIKey: true,
			},
			wantKey: "openai-key",
		},
		{
			name: "hunyuan uses openai credential",
			opts: runOptions{
				ModelMode:              modeOpenAI,
				OpenAIVariant:          "hunyuan",
				OpenAIUseVariantAPIKey: true,
			},
			wantKey: "openai-key",
		},
		{
			name: "custom mode keeps legacy credential",
			opts: runOptions{
				ModelMode:              modeMock,
				OpenAIVariant:          "deepseek",
				OpenAIUseVariantAPIKey: true,
			},
			wantKey: "openai-key",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := openAIAPIKeyFromOptions(tt.opts)
			require.NoError(t, err)
			require.Equal(t, tt.wantKey, got)
		})
	}

	t.Run("provider credential falls back to openai", func(t *testing.T) {
		t.Setenv(deepSeekAPIKeyEnvName, "")
		got, err := openAIAPIKeyFromOptions(runOptions{
			ModelMode:              modeOpenAI,
			OpenAIVariant:          "deepseek",
			OpenAIUseVariantAPIKey: true,
		})
		require.NoError(t, err)
		require.Equal(t, "openai-key", got)
	})

	_, err := openAIAPIKeyFromOptions(runOptions{
		ModelMode:              modeOpenAI,
		OpenAIVariant:          "invalid",
		OpenAIUseVariantAPIKey: true,
	})
	require.ErrorContains(t, err, "unsupported openai variant")
}

func TestResolvedModelCatalog_ProviderBehaviorFollowsSelectedAlias(
	t *testing.T,
) {
	catalog := resolvedModelCatalog{
		defaultName: "openai",
		models: map[string]model.Model{
			"openai":   &echoModel{name: "openai-model"},
			"deepseek": &echoModel{name: "deepseek-model"},
			"glm":      &echoModel{name: "glm-model"},
		},
		metadata: map[string]ModelMetadata{
			"openai": {
				Mode:          modeOpenAI,
				OpenAIVariant: "openai",
			},
			"deepseek": {
				Mode:          modeOpenAI,
				OpenAIVariant: "deepseek",
			},
			"glm": {
				Mode:          modeOpenAI,
				OpenAIVariant: "auto",
				BaseURL:       "https://open.bigmodel.cn/api/paas/v4",
			},
		},
		explicit: true,
	}
	base := runOptions{DeadlineFinalizationWindow: time.Minute}

	require.Empty(
		t,
		modelCompatibilityRunOptions(
			catalog.runOptionsForModel(base, "deepseek"),
		),
	)
	require.NotEmpty(
		t,
		modelCompatibilityRunOptions(catalog.runOptionsForModel(base, "glm")),
	)
	require.True(
		t,
		modelCallBudgetFinalRequestFromOptions(
			catalog.runOptionsForModel(base, "deepseek"),
		).DisableThinking,
	)
	require.False(
		t,
		modelCallBudgetFinalRequestFromOptions(
			catalog.runOptionsForModel(base, "openai"),
		).DisableThinking,
	)

	profileCtx := runtimeprofile.WithProfile(
		context.Background(),
		runtimeprofile.Profile{ModelName: "deepseek"},
	)
	require.Equal(t, "openai", catalog.selectedModelName(
		context.Background(),
		"",
	))
	require.Equal(t, "deepseek", catalog.selectedModelName(profileCtx, ""))
	require.Equal(t, "glm", catalog.selectedModelName(profileCtx, "glm"))
}

func TestResolvedModelCatalogHelperFallbacks(t *testing.T) {
	base := runOptions{
		ModelMode:     modeMock,
		OpenAIModel:   "legacy",
		OpenAIVariant: "openai",
	}
	implicit := resolvedModelCatalog{
		defaultName: " default ",
		models: map[string]model.Model{
			"default": nil,
		},
	}
	require.Equal(t, base, implicit.runOptionsForModel(base, "missing"))
	require.Equal(t, base, implicit.adminRunOptions(base))
	require.Equal(t, "default", implicit.defaultAlias())
	require.Contains(t, implicit.identityParts(), "model=")

	explicitWithoutMetadata := implicit
	explicitWithoutMetadata.explicit = true
	adminOpts := explicitWithoutMetadata.adminRunOptions(base)
	require.Equal(t, "default", adminOpts.OpenAIModel)
	require.Equal(t, modeMock, adminOpts.ModelMode)
}

func TestAppendModelCatalogGatewayOptionsExplicitOnly(t *testing.T) {
	catalog := resolvedModelCatalog{
		defaultName: "fast",
		models: map[string]model.Model{
			"fast": &echoModel{name: "fast-model"},
		},
		metadata: map[string]ModelMetadata{
			"fast": {Mode: modeMock},
		},
	}
	require.Empty(t, appendModelCatalogGatewayOptions(nil, catalog))

	catalog.explicit = true
	require.Len(t, appendModelCatalogGatewayOptions(nil, catalog), 2)
	catalog.models = nil
	require.Empty(t, appendModelCatalogGatewayOptions(nil, catalog))
}

func TestAppendModelCatalogCallBudgetGatewayOptionUsesSelectedAlias(
	t *testing.T,
) {
	runner := &capturingRuntimeRunOptionRunner{reply: "ok"}
	catalog := resolvedModelCatalog{
		defaultName: "openai",
		models: map[string]model.Model{
			"openai":   &echoModel{name: "openai-model"},
			"deepseek": &echoModel{name: "deepseek-model"},
		},
		metadata: map[string]ModelMetadata{
			"openai": {
				Mode:          modeOpenAI,
				OpenAIVariant: "openai",
			},
			"deepseek": {
				Mode:          modeOpenAI,
				OpenAIVariant: "deepseek",
			},
		},
		explicit: true,
	}
	base := runOptions{DeadlineFinalizationWindow: time.Minute}
	gwOpts := appendModelCatalogCallBudgetGatewayOption(
		nil,
		base,
		catalog,
		0,
		false,
		time.Minute,
	)
	srv, err := gateway.New(runner, gwOpts...)
	require.NoError(t, err)

	_, status := srv.ProcessMessage(
		context.Background(),
		gwproto.MessageRequest{
			From:  "u1",
			Text:  "hello",
			Model: "deepseek",
		},
	)
	require.Equal(t, http.StatusOK, status)
	factory, ok := runner.opts.RuntimeState[modelCallBudgetRuntimeStateKey].(*modelCallBudgetFactory)
	require.True(t, ok)
	require.True(t, factory.finalRequest.DisableThinking)

	profileCtx := runtimeprofile.WithProfile(
		context.Background(),
		runtimeprofile.Profile{ModelName: "openai"},
	)
	_, status = srv.ProcessMessage(profileCtx, gwproto.MessageRequest{
		From: "u1",
		Text: "hello",
	})
	require.Equal(t, http.StatusOK, status)
	factory, ok = runner.opts.RuntimeState[modelCallBudgetRuntimeStateKey].(*modelCallBudgetFactory)
	require.True(t, ok)
	require.False(t, factory.finalRequest.DisableThinking)
}

func TestAppendModelCatalogCallBudgetGatewayOptionLegacyAndDisabled(
	t *testing.T,
) {
	require.Empty(t, appendModelCatalogCallBudgetGatewayOption(
		nil,
		runOptions{},
		resolvedModelCatalog{},
		0,
		false,
		0,
	))

	runner := &capturingRuntimeRunOptionRunner{reply: "ok"}
	gwOpts := appendModelCatalogCallBudgetGatewayOption(
		nil,
		runOptions{
			ModelMode:                  modeOpenAI,
			OpenAIVariant:              "deepseek",
			DeadlineFinalizationWindow: time.Minute,
		},
		resolvedModelCatalog{
			defaultName: "legacy",
			models: map[string]model.Model{
				"legacy": &echoModel{name: "legacy-model"},
			},
		},
		0,
		false,
		time.Minute,
	)
	srv, err := gateway.New(runner, gwOpts...)
	require.NoError(t, err)
	_, status := srv.ProcessMessage(
		context.Background(),
		gwproto.MessageRequest{From: "u1", Text: "hello"},
	)
	require.Equal(t, http.StatusOK, status)
	factory, ok := runner.opts.RuntimeState[modelCallBudgetRuntimeStateKey].(*modelCallBudgetFactory)
	require.True(t, ok)
	require.True(t, factory.finalRequest.DisableThinking)
}

func TestResolvedModelCatalog_AdminStartupAndIdentity(t *testing.T) {
	base := runOptions{
		ModelMode:     modeOpenAI,
		OpenAIModel:   "unrelated-legacy-default",
		OpenAIVariant: "openai",
	}
	catalog := resolvedModelCatalog{
		defaultName: "fast",
		models: map[string]model.Model{
			"fast":   &echoModel{name: "fast-model"},
			"strong": &echoModel{name: "strong-model"},
		},
		metadata: map[string]ModelMetadata{
			"fast": {
				Mode:          modeOpenAI,
				OpenAIVariant: "deepseek",
			},
			"strong": {
				Mode:          modeOpenAI,
				OpenAIVariant: "openai",
			},
		},
		explicit: true,
	}

	adminOpts := catalog.adminRunOptions(base)
	require.Equal(t, "fast", adminModelName(adminOpts, agentTypeLLM))
	require.Equal(t, "deepseek", adminOpts.OpenAIVariant)
	require.Equal(t, "fast (2 configured)", modelStartupSummary(
		base,
		true,
		catalog,
	))

	firstID := runtimeInstanceID(
		agentTypeLLM,
		base,
		true,
		"/state",
		catalog,
	)
	changed := catalog
	changed.metadata = map[string]ModelMetadata{
		"fast": {
			Mode:          modeOpenAI,
			OpenAIVariant: "glm",
		},
		"strong": catalog.metadata["strong"],
	}
	require.NotEqual(t, firstID, runtimeInstanceID(
		agentTypeLLM,
		base,
		true,
		"/state",
		changed,
	))
}

func TestRuntimeRunCatalogDefaultsFollowSelectedAlias(
	t *testing.T,
) {
	runner := &capturingRuntimeRunOptionRunner{reply: "ok"}
	rt := &Runtime{
		runner: runner,
		modelCatalog: resolvedModelCatalog{
			defaultName: "openai",
			models: map[string]model.Model{
				"openai":   &echoModel{name: "openai-model"},
				"deepseek": &echoModel{name: "deepseek-model"},
				"glm":      &echoModel{name: "glm-model"},
			},
			metadata: map[string]ModelMetadata{
				"openai": {
					Mode:          modeOpenAI,
					OpenAIVariant: "openai",
				},
				"deepseek": {
					Mode:          modeOpenAI,
					OpenAIVariant: "deepseek",
				},
				"glm": {
					Mode:          modeOpenAI,
					OpenAIVariant: "glm",
				},
			},
			explicit: true,
		},
		modelCallBudgetDeadlineWindow: time.Minute,
		modelRunOptions: runOptions{
			DeadlineFinalizationWindow: time.Minute,
		},
	}
	require.NotPanics(t, func() {
		rt.modelCatalogRunOption()(nil)
	})

	_, err := rt.Run(
		context.Background(),
		"u1",
		"default",
		model.NewUserMessage("hello"),
	)
	require.NoError(t, err)
	factory, ok := runner.opts.RuntimeState[modelCallBudgetRuntimeStateKey].(*modelCallBudgetFactory)
	require.True(t, ok)
	require.False(t, factory.finalRequest.DisableThinking)

	_, err = rt.Run(
		context.Background(),
		"u1",
		"deepseek",
		model.NewUserMessage("hello"),
		agent.WithModelName("deepseek"),
	)
	require.NoError(t, err)
	factory, ok = runner.opts.RuntimeState[modelCallBudgetRuntimeStateKey].(*modelCallBudgetFactory)
	require.True(t, ok)
	require.True(t, factory.finalRequest.DisableThinking)

	_, err = rt.Run(
		context.Background(),
		"u1",
		"glm",
		model.NewUserMessage("hello"),
		agent.WithModelName(" glm "),
	)
	require.NoError(t, err)
	require.Equal(t, "glm", runner.opts.ModelName)
	require.NotNil(t, runner.opts.ToolCallArgumentsJSONRepairEnabled)
	require.True(t, *runner.opts.ToolCallArgumentsJSONRepairEnabled)
	require.NotNil(t, runner.opts.ToolCallTextRepairEnabled)
	require.True(t, *runner.opts.ToolCallTextRepairEnabled)
}

func TestRuntimeRunCatalogExecutesCallerOptionsOnceAndPreservesOverrides(
	t *testing.T,
) {
	runner := &capturingRuntimeRunOptionRunner{reply: "ok"}
	rt := &Runtime{
		runner: runner,
		modelCatalog: resolvedModelCatalog{
			defaultName: "glm",
			models: map[string]model.Model{
				"glm": &echoModel{name: "glm-model"},
			},
			metadata: map[string]ModelMetadata{
				"glm": {
					Mode:          modeOpenAI,
					OpenAIVariant: "glm",
				},
			},
			explicit: true,
		},
		modelCallBudgetDeadlineWindow: time.Minute,
		modelRunOptions: runOptions{
			DeadlineFinalizationWindow: time.Minute,
		},
	}
	const callerBudget = "caller-budget"
	calls := 0
	callerOption := func(opts *agent.RunOptions) {
		calls++
		agent.MergeRuntimeState(map[string]any{
			modelCallBudgetRuntimeStateKey: callerBudget,
		})(opts)
		agent.WithToolCallArgumentsJSONRepairEnabled(false)(opts)
		agent.WithToolCallTextRepairEnabled(false)(opts)
	}

	_, err := rt.Run(
		context.Background(),
		"u1",
		"once",
		model.NewUserMessage("hello"),
		callerOption,
	)
	require.NoError(t, err)
	require.Equal(t, 1, calls)
	require.Equal(
		t,
		callerBudget,
		runner.opts.RuntimeState[modelCallBudgetRuntimeStateKey],
	)
	require.NotNil(t, runner.opts.ToolCallArgumentsJSONRepairEnabled)
	require.False(t, *runner.opts.ToolCallArgumentsJSONRepairEnabled)
	require.NotNil(t, runner.opts.ToolCallTextRepairEnabled)
	require.False(t, *runner.opts.ToolCallTextRepairEnabled)
}

func TestRuntimeRunCatalogRejectsUnknownModelName(t *testing.T) {
	runner := &capturingRuntimeRunOptionRunner{reply: "ok"}
	rt := &Runtime{
		runner: runner,
		modelCatalog: resolvedModelCatalog{
			defaultName: "fast",
			models: map[string]model.Model{
				"fast": &echoModel{name: "fast-model"},
			},
			metadata: map[string]ModelMetadata{
				"fast": {
					Mode:          modeOpenAI,
					OpenAIVariant: "glm",
				},
			},
			explicit: true,
		},
	}

	_, err := rt.Run(
		context.Background(),
		"u1",
		"unknown",
		model.NewUserMessage("hello"),
		agent.WithModelName("missing"),
	)
	require.NoError(t, err)
	require.Empty(t, runner.opts.ModelName)
	require.NotNil(t, runner.opts.ModelSelector)
	_, err = runner.opts.ModelSelector(
		context.Background(),
		&agent.Invocation{},
	)
	require.ErrorContains(t, err, `model "missing" is not configured`)

	custom := &echoModel{name: "custom-model"}
	_, err = rt.Run(
		context.Background(),
		"u1",
		"custom",
		model.NewUserMessage("hello"),
		agent.WithModelName("missing"),
		agent.WithModel(custom),
	)
	require.NoError(t, err)
	require.Same(t, custom, runner.opts.Model)
	require.Nil(t, runner.opts.ModelSelector)
	require.Nil(t, runner.opts.ToolCallArgumentsJSONRepairEnabled)
	require.Nil(t, runner.opts.ToolCallTextRepairEnabled)

	selectorCalled := false
	_, err = rt.Run(
		context.Background(),
		"u1",
		"conflict",
		model.NewUserMessage("hello"),
		agent.WithModelName("missing"),
		agent.WithModelSelector(func(
			context.Context,
			*agent.Invocation,
		) (model.Model, error) {
			selectorCalled = true
			return custom, nil
		}),
	)
	require.NoError(t, err)
	require.NotNil(t, runner.opts.ModelSelector)
	_, err = runner.opts.ModelSelector(
		context.Background(),
		&agent.Invocation{},
	)
	require.ErrorContains(
		t,
		err,
		`model "missing" is not configured and cannot be combined `+
			`with a custom model selector`,
	)
	require.False(t, selectorCalled)
}

func TestRuntimeRunCatalogCustomSelectorSkipsCompatibilityDefaults(
	t *testing.T,
) {
	runner := &capturingRuntimeRunOptionRunner{reply: "ok"}
	rt := &Runtime{
		runner: runner,
		modelCatalog: resolvedModelCatalog{
			defaultName: "glm",
			models: map[string]model.Model{
				"glm": &echoModel{name: "glm-model"},
			},
			metadata: map[string]ModelMetadata{
				"glm": {
					Mode:          modeOpenAI,
					OpenAIVariant: "glm",
				},
			},
			explicit: true,
		},
	}
	custom := &echoModel{name: "custom-model"}
	selector := func(
		context.Context,
		*agent.Invocation,
	) (model.Model, error) {
		return custom, nil
	}

	_, err := rt.Run(
		context.Background(),
		"u1",
		"selector",
		model.NewUserMessage("hello"),
		agent.WithModelSelector(selector),
	)
	require.NoError(t, err)
	require.NotNil(t, runner.opts.ModelSelector)
	selected, err := runner.opts.ModelSelector(
		context.Background(),
		&agent.Invocation{},
	)
	require.NoError(t, err)
	require.Same(t, custom, selected)
	require.Nil(t, runner.opts.ToolCallArgumentsJSONRepairEnabled)
	require.Nil(t, runner.opts.ToolCallTextRepairEnabled)
}

func TestParseRunOptions_ModelCatalogRequiresDefault(t *testing.T) {
	cfgPath := writeTempConfig(t, `
model:
  models:
    strong:
      mode: mock
`)

	_, err := parseRunOptions([]string{"-config", cfgPath})
	require.ErrorContains(t, err, "model.default is required")
}

func TestParseRunOptions_ModelDefaultRequiresModels(t *testing.T) {
	cfgPath := writeTempConfig(t, `
model:
  default: strong
`)
	_, err := parseRunOptions([]string{"-config", cfgPath})
	require.ErrorContains(t, err, "model.default requires model.models")
}

func TestModelCatalogConfigHelpers(t *testing.T) {
	require.False(t, modelConfigHasLegacyFields(nil))
	require.False(t, modelConfigHasLegacyFields(&modelConfig{}))
	require.True(t, modelConfigHasLegacyFields(&modelConfig{
		Mode: modelCatalogStringPtr(modeMock),
	}))
	require.False(t, modelFlagsWereSet(nil))
	require.True(t, modelFlagsWereSet(map[string]struct{}{
		"openai-variant": {},
	}))
	require.Nil(t, cloneModelEntryConfigs(nil))

	cloned := cloneModelEntryConfigs(map[string]modelEntryConfig{
		"fast": {
			Headers: map[string]string{
				" X-Test ": " value ",
				"X-Blank":  " ",
			},
		},
	})
	require.Equal(
		t,
		map[string]string{"X-Test": "value"},
		cloned["fast"].Headers,
	)
}

func TestParseRunOptions_ModelCatalogRejectsLegacyMix(t *testing.T) {
	cfgPath := writeTempConfig(t, `
model:
  name: legacy
  default: strong
  models:
    strong:
      mode: mock
`)

	_, err := parseRunOptions([]string{"-config", cfgPath})
	require.ErrorContains(t, err, "cannot be combined")
}

func TestResolveModelCatalog_RejectsYAMLAndRuntimeCatalog(t *testing.T) {
	_, err := resolveModelCatalog(
		runOptions{
			ModelDefault: "fast",
			ModelEntries: map[string]modelEntryConfig{
				"fast": {Mode: modelCatalogStringPtr(modeMock)},
			},
		},
		buildRuntimeOptions([]RuntimeOption{
			WithModelCatalog(ModelCatalog{
				Default: "fast",
				Models: map[string]model.Model{
					"fast": &echoModel{},
				},
			}),
		}),
	)
	require.ErrorContains(t, err, "cannot be combined")
}

func TestValidateModelCatalogAgentType(t *testing.T) {
	err := validateModelCatalogAgentType(
		agentTypeClaudeCode,
		runOptions{},
		buildRuntimeOptions([]RuntimeOption{
			WithModelCatalog(ModelCatalog{
				Default: "fast",
				Models: map[string]model.Model{
					"fast": &echoModel{},
				},
			}),
		}),
	)
	require.ErrorContains(t, err, `require agent-type "llm"`)
	require.NoError(t, validateModelCatalogAgentType(
		agentTypeLLM,
		runOptions{
			ModelEntries: map[string]modelEntryConfig{
				"fast": {},
			},
		},
		runtimeOptions{},
	))
}

func TestNewRuntime_ModelCatalogGatewaySelection(t *testing.T) {
	rt, err := NewRuntimeWithOptions(
		context.Background(),
		[]string{
			"-state-dir", t.TempDir(),
			"-admin-enabled=false",
			"-enable-openclaw-tools=false",
		},
		WithModelCatalog(ModelCatalog{
			Default: "fast",
			Models: map[string]model.Model{
				"fast": &catalogResponseModel{
					name:  "fast-model",
					reply: "fast reply",
				},
				"strong": &catalogResponseModel{
					name:  "strong-model",
					reply: "strong reply",
				},
			},
		}),
	)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, rt.Close())
	})

	client, err := gwclient.New(
		rt.Gateway.Handler,
		rt.Gateway.MessagesPath,
		rt.Gateway.CancelPath,
	)
	require.NoError(t, err)

	rsp, err := client.SendMessage(
		context.Background(),
		gwclient.MessageRequest{
			From:      "u1",
			SessionID: "session-strong",
			Text:      "hello",
			Model:     "strong",
		},
	)
	require.NoError(t, err)
	require.Equal(t, "strong reply", rsp.Reply)

	rsp, err = client.SendMessage(
		context.Background(),
		gwclient.MessageRequest{
			From:      "u1",
			SessionID: "session-default",
			Text:      "hello",
		},
	)
	require.NoError(t, err)
	require.Equal(t, "fast reply", rsp.Reply)

	rsp, err = client.SendMessage(
		context.Background(),
		gwclient.MessageRequest{
			From:      "u1",
			SessionID: "session-unknown",
			Text:      "hello",
			Model:     "unknown",
		},
	)
	require.Error(t, err)
	require.Equal(t, http.StatusBadRequest, rsp.StatusCode)
	require.NotNil(t, rsp.Error)
	require.Equal(t, "invalid_model", rsp.Error.Type)

	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for i, name := range []string{"fast", "strong"} {
		wg.Add(1)
		go func(i int, name string) {
			defer wg.Done()
			got, sendErr := client.SendMessage(
				context.Background(),
				gwclient.MessageRequest{
					From:      "u1",
					SessionID: fmt.Sprintf("concurrent-%d", i),
					Text:      "hello",
					Model:     name,
				},
			)
			if sendErr != nil {
				errs <- sendErr
				return
			}
			if got.Reply != name+" reply" {
				errs <- fmt.Errorf(
					"model %s returned %q",
					name,
					got.Reply,
				)
			}
		}(i, name)
	}
	wg.Wait()
	close(errs)
	for concurrentErr := range errs {
		require.NoError(t, concurrentErr)
	}
}

func TestNewRuntime_ModelCatalogDirectRunSelection(t *testing.T) {
	rt, err := NewRuntimeWithOptions(
		context.Background(),
		[]string{
			"-state-dir", t.TempDir(),
			"-admin-enabled=false",
			"-enable-openclaw-tools=false",
		},
		WithModelCatalog(ModelCatalog{
			Default: "fast",
			Models: map[string]model.Model{
				"fast": &catalogResponseModel{
					name:  "fast-model",
					reply: "fast reply",
				},
				"strong": &catalogResponseModel{
					name:  "strong-model",
					reply: "strong reply",
				},
			},
		}),
	)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, rt.Close())
	})

	events, err := rt.Run(
		context.Background(),
		"u1",
		"direct-strong",
		model.NewUserMessage("hello"),
		agent.WithModelName("strong"),
	)
	require.NoError(t, err)
	var reply string
	for evt := range events {
		if evt == nil || evt.Response == nil ||
			len(evt.Response.Choices) == 0 {
			continue
		}
		reply = evt.Response.Choices[0].Message.Content
	}
	require.Equal(t, "strong reply", reply)

	events, err = rt.Run(
		context.Background(),
		"u1",
		"direct-unknown",
		model.NewUserMessage("hello"),
		agent.WithModelName("missing"),
	)
	require.NoError(t, err)
	var runError string
	for evt := range events {
		if evt == nil || evt.Response == nil ||
			evt.Response.Error == nil {
			continue
		}
		runError = evt.Response.Error.Message
	}
	require.Contains(t, runError, `model "missing" is not configured`)
}

func TestNewRuntime_LegacyGatewayIgnoresRequestModel(t *testing.T) {
	cfgPath := writeTempConfig(t, `
runtime_profiles:
  profiles:
    legacy:
      model_name: formerly-ignored-profile-model
`)
	rt, err := NewRuntimeWithOptions(
		context.Background(),
		[]string{
			"-config", cfgPath,
			"-mode", modeMock,
			"-state-dir", t.TempDir(),
			"-admin-enabled=false",
			"-enable-openclaw-tools=false",
		},
	)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, rt.Close())
	})
	client, err := gwclient.New(
		rt.Gateway.Handler,
		rt.Gateway.MessagesPath,
		rt.Gateway.CancelPath,
	)
	require.NoError(t, err)
	profileExtension, err := json.Marshal(runtimeprofile.Extension{
		ProfileID: "legacy",
	})
	require.NoError(t, err)

	rsp, err := client.SendMessage(
		context.Background(),
		gwclient.MessageRequest{
			From:      "u1",
			SessionID: "legacy-model-field",
			Text:      "hello",
			Model:     "formerly-ignored",
			Extensions: map[string]json.RawMessage{
				runtimeprofile.ExtensionKey: profileExtension,
			},
		},
	)
	require.NoError(t, err)
	require.Contains(t, rsp.Reply, "hello")
}

func TestNewRuntime_RequestModelOverridesRuntimeProfile(t *testing.T) {
	cfgPath := writeTempConfig(t, `
runtime_profiles:
  profiles:
    premium:
      model_name: strong
`)
	rt, err := NewRuntimeWithOptions(
		context.Background(),
		[]string{
			"-config", cfgPath,
			"-state-dir", t.TempDir(),
			"-admin-enabled=false",
			"-enable-openclaw-tools=false",
		},
		WithModelCatalog(ModelCatalog{
			Default: "fast",
			Models: map[string]model.Model{
				"fast": &catalogResponseModel{
					name:  "fast-model",
					reply: "fast reply",
				},
				"strong": &catalogResponseModel{
					name:  "strong-model",
					reply: "strong reply",
				},
			},
		}),
	)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, rt.Close())
	})
	client, err := gwclient.New(
		rt.Gateway.Handler,
		rt.Gateway.MessagesPath,
		rt.Gateway.CancelPath,
	)
	require.NoError(t, err)

	profileExtension, err := json.Marshal(runtimeprofile.Extension{
		ProfileID: "premium",
	})
	require.NoError(t, err)
	baseRequest := gwclient.MessageRequest{
		From:      "u1",
		SessionID: "profile-default",
		Text:      "hello",
		Extensions: map[string]json.RawMessage{
			runtimeprofile.ExtensionKey: profileExtension,
		},
	}
	rsp, err := client.SendMessage(context.Background(), baseRequest)
	require.NoError(t, err)
	require.Equal(t, "strong reply", rsp.Reply)

	baseRequest.SessionID = "profile-overridden"
	baseRequest.Model = "fast"
	rsp, err = client.SendMessage(context.Background(), baseRequest)
	require.NoError(t, err)
	require.Equal(t, "fast reply", rsp.Reply)
}

func TestNewRuntime_DynamicRuntimeProfileModelValidation(t *testing.T) {
	rt, err := NewRuntimeWithOptions(
		context.Background(),
		[]string{
			"-state-dir", t.TempDir(),
			"-admin-enabled=false",
			"-enable-openclaw-tools=false",
		},
		WithModelCatalog(ModelCatalog{
			Default: "fast",
			Models: map[string]model.Model{
				"fast": &catalogResponseModel{
					name:  "fast-model",
					reply: "fast reply",
				},
			},
		}),
		WithRuntimeProfileResolver(
			runtimeprofile.ResolverFunc(func(
				context.Context,
				runtimeprofile.Request,
			) (runtimeprofile.Profile, error) {
				return runtimeprofile.Profile{
					ID:        "dynamic",
					ModelName: "unknown",
				}, nil
			}),
			false,
		),
	)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, rt.Close())
	})
	client, err := gwclient.New(
		rt.Gateway.Handler,
		rt.Gateway.MessagesPath,
		rt.Gateway.CancelPath,
	)
	require.NoError(t, err)

	_, err = client.SendMessage(
		context.Background(),
		gwclient.MessageRequest{
			From:      "u1",
			SessionID: "dynamic-profile-invalid",
			Text:      "hello",
		},
	)
	require.ErrorContains(
		t,
		err,
		`runtime profile model "unknown" is not configured`,
	)

	rsp, err := client.SendMessage(
		context.Background(),
		gwclient.MessageRequest{
			From:      "u1",
			SessionID: "dynamic-profile-overridden",
			Text:      "hello",
			Model:     "fast",
		},
	)
	require.NoError(t, err)
	require.Equal(t, "fast reply", rsp.Reply)
}

func TestNewRuntime_StaticRuntimeProfileModelValidation(t *testing.T) {
	cfgPath := writeTempConfig(t, `
runtime_profiles:
  profiles:
    premium:
      model_name: missing
`)
	_, err := NewRuntimeWithOptions(
		context.Background(),
		[]string{
			"-config", cfgPath,
			"-state-dir", t.TempDir(),
			"-admin-enabled=false",
			"-enable-openclaw-tools=false",
		},
		WithModelCatalog(ModelCatalog{
			Default: "fast",
			Models: map[string]model.Model{
				"fast": &catalogResponseModel{
					name:  "fast-model",
					reply: "fast reply",
				},
			},
		}),
	)
	require.ErrorContains(t, err, "model catalog validation failed")
	require.ErrorContains(t, err, `model_name "missing" is not configured`)
}

func TestNewRuntime_ModelCatalogRejectsClaudeCodeAgent(t *testing.T) {
	_, err := NewRuntimeWithOptions(
		context.Background(),
		[]string{
			"-agent-type", agentTypeClaudeCode,
			"-state-dir", t.TempDir(),
			"-admin-enabled=false",
		},
		WithModelCatalog(ModelCatalog{
			Default: "fast",
			Models: map[string]model.Model{
				"fast": &echoModel{name: "fast-model"},
			},
		}),
	)
	require.ErrorContains(t, err, `require agent-type "llm"`)
}

func TestValidateRuntimeProfileModelsRejectsUnknown(t *testing.T) {
	err := validateRuntimeProfileModels(
		agentTypeLLM,
		&runtimeprofile.Config{
			Profiles: map[string]runtimeprofile.Profile{
				"premium": {ModelName: "unknown"},
			},
		},
		resolvedModelCatalog{
			defaultName: "fast",
			models: map[string]model.Model{
				"fast": &echoModel{},
			},
			explicit: true,
		},
	)
	require.ErrorContains(t, err, `model_name "unknown" is not configured`)

	require.NoError(t, validateRuntimeProfileModels(
		agentTypeClaudeCode,
		&runtimeprofile.Config{
			Profiles: map[string]runtimeprofile.Profile{
				"premium": {ModelName: "unknown"},
			},
		},
		resolvedModelCatalog{
			defaultName: "fast",
			models: map[string]model.Model{
				"fast": &echoModel{},
			},
			explicit: true,
		},
	))

	require.NoError(t, validateRuntimeProfileModels(
		agentTypeLLM,
		&runtimeprofile.Config{
			Profiles: map[string]runtimeprofile.Profile{
				"legacy": {ModelName: "formerly-ignored"},
			},
		},
		resolvedModelCatalog{
			defaultName: "default",
			models: map[string]model.Model{
				"default": &echoModel{},
			},
		},
	))

	require.NoError(t, validateRuntimeProfileModels(
		agentTypeLLM,
		&runtimeprofile.Config{
			Profiles: map[string]runtimeprofile.Profile{
				"blank": {},
				"known": {ModelName: "fast"},
			},
		},
		resolvedModelCatalog{
			defaultName: "fast",
			models: map[string]model.Model{
				"fast": &echoModel{},
			},
			explicit: true,
		},
	))
}

func TestValidateResolvedRuntimeProfileModel(t *testing.T) {
	catalog := resolvedModelCatalog{
		defaultName: "default",
		models: map[string]model.Model{
			"default": &echoModel{},
		},
		explicit: true,
	}
	require.NoError(t, validateResolvedRuntimeProfileModel(
		context.Background(),
		catalog,
	))
	require.NoError(t, validateResolvedRuntimeProfileModel(
		runtimeprofile.WithProfile(
			context.Background(),
			runtimeprofile.Profile{},
		),
		catalog,
	))
	require.NoError(t, validateResolvedRuntimeProfileModel(
		runtimeprofile.WithProfile(
			context.Background(),
			runtimeprofile.Profile{ModelName: "default"},
		),
		catalog,
	))
	err := validateResolvedRuntimeProfileModel(
		runtimeprofile.WithProfile(
			context.Background(),
			runtimeprofile.Profile{ModelName: "missing"},
		),
		catalog,
	)
	require.ErrorContains(t, err, `model "missing" is not configured`)

	legacyCtx := runtimeprofile.WithProfile(
		context.Background(),
		runtimeprofile.Profile{ModelName: "formerly-ignored"},
	)
	require.NoError(t, validateResolvedRuntimeProfileModel(
		legacyCtx,
		resolvedModelCatalog{
			defaultName: "default",
			models: map[string]model.Model{
				"default": &echoModel{},
			},
		},
	))
}

func modelCatalogStringPtr(value string) *string {
	return &value
}
