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
	"os"
	"strings"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/baggage"
	oteltrace "go.opentelemetry.io/otel/trace"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/admin"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/internal/gateway"
	langfuseobs "trpc.group/trpc-go/trpc-agent-go/telemetry/langfuse"
)

const (
	langfuseHostEnv        = "LANGFUSE_HOST"
	langfuseInsecureEnv    = "LANGFUSE_INSECURE"
	langfuseInitProjectEnv = "LANGFUSE_INIT_PROJECT_ID"

	langfuseTraceIDPlaceholder = "{{trace_id}}"

	langfuseTraceNameKey      = "langfuse.trace.name"
	langfuseUserIDKey         = "langfuse.user.id"
	langfuseSessionIDKey      = "langfuse.session.id"
	langfuseMetadataPrefix    = "langfuse.trace.metadata."
	langfuseMetadataAppName   = langfuseMetadataPrefix + "app_name"
	langfuseMetadataChannel   = langfuseMetadataPrefix + "channel"
	langfuseMetadataRequestID = langfuseMetadataPrefix + "request_id"
	langfuseMetadataMessageID = langfuseMetadataPrefix + "message_id"

	langfuseTraceDefaultName = "request"
)

var langfuseStart = langfuseobs.Start

type langfuseRuntime struct {
	adminStatus       admin.LangfuseStatus
	runOptionResolver gateway.RunOptionResolver
	shutdown          func(context.Context) error
}

func maybeEnableLangfuse(
	ctx context.Context,
	opts runOptions,
) (*langfuseRuntime, error) {
	status := buildLangfuseAdminStatus(opts)
	if !opts.LangfuseEnabled {
		return &langfuseRuntime{
			adminStatus: status,
		}, nil
	}

	shutdown, err := langfuseStart(
		ctx,
		langfuseStartOptions(opts)...,
	)
	if err != nil {
		status.Error = err.Error()
		if opts.LangfuseRequired {
			return nil, err
		}
		log.Warnf("openclaw: langfuse disabled: %v", err)
		return &langfuseRuntime{
			adminStatus: status,
		}, nil
	}

	status.Ready = true
	return &langfuseRuntime{
		adminStatus:       status,
		runOptionResolver: buildLangfuseRunOptionResolver(opts),
		shutdown:          shutdown,
	}, nil
}

func langfuseStartOptions(
	opts runOptions,
) []langfuseobs.Option {
	if opts.LangfuseObservationLeafValueMaxBytes == nil {
		return nil
	}
	return []langfuseobs.Option{
		langfuseobs.WithObservationLeafValueMaxBytes(
			*opts.LangfuseObservationLeafValueMaxBytes,
		),
	}
}

func buildLangfuseAdminStatus(
	opts runOptions,
) admin.LangfuseStatus {
	uiBaseURL := resolvedLangfuseUIBaseURL(opts)
	return admin.LangfuseStatus{
		Enabled:   opts.LangfuseEnabled,
		UIBaseURL: uiBaseURL,
		TraceURLTemplate: resolvedLangfuseTraceURLTemplate(
			opts,
			uiBaseURL,
		),
	}
}

func resolvedLangfuseUIBaseURL(opts runOptions) string {
	if baseURL := strings.TrimSpace(opts.LangfuseUIBaseURL); baseURL != "" {
		return strings.TrimRight(baseURL, "/")
	}

	host := strings.TrimSpace(os.Getenv(langfuseHostEnv))
	if host == "" {
		return ""
	}
	if strings.Contains(host, "://") {
		return strings.TrimRight(host, "/")
	}

	scheme := "https"
	if strings.EqualFold(
		strings.TrimSpace(os.Getenv(langfuseInsecureEnv)),
		"true",
	) {
		scheme = "http"
	}
	return scheme + "://" + host
}

func resolvedLangfuseTraceURLTemplate(
	opts runOptions,
	uiBaseURL string,
) string {
	if template := strings.TrimSpace(
		opts.LangfuseTraceURLTemplate,
	); template != "" {
		return template
	}
	projectID := strings.TrimSpace(os.Getenv(langfuseInitProjectEnv))
	if uiBaseURL == "" || projectID == "" {
		return ""
	}
	return strings.TrimRight(uiBaseURL, "/") +
		"/project/" + projectID + "/traces/" +
		langfuseTraceIDPlaceholder
}

func buildLangfuseRunOptionResolver(
	opts runOptions,
) gateway.RunOptionResolver {
	appName := strings.TrimSpace(opts.AppName)
	return func(
		ctx context.Context,
		input gateway.RunOptionInput,
	) (context.Context, []agent.RunOption) {
		ctx = withLangfuseBaggage(ctx, appName, input)

		runOpts := make([]agent.RunOption, 0, 2)
		traceName := buildLangfuseTraceName(appName, input)
		if traceName != "" {
			runOpts = append(
				runOpts,
				agent.WithSpanAttributes(
					attribute.String(
						langfuseTraceNameKey,
						traceName,
					),
				),
			)
		}
		if input.Trace != nil {
			traceRef := input.Trace
			runOpts = append(
				runOpts,
				agent.WithTraceStartedCallback(
					func(spanCtx oteltrace.SpanContext) {
						if !spanCtx.IsValid() {
							return
						}
						if err := traceRef.SetTraceID(
							spanCtx.TraceID().String(),
						); err != nil {
							log.Warnf(
								"openclaw: persist trace id failed: %v",
								err,
							)
						}
					},
				),
			)
		}
		return ctx, runOpts
	}
}

func withLangfuseBaggage(
	ctx context.Context,
	appName string,
	input gateway.RunOptionInput,
) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	bag := baggage.FromContext(ctx)
	bag = setLangfuseBaggageMember(
		bag,
		langfuseUserIDKey,
		input.UserID,
	)
	bag = setLangfuseBaggageMember(
		bag,
		langfuseSessionIDKey,
		input.SessionID,
	)
	bag = setLangfuseBaggageMember(
		bag,
		langfuseMetadataAppName,
		appName,
	)
	bag = setLangfuseBaggageMember(
		bag,
		langfuseMetadataChannel,
		input.Inbound.Channel,
	)
	bag = setLangfuseBaggageMember(
		bag,
		langfuseMetadataRequestID,
		input.RequestID,
	)
	bag = setLangfuseBaggageMember(
		bag,
		langfuseMetadataMessageID,
		input.Inbound.MessageID,
	)
	return baggage.ContextWithBaggage(ctx, bag)
}

func setLangfuseBaggageMember(
	bag baggage.Baggage,
	key string,
	value string,
) baggage.Baggage {
	key = strings.TrimSpace(key)
	value = strings.TrimSpace(value)
	if key == "" || value == "" {
		return bag
	}

	member, err := baggage.NewMemberRaw(key, value)
	if err != nil {
		return bag
	}
	next, err := bag.SetMember(member)
	if err != nil {
		return bag
	}
	return next
}

func buildLangfuseTraceName(
	fallbackAppName string,
	input gateway.RunOptionInput,
) string {
	channel := strings.TrimSpace(input.Inbound.Channel)
	if channel == "" {
		channel = strings.TrimSpace(fallbackAppName)
	}
	if channel == "" {
		channel = appName
	}
	if messageID := strings.TrimSpace(
		input.Inbound.MessageID,
	); messageID != "" {
		return channel + " " + messageID
	}
	if requestID := strings.TrimSpace(input.RequestID); requestID != "" {
		return channel + " " + requestID
	}
	return channel + " " + langfuseTraceDefaultName
}
