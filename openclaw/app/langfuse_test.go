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
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/baggage"
	oteltrace "go.opentelemetry.io/otel/trace"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/internal/debugrecorder"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/internal/gateway"
	langfuseobs "trpc.group/trpc-go/trpc-agent-go/telemetry/langfuse"
)

func TestBuildLangfuseAdminStatus_UsesEnvFallbacks(t *testing.T) {
	t.Setenv(langfuseHostEnv, "127.0.0.1:3000")
	t.Setenv(langfuseInsecureEnv, "true")
	t.Setenv(langfuseInitProjectEnv, "local-dev")

	status := buildLangfuseAdminStatus(runOptions{
		LangfuseEnabled: true,
	})
	require.True(t, status.Enabled)
	require.Equal(t, "http://127.0.0.1:3000", status.UIBaseURL)
	require.Equal(
		t,
		"http://127.0.0.1:3000/project/local-dev/traces/{{trace_id}}",
		status.TraceURLTemplate,
	)
}

func TestMaybeEnableLangfuse_OptionalFailureReturnsStatus(t *testing.T) {
	restore := langfuseStart
	t.Cleanup(func() {
		langfuseStart = restore
	})
	langfuseStart = func(
		context.Context,
		...langfuseobs.Option,
	) (func(context.Context) error, error) {
		return nil, errors.New("boom")
	}

	rt, err := maybeEnableLangfuse(context.Background(), runOptions{
		LangfuseEnabled: true,
	})
	require.NoError(t, err)
	require.NotNil(t, rt)
	require.True(t, rt.adminStatus.Enabled)
	require.False(t, rt.adminStatus.Ready)
	require.Equal(t, "boom", rt.adminStatus.Error)
	require.Nil(t, rt.runOptionResolver)
}

func TestMaybeEnableLangfuse_RequiredFailureReturnsError(t *testing.T) {
	restore := langfuseStart
	t.Cleanup(func() {
		langfuseStart = restore
	})
	langfuseStart = func(
		context.Context,
		...langfuseobs.Option,
	) (func(context.Context) error, error) {
		return nil, errors.New("boom")
	}

	_, err := maybeEnableLangfuse(context.Background(), runOptions{
		LangfuseEnabled:  true,
		LangfuseRequired: true,
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "boom")
}

func TestMaybeEnableLangfuse_SuccessBuildsResolver(t *testing.T) {
	restore := langfuseStart
	t.Cleanup(func() {
		langfuseStart = restore
	})
	langfuseStart = func(
		context.Context,
		...langfuseobs.Option,
	) (func(context.Context) error, error) {
		return func(context.Context) error { return nil }, nil
	}

	rt, err := maybeEnableLangfuse(context.Background(), runOptions{
		AppName:         "openclaw",
		LangfuseEnabled: true,
	})
	require.NoError(t, err)
	require.NotNil(t, rt)
	require.True(t, rt.adminStatus.Enabled)
	require.True(t, rt.adminStatus.Ready)
	require.NotNil(t, rt.runOptionResolver)
	require.NotNil(t, rt.shutdown)
}

func TestMaybeEnableLangfuse_DisabledReturnsStatusOnly(t *testing.T) {
	t.Parallel()

	rt, err := maybeEnableLangfuse(context.Background(), runOptions{
		LangfuseEnabled: false,
	})
	require.NoError(t, err)
	require.NotNil(t, rt)
	require.False(t, rt.adminStatus.Enabled)
	require.False(t, rt.adminStatus.Ready)
	require.Nil(t, rt.runOptionResolver)
	require.Nil(t, rt.shutdown)
}

func TestResolvedLangfuseUIBaseURL(t *testing.T) {
	t.Run("uses configured base url", func(t *testing.T) {
		t.Setenv(langfuseHostEnv, "")

		got := resolvedLangfuseUIBaseURL(runOptions{
			LangfuseUIBaseURL: " http://127.0.0.1:3000/ ",
		})
		require.Equal(t, "http://127.0.0.1:3000", got)
	})

	t.Run("defaults to https", func(t *testing.T) {
		t.Setenv(langfuseHostEnv, "langfuse.example.com")
		t.Setenv(langfuseInsecureEnv, "")

		got := resolvedLangfuseUIBaseURL(runOptions{})
		require.Equal(t, "https://langfuse.example.com", got)
	})

	t.Run("keeps explicit scheme", func(t *testing.T) {
		t.Setenv(langfuseHostEnv, "https://langfuse.example.com/")

		got := resolvedLangfuseUIBaseURL(runOptions{})
		require.Equal(t, "https://langfuse.example.com", got)
	})

	t.Run("empty without host", func(t *testing.T) {
		t.Setenv(langfuseHostEnv, "")

		got := resolvedLangfuseUIBaseURL(runOptions{})
		require.Empty(t, got)
	})
}

func TestResolvedLangfuseTraceURLTemplate(t *testing.T) {
	t.Run("uses configured template", func(t *testing.T) {
		got := resolvedLangfuseTraceURLTemplate(
			runOptions{
				LangfuseTraceURLTemplate: " http://ui/traces/{{trace_id}} ",
			},
			"http://ignored",
		)
		require.Equal(t, "http://ui/traces/{{trace_id}}", got)
	})

	t.Run("builds from ui base url and project", func(t *testing.T) {
		t.Setenv(langfuseInitProjectEnv, "local-dev")

		got := resolvedLangfuseTraceURLTemplate(
			runOptions{},
			"http://127.0.0.1:3000",
		)
		require.Equal(
			t,
			"http://127.0.0.1:3000/project/local-dev/traces/{{trace_id}}",
			got,
		)
	})

	t.Run("empty without project", func(t *testing.T) {
		t.Setenv(langfuseInitProjectEnv, "")

		got := resolvedLangfuseTraceURLTemplate(
			runOptions{},
			"http://127.0.0.1:3000",
		)
		require.Empty(t, got)
	})
}

func TestBuildLangfuseRunOptionResolver_SetsTraceID(t *testing.T) {
	rec, err := debugrecorder.New(t.TempDir(), "")
	require.NoError(t, err)

	trace, err := rec.Start(debugrecorder.TraceStart{
		Channel:   "wecom",
		SessionID: "s1",
		RequestID: "req-1",
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, trace.Close(debugrecorder.TraceEnd{
			Status: "ok",
		}))
	})

	resolver := buildLangfuseRunOptionResolver(runOptions{
		AppName: "openclaw",
	})
	ctx, runOpts := resolver(context.Background(), gateway.RunOptionInput{
		Inbound: gateway.InboundMessage{
			Channel:   "wecom",
			MessageID: "msg-1",
		},
		UserID:    "u1",
		SessionID: "s1",
		RequestID: "req-1",
		Trace:     trace,
	})

	bag := baggage.FromContext(ctx)
	require.Equal(t, "u1", bag.Member(langfuseUserIDKey).Value())
	require.Equal(
		t,
		"s1",
		bag.Member(langfuseSessionIDKey).Value(),
	)
	require.Equal(
		t,
		"openclaw",
		bag.Member(langfuseMetadataAppName).Value(),
	)
	require.Equal(
		t,
		"wecom",
		bag.Member(langfuseMetadataChannel).Value(),
	)
	require.Equal(
		t,
		"req-1",
		bag.Member(langfuseMetadataRequestID).Value(),
	)
	require.Equal(
		t,
		"msg-1",
		bag.Member(langfuseMetadataMessageID).Value(),
	)

	opts := &agent.RunOptions{}
	for _, opt := range runOpts {
		opt(opts)
	}
	foundTraceName := false
	for _, attr := range opts.SpanAttributes {
		if string(attr.Key) != langfuseTraceNameKey {
			continue
		}
		require.Equal(t, "wecom msg-1", attr.Value.AsString())
		foundTraceName = true
	}
	require.True(t, foundTraceName)
	require.NotEmpty(t, opts.TraceStartedCallbacks)

	traceID, err := oteltrace.TraceIDFromHex(
		"0123456789abcdef0123456789abcdef",
	)
	require.NoError(t, err)
	spanID, err := oteltrace.SpanIDFromHex("0123456789abcdef")
	require.NoError(t, err)
	for _, callback := range opts.TraceStartedCallbacks {
		callback(oteltrace.NewSpanContext(
			oteltrace.SpanContextConfig{
				TraceID:    traceID,
				SpanID:     spanID,
				TraceFlags: oteltrace.FlagsSampled,
			},
		))
	}

	metaPath := filepath.Join(trace.Dir(), "meta.json")
	metaRaw, err := os.ReadFile(metaPath)
	require.NoError(t, err)

	var meta map[string]any
	require.NoError(t, json.Unmarshal(metaRaw, &meta))
	require.Equal(
		t,
		traceID.String(),
		meta["trace_id"],
	)

	refPaths, err := filepath.Glob(
		filepath.Join(
			rec.Dir(),
			"by-session",
			"s1",
			"*",
			"*",
			"trace.json",
		),
	)
	require.NoError(t, err)
	require.Len(t, refPaths, 1)

	refRaw, err := os.ReadFile(refPaths[0])
	require.NoError(t, err)

	var ref map[string]any
	require.NoError(t, json.Unmarshal(refRaw, &ref))
	require.Equal(
		t,
		traceID.String(),
		ref["trace_id"],
	)
}

func TestBuildLangfuseRunOptionResolver_WithoutTraceSkipsCallback(
	t *testing.T,
) {
	t.Parallel()

	resolver := buildLangfuseRunOptionResolver(runOptions{
		AppName: "openclaw",
	})

	ctx, runOpts := resolver(context.Background(), gateway.RunOptionInput{
		Inbound: gateway.InboundMessage{
			Channel: "wecom",
		},
		UserID:    "u1",
		SessionID: "s1",
		RequestID: "req-1",
	})

	require.NotNil(t, ctx)

	opts := &agent.RunOptions{}
	for _, opt := range runOpts {
		opt(opts)
	}
	require.Len(t, opts.SpanAttributes, 1)
	require.Empty(t, opts.TraceStartedCallbacks)
}

func TestBuildLangfuseRunOptionResolver_InvalidSpanContext(
	t *testing.T,
) {
	rec, err := debugrecorder.New(t.TempDir(), "")
	require.NoError(t, err)

	trace, err := rec.Start(debugrecorder.TraceStart{
		Channel:   "wecom",
		SessionID: "s1",
		RequestID: "req-1",
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, trace.Close(debugrecorder.TraceEnd{
			Status: "ok",
		}))
	})

	resolver := buildLangfuseRunOptionResolver(runOptions{
		AppName: "openclaw",
	})
	_, runOpts := resolver(context.Background(), gateway.RunOptionInput{
		Inbound: gateway.InboundMessage{
			Channel: "wecom",
		},
		SessionID: "s1",
		RequestID: "req-1",
		Trace:     trace,
	})

	opts := &agent.RunOptions{}
	for _, opt := range runOpts {
		opt(opts)
	}
	require.Len(t, opts.TraceStartedCallbacks, 1)

	opts.TraceStartedCallbacks[0](oteltrace.SpanContext{})

	metaRaw, err := os.ReadFile(filepath.Join(trace.Dir(), "meta.json"))
	require.NoError(t, err)
	require.NotContains(t, string(metaRaw), "\"trace_id\"")
}

func TestBuildLangfuseTraceName_Fallbacks(t *testing.T) {
	t.Parallel()

	require.Equal(
		t,
		"wecom req-1",
		buildLangfuseTraceName("", gateway.RunOptionInput{
			Inbound:   gateway.InboundMessage{Channel: "wecom"},
			RequestID: "req-1",
		}),
	)
	require.Equal(
		t,
		"wecom request",
		buildLangfuseTraceName("", gateway.RunOptionInput{
			Inbound: gateway.InboundMessage{Channel: "wecom"},
		}),
	)
	require.Equal(
		t,
		"custom-app request",
		buildLangfuseTraceName("custom-app", gateway.RunOptionInput{}),
	)
}

func TestSetLangfuseBaggageMember_IgnoresEmptyInput(t *testing.T) {
	t.Parallel()

	bag, err := baggage.New()
	require.NoError(t, err)
	bag = setLangfuseBaggageMember(bag, "", "value")
	require.Empty(t, bag.Members())

	bag = setLangfuseBaggageMember(bag, "langfuse.user.id", "")
	require.Empty(t, bag.Members())
}
