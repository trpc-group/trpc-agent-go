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
	require.Len(t, opts.SpanAttributes, 1)
	require.Equal(
		t,
		langfuseTraceNameKey,
		string(opts.SpanAttributes[0].Key),
	)
	require.Equal(t, "wecom msg-1", opts.SpanAttributes[0].Value.AsString())
	require.Len(t, opts.TraceStartedCallbacks, 1)

	traceID, err := oteltrace.TraceIDFromHex(
		"0123456789abcdef0123456789abcdef",
	)
	require.NoError(t, err)
	spanID, err := oteltrace.SpanIDFromHex("0123456789abcdef")
	require.NoError(t, err)
	opts.TraceStartedCallbacks[0](oteltrace.NewSpanContext(
		oteltrace.SpanContextConfig{
			TraceID:    traceID,
			SpanID:     spanID,
			TraceFlags: oteltrace.FlagsSampled,
		},
	))

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
