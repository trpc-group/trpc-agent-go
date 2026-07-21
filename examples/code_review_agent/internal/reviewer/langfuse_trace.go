//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package reviewer

import (
	"context"
	"flag"
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"
	"time"

	agentpkg "trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/telemetry/langfuse"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/baggage"
)

var (
	langfusePublicKey = flag.String("langfuse-public-key", os.Getenv("LANGFUSE_PUBLIC_KEY"), "Langfuse public key")
	langfuseSecretKey = flag.String("langfuse-secret-key", os.Getenv("LANGFUSE_SECRET_KEY"), "Langfuse secret key")
	langfuseBaseURL   = flag.String("langfuse-base-url", os.Getenv("LANGFUSE_BASE_URL"), "Langfuse base URL")
	langfuseHost      = flag.String("langfuse-host", os.Getenv("LANGFUSE_HOST"), "Langfuse host endpoint")
	langfuseInsecure  = flag.Bool("langfuse-insecure", false, "Use insecure Langfuse transport")
)

// The SDK's batch processor permits a single export to take up to 30 seconds.
// Shutdown needs a longer but still finite window so a final batch can finish
// or retry instead of silently dropping the Agent's last model and tool spans.
const langfuseCleanupTimeout = time.Minute

func setupLangfuseRun(
	ctx context.Context,
	userID string,
	sessionID string,
	sandbox string,
	input string,
) (
	runContext context.Context,
	runOptions []agentpkg.RunOption,
	cleanup func(context.Context) error,
	err error,
) {
	clean, enabled, err := startLangfuseTraceIfConfigured(ctx)
	if err != nil {
		return nil, nil, nil, err
	}

	cleanup = func(context.Context) error { return nil }
	if clean != nil {
		cleanup = clean
	}

	runContext, runOptions, err = langfuseRunOptions(ctx, enabled, userID, sessionID, sandbox, input)
	if err != nil {
		cleanupCtx, cancel := context.WithTimeout(
			context.WithoutCancel(ctx),
			langfuseCleanupTimeout,
		)
		defer cancel()
		_ = cleanup(cleanupCtx)
		return nil, nil, nil, err
	}

	return runContext, runOptions, cleanup, nil
}

type langfuseConfig struct {
	publicKey string
	secretKey string
	host      string
	insecure  bool
	enabled   bool
}

func startLangfuseTraceIfConfigured(
	ctx context.Context,
) (cleanup func(context.Context) error, enabled bool, err error) {
	cfg, err := resolveLangfuseConfig()
	if err != nil {
		return nil, false, err
	}
	if !cfg.enabled {
		return nil, false, nil
	}

	options := []langfuse.Option{
		langfuse.WithPublicKey(cfg.publicKey),
		langfuse.WithSecretKey(cfg.secretKey),
		langfuse.WithHost(cfg.host),
	}
	if cfg.insecure {
		options = append(options, langfuse.WithInsecure())
	}

	clean, err := langfuse.Start(ctx, options...)
	if err != nil {
		return nil, false, err
	}

	return clean, true, nil
}

func langfuseRunOptions(
	ctx context.Context,
	enabled bool,
	userID string,
	sessionID string,
	sandbox string,
	input string,
) (runContext context.Context, runOptions []agentpkg.RunOption, err error) {
	if !enabled {
		return ctx, nil, nil
	}

	members := make([]baggage.Member, 0, 4)
	for _, item := range []struct {
		key   string
		value string
	}{
		{key: "langfuse.user.id", value: userID},
		{key: "langfuse.session.id", value: sessionID},
		{key: "langfuse.trace.metadata.sandbox", value: sandbox},
		{key: "langfuse.trace.metadata.example", value: codeReviewAgentName},
	} {
		member, err := baggage.NewMemberRaw(item.key, item.value)
		if err != nil {
			return nil, nil, fmt.Errorf("create langfuse baggage member %q: %w", item.key, err)
		}
		members = append(members, member)
	}

	bag, err := baggage.New(members...)
	if err != nil {
		return nil, nil, fmt.Errorf("create langfuse baggage: %w", err)
	}
	ctx = baggage.ContextWithBaggage(ctx, bag)

	return ctx, []agentpkg.RunOption{
		agentpkg.WithSpanAttributes(
			attribute.String("langfuse.trace.name", "code_review_agent.review"),
			attribute.String("langfuse.user.id", userID),
			attribute.String("langfuse.session.id", sessionID),
			attribute.String("langfuse.environment", "development"),
			attribute.String("langfuse.trace.metadata.sandbox", sandbox),
			attribute.String("langfuse.trace.metadata.example", codeReviewAgentName),
			attribute.String("langfuse.trace.input", input),
		),
	}, nil
}

func resolveLangfuseConfig() (config langfuseConfig, err error) {
	publicKey := strings.TrimSpace(*langfusePublicKey)
	secretKey := strings.TrimSpace(*langfuseSecretKey)
	baseURL := strings.TrimSpace(*langfuseBaseURL)
	host := strings.TrimSpace(*langfuseHost)

	if publicKey == "" && secretKey == "" && baseURL == "" && host == "" {
		return langfuseConfig{}, nil
	}
	if publicKey == "" || secretKey == "" || (baseURL == "" && host == "") {
		return langfuseConfig{}, fmt.Errorf("incomplete Langfuse config: provide public key, secret key, and base URL or host")
	}

	insecure := *langfuseInsecure
	if host == "" {
		resolvedHost, baseInsecure, err := langfuseHostFromBaseURL(baseURL)
		if err != nil {
			return langfuseConfig{}, err
		}
		host = resolvedHost
		insecure = insecure || baseInsecure
	}

	return langfuseConfig{
		publicKey: publicKey,
		secretKey: secretKey,
		host:      host,
		insecure:  insecure,
		enabled:   true,
	}, nil
}

func langfuseHostFromBaseURL(raw string) (host string, insecure bool, err error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "", false, fmt.Errorf("parse Langfuse base URL: %w", err)
	}
	if u.Scheme == "" || u.Host == "" {
		return "", false, fmt.Errorf("Langfuse base URL must include scheme and host")
	}

	switch u.Scheme {
	case "https":
		return addDefaultPort(u.Host, "443"), false, nil
	case "http":
		return addDefaultPort(u.Host, "80"), true, nil
	default:
		return "", false, fmt.Errorf("unsupported Langfuse base URL scheme %q", u.Scheme)
	}
}

func addDefaultPort(host, port string) string {
	if _, _, err := net.SplitHostPort(host); err == nil {
		return host
	}
	return net.JoinHostPort(host, port)
}
