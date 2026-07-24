//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package toolresultfile

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/artifact"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/plugin"
)

const (
	textMimeType = "text/plain"
	jsonMimeType = "application/json"
)

type toolResultFilePlugin struct {
	name           string
	thresholdBytes int
}

// New creates an opt-in tool-result externalization plugin.
//
// The Runner must have an artifact.Service. If no artifact service or complete
// session identity is available, the plugin preserves tool results inline.
func New(opts ...Option) plugin.Plugin {
	o := newOptions(opts...)
	return &toolResultFilePlugin{
		name:           o.name,
		thresholdBytes: o.thresholdBytes,
	}
}

// Name implements plugin.Plugin.
func (p *toolResultFilePlugin) Name() string {
	return p.name
}

// Register implements plugin.Plugin.
func (p *toolResultFilePlugin) Register(r *plugin.Registry) {
	if p == nil || r == nil {
		return
	}
	r.AfterToolMessages(p.afterToolMessages)
}

func (p *toolResultFilePlugin) afterToolMessages(
	ctx context.Context,
	args *plugin.AfterToolMessagesArgs,
) (*plugin.AfterToolMessagesResult, error) {
	service, info, ok := artifactTarget(args)
	if !ok {
		return nil, nil
	}

	replacements := append([]model.Message(nil), args.ToolResultMessages...)
	changed := false
	for i := range replacements {
		payload, mimeType, err := artifactPayload(replacements[i])
		if err != nil {
			return nil, fmt.Errorf(
				"encode tool result %q: %w",
				replacements[i].ToolID,
				err,
			)
		}
		if len(payload) < p.thresholdBytes {
			continue
		}

		filename := artifactName(args.Invocation, replacements[i].ToolID)
		version, err := service.SaveArtifact(
			ctx,
			info,
			filename,
			&artifact.Artifact{
				Data:     payload,
				MimeType: mimeType,
				Name:     filename,
			},
		)
		if err != nil {
			return nil, fmt.Errorf(
				"externalize tool result %q: %w",
				replacements[i].ToolID,
				err,
			)
		}

		ref := fmt.Sprintf("artifact://%s@%d", filename, version)
		replacements[i].Content = fmt.Sprintf(
			"[Tool result externalized: %d bytes saved at %s. "+
				"Use read_file with this artifact reference to inspect it.]",
			len(payload),
			ref,
		)
		replacements[i].ContentParts = nil
		changed = true
	}
	if !changed {
		return nil, nil
	}
	return &plugin.AfterToolMessagesResult{
		ToolResultMessages: replacements,
	}, nil
}

func artifactPayload(message model.Message) ([]byte, string, error) {
	if len(message.ContentParts) == 0 {
		return []byte(message.Content), contentMimeType(message.Content), nil
	}
	payload, err := json.Marshal(struct {
		Content      string              `json:"content,omitempty"`
		ContentParts []model.ContentPart `json:"content_parts"`
	}{
		Content:      message.Content,
		ContentParts: message.ContentParts,
	})
	if err != nil {
		return nil, "", err
	}
	return payload, jsonMimeType, nil
}

func artifactTarget(
	args *plugin.AfterToolMessagesArgs,
) (artifact.Service, artifact.SessionInfo, bool) {
	if args == nil || args.Invocation == nil ||
		args.Invocation.ArtifactService == nil ||
		args.Invocation.Session == nil {
		return nil, artifact.SessionInfo{}, false
	}
	sess := args.Invocation.Session
	if sess.AppName == "" || sess.UserID == "" || sess.ID == "" {
		return nil, artifact.SessionInfo{}, false
	}
	return args.Invocation.ArtifactService, artifact.SessionInfo{
		AppName:   sess.AppName,
		UserID:    sess.UserID,
		SessionID: sess.ID,
	}, true
}

func artifactName(inv *agent.Invocation, toolID string) string {
	sum := sha256.Sum256([]byte(inv.InvocationID + "\x00" + toolID))
	return "tool_result_" + hex.EncodeToString(sum[:8]) + ".txt"
}

func contentMimeType(content string) string {
	if json.Valid([]byte(content)) {
		return jsonMimeType
	}
	return textMimeType
}
