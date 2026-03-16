//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package skill

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/artifact"
	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

const (
	workspaceToolsWorkspaceID       = "_workspace"
	listDirStatusMarker             = "__workspace_list_dir_status__"
	workspaceReadFileToolName       = "workspace_read_file"
	workspaceListDirToolName        = "workspace_list_dir"
	workspaceWriteFileToolName      = "workspace_write_file"
	workspaceReplaceContentToolName = "workspace_replace_content"
	artifactPublishToolName         = "artifact_publish"
)

type workspaceToolHelper struct {
	run *RunTool
}

// WorkspaceReadFileTool reads UTF-8 text files from the live workspace.
type WorkspaceReadFileTool struct {
	helper *workspaceToolHelper
}

// WorkspaceListDirTool lists direct children of live workspace directories.
type WorkspaceListDirTool struct {
	helper *workspaceToolHelper
}

// WorkspaceWriteFileTool writes UTF-8 text files under writable workspace roots.
type WorkspaceWriteFileTool struct {
	helper *workspaceToolHelper
}

// WorkspaceReplaceContentTool replaces text in writable workspace files.
type WorkspaceReplaceContentTool struct {
	helper *workspaceToolHelper
}

// ArtifactPublishTool publishes live workspace files as stable artifacts.
type ArtifactPublishTool struct {
	helper *workspaceToolHelper
}

type workspaceReadFileInput struct {
	Path string `json:"path"`
}

type workspaceReadFileOutput struct {
	Path         string `json:"path"`
	ResolvedPath string `json:"resolved_path,omitempty"`
	Content      string `json:"content,omitempty"`
	MIMEType     string `json:"mime_type,omitempty"`
	SizeBytes    int64  `json:"size_bytes,omitempty"`
	Truncated    bool   `json:"truncated,omitempty"`
	Message      string `json:"message,omitempty"`
}

type workspaceListDirInput struct {
	Path string `json:"path,omitempty"`
}

type workspaceDirEntry struct {
	Name string `json:"name"`
	Path string `json:"path"`
	Kind string `json:"kind"`
}

type workspaceListDirOutput struct {
	Path    string              `json:"path"`
	Entries []workspaceDirEntry `json:"entries"`
	Message string              `json:"message,omitempty"`
}

type workspaceWriteFileInput struct {
	Path      string `json:"path"`
	Content   string `json:"content"`
	Overwrite bool   `json:"overwrite,omitempty"`
}

type workspaceWriteFileOutput struct {
	Path    string `json:"path"`
	Message string `json:"message,omitempty"`
}

type workspaceReplaceContentInput struct {
	Path            string `json:"path"`
	OldString       string `json:"old_string"`
	NewString       string `json:"new_string,omitempty"`
	NumReplacements int    `json:"num_replacements,omitempty"`
}

type workspaceReplaceContentOutput struct {
	Path          string `json:"path"`
	ReplacedCount int    `json:"replaced_count,omitempty"`
	TotalMatches  int    `json:"total_matches,omitempty"`
	Message       string `json:"message,omitempty"`
}

type artifactPublishInput struct {
	Paths          []string `json:"paths"`
	ArtifactPrefix string   `json:"artifact_prefix,omitempty"`
}

type artifactPublishFile struct {
	RequestedPath string `json:"requested_path"`
	SourcePath    string `json:"source_path"`
	ArtifactName  string `json:"artifact_name"`
	Version       int    `json:"version"`
	Ref           string `json:"ref"`
	MIMEType      string `json:"mime_type,omitempty"`
	SizeBytes     int64  `json:"size_bytes,omitempty"`
}

type artifactPublishOutput struct {
	Published []artifactPublishFile `json:"published"`
	Message   string                `json:"message,omitempty"`
}

// NewWorkspaceReadFileTool creates a lightweight live workspace text reader.
func NewWorkspaceReadFileTool(run *RunTool) tool.Tool {
	return &WorkspaceReadFileTool{
		helper: &workspaceToolHelper{run: run},
	}
}

// NewWorkspaceListDirTool creates a lightweight live workspace directory lister.
func NewWorkspaceListDirTool(run *RunTool) tool.Tool {
	return &WorkspaceListDirTool{
		helper: &workspaceToolHelper{run: run},
	}
}

// NewWorkspaceWriteFileTool creates a lightweight live workspace text writer.
func NewWorkspaceWriteFileTool(run *RunTool) tool.Tool {
	return &WorkspaceWriteFileTool{
		helper: &workspaceToolHelper{run: run},
	}
}

// NewWorkspaceReplaceContentTool creates a lightweight live workspace text replacer.
func NewWorkspaceReplaceContentTool(run *RunTool) tool.Tool {
	return &WorkspaceReplaceContentTool{
		helper: &workspaceToolHelper{run: run},
	}
}

// NewArtifactPublishTool creates a lightweight artifact publisher for live workspace files.
func NewArtifactPublishTool(run *RunTool) tool.Tool {
	return &ArtifactPublishTool{
		helper: &workspaceToolHelper{run: run},
	}
}

// Declaration implements tool.Tool.
func (*WorkspaceReadFileTool) Declaration() *tool.Declaration {
	return &tool.Declaration{
		Name: workspaceReadFileToolName,
		Description: "Read a UTF-8 text file from the live skill workspace. " +
			"Use workspace-relative paths rooted at skills/, work/, out/, or runs/. " +
			"Prefer this over skill_run when you only need to inspect file contents.",
		InputSchema: &tool.Schema{
			Type:        "object",
			Description: "Read a live workspace text file",
			Required:    []string{"path"},
			Properties: map[string]*tool.Schema{
				"path": {
					Type: "string",
					Description: "Workspace-relative path to a UTF-8 text file " +
						"under skills/, work/, out/, or runs/",
				},
			},
		},
		OutputSchema: &tool.Schema{
			Type:        "object",
			Description: "Live workspace text file contents",
			Properties: map[string]*tool.Schema{
				"path": {
					Type:        "string",
					Description: "Requested workspace-relative path",
				},
				"resolved_path": {
					Type:        "string",
					Description: "Resolved workspace path returned by the executor",
				},
				"content": {
					Type:        "string",
					Description: "File contents",
				},
				"mime_type": {
					Type:        "string",
					Description: "Detected MIME type",
				},
				"size_bytes": {
					Type:        "number",
					Description: "File size in bytes",
				},
				"truncated": {
					Type:        "boolean",
					Description: "Whether the file content was truncated by executor limits",
				},
				"message": {
					Type:        "string",
					Description: "Human-readable summary",
				},
			},
		},
	}
}

// Call implements tool.CallableTool.
func (t *WorkspaceReadFileTool) Call(
	ctx context.Context,
	args []byte,
) (any, error) {
	var in workspaceReadFileInput
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid args: %w", err)
	}
	return t.helper.readFile(ctx, in)
}

// Declaration implements tool.Tool.
func (*WorkspaceListDirTool) Declaration() *tool.Declaration {
	return &tool.Declaration{
		Name: workspaceListDirToolName,
		Description: "List direct children of a live workspace directory. " +
			"Use workspace-relative paths rooted at skills/, work/, out/, or runs/. " +
			"Prefer this over skill_run when you only need to inspect directory contents.",
		InputSchema: &tool.Schema{
			Type:        "object",
			Description: "List a live workspace directory",
			Properties: map[string]*tool.Schema{
				"path": {
					Type: "string",
					Description: "Workspace-relative directory path under skills/, " +
						"work/, out/, or runs/. Defaults to workspace root when omitted",
				},
			},
		},
		OutputSchema: &tool.Schema{
			Type:        "object",
			Description: "Immediate children of the listed workspace directory",
			Properties: map[string]*tool.Schema{
				"path": {
					Type:        "string",
					Description: "Listed workspace-relative directory path",
				},
				"entries": {
					Type:        "array",
					Description: "Immediate children of the directory",
					Items: &tool.Schema{
						Type: "object",
						Properties: map[string]*tool.Schema{
							"name": {
								Type:        "string",
								Description: "Base name of the entry",
							},
							"path": {
								Type:        "string",
								Description: "Workspace-relative path to the entry",
							},
							"kind": {
								Type:        "string",
								Description: "file, directory, or other",
							},
						},
					},
				},
				"message": {
					Type:        "string",
					Description: "Human-readable summary",
				},
			},
		},
	}
}

// Call implements tool.CallableTool.
func (t *WorkspaceListDirTool) Call(
	ctx context.Context,
	args []byte,
) (any, error) {
	var in workspaceListDirInput
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid args: %w", err)
	}
	return t.helper.listDir(ctx, in)
}

// Declaration implements tool.Tool.
func (*WorkspaceWriteFileTool) Declaration() *tool.Declaration {
	return &tool.Declaration{
		Name: workspaceWriteFileToolName,
		Description: "Write a UTF-8 text file under live workspace writable roots. " +
			"Use workspace-relative paths rooted at work/, out/, or runs/. " +
			"Prefer this over skill_run when you only need to create or update a text file.",
		InputSchema: &tool.Schema{
			Type:        "object",
			Description: "Write a live workspace text file",
			Required:    []string{"path", "content"},
			Properties: map[string]*tool.Schema{
				"path": {
					Type:        "string",
					Description: "Workspace-relative file path under work/, out/, or runs/",
				},
				"content": {
					Type:        "string",
					Description: "UTF-8 text content to write",
				},
				"overwrite": {
					Type:        "boolean",
					Description: "Whether to replace an existing file",
				},
			},
		},
		OutputSchema: &tool.Schema{
			Type:        "object",
			Description: "Write result",
			Properties: map[string]*tool.Schema{
				"path": {
					Type:        "string",
					Description: "Written workspace-relative path",
				},
				"message": {
					Type:        "string",
					Description: "Human-readable summary",
				},
			},
		},
	}
}

// Call implements tool.CallableTool.
func (t *WorkspaceWriteFileTool) Call(
	ctx context.Context,
	args []byte,
) (any, error) {
	var in workspaceWriteFileInput
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid args: %w", err)
	}
	return t.helper.writeFile(ctx, in)
}

// Declaration implements tool.Tool.
func (*WorkspaceReplaceContentTool) Declaration() *tool.Declaration {
	return &tool.Declaration{
		Name: workspaceReplaceContentToolName,
		Description: "Replace text inside a live workspace UTF-8 text file. " +
			"Use workspace-relative paths rooted at work/, out/, or runs/.",
		InputSchema: &tool.Schema{
			Type:        "object",
			Description: "Replace text in a live workspace file",
			Required:    []string{"path", "old_string"},
			Properties: map[string]*tool.Schema{
				"path": {
					Type:        "string",
					Description: "Workspace-relative file path under work/, out/, or runs/",
				},
				"old_string": {
					Type:        "string",
					Description: "Existing text to replace; supports multi-line content",
				},
				"new_string": {
					Type:        "string",
					Description: "Replacement text; supports multi-line content",
				},
				"num_replacements": {
					Type:        "number",
					Description: "Optional replacement limit; 0 means 1 and negative means replace all matches",
				},
			},
		},
		OutputSchema: &tool.Schema{
			Type:        "object",
			Description: "Replace result",
			Properties: map[string]*tool.Schema{
				"path": {
					Type:        "string",
					Description: "Updated workspace-relative path",
				},
				"replaced_count": {
					Type:        "number",
					Description: "Number of replacements applied",
				},
				"total_matches": {
					Type:        "number",
					Description: "Total matches found before replacement",
				},
				"message": {
					Type:        "string",
					Description: "Human-readable summary",
				},
			},
		},
	}
}

// Call implements tool.CallableTool.
func (t *WorkspaceReplaceContentTool) Call(
	ctx context.Context,
	args []byte,
) (any, error) {
	var in workspaceReplaceContentInput
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid args: %w", err)
	}
	return t.helper.replaceContent(ctx, in)
}

// Declaration implements tool.Tool.
func (*ArtifactPublishTool) Declaration() *tool.Declaration {
	return &tool.Declaration{
		Name: artifactPublishToolName,
		Description: "Publish live workspace files as stable artifact:// references. " +
			"Use explicit workspace-relative file paths under skills/, work/, out/, or runs/. " +
			"Prefer this when you already have the desired file and need a durable artifact without rerunning skill_run.",
		InputSchema: &tool.Schema{
			Type:        "object",
			Description: "Publish live workspace files as artifacts",
			Required:    []string{"paths"},
			Properties: map[string]*tool.Schema{
				"paths": {
					Type:        "array",
					Description: "Explicit workspace-relative file paths to publish",
					Items:       &tool.Schema{Type: "string"},
				},
				"artifact_prefix": {
					Type:        "string",
					Description: "Optional prefix prepended to each artifact name",
				},
			},
		},
		OutputSchema: &tool.Schema{
			Type:        "object",
			Description: "Published artifact refs",
			Properties: map[string]*tool.Schema{
				"published": {
					Type:        "array",
					Description: "Published artifact files",
					Items: &tool.Schema{
						Type: "object",
						Properties: map[string]*tool.Schema{
							"requested_path": {Type: "string", Description: "Original requested workspace path"},
							"source_path":    {Type: "string", Description: "Resolved workspace-relative source path"},
							"artifact_name":  {Type: "string", Description: "Saved artifact name"},
							"version":        {Type: "number", Description: "Saved artifact version"},
							"ref":            {Type: "string", Description: "Stable artifact:// reference"},
							"mime_type":      {Type: "string", Description: "Detected MIME type"},
							"size_bytes":     {Type: "number", Description: "Original file size in bytes"},
						},
					},
				},
				"message": {Type: "string", Description: "Human-readable summary"},
			},
		},
	}
}

// Call implements tool.CallableTool.
func (t *ArtifactPublishTool) Call(
	ctx context.Context,
	args []byte,
) (any, error) {
	var in artifactPublishInput
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid args: %w", err)
	}
	return t.helper.publishArtifacts(ctx, in)
}

func (h *workspaceToolHelper) readFile(
	ctx context.Context,
	in workspaceReadFileInput,
) (workspaceReadFileOutput, error) {
	rel, err := normalizeWorkspaceInspectPath(in.Path, "")
	if err != nil {
		return workspaceReadFileOutput{}, err
	}

	eng, ws, err := h.prepareWorkspaceForPath(ctx, rel)
	if err != nil {
		return workspaceReadFileOutput{}, err
	}
	file, err := h.collectSingleFile(ctx, eng, ws, rel)
	if err != nil {
		return workspaceReadFileOutput{}, err
	}
	if err := validateWorkspaceTextFile(file); err != nil {
		return workspaceReadFileOutput{}, err
	}

	out := workspaceReadFileOutput{
		Path:         rel,
		ResolvedPath: file.Name,
		Content:      file.Content,
		MIMEType:     file.MIMEType,
		SizeBytes:    file.SizeBytes,
		Truncated:    file.Truncated,
		Message:      fmt.Sprintf("Successfully read %s", rel),
	}
	if out.ResolvedPath == "" {
		out.ResolvedPath = rel
	}
	return out, nil
}

func (h *workspaceToolHelper) listDir(
	ctx context.Context,
	in workspaceListDirInput,
) (workspaceListDirOutput, error) {
	rel, err := normalizeWorkspaceInspectPath(in.Path, ".")
	if err != nil {
		return workspaceListDirOutput{}, err
	}
	if rel == codeexecutor.DirSkills {
		return h.listSkillsDir(rel), nil
	}

	eng, ws, err := h.prepareWorkspaceForPath(ctx, rel)
	if err != nil {
		return workspaceListDirOutput{}, err
	}
	entries, err := h.listDirEntries(ctx, eng, ws, rel)
	if err != nil {
		return workspaceListDirOutput{}, err
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Path < entries[j].Path
	})
	return workspaceListDirOutput{
		Path:    rel,
		Entries: entries,
		Message: fmt.Sprintf("Listed %d entries under %s", len(entries), rel),
	}, nil
}

func (h *workspaceToolHelper) writeFile(
	ctx context.Context,
	in workspaceWriteFileInput,
) (workspaceWriteFileOutput, error) {
	rel, err := normalizeWorkspaceWritePath(in.Path)
	if err != nil {
		return workspaceWriteFileOutput{}, err
	}
	eng, ws, err := h.prepareWorkspaceForPath(ctx, rel)
	if err != nil {
		return workspaceWriteFileOutput{}, err
	}
	if err := h.writeWorkspaceTextFile(ctx, eng, ws, rel, in.Content, in.Overwrite); err != nil {
		return workspaceWriteFileOutput{}, err
	}
	return workspaceWriteFileOutput{
		Path:    rel,
		Message: fmt.Sprintf("Successfully wrote %s", rel),
	}, nil
}

func (h *workspaceToolHelper) replaceContent(
	ctx context.Context,
	in workspaceReplaceContentInput,
) (workspaceReplaceContentOutput, error) {
	if in.OldString == "" {
		return workspaceReplaceContentOutput{}, errors.New("old_string cannot be empty")
	}
	rel, err := normalizeWorkspaceWritePath(in.Path)
	if err != nil {
		return workspaceReplaceContentOutput{}, err
	}
	eng, ws, err := h.prepareWorkspaceForPath(ctx, rel)
	if err != nil {
		return workspaceReplaceContentOutput{}, err
	}
	file, err := h.collectSingleFile(ctx, eng, ws, rel)
	if err != nil {
		return workspaceReplaceContentOutput{}, err
	}
	if err := validateWorkspaceTextFile(file); err != nil {
		return workspaceReplaceContentOutput{}, err
	}
	if in.OldString == in.NewString {
		return workspaceReplaceContentOutput{
			Path:    rel,
			Message: "old_string equals new_string; no changes made",
		}, nil
	}
	total := strings.Count(file.Content, in.OldString)
	if total == 0 {
		return workspaceReplaceContentOutput{
			Path:    rel,
			Message: fmt.Sprintf("%q not found in %s", in.OldString, rel),
		}, nil
	}
	n := in.NumReplacements
	if n == 0 {
		n = 1
	}
	if n < 0 || n > total {
		n = total
	}
	newContent := strings.Replace(file.Content, in.OldString, in.NewString, n)
	if err := h.writeWorkspaceTextFile(ctx, eng, ws, rel, newContent, true); err != nil {
		return workspaceReplaceContentOutput{}, err
	}
	return workspaceReplaceContentOutput{
		Path:          rel,
		ReplacedCount: n,
		TotalMatches:  total,
		Message:       fmt.Sprintf("Successfully replaced %d of %d in %s", n, total, rel),
	}, nil
}

func (h *workspaceToolHelper) publishArtifacts(
	ctx context.Context,
	in artifactPublishInput,
) (artifactPublishOutput, error) {
	if len(in.Paths) == 0 {
		return artifactPublishOutput{}, errors.New("paths is required")
	}
	ctxIO := withWorkspaceArtifactContext(ctx)
	out := artifactPublishOutput{
		Published: make([]artifactPublishFile, 0, len(in.Paths)),
	}
	for _, raw := range in.Paths {
		rel, err := normalizeWorkspacePublishPath(raw)
		if err != nil {
			return artifactPublishOutput{}, err
		}
		eng, ws, err := h.prepareWorkspaceForPath(ctx, rel)
		if err != nil {
			return artifactPublishOutput{}, err
		}
		file, err := h.collectSingleFile(ctx, eng, ws, rel)
		if err != nil {
			return artifactPublishOutput{}, err
		}
		if file.Truncated {
			return artifactPublishOutput{}, fmt.Errorf(
				"artifact_publish only supports files up to 4 MiB on the current executor: %s",
				file.Name,
			)
		}
		artifactName := file.Name
		if in.ArtifactPrefix != "" {
			artifactName = in.ArtifactPrefix + artifactName
		}
		ver, err := codeexecutor.SaveArtifactHelper(
			ctxIO,
			artifactName,
			[]byte(file.Content),
			file.MIMEType,
		)
		if err != nil {
			return artifactPublishOutput{}, err
		}
		out.Published = append(out.Published, artifactPublishFile{
			RequestedPath: rel,
			SourcePath:    file.Name,
			ArtifactName:  artifactName,
			Version:       ver,
			Ref:           fmt.Sprintf("artifact://%s@%d", artifactName, ver),
			MIMEType:      file.MIMEType,
			SizeBytes:     file.SizeBytes,
		})
	}
	out.Message = fmt.Sprintf("Published %d artifact files", len(out.Published))
	return out, nil
}

func (h *workspaceToolHelper) listSkillsDir(
	rel string,
) workspaceListDirOutput {
	out := workspaceListDirOutput{Path: rel}
	if h == nil || h.run == nil || h.run.repo == nil {
		out.Message = fmt.Sprintf("Listed 0 entries under %s", rel)
		return out
	}
	summaries := h.run.repo.Summaries()
	entries := make([]workspaceDirEntry, 0, len(summaries))
	for _, sum := range summaries {
		name := strings.TrimSpace(sum.Name)
		if name == "" {
			continue
		}
		entries = append(entries, workspaceDirEntry{
			Name: name,
			Path: codeexecutor.DirSkills + "/" + name,
			Kind: "directory",
		})
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Path < entries[j].Path
	})
	out.Entries = entries
	out.Message = fmt.Sprintf("Listed %d entries under %s", len(entries), rel)
	return out
}

func (h *workspaceToolHelper) prepareWorkspaceForPath(
	ctx context.Context,
	rel string,
) (codeexecutor.Engine, codeexecutor.Workspace, error) {
	if h == nil || h.run == nil {
		return nil, codeexecutor.Workspace{}, errors.New(
			"workspace tools are not configured",
		)
	}
	eng := h.run.ensureEngine()
	if eng == nil {
		return nil, codeexecutor.Workspace{}, errors.New(
			"workspace engine is not available",
		)
	}
	ws, err := h.run.createWorkspace(ctx, eng, workspaceToolsWorkspaceID)
	if err != nil {
		return nil, codeexecutor.Workspace{}, err
	}
	skillName := workspaceSkillName(rel)
	if skillName == "" {
		return eng, ws, nil
	}
	if h.run.repo == nil {
		return nil, codeexecutor.Workspace{}, fmt.Errorf(
			"workspace path %q requires a skills repository",
			rel,
		)
	}
	root, err := h.run.repo.Path(skillName)
	if err != nil {
		return nil, codeexecutor.Workspace{}, err
	}
	if err := h.run.stageSkill(ctx, eng, ws, root, skillName); err != nil {
		return nil, codeexecutor.Workspace{}, err
	}
	return eng, ws, nil
}

func (h *workspaceToolHelper) collectSingleFile(
	ctx context.Context,
	eng codeexecutor.Engine,
	ws codeexecutor.Workspace,
	rel string,
) (codeexecutor.File, error) {
	files, err := eng.FS().Collect(ctx, ws, []string{rel})
	if err != nil {
		return codeexecutor.File{}, err
	}
	if len(files) == 0 {
		return codeexecutor.File{}, fmt.Errorf("workspace file not found: %s", rel)
	}
	file := files[0]
	if len(files) > 1 {
		for _, candidate := range files {
			if candidate.Name == rel {
				file = candidate
				break
			}
		}
	}
	resolved, err := normalizeCollectedWorkspacePath(file.Name)
	if err != nil {
		return codeexecutor.File{}, err
	}
	file.Name = resolved
	return file, nil
}

func (h *workspaceToolHelper) listDirEntries(
	ctx context.Context,
	eng codeexecutor.Engine,
	ws codeexecutor.Workspace,
	rel string,
) ([]workspaceDirEntry, error) {
	if eng.Runner() == nil {
		return nil, errors.New("workspace runner is not configured")
	}
	script := buildListDirScript(rel)
	rr, err := eng.Runner().RunProgram(
		ctx,
		ws,
		codeexecutor.RunProgramSpec{
			Cmd:     "sh",
			Args:    []string{"-c", script},
			Env:     map[string]string{},
			Cwd:     ".",
			Timeout: 5 * time.Second,
		},
	)
	if err != nil {
		return nil, err
	}
	if rr.ExitCode != 0 {
		if strings.TrimSpace(rr.Stderr) != "" {
			return nil, errors.New(strings.TrimSpace(rr.Stderr))
		}
		return nil, fmt.Errorf("workspace_list_dir failed for %s", rel)
	}
	return parseListDirOutput(rel, rr.Stdout)
}

func (h *workspaceToolHelper) writeWorkspaceTextFile(
	ctx context.Context,
	eng codeexecutor.Engine,
	ws codeexecutor.Workspace,
	rel string,
	content string,
	overwrite bool,
) error {
	if eng.Runner() == nil {
		return errors.New("workspace runner is not configured")
	}
	script := buildWriteFileScript(rel, overwrite)
	rr, err := eng.Runner().RunProgram(
		ctx,
		ws,
		codeexecutor.RunProgramSpec{
			Cmd:     "sh",
			Args:    []string{"-c", script},
			Env:     map[string]string{},
			Cwd:     ".",
			Stdin:   content,
			Timeout: 5 * time.Second,
		},
	)
	if err != nil {
		return err
	}
	if rr.ExitCode != 0 {
		if strings.TrimSpace(rr.Stderr) != "" {
			return errors.New(strings.TrimSpace(rr.Stderr))
		}
		return fmt.Errorf("workspace_write_file failed for %s", rel)
	}
	return nil
}

func buildListDirScript(rel string) string {
	var sb strings.Builder
	target := "./" + strings.TrimPrefix(rel, "./")
	sb.WriteString("set -e; root=$(pwd -P); target=")
	sb.WriteString(shellQuote(target))
	sb.WriteString("; ")
	sb.WriteString("if [ ! -e \"$target\" ]; then ")
	sb.WriteString("printf '%s\\0%s\\0' ")
	sb.WriteString(shellQuote(listDirStatusMarker))
	sb.WriteString(" 'missing'; exit 0; fi; ")
	sb.WriteString("if [ ! -d \"$target\" ]; then ")
	sb.WriteString("printf '%s\\0%s\\0' ")
	sb.WriteString(shellQuote(listDirStatusMarker))
	sb.WriteString(" 'not_directory'; exit 0; fi; ")
	sb.WriteString("case \"$target\" in ")
	sb.WriteString("'.'|'./.') target_real=\"$root\" ;; ")
	sb.WriteString("*) target_real=$(CDPATH= cd -P \"$target\" 2>/dev/null && pwd -P) ;; ")
	sb.WriteString("esac; ")
	sb.WriteString("if [ -z \"$target_real\" ]; then ")
	sb.WriteString("printf '%s\\0%s\\0' ")
	sb.WriteString(shellQuote(listDirStatusMarker))
	sb.WriteString(" 'missing'; exit 0; fi; ")
	sb.WriteString("case \"$target_real\" in \"$root\"|\"$root\"/*) ;; ")
	sb.WriteString("*) printf '%s\\0%s\\0' ")
	sb.WriteString(shellQuote(listDirStatusMarker))
	sb.WriteString(" 'outside_workspace'; exit 0 ;; ")
	sb.WriteString("esac; ")
	sb.WriteString("for entry in \"$target\"/.[!.]* \"$target\"/..?* \"$target\"/*; do ")
	sb.WriteString("[ -e \"$entry\" ] || continue; ")
	sb.WriteString("kind=other; ")
	sb.WriteString("if [ -L \"$entry\" ]; then kind=other; ")
	sb.WriteString("elif [ -d \"$entry\" ]; then kind=directory; ")
	sb.WriteString("elif [ -f \"$entry\" ]; then kind=file; fi; ")
	sb.WriteString("name=${entry##*/}; relp=${entry#./}; ")
	sb.WriteString("printf '%s\\0%s\\0%s\\0' \"$kind\" \"$name\" \"$relp\"; ")
	sb.WriteString("done")
	return sb.String()
}

func buildWriteFileScript(rel string, overwrite bool) string {
	var sb strings.Builder
	target := "./" + strings.TrimPrefix(rel, "./")
	overwriteValue := "0"
	if overwrite {
		overwriteValue = "1"
	}
	sb.WriteString("set -e; ")
	sb.WriteString("fail() { printf '%s\\n' \"$1\" >&2; exit 1; }; ")
	sb.WriteString("root=$(pwd -P); ")
	sb.WriteString("target=")
	sb.WriteString(shellQuote(target))
	sb.WriteString("; ")
	sb.WriteString("overwrite=")
	sb.WriteString(overwriteValue)
	sb.WriteString("; ")
	sb.WriteString("dir=$target; case \"$target\" in */*) dir=${target%/*} ;; *) dir='.' ;; esac; ")
	sb.WriteString("base=${target##*/}; ")
	sb.WriteString("cur=\"$root\"; ")
	sb.WriteString("oldifs=$IFS; IFS='/'; set -f; ")
	sb.WriteString("for part in $dir; do ")
	sb.WriteString("[ -n \"$part\" ] || continue; ")
	sb.WriteString("[ \"$part\" = '.' ] && continue; ")
	sb.WriteString("next=\"$cur/$part\"; ")
	sb.WriteString("[ -L \"$next\" ] && fail ")
	sb.WriteString(shellQuote("workspace path escapes workspace root: " + rel))
	sb.WriteString("; ")
	sb.WriteString("if [ -e \"$next\" ]; then [ -d \"$next\" ] || fail ")
	sb.WriteString(shellQuote("workspace parent is not a directory: " + rel))
	sb.WriteString("; else mkdir \"$next\"; fi; ")
	sb.WriteString("cur=$(CDPATH= cd -P \"$next\" 2>/dev/null && pwd -P) || fail ")
	sb.WriteString(shellQuote("workspace parent is not accessible: " + rel))
	sb.WriteString("; ")
	sb.WriteString("case \"$cur\" in \"$root\"|\"$root\"/*) ;; *) fail ")
	sb.WriteString(shellQuote("workspace path escapes workspace root: " + rel))
	sb.WriteString(" ;; esac; ")
	sb.WriteString("done; ")
	sb.WriteString("IFS=$oldifs; ")
	sb.WriteString("dest=\"$cur/$base\"; ")
	sb.WriteString("[ -L \"$dest\" ] && fail ")
	sb.WriteString(shellQuote("workspace path escapes workspace root: " + rel))
	sb.WriteString("; ")
	sb.WriteString("if [ -e \"$dest\" ]; then [ -f \"$dest\" ] || fail ")
	sb.WriteString(shellQuote("workspace path is not a file: " + rel))
	sb.WriteString("; [ \"$overwrite\" = 1 ] || fail ")
	sb.WriteString(shellQuote("workspace file exists and overwrite=false: " + rel))
	sb.WriteString("; fi; ")
	sb.WriteString("umask 022; cat > \"$dest\"")
	return sb.String()
}

func parseListDirOutput(
	rel string,
	stdout string,
) ([]workspaceDirEntry, error) {
	if stdout == "" {
		return nil, nil
	}
	fields := strings.Split(stdout, "\x00")
	if len(fields) > 0 && fields[len(fields)-1] == "" {
		fields = fields[:len(fields)-1]
	}
	if len(fields) == 2 && fields[0] == listDirStatusMarker {
		switch fields[1] {
		case "missing":
			return nil, fmt.Errorf("workspace path not found: %s", rel)
		case "not_directory":
			return nil, fmt.Errorf("workspace path is not a directory: %s", rel)
		case "outside_workspace":
			return nil, fmt.Errorf("workspace path escapes workspace root: %s", rel)
		default:
			return nil, fmt.Errorf("workspace_list_dir failed for %s", rel)
		}
	}
	if len(fields)%3 != 0 {
		return nil, fmt.Errorf("invalid workspace_list_dir response")
	}
	entries := make([]workspaceDirEntry, 0, len(fields)/3)
	for i := 0; i < len(fields); i += 3 {
		entries = append(entries, workspaceDirEntry{
			Kind: fields[i],
			Name: fields[i+1],
			Path: fields[i+2],
		})
	}
	return entries, nil
}

func normalizeWorkspaceInspectPath(
	input string,
	defaultPath string,
) (string, error) {
	s := strings.TrimSpace(input)
	if s == "" {
		s = defaultPath
	}
	if strings.TrimSpace(s) == "" {
		return "", errors.New("path is required")
	}
	s = strings.ReplaceAll(s, "\\", "/")
	if containsGlobMeta(s) {
		return "", errors.New(
			"workspace tools only support explicit file or directory paths, not glob patterns",
		)
	}
	if normalized := codeexecutor.NormalizeGlobs([]string{s}); len(normalized) > 0 {
		s = normalized[0]
	}
	s = strings.TrimPrefix(s, "/")
	if s == "" {
		s = "."
	}
	s = sanitizeWorkspaceRelPath(s, "")
	if s == "" {
		return "", fmt.Errorf(
			"path must stay within workspace roots skills/, work/, out/, or runs/",
		)
	}
	return s, nil
}

func normalizeWorkspaceWritePath(input string) (string, error) {
	s := strings.TrimSpace(input)
	s = strings.ReplaceAll(s, "\\", "/")
	if containsGlobMeta(s) {
		return "", errors.New(
			"workspace tools only support explicit file or directory paths, not glob patterns",
		)
	}
	rel, err := normalizeWorkspaceInspectPath(input, "")
	if err != nil {
		return "", err
	}
	if !isAllowedWorkspaceWritePath(rel) {
		return "", fmt.Errorf(
			"path must stay within writable workspace roots work/, out/, or runs/ (excluding work/inputs)",
		)
	}
	switch rel {
	case ".", codeexecutor.DirWork, codeexecutor.DirOut, codeexecutor.DirRuns:
		return "", errors.New("path must name a file, not a workspace root")
	}
	return rel, nil
}

func normalizeWorkspacePublishPath(input string) (string, error) {
	rel, err := normalizeWorkspaceInspectPath(input, "")
	if err != nil {
		return "", err
	}
	if containsGlobMeta(rel) {
		return "", errors.New(
			"artifact_publish only supports explicit file paths, not glob patterns",
		)
	}
	if rel == "." {
		return "", errors.New("path must name a file, not the workspace root")
	}
	return rel, nil
}

func normalizeCollectedWorkspacePath(path string) (string, error) {
	s := strings.TrimSpace(path)
	s = strings.ReplaceAll(s, "\\", "/")
	if s == "" || strings.HasPrefix(s, "/") {
		return "", fmt.Errorf("workspace file resolves outside workspace: %s", path)
	}
	s = sanitizeWorkspaceRelPath(s, "")
	if s == "" {
		return "", fmt.Errorf("workspace file resolves outside workspace: %s", path)
	}
	return s, nil
}

func isAllowedWorkspaceWritePath(rel string) bool {
	switch {
	case rel == codeexecutor.DirWork ||
		strings.HasPrefix(rel, codeexecutor.DirWork+"/"):
		return !isWorkspaceInputsPath(rel)
	case rel == codeexecutor.DirOut ||
		strings.HasPrefix(rel, codeexecutor.DirOut+"/"):
		return true
	case rel == codeexecutor.DirRuns ||
		strings.HasPrefix(rel, codeexecutor.DirRuns+"/"):
		return true
	default:
		return false
	}
}

func isWorkspaceInputsPath(rel string) bool {
	inputs := codeexecutor.DirWork + "/inputs"
	return rel == inputs || strings.HasPrefix(rel, inputs+"/")
}

func containsGlobMeta(s string) bool {
	return strings.ContainsAny(s, "*?[")
}

func withWorkspaceArtifactContext(ctx context.Context) context.Context {
	if svc, ok := codeexecutor.ArtifactServiceFromContext(ctx); ok && svc != nil {
		return ctx
	}
	inv, ok := agent.InvocationFromContext(ctx)
	if !ok || inv == nil || inv.ArtifactService == nil || inv.Session == nil {
		return ctx
	}
	info := artifact.SessionInfo{
		AppName:   inv.Session.AppName,
		UserID:    inv.Session.UserID,
		SessionID: inv.Session.ID,
	}
	ctx = codeexecutor.WithArtifactService(ctx, inv.ArtifactService)
	return codeexecutor.WithArtifactSession(ctx, info)
}

func workspaceSkillName(rel string) string {
	if rel == codeexecutor.DirSkills ||
		!strings.HasPrefix(rel, codeexecutor.DirSkills+"/") {
		return ""
	}
	rest := strings.TrimPrefix(rel, codeexecutor.DirSkills+"/")
	parts := strings.Split(rest, "/")
	if len(parts) == 0 {
		return ""
	}
	return strings.TrimSpace(parts[0])
}

func validateWorkspaceTextFile(file codeexecutor.File) error {
	if !codeexecutor.IsTextMIME(file.MIMEType) {
		return fmt.Errorf(
			"workspace_read_file only supports UTF-8 text files (mime: %s)",
			strings.TrimSpace(file.MIMEType),
		)
	}
	if strings.IndexByte(file.Content, 0) >= 0 ||
		!utf8.ValidString(file.Content) {
		return errors.New(
			"workspace_read_file only supports UTF-8 text files",
		)
	}
	return nil
}

var _ tool.Tool = (*WorkspaceReadFileTool)(nil)
var _ tool.CallableTool = (*WorkspaceReadFileTool)(nil)
var _ tool.Tool = (*WorkspaceListDirTool)(nil)
var _ tool.CallableTool = (*WorkspaceListDirTool)(nil)
var _ tool.Tool = (*WorkspaceWriteFileTool)(nil)
var _ tool.CallableTool = (*WorkspaceWriteFileTool)(nil)
var _ tool.Tool = (*WorkspaceReplaceContentTool)(nil)
var _ tool.CallableTool = (*WorkspaceReplaceContentTool)(nil)
var _ tool.Tool = (*ArtifactPublishTool)(nil)
var _ tool.CallableTool = (*ArtifactPublishTool)(nil)
