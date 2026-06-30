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

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/internal/gateway"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/runtimeprofile"
)

// RuntimeOption customizes an embedded OpenClaw runtime.
type RuntimeOption func(*runtimeOptions)

type codeExecutorConfigLoader func(
	stateDir string,
	enableLocal bool,
	cfg codeExecutorOptions,
) (codeexecutor.CodeExecutor, error)

// GatewayInboundMessage describes the inbound gateway message for one run.
type GatewayInboundMessage struct {
	Channel   string
	From      string
	To        string
	Thread    string
	MessageID string
	Text      string
}

// GatewayRunOptionInput describes one OpenClaw gateway run before runner.Run.
type GatewayRunOptionInput struct {
	Inbound    GatewayInboundMessage
	UserID     string
	SessionID  string
	RequestID  string
	Message    model.Message
	Extensions map[string]json.RawMessage
}

// GatewayRunOptionResolver decorates context and agent run options for one
// OpenClaw gateway run.
type GatewayRunOptionResolver func(
	ctx context.Context,
	input GatewayRunOptionInput,
) (context.Context, []agent.RunOption, error)

type runtimeOptions struct {
	runtimeProfileResolver runtimeprofile.Resolver
	runtimeProfileCatalog  runtimeprofile.Catalog
	runtimeProfileRequired bool
	gatewayResolvers       []GatewayRunOptionResolver
	postToolPromptEnabled  *bool
	codeExecutorLoader     codeExecutorConfigLoader
}

// WithRuntimeProfileResolver injects per-request runtime profile resolution.
func WithRuntimeProfileResolver(
	resolver runtimeprofile.Resolver,
	required bool,
) RuntimeOption {
	return func(opts *runtimeOptions) {
		if resolver == nil {
			return
		}
		opts.runtimeProfileResolver = resolver
		opts.runtimeProfileRequired = required
	}
}

// WithRuntimeProfileCatalog injects profile metadata for cleanup/catalog use.
func WithRuntimeProfileCatalog(
	catalog runtimeprofile.Catalog,
) RuntimeOption {
	return func(opts *runtimeOptions) {
		if catalog == nil {
			return
		}
		opts.runtimeProfileCatalog = catalog
	}
}

// WithRuntimeProfileStore injects a reloadable runtime profile store.
//
// Callers that need Reload or Invalidate control can create a
// runtimeprofile.CachedResolver and pass WithRuntimeProfileResolver.
func WithRuntimeProfileStore(
	store runtimeprofile.Store,
	required bool,
) RuntimeOption {
	return func(opts *runtimeOptions) {
		resolver := runtimeprofile.NewCachedResolver(store)
		if resolver == nil {
			return
		}
		opts.runtimeProfileResolver = resolver
		opts.runtimeProfileCatalog = resolver
		opts.runtimeProfileRequired = required
	}
}

// WithEnablePostToolPrompt controls whether OpenClaw injects post-tool
// guidance into agent requests.
func WithEnablePostToolPrompt(enable bool) RuntimeOption {
	return func(opts *runtimeOptions) {
		opts.postToolPromptEnabled = &enable
	}
}

// WithGatewayRunOptions appends static agent run options to every OpenClaw
// gateway run.
func WithGatewayRunOptions(runOpts ...agent.RunOption) RuntimeOption {
	return func(opts *runtimeOptions) {
		if len(runOpts) == 0 {
			return
		}
		staticRunOpts := make([]agent.RunOption, 0, len(runOpts))
		for _, runOpt := range runOpts {
			if runOpt != nil {
				staticRunOpts = append(staticRunOpts, runOpt)
			}
		}
		if len(staticRunOpts) == 0 {
			return
		}
		opts.gatewayResolvers = append(
			opts.gatewayResolvers,
			staticGatewayRunOptionResolver(staticRunOpts),
		)
	}
}

// WithGatewayRunOptionResolver injects per-request agent run options for
// OpenClaw gateway runs.
func WithGatewayRunOptionResolver(
	resolver GatewayRunOptionResolver,
) RuntimeOption {
	return func(opts *runtimeOptions) {
		if resolver == nil {
			return
		}
		opts.gatewayResolvers = append(opts.gatewayResolvers, resolver)
	}
}

func buildRuntimeOptions(options []RuntimeOption) runtimeOptions {
	var opts runtimeOptions
	for _, option := range options {
		if option == nil {
			continue
		}
		option(&opts)
	}
	return opts
}

func appendRuntimeGatewayRunOptions(
	opts []gateway.Option,
	runtimeOpts runtimeOptions,
) []gateway.Option {
	for _, resolver := range runtimeOpts.gatewayResolvers {
		if resolver == nil {
			continue
		}
		opts = append(
			opts,
			gateway.WithRunOptionResolver(
				adaptGatewayRunOptionResolver(resolver),
			),
		)
	}
	return opts
}

func staticGatewayRunOptionResolver(
	staticRunOpts []agent.RunOption,
) GatewayRunOptionResolver {
	return func(
		ctx context.Context,
		_ GatewayRunOptionInput,
	) (context.Context, []agent.RunOption, error) {
		return ctx, append([]agent.RunOption(nil), staticRunOpts...), nil
	}
}

func adaptGatewayRunOptionResolver(
	resolver GatewayRunOptionResolver,
) gateway.RunOptionResolver {
	return func(
		ctx context.Context,
		input gateway.RunOptionInput,
	) (context.Context, []agent.RunOption, error) {
		return resolver(ctx, gatewayRunOptionInput(input))
	}
}

func gatewayRunOptionInput(
	input gateway.RunOptionInput,
) GatewayRunOptionInput {
	return GatewayRunOptionInput{
		Inbound: GatewayInboundMessage{
			Channel:   input.Inbound.Channel,
			From:      input.Inbound.From,
			To:        input.Inbound.To,
			Thread:    input.Inbound.Thread,
			MessageID: input.Inbound.MessageID,
			Text:      input.Inbound.Text,
		},
		UserID:     input.UserID,
		SessionID:  input.SessionID,
		RequestID:  input.RequestID,
		Message:    input.Message,
		Extensions: cloneGatewayRunOptionExtensions(input.Extensions),
	}
}

func cloneGatewayRunOptionExtensions(
	extensions map[string]json.RawMessage,
) map[string]json.RawMessage {
	if len(extensions) == 0 {
		return nil
	}
	out := make(map[string]json.RawMessage, len(extensions))
	for key, value := range extensions {
		out[key] = append(json.RawMessage(nil), value...)
	}
	return out
}
