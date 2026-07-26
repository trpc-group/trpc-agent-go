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
	"sort"
	"strings"
	"unicode/utf8"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/artifact"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/plugin"
)

const (
	textMimeType      = "text/plain"
	jsonMimeType      = "application/json"
	artifactChunkSize = 512 * 1024
)

type artifactChunkManifest struct {
	ByteCount int      `json:"byte_count"`
	MimeType  string   `json:"mime_type"`
	Parts     []string `json:"parts"`
}

type artifactWriteCapability interface {
	ArtifactWritesEnabled() bool
}

type toolResultFilePlugin struct {
	name           string
	thresholdBytes int
}

// New creates an opt-in tool-result externalization plugin.
//
// The Runner must have an artifact.Service, a complete session identity, and
// the active model request must expose read_file (possibly with a tool-set
// prefix such as file_read_file). If any prerequisite is unavailable, or the
// artifact service reports that writes are disabled, the plugin preserves tool
// results inline.
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
	readToolName := retrievalToolName(args.Request)
	if readToolName == "" {
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
		if !utf8.Valid(payload) {
			continue
		}

		ref, partCount, err := savePayload(
			ctx,
			service,
			info,
			args.Invocation,
			replacements[i].ToolID,
			payload,
			mimeType,
		)
		if err != nil {
			return nil, fmt.Errorf(
				"externalize tool result %q: %w",
				replacements[i].ToolID,
				err,
			)
		}

		if partCount == 1 {
			replacements[i].Content = fmt.Sprintf(
				"[Tool result externalized: %d bytes saved at %s. "+
					"Use %s with this artifact reference to inspect it.]",
				len(payload),
				ref,
				readToolName,
			)
		} else {
			replacements[i].Content = fmt.Sprintf(
				"[Tool result externalized: %d bytes split into %d "+
					"ordered parts. Use %s on manifest %s, then read "+
					"each listed artifact reference in order.]",
				len(payload),
				partCount,
				readToolName,
				ref,
			)
		}
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

func savePayload(
	ctx context.Context,
	service artifact.Service,
	info artifact.SessionInfo,
	inv *agent.Invocation,
	toolID string,
	payload []byte,
	mimeType string,
) (string, int, error) {
	if len(payload) <= artifactChunkSize {
		filename := artifactName(inv, toolID)
		ref, err := saveArtifact(
			ctx,
			service,
			info,
			filename,
			payload,
			mimeType,
		)
		return ref, 1, err
	}

	chunks := splitPayload(payload)
	refs := make([]string, 0, len(chunks))
	for i, chunk := range chunks {
		filename := artifactPartName(inv, toolID, i)
		ref, err := saveArtifact(
			ctx,
			service,
			info,
			filename,
			chunk,
			textMimeType,
		)
		if err != nil {
			return "", 0, err
		}
		refs = append(refs, ref)
	}
	manifest, err := json.Marshal(artifactChunkManifest{
		ByteCount: len(payload),
		MimeType:  mimeType,
		Parts:     refs,
	})
	if err != nil {
		return "", 0, err
	}
	manifestRef, err := saveArtifact(
		ctx,
		service,
		info,
		artifactManifestName(inv, toolID),
		manifest,
		jsonMimeType,
	)
	if err != nil {
		return "", 0, err
	}
	return manifestRef, len(chunks), nil
}

func saveArtifact(
	ctx context.Context,
	service artifact.Service,
	info artifact.SessionInfo,
	filename string,
	data []byte,
	mimeType string,
) (string, error) {
	version, err := service.SaveArtifact(
		ctx,
		info,
		filename,
		&artifact.Artifact{
			Data:     data,
			MimeType: mimeType,
			Name:     filename,
		},
	)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("artifact://%s@%d", filename, version), nil
}

func splitPayload(payload []byte) [][]byte {
	chunks := make([][]byte, 0, (len(payload)+artifactChunkSize-1)/artifactChunkSize)
	for len(payload) > artifactChunkSize {
		end := artifactChunkSize
		for end > 0 && !utf8.RuneStart(payload[end]) {
			end--
		}
		chunks = append(chunks, payload[:end])
		payload = payload[end:]
	}
	if len(payload) > 0 {
		chunks = append(chunks, payload)
	}
	return chunks
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
	if capability, ok := args.Invocation.ArtifactService.(artifactWriteCapability); ok && !capability.ArtifactWritesEnabled() {
		return nil, artifact.SessionInfo{}, false
	}
	return args.Invocation.ArtifactService, artifact.SessionInfo{
		AppName:   sess.AppName,
		UserID:    sess.UserID,
		SessionID: sess.ID,
	}, true
}

func artifactName(inv *agent.Invocation, toolID string) string {
	return artifactNamePrefix(inv, toolID) + ".txt"
}

func artifactPartName(
	inv *agent.Invocation,
	toolID string,
	index int,
) string {
	return fmt.Sprintf("%s.part-%04d.txt", artifactNamePrefix(inv, toolID), index+1)
}

func artifactManifestName(inv *agent.Invocation, toolID string) string {
	return artifactNamePrefix(inv, toolID) + ".manifest.json"
}

func artifactNamePrefix(inv *agent.Invocation, toolID string) string {
	sum := sha256.Sum256([]byte(inv.InvocationID + "\x00" + toolID))
	return "tool_result_" + hex.EncodeToString(sum[:8])
}

func retrievalToolName(request *model.Request) string {
	if request == nil || len(request.Tools) == 0 {
		return ""
	}
	names := make([]string, 0, len(request.Tools))
	for mapName, candidate := range request.Tools {
		name := mapName
		if candidate != nil && candidate.Declaration() != nil &&
			candidate.Declaration().Name != "" {
			name = candidate.Declaration().Name
		}
		if name == "read_file" || strings.HasSuffix(name, "_read_file") {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	if len(names) == 0 {
		return ""
	}
	return names[0]
}

func contentMimeType(content string) string {
	if json.Valid([]byte(content)) {
		return jsonMimeType
	}
	return textMimeType
}
