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
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/graph"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/conversation"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/internal/conversationscope"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/internal/gateway"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

const includeContentsNone = "none"

func buildConversationRunOptionResolver(
	appName string,
	sessionSvc session.Service,
	historyOpts conversation.HistoryOptions,
) gateway.RunOptionResolver {
	return func(
		ctx context.Context,
		input gateway.RunOptionInput,
	) (context.Context, []agent.RunOption) {
		annotation, ok, err := conversation.
			AnnotationFromRequestExtensions(
				input.Extensions,
			)
		if err != nil || !ok {
			return ctx, nil
		}
		if storageUserID := strings.TrimSpace(
			annotation.StorageUserID,
		); storageUserID != "" {
			ctx = conversationscope.WithStorageUserID(
				ctx,
				storageUserID,
			)
		}

		runtimeState := conversation.RuntimeState(annotation)
		runOpts := make([]agent.RunOption, 0, 2)
		if annotation.HistoryMode == conversation.HistoryModeShared {
			if runtimeState == nil {
				runtimeState = make(map[string]any)
			}
			runtimeState[graph.CfgKeyIncludeContents] =
				includeContentsNone
			if sessionSvc != nil {
				sess, err := sessionSvc.GetSession(
					ctx,
					session.Key{
						AppName:   appName,
						UserID:    input.UserID,
						SessionID: input.SessionID,
					},
				)
				if err == nil && sess != nil {
					opts := historyOpts
					opts.LabelOverrides = annotation.ActorLabels
					history := conversation.
						BuildInjectedContextMessages(
							sess,
							opts,
						)
					if len(history) > 0 {
						runOpts = append(
							runOpts,
							agent.WithInjectedContextMessages(
								history,
							),
						)
					}
				}
			}
		}
		if len(runtimeState) > 0 {
			runOpts = append(
				runOpts,
				agent.MergeRuntimeState(runtimeState),
			)
		}
		return ctx, runOpts
	}
}
