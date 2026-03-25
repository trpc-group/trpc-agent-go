//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package workspaceexec

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/artifact"
	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
	"trpc.group/trpc-go/trpc-agent-go/internal/fileref"
	"trpc.group/trpc-go/trpc-agent-go/skill"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

const defaultWorkspaceArtifactMaxBytes = 64 * 1024 * 1024

// PublishArtifactTool publishes an existing workspace file as an artifact.
type PublishArtifactTool struct {
	exec *ExecTool
}

type publishArtifactInput struct {
	Path string `json:"path"`
}

type publishArtifactOutput struct {
	Path      string `json:"path"`
	SavedAs   string `json:"saved_as"`
	Version   int    `json:"version"`
	Ref       string `json:"ref"`
	MIMEType  string `json:"mime_type,omitempty"`
	SizeBytes int64  `json:"size_bytes"`
}

type artifactStateRef struct {
	Name    string `json:"name"`
	Version int    `json:"version"`
	Ref     string `json:"ref"`
}

type publishArtifactStateDelta struct {
	ToolCallID string             `json:"tool_call_id"`
	Artifacts  []artifactStateRef `json:"artifacts"`
}

// NewPublishArtifactTool creates a tool for publishing final workspace files.
func NewPublishArtifactTool(exec *ExecTool) *PublishArtifactTool {
	return &PublishArtifactTool{exec: exec}
}

// Declaration returns the schema for workspace_publish_artifact.
func (t *PublishArtifactTool) Declaration() *tool.Declaration {
	return &tool.Declaration{
		Name: "workspace_publish_artifact",
		Description: "Publish an existing file from the current shared " +
			"executor workspace as an artifact. Use this only for final " +
			"deliverables that already exist in work/, out/, or runs/.",
		InputSchema: &tool.Schema{
			Type:     "object",
			Required: []string{"path"},
			Properties: map[string]*tool.Schema{
				"path": {
					Type: "string",
					Description: "Workspace-relative path to an existing file " +
						"under work/, out/, or runs/.",
				},
			},
		},
		OutputSchema: &tool.Schema{
			Type:        "object",
			Description: "Artifact reference for a published workspace file.",
			Required:    []string{"path", "saved_as", "version", "ref", "size_bytes"},
			Properties: map[string]*tool.Schema{
				"path":       {Type: "string", Description: "Workspace-relative source path."},
				"saved_as":   {Type: "string", Description: "Artifact key used for persistence."},
				"version":    {Type: "integer", Description: "Artifact version returned by the artifact service."},
				"ref":        {Type: "string", Description: "artifact:// reference for the saved artifact."},
				"mime_type":  {Type: "string", Description: "Detected mime type for the saved file."},
				"size_bytes": {Type: "integer", Description: "Original file size in bytes."},
			},
		},
	}
}

// Call persists an existing workspace file through the artifact service.
func (t *PublishArtifactTool) Call(
	ctx context.Context,
	input []byte,
) (any, error) {
	if t == nil || t.exec == nil {
		return nil, errors.New("workspace_publish_artifact is not configured")
	}
	var in publishArtifactInput
	if err := json.Unmarshal(input, &in); err != nil {
		return nil, err
	}
	rel, err := normalizeArtifactPath(in.Path)
	if err != nil {
		return nil, err
	}
	reason := artifactPublishSkipReason(ctx)
	if reason != "" {
		return nil, fmt.Errorf(
			"workspace_publish_artifact requires artifact service and session info: %s",
			reason,
		)
	}
	ctxIO := withArtifactContext(ctx)
	eng, err := t.exec.liveEngine()
	if err != nil {
		return nil, err
	}
	ws, err := t.exec.resolver.CreateWorkspace(ctxIO, eng, "workspace")
	if err != nil {
		return nil, err
	}
	manifest, err := eng.FS().CollectOutputs(ctxIO, ws, codeexecutor.OutputSpec{
		Globs:         []string{rel},
		MaxFiles:      1,
		MaxFileBytes:  defaultWorkspaceArtifactMaxBytes,
		MaxTotalBytes: defaultWorkspaceArtifactMaxBytes,
		Save:          true,
		Inline:        false,
	})
	if err != nil {
		return nil, err
	}
	if len(manifest.Files) == 0 {
		return nil, fmt.Errorf("workspace artifact file not found: %s", rel)
	}
	if len(manifest.Files) != 1 {
		return nil, fmt.Errorf(
			"workspace artifact path matched %d files: %s",
			len(manifest.Files),
			rel,
		)
	}
	ref := manifest.Files[0]
	if ref.SavedAs == "" {
		return nil, fmt.Errorf("workspace artifact was not persisted: %s", rel)
	}
	return publishArtifactOutput{
		Path:      rel,
		SavedAs:   ref.SavedAs,
		Version:   ref.Version,
		Ref:       fileref.ArtifactPrefix + ref.SavedAs + "@" + fmt.Sprintf("%d", ref.Version),
		MIMEType:  ref.MIMEType,
		SizeBytes: ref.SizeBytes,
	}, nil
}

// StateDelta returns a replayable artifact ref list when workspace_publish_artifact
// successfully persists a workspace file via Artifact service.
func (t *PublishArtifactTool) StateDelta(
	toolCallID string,
	_ []byte,
	resultJSON []byte,
) map[string][]byte {
	toolCallID = strings.TrimSpace(toolCallID)
	if toolCallID == "" || len(resultJSON) == 0 {
		return nil
	}
	var out publishArtifactOutput
	if err := json.Unmarshal(resultJSON, &out); err != nil {
		return nil
	}
	savedAs := strings.TrimSpace(out.SavedAs)
	if savedAs == "" || out.Version < 0 {
		return nil
	}
	ref := strings.TrimSpace(out.Ref)
	if ref == "" {
		ref = fmt.Sprintf("artifact://%s@%d", savedAs, out.Version)
	}
	b, err := json.Marshal(publishArtifactStateDelta{
		ToolCallID: toolCallID,
		Artifacts: []artifactStateRef{{
			Name:    savedAs,
			Version: out.Version,
			Ref:     ref,
		}},
	})
	if err != nil {
		return nil
	}
	return map[string][]byte{
		skill.StateKeyArtifacts: b,
	}
}

const (
	publishReasonNoInvocation = "invocation is missing from context"
	publishReasonNoService    = "artifact service is not configured"
	publishReasonNoSession    = "session is missing from invocation"
	publishReasonNoSessionIDs = "session app/user/session IDs are missing"
)

func artifactPublishSkipReason(ctx context.Context) string {
	inv, ok := agent.InvocationFromContext(ctx)
	if !ok || inv == nil {
		return publishReasonNoInvocation
	}
	if inv.ArtifactService == nil {
		return publishReasonNoService
	}
	if inv.Session == nil {
		return publishReasonNoSession
	}
	if inv.Session.AppName == "" || inv.Session.UserID == "" ||
		inv.Session.ID == "" {
		return publishReasonNoSessionIDs
	}
	return ""
}

func withArtifactContext(ctx context.Context) context.Context {
	ctxIO := ctx
	if inv, ok := agent.InvocationFromContext(ctx); ok &&
		inv != nil && inv.ArtifactService != nil &&
		inv.Session != nil {
		ctxIO = codeexecutor.WithArtifactService(ctxIO, inv.ArtifactService)
		ctxIO = codeexecutor.WithArtifactSession(ctxIO, artifact.SessionInfo{
			AppName:   inv.Session.AppName,
			UserID:    inv.Session.UserID,
			SessionID: inv.Session.ID,
		})
	}
	return ctxIO
}

func normalizeArtifactPath(raw string) (string, error) {
	s := strings.TrimSpace(raw)
	s = strings.ReplaceAll(s, "\\", "/")
	if s == "" {
		return "", errors.New("path is required")
	}
	if hasGlobMeta(s) {
		return "", errors.New("path must not contain glob patterns")
	}
	if isWorkspaceEnvPath(s) {
		out := codeexecutor.NormalizeGlobs([]string{s})
		if len(out) == 0 {
			return "", errors.New("invalid path")
		}
		s = out[0]
	}
	if strings.HasPrefix(s, "/") {
		rel := strings.TrimPrefix(path.Clean(s), "/")
		if rel == "" || rel == "." {
			return "", errors.New("path must point to a file inside the workspace")
		}
		if !isAllowedPublishArtifactPath(rel) {
			return "", fmt.Errorf(
				"path must stay under supported publish roots such as work/, out/, or runs/: %q",
				raw,
			)
		}
		return rel, nil
	}
	rel := path.Clean(s)
	if rel == "." || rel == ".." || strings.HasPrefix(rel, "../") {
		return "", errors.New("path must stay within the workspace")
	}
	if !isAllowedPublishArtifactPath(rel) {
		return "", fmt.Errorf(
			"path must stay under supported publish roots such as work/, out/, or runs/: %q",
			raw,
		)
	}
	return rel, nil
}

func isAllowedPublishArtifactPath(rel string) bool {
	switch {
	case rel == codeexecutor.DirWork || strings.HasPrefix(rel, codeexecutor.DirWork+"/"):
		return true
	case rel == codeexecutor.DirOut || strings.HasPrefix(rel, codeexecutor.DirOut+"/"):
		return true
	case rel == codeexecutor.DirRuns || strings.HasPrefix(rel, codeexecutor.DirRuns+"/"):
		return true
	default:
		return false
	}
}

var _ tool.Tool = (*PublishArtifactTool)(nil)
var _ tool.CallableTool = (*PublishArtifactTool)(nil)
