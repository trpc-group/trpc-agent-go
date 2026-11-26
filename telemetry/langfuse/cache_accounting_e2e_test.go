//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package langfuse

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"

	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	itelemetry "trpc.group/trpc-go/trpc-agent-go/internal/telemetry"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/failover"
	"trpc.group/trpc-go/trpc-agent-go/model/hedge"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/telemetry/trace"
	"trpc.group/trpc-go/trpc-agent-go/telemetry/tracetransform"
)

// exclusiveUsage builds Usage the way the Anthropic and Bedrock adapters do:
// PromptTokens excludes cache writes and the value declares exclusive
// accounting.
func exclusiveUsage() *model.Usage {
	u := &model.Usage{
		PromptTokens:        100,
		CompletionTokens:    11,
		TotalTokens:         111,
		PromptTokensDetails: model.PromptTokensDetails{CacheCreationTokens: 80},
	}
	u.PromptCacheAccounting = model.PromptCacheAccountingExclusive
	return u
}

// staticModel returns a fixed response, standing in for an exclusive provider.
type staticModel struct {
	name  string
	build func() *model.Response
}

func (m *staticModel) Info() model.Info { return model.Info{Name: m.name} }

func (m *staticModel) GenerateContent(context.Context, *model.Request) (<-chan *model.Response, error) {
	ch := make(chan *model.Response, 1)
	ch <- m.build()
	close(ch)
	return ch, nil
}

// compositeModel obtains an exclusive response, runs an auxiliary inclusive
// call whose response is discarded, and returns the exclusive response.
type compositeModel struct{ name string }

func (m *compositeModel) Info() model.Info { return model.Info{Name: m.name} }

func (m *compositeModel) GenerateContent(context.Context, *model.Request) (<-chan *model.Response, error) {
	respA := &model.Response{
		ID:      "resp-a-exclusive",
		Model:   "claude-fallback",
		Done:    true,
		Choices: []model.Choice{{Message: model.NewAssistantMessage("from-a")}},
		Usage:   exclusiveUsage(),
	}
	aux := &model.Usage{PromptTokens: 20, PromptTokensDetails: model.PromptTokensDetails{CachedTokens: 10}}
	aux.PromptCacheAccounting = model.PromptCacheAccountingInclusive
	_ = aux
	ch := make(chan *model.Response, 1)
	ch <- respA
	close(ch)
	return ch, nil
}

func useLangfuseTestSpanRecorder(t *testing.T) *tracetest.SpanRecorder {
	t.Helper()
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	origProvider := trace.TracerProvider
	origTracer := trace.Tracer
	trace.TracerProvider = provider
	trace.Tracer = provider.Tracer("langfuse-e2e-cache-accounting-test")
	t.Cleanup(func() {
		_ = provider.Shutdown(context.Background())
		trace.TracerProvider = origProvider
		trace.Tracer = origTracer
	})
	return recorder
}

func extractChatUsageDetails(t *testing.T, spans []sdktrace.ReadOnlySpan, modelName string) map[string]int64 {
	t.Helper()
	wantName := itelemetry.NewChatSpanName(modelName)
	var chatSpan *tracepb.Span
	for _, rs := range transform(tracetransform.Spans(spans)) {
		for _, ss := range rs.GetScopeSpans() {
			for _, sp := range ss.GetSpans() {
				if sp.GetName() == wantName {
					chatSpan = sp
				}
			}
		}
	}
	require.NotNil(t, chatSpan, "expected transformed chat span %q", wantName)
	for _, attr := range chatSpan.GetAttributes() {
		if attr.GetKey() == observationUsageDetails {
			var usage map[string]int64
			require.NoError(t, json.Unmarshal([]byte(attr.GetValue().GetStringValue()), &usage))
			return usage
		}
	}
	t.Fatalf("usage details missing on %q", wantName)
	return nil
}

// TestRunnerToLangfuse_PromptCacheAccounting drives Runner -> LLMAgent ->
// OpenAI adapter -> chat span -> Langfuse transform. The OpenAI response
// reports prompt=100 (inclusive), cache_write=80, output=10.
func TestRunnerToLangfuse_PromptCacheAccounting(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id": "chatcmpl-openai-orig",
			"object": "chat.completion",
			"created": 1700000000,
			"model": "gpt-5",
			"choices": [{"index": 0, "message": {"role": "assistant", "content": "orig"}, "finish_reason": "stop"}],
			"usage": {"prompt_tokens": 100, "completion_tokens": 10, "total_tokens": 110,
				"prompt_tokens_details": {"cache_write_tokens": 80}}
		}`))
	}))
	defer server.Close()

	run := func(t *testing.T, callbacks *model.Callbacks) map[string]int64 {
		t.Helper()
		recorder := useLangfuseTestSpanRecorder(t)
		opts := []llmagent.Option{llmagent.WithModel(openai.New(
			"gpt-5", openai.WithAPIKey("test-key"), openai.WithBaseURL(server.URL),
		))}
		if callbacks != nil {
			opts = append(opts, llmagent.WithModelCallbacks(callbacks))
		}
		r := runner.NewRunner("test-app", llmagent.New("test-agent", opts...))
		defer r.Close()
		events, err := r.Run(context.Background(), "user-1", "session-1", model.NewUserMessage("hi"))
		require.NoError(t, err)
		for range events {
		}
		return extractChatUsageDetails(t, recorder.Ended(), "gpt-5")
	}
	afterModel := func(fn func(ctx context.Context, args *model.AfterModelArgs) *model.Response) *model.Callbacks {
		return model.NewCallbacks().RegisterAfterModel(
			func(ctx context.Context, args *model.AfterModelArgs) (*model.AfterModelResult, error) {
				return &model.AfterModelResult{CustomResponse: fn(ctx, args)}, nil
			},
		)
	}
	inclusive110 := map[string]int64{"input": 20, "output": 10, "input_cache_creation": 80}
	inclusive111 := map[string]int64{"input": 20, "output": 11, "input_cache_creation": 80}
	exclusive191 := map[string]int64{"input": 100, "output": 11, "input_cache_creation": 80}

	t.Run("inclusive write-only usage is not double counted", func(t *testing.T) {
		require.Equal(t, inclusive110, run(t, nil))
	})

	t.Run("in-place output-only edit keeps inclusive", func(t *testing.T) {
		require.Equal(t, inclusive111, run(t, afterModel(func(_ context.Context, args *model.AfterModelArgs) *model.Response {
			args.Response.Usage.CompletionTokens = 11
			args.Response.Usage.TotalTokens = 111
			return nil
		})))
	})

	t.Run("JSON output-only edit keeps inclusive", func(t *testing.T) {
		require.Equal(t, inclusive111, run(t, afterModel(func(_ context.Context, args *model.AfterModelArgs) *model.Response {
			require.NoError(t, json.Unmarshal([]byte(`{"usage":{"completion_tokens":11,"total_tokens":111}}`), args.Response))
			return nil
		})))
	})

	t.Run("output-only edit on shallow copy followed by Clone keeps inclusive", func(t *testing.T) {
		require.Equal(t, inclusive111, run(t, afterModel(func(_ context.Context, args *model.AfterModelArgs) *model.Response {
			copied := *args.Response
			copied.Choices = []model.Choice{{Message: model.NewAssistantMessage("edited")}}
			replacement := copied.Clone()
			replacement.Usage.CompletionTokens = 11
			replacement.Usage.TotalTokens = 111
			return replacement
		})))
	})

	t.Run("cross-provider replacement with identical counts uses exclusive", func(t *testing.T) {
		fallback := &staticModel{name: "claude-fallback", build: func() *model.Response {
			return &model.Response{
				ID: "resp-exclusive", Model: "claude-fallback", Done: true,
				Choices: []model.Choice{{Message: model.NewAssistantMessage("fallback")}},
				Usage:   exclusiveUsage(),
			}
		}}
		require.Equal(t, exclusive191, run(t, afterModel(func(ctx context.Context, args *model.AfterModelArgs) *model.Response {
			ch, err := fallback.GenerateContent(ctx, args.Request)
			require.NoError(t, err)
			var replaced *model.Response
			for rsp := range ch {
				replaced = rsp
			}
			return replaced
		})))
	})

	t.Run("in-place value overwrite with exclusive usage uses exclusive", func(t *testing.T) {
		require.Equal(t, exclusive191, run(t, afterModel(func(_ context.Context, args *model.AfterModelArgs) *model.Response {
			*args.Response.Usage = *exclusiveUsage()
			return nil
		})))
	})

	t.Run("Usage pointer reassignment takes the assigned usage accounting", func(t *testing.T) {
		require.Equal(t, exclusive191, run(t, afterModel(func(_ context.Context, args *model.AfterModelArgs) *model.Response {
			args.Response.Usage = exclusiveUsage()
			return nil
		})))
	})

	t.Run("user-built usage falls back to legacy normalization", func(t *testing.T) {
		require.Equal(t, exclusive191, run(t, afterModel(func(_ context.Context, args *model.AfterModelArgs) *model.Response {
			args.Response.Usage = &model.Usage{
				PromptTokens:        100,
				CompletionTokens:    11,
				TotalTokens:         111,
				PromptTokensDetails: model.PromptTokensDetails{CacheCreationTokens: 80},
			}
			return nil
		})))
	})

	for _, tc := range []struct {
		name   string
		decode func([]byte, *model.Response) error
		want   map[string]int64
	}{
		{
			name: "JSON replacement decoded into a fresh response has no declaration",
			decode: func(data []byte, dst *model.Response) error {
				var next model.Response
				if err := json.Unmarshal(data, &next); err != nil {
					return err
				}
				*dst = next
				return nil
			},
			want: exclusive191,
		},
		{
			name: "explicit reset before JSON replacement clears accounting",
			decode: func(data []byte, dst *model.Response) error {
				dst.Usage.PromptCacheAccounting = model.PromptCacheAccountingUnspecified
				return json.Unmarshal(data, dst)
			},
			want: exclusive191,
		},
		{
			name: "in-place JSON decoding preserves the existing declaration",
			decode: func(data []byte, dst *model.Response) error {
				return json.Unmarshal(data, dst)
			},
			want: inclusive111,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, run(t, afterModel(func(_ context.Context, args *model.AfterModelArgs) *model.Response {
				replacement := &model.Response{
					ID: "json-replacement", Model: "gpt-5", Done: true,
					Choices: []model.Choice{{Message: model.NewAssistantMessage("replacement")}},
					Usage: &model.Usage{PromptTokens: 100, CompletionTokens: 11, TotalTokens: 111,
						PromptTokensDetails: model.PromptTokensDetails{CacheCreationTokens: 80}},
				}
				data, err := json.Marshal(replacement)
				require.NoError(t, err)
				require.NoError(t, tc.decode(data, args.Response))
				return nil
			})))
		})
	}

	t.Run("user-built cached and creation usage preserves legacy buckets", func(t *testing.T) {
		want := map[string]int64{"input": 100, "output": 11, "input_cache_creation": 10}
		require.Equal(t, want, run(t, afterModel(func(_ context.Context, args *model.AfterModelArgs) *model.Response {
			args.Response.Usage = &model.Usage{
				PromptTokens:     100,
				CompletionTokens: 11,
				TotalTokens:      111,
				PromptTokensDetails: model.PromptTokensDetails{
					CachedTokens:        80,
					CacheCreationTokens: 10,
				},
			}
			return nil
		})))
	})

	t.Run("composite candidate keeps the returned response accounting directly and via failover/hedge", func(t *testing.T) {
		failoverWrapped, err := failover.New(failover.WithCandidates(&compositeModel{name: "composite"}))
		require.NoError(t, err)
		hedgeWrapped, err := hedge.New(hedge.WithCandidates(&compositeModel{name: "composite"}))
		require.NoError(t, err)
		for _, tc := range []struct {
			name      string
			candidate model.Model
		}{
			{name: "direct", candidate: &compositeModel{name: "composite"}},
			{name: "failover", candidate: failoverWrapped},
			{name: "hedge", candidate: hedgeWrapped},
		} {
			t.Run(tc.name, func(t *testing.T) {
				require.Equal(t, exclusive191, run(t, afterModel(func(ctx context.Context, args *model.AfterModelArgs) *model.Response {
					ch, err := tc.candidate.GenerateContent(ctx, args.Request)
					require.NoError(t, err)
					var replaced *model.Response
					for rsp := range ch {
						replaced = rsp
					}
					return replaced
				})))
			})
		}
	})
}

// cacheUsageStreamModel emits an inclusive snapshot before its final response,
// as streaming providers can do when reporting cumulative usage.
type cacheUsageStreamModel struct{}

func (*cacheUsageStreamModel) Info() model.Info { return model.Info{Name: "cache-usage-stream"} }

func (m *cacheUsageStreamModel) GenerateContent(context.Context, *model.Request) (<-chan *model.Response, error) {
	usage := &model.Usage{
		PromptTokens: 100, CompletionTokens: 10, TotalTokens: 110,
		PromptTokensDetails: model.PromptTokensDetails{CacheCreationTokens: 80},
	}
	usage.PromptCacheAccounting = model.PromptCacheAccountingInclusive
	partial := &model.Response{
		ID: "stream", Model: m.Info().Name, IsPartial: true, Usage: usage,
		Choices: []model.Choice{{Delta: model.NewAssistantMessage("hello")}},
	}
	final := partial.Clone()
	final.IsPartial, final.Done = false, true
	final.Choices = []model.Choice{{Message: model.NewAssistantMessage("hello")}}
	ch := make(chan *model.Response, 2)
	ch <- partial
	ch <- final
	close(ch)
	return ch, nil
}

func TestRunnerToLangfuse_StreamingCacheRemoval(t *testing.T) {
	recorder := useLangfuseTestSpanRecorder(t)
	callbacks := model.NewCallbacks().RegisterAfterModel(
		func(_ context.Context, args *model.AfterModelArgs) (*model.AfterModelResult, error) {
			if args.Response.Done {
				args.Response.Usage = &model.Usage{PromptTokens: 100, CompletionTokens: 11, TotalTokens: 111}
			}
			return nil, nil
		},
	)
	r := runner.NewRunner("test-app", llmagent.New("test-agent",
		llmagent.WithModel(&cacheUsageStreamModel{}),
		llmagent.WithModelCallbacks(callbacks),
		llmagent.WithGenerationConfig(model.GenerationConfig{Stream: true}),
	))
	defer r.Close()
	events, err := r.Run(context.Background(), "user-1", "session-1", model.NewUserMessage("hi"))
	require.NoError(t, err)
	for ev := range events {
		require.Nil(t, ev.Error)
	}
	require.Equal(t, map[string]int64{"input": 100, "output": 11},
		extractChatUsageDetails(t, recorder.Ended(), "cache-usage-stream"))
}
