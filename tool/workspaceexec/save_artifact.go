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
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
	"trpc.group/trpc-go/trpc-agent-go/internal/fileref"
	"trpc.group/trpc-go/trpc-agent-go/internal/workspacefacade"
	"trpc.group/trpc-go/trpc-agent-go/skill"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// SaveArtifactTool persists an existing workspace file as an artifact.
type SaveArtifactTool struct {
	exec *ExecTool
}

type saveArtifactInput struct {
	Path string `json:"path"`
}

type saveArtifactOutput struct {
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

type saveArtifactStateDelta struct {
	ToolCallID string             `json:"tool_call_id"`
	Artifacts  []artifactStateRef `json:"artifacts"`
}

// NewSaveArtifactTool creates a tool for persisting final workspace files.
func NewSaveArtifactTool(exec *ExecTool) *SaveArtifactTool {
	return &SaveArtifactTool{exec: exec}
}

// Declaration returns the schema for workspace_save_artifact.
func (t *SaveArtifactTool) Declaration() *tool.Declaration {
	return &tool.Declaration{
		Name: "workspace_save_artifact",
		Description: "Save an existing file from the current shared " +
			"executor workspace as an artifact. Use this when you need " +
			"a stable artifact reference for an already existing file " +
			"under work/, out/, or runs/.",
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
			Description: "Artifact reference for a saved workspace file.",
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
func (t *SaveArtifactTool) Call(
	ctx context.Context,
	input []byte,
) (any, error) {
	if t == nil || t.exec == nil {
		return nil, errors.New("workspace_save_artifact is not configured")
	}
	var in saveArtifactInput
	if err := json.Unmarshal(input, &in); err != nil {
		return nil, err
	}
	rel, err := workspacefacade.NormalizeArtifactPath(in.Path)
	if err != nil {
		return nil, err
	}
	reason := workspacefacade.ArtifactSaveSkipReason(ctx)
	if reason != "" {
		return nil, fmt.Errorf(
			"workspace_save_artifact requires artifact service and session info: %s",
			reason,
		)
	}
	ctxIO := workspacefacade.WithArtifactContext(ctx)
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
		MaxFileBytes:  workspacefacade.DefaultArtifactMaxBytes,
		MaxTotalBytes: workspacefacade.DefaultArtifactMaxBytes,
		Save:          true,
		Inline:        false,
	})
	if err != nil && !errors.Is(err, codeexecutor.ErrPartialOutputCommit) {
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
	return saveArtifactOutput{
		Path:      rel,
		SavedAs:   ref.SavedAs,
		Version:   ref.Version,
		Ref:       fileref.ArtifactPrefix + ref.SavedAs + "@" + fmt.Sprintf("%d", ref.Version),
		MIMEType:  ref.MIMEType,
		SizeBytes: ref.SizeBytes,
	}, nil
}

// StateDelta returns a replayable artifact ref list when workspace_save_artifact
// successfully persists a workspace file via Artifact service.
func (t *SaveArtifactTool) StateDelta(
	toolCallID string,
	_ []byte,
	resultJSON []byte,
) map[string][]byte {
	toolCallID = strings.TrimSpace(toolCallID)
	if toolCallID == "" || len(resultJSON) == 0 {
		return nil
	}
	var out saveArtifactOutput
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
	b, err := json.Marshal(saveArtifactStateDelta{
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

// SupportsArtifactSave reports whether the current invocation can
// persist artifacts for workspace tools. It forwards to
// workspacefacade.ArtifactSaveSkipReasonInv so the predicate stays in
// sync with workspacefacade.ArtifactSaveSkipReason (used by Call).
func SupportsArtifactSave(inv *agent.Invocation) bool {
	return workspacefacade.ArtifactSaveSkipReasonInv(inv) == ""
}

var _ tool.Tool = (*SaveArtifactTool)(nil)
var _ tool.CallableTool = (*SaveArtifactTool)(nil)
