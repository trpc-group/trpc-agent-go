//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package skill

import (
	"context"
	"encoding/json"
	"fmt"
	"path"
	"slices"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

const (
	maxReadLinesDefault = 4000
)

type WorkspaceReadFileTool struct {
	run *RunTool
}

type WorkspaceWriteFileTool struct {
	run *RunTool
}

type WorkspaceReplaceContentTool struct {
	run *RunTool
}

type WorkspaceListDirTool struct {
	run *RunTool
}

type workspaceReadFileInput struct {
	Skill     string `json:"skill"`
	Path      string `json:"path"`
	StartLine *int   `json:"start_line,omitempty"`
	NumLines  *int   `json:"num_lines,omitempty"`
}

type workspaceReadFileOutput struct {
	Path      string `json:"path"`
	Content   string `json:"content"`
	StartLine int    `json:"start_line"`
	EndLine   int    `json:"end_line"`
	Total     int    `json:"total_lines"`
}

type workspaceWriteFileInput struct {
	Skill      string `json:"skill"`
	Path       string `json:"path"`
	Content    string `json:"content"`
	Overwrite  bool   `json:"overwrite,omitempty"`
	CreateDirs bool   `json:"create_dirs,omitempty"`
}

type workspaceWriteFileOutput struct {
	Path         string `json:"path"`
	BytesWritten int    `json:"bytes_written"`
	Changed      bool   `json:"changed"`
}

type workspaceReplaceContentInput struct {
	Skill           string `json:"skill"`
	Path            string `json:"path"`
	OldString       string `json:"old_string"`
	NewString       string `json:"new_string"`
	NumReplacements int    `json:"num_replacements,omitempty"`
}

type workspaceReplaceContentOutput struct {
	Path          string `json:"path"`
	Replacements  int    `json:"replacements"`
	TotalMatches  int    `json:"total_matches"`
	Changed       bool   `json:"changed"`
	BytesWritten  int    `json:"bytes_written"`
}

type workspaceListDirInput struct {
	Skill string `json:"skill"`
	Path  string `json:"path,omitempty"`
}

type workspaceListDirOutput struct {
	Path    string   `json:"path"`
	Files   []string `json:"files"`
	Folders []string `json:"folders"`
}

func NewWorkspaceReadFileTool(run *RunTool) *WorkspaceReadFileTool {
	return &WorkspaceReadFileTool{run: run}
}

func NewWorkspaceWriteFileTool(run *RunTool) *WorkspaceWriteFileTool {
	return &WorkspaceWriteFileTool{run: run}
}

func NewWorkspaceReplaceContentTool(
	run *RunTool,
) *WorkspaceReplaceContentTool {
	return &WorkspaceReplaceContentTool{run: run}
}

func NewWorkspaceListDirTool(run *RunTool) *WorkspaceListDirTool {
	return &WorkspaceListDirTool{run: run}
}

func (t *WorkspaceReadFileTool) Declaration() *tool.Declaration {
	return &tool.Declaration{
		Name: "skill_ws_read_file",
		Description: "Read a UTF-8 text file from a skill workspace. " +
			"Only paths under skills/, work/, out/, runs/ are allowed.",
		InputSchema: &tool.Schema{
			Type:     "object",
			Required: []string{"skill", "path"},
			Properties: map[string]*tool.Schema{
				"skill":      skillNameSchema(t.run.repo, "Skill name"),
				"path":       {Type: "string", Description: "Workspace-relative file path"},
				"start_line": {Type: "integer", Description: "1-based start line"},
				"num_lines":  {Type: "integer", Description: "Max lines to read"},
			},
		},
		OutputSchema: &tool.Schema{
			Type:     "object",
			Required: []string{"path", "content", "start_line", "end_line", "total_lines"},
			Properties: map[string]*tool.Schema{
				"path":       {Type: "string"},
				"content":    {Type: "string"},
				"start_line": {Type: "integer"},
				"end_line":   {Type: "integer"},
				"total_lines": {Type: "integer"},
			},
		},
	}
}

func (t *WorkspaceWriteFileTool) Declaration() *tool.Declaration {
	return &tool.Declaration{
		Name: "skill_ws_write_file",
		Description: "Write a UTF-8 text file into a skill workspace. " +
			"Only paths under skills/, work/, out/, runs/ are allowed.",
		InputSchema: &tool.Schema{
			Type:     "object",
			Required: []string{"skill", "path", "content"},
			Properties: map[string]*tool.Schema{
				"skill":       skillNameSchema(t.run.repo, "Skill name"),
				"path":        {Type: "string", Description: "Workspace-relative file path"},
				"content":     {Type: "string", Description: "Text file content"},
				"overwrite":   {Type: "boolean", Description: "Overwrite existing file"},
				"create_dirs": {Type: "boolean", Description: "Create parent directories"},
			},
		},
		OutputSchema: &tool.Schema{
			Type:     "object",
			Required: []string{"path", "bytes_written", "changed"},
			Properties: map[string]*tool.Schema{
				"path":          {Type: "string"},
				"bytes_written": {Type: "integer"},
				"changed":       {Type: "boolean"},
			},
		},
	}
}

func (t *WorkspaceReplaceContentTool) Declaration() *tool.Declaration {
	return &tool.Declaration{
		Name: "skill_ws_replace_content",
		Description: "Replace text content in a skill workspace file. " +
			"Only paths under skills/, work/, out/, runs/ are allowed.",
		InputSchema: &tool.Schema{
			Type:     "object",
			Required: []string{"skill", "path", "old_string", "new_string"},
			Properties: map[string]*tool.Schema{
				"skill": {
					Type: "string",
				},
				"path": {
					Type: "string",
				},
				"old_string": {
					Type: "string",
				},
				"new_string": {
					Type: "string",
				},
				"num_replacements": {
					Type: "integer",
				},
			},
		},
		OutputSchema: &tool.Schema{
			Type:     "object",
			Required: []string{"path", "replacements", "total_matches", "changed", "bytes_written"},
			Properties: map[string]*tool.Schema{
				"path":           {Type: "string"},
				"replacements":   {Type: "integer"},
				"total_matches":  {Type: "integer"},
				"changed":        {Type: "boolean"},
				"bytes_written":  {Type: "integer"},
			},
		},
	}
}

func (t *WorkspaceListDirTool) Declaration() *tool.Declaration {
	return &tool.Declaration{
		Name: "skill_ws_list_dir",
		Description: "List files and folders under a workspace path. " +
			"Only paths under skills/, work/, out/, runs/ are allowed.",
		InputSchema: &tool.Schema{
			Type:     "object",
			Required: []string{"skill"},
			Properties: map[string]*tool.Schema{
				"skill": skillNameSchema(t.run.repo, "Skill name"),
				"path":  {Type: "string", Description: "Workspace-relative directory path"},
			},
		},
		OutputSchema: &tool.Schema{
			Type:     "object",
			Required: []string{"path", "files", "folders"},
			Properties: map[string]*tool.Schema{
				"path":    {Type: "string"},
				"files":   {Type: "array", Items: &tool.Schema{Type: "string"}},
				"folders": {Type: "array", Items: &tool.Schema{Type: "string"}},
			},
		},
	}
}

func (t *WorkspaceReadFileTool) Call(
	ctx context.Context,
	args []byte,
) (any, error) {
	if t == nil || t.run == nil {
		return nil, fmt.Errorf("tool is not configured")
	}
	var in workspaceReadFileInput
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid args: %w", err)
	}
	rel, err := normalizeWorkspacePath(in.Path)
	if err != nil {
		return nil, err
	}
	if hasGlob(rel) {
		return nil, fmt.Errorf("path does not support glob patterns")
	}
	eng, ws, err := t.run.ensureSkillWorkspace(ctx, in.Skill)
	if err != nil {
		return nil, err
	}
	content, err := loadWorkspaceTextFile(ctx, eng, ws, rel)
	if err != nil {
		return nil, err
	}
	chunk, start, end, total, err := sliceWorkspaceText(
		content, in.StartLine, in.NumLines,
	)
	if err != nil {
		return nil, err
	}
	return workspaceReadFileOutput{
		Path:      rel,
		Content:   chunk,
		StartLine: start,
		EndLine:   end,
		Total:     total,
	}, nil
}

func (t *WorkspaceWriteFileTool) Call(
	ctx context.Context,
	args []byte,
) (any, error) {
	if t == nil || t.run == nil {
		return nil, fmt.Errorf("tool is not configured")
	}
	var in workspaceWriteFileInput
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid args: %w", err)
	}
	rel, err := normalizeWorkspacePath(in.Path)
	if err != nil {
		return nil, err
	}
	if hasGlob(rel) {
		return nil, fmt.Errorf("path does not support glob patterns")
	}
	eng, ws, err := t.run.ensureSkillWorkspace(ctx, in.Skill)
	if err != nil {
		return nil, err
	}
	changed, err := writeWorkspaceTextFile(
		ctx, eng, ws, rel, in.Content, in.Overwrite, in.CreateDirs,
	)
	if err != nil {
		return nil, err
	}
	return workspaceWriteFileOutput{
		Path:         rel,
		BytesWritten: len(in.Content),
		Changed:      changed,
	}, nil
}

func (t *WorkspaceReplaceContentTool) Call(
	ctx context.Context,
	args []byte,
) (any, error) {
	if t == nil || t.run == nil {
		return nil, fmt.Errorf("tool is not configured")
	}
	var in workspaceReplaceContentInput
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid args: %w", err)
	}
	if in.OldString == "" {
		return nil, fmt.Errorf("old_string is required")
	}
	rel, err := normalizeWorkspacePath(in.Path)
	if err != nil {
		return nil, err
	}
	if hasGlob(rel) {
		return nil, fmt.Errorf("path does not support glob patterns")
	}
	eng, ws, err := t.run.ensureSkillWorkspace(ctx, in.Skill)
	if err != nil {
		return nil, err
	}
	out, err := replaceWorkspaceText(
		ctx, eng, ws, rel, in.OldString, in.NewString, in.NumReplacements,
	)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (t *WorkspaceListDirTool) Call(
	ctx context.Context,
	args []byte,
) (any, error) {
	if t == nil || t.run == nil {
		return nil, fmt.Errorf("tool is not configured")
	}
	var in workspaceListDirInput
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid args: %w", err)
	}
	rel := strings.TrimSpace(in.Path)
	if rel == "" {
		rel = codeexecutor.DirWork
	}
	rel, err := normalizeWorkspacePath(rel)
	if err != nil {
		return nil, err
	}
	eng, ws, err := t.run.ensureSkillWorkspace(ctx, in.Skill)
	if err != nil {
		return nil, err
	}
	files, folders, err := listWorkspaceEntries(ctx, eng, ws, rel)
	if err != nil {
		return nil, err
	}
	return workspaceListDirOutput{
		Path:    rel,
		Files:   files,
		Folders: folders,
	}, nil
}

func (t *RunTool) ensureSkillWorkspace(
	ctx context.Context,
	skillName string,
) (codeexecutor.Engine, codeexecutor.Workspace, error) {
	name := strings.TrimSpace(skillName)
	if name == "" {
		return nil, codeexecutor.Workspace{}, fmt.Errorf("skill is required")
	}
	root, err := t.repo.Path(name)
	if err != nil {
		return nil, codeexecutor.Workspace{}, err
	}
	eng := t.ensureEngine()
	ws, err := t.createWorkspace(ctx, eng, name)
	if err != nil {
		return nil, codeexecutor.Workspace{}, err
	}
	if err := t.stageSkill(ctx, eng, ws, root, name); err != nil {
		return nil, codeexecutor.Workspace{}, err
	}
	return eng, ws, nil
}

func normalizeWorkspacePath(p string) (string, error) {
	trimmed := strings.TrimSpace(strings.ReplaceAll(p, "\\", "/"))
	if trimmed == "" {
		return "", fmt.Errorf("path is required")
	}
	cleaned := strings.TrimPrefix(path.Clean(trimmed), "/")
	if cleaned == "." || cleaned == "" {
		return "", fmt.Errorf("path is required")
	}
	if cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", fmt.Errorf("path escapes workspace: %s", p)
	}
	if !isAllowedWorkspacePath(cleaned) {
		return "", fmt.Errorf("path is not allowed: %s", p)
	}
	return cleaned, nil
}

func hasGlob(p string) bool {
	return strings.ContainsAny(p, "*?[]{}")
}

func loadWorkspaceTextFile(
	ctx context.Context,
	eng codeexecutor.Engine,
	ws codeexecutor.Workspace,
	rel string,
) (string, error) {
	files, err := eng.FS().Collect(ctx, ws, []string{rel})
	if err != nil {
		return "", err
	}
	for _, f := range files {
		if strings.TrimSpace(f.Name) == rel {
			return f.Content, nil
		}
	}
	return "", fmt.Errorf("file not found: %s", rel)
}

func writeWorkspaceTextFile(
	ctx context.Context,
	eng codeexecutor.Engine,
	ws codeexecutor.Workspace,
	rel string,
	content string,
	overwrite bool,
	createDirs bool,
) (bool, error) {
	if !createDirs {
		parent := path.Dir(rel)
		ok, err := workspaceDirExists(ctx, eng, ws, parent)
		if err != nil {
			return false, err
		}
		if !ok {
			return false, fmt.Errorf(
				"parent directory does not exist: %s", parent,
			)
		}
	}
	existing, err := loadWorkspaceTextFile(ctx, eng, ws, rel)
	if err == nil && !overwrite {
		return false, fmt.Errorf("file exists and overwrite=false: %s", rel)
	}
	if err == nil && existing == content {
		return false, nil
	}
	put := codeexecutor.PutFile{
		Path:    rel,
		Content: []byte(content),
		Mode:    codeexecutor.DefaultScriptFileMode,
	}
	if err := eng.FS().PutFiles(ctx, ws, []codeexecutor.PutFile{put}); err != nil {
		return false, err
	}
	return true, nil
}

func workspaceDirExists(
	ctx context.Context,
	eng codeexecutor.Engine,
	ws codeexecutor.Workspace,
	dir string,
) (bool, error) {
	if dir == "" || dir == "." {
		return true, nil
	}
	if eng == nil || eng.Runner() == nil {
		return false, fmt.Errorf("workspace runner is not configured")
	}
	cmd := "test -d " + shellQuote(dir)
	res, err := eng.Runner().RunProgram(
		ctx,
		ws,
		codeexecutor.RunProgramSpec{
			Cmd:     "bash",
			Args:    []string{"-lc", cmd},
			Cwd:     ".",
			Timeout: 2 * time.Second,
			Env:     map[string]string{},
		},
	)
	if err != nil {
		return false, err
	}
	return res.ExitCode == 0, nil
}

func replaceWorkspaceText(
	ctx context.Context,
	eng codeexecutor.Engine,
	ws codeexecutor.Workspace,
	rel string,
	oldValue string,
	newValue string,
	num int,
) (workspaceReplaceContentOutput, error) {
	content, err := loadWorkspaceTextFile(ctx, eng, ws, rel)
	if err != nil {
		return workspaceReplaceContentOutput{}, err
	}
	total := strings.Count(content, oldValue)
	if total == 0 {
		return workspaceReplaceContentOutput{
			Path: rel, TotalMatches: 0, Replacements: 0, Changed: false,
		}, nil
	}
	if num == 0 {
		num = 1
	}
	if num < 0 || num > total {
		num = total
	}
	replaced := strings.Replace(content, oldValue, newValue, num)
	put := codeexecutor.PutFile{
		Path:    rel,
		Content: []byte(replaced),
		Mode:    codeexecutor.DefaultScriptFileMode,
	}
	if err := eng.FS().PutFiles(ctx, ws, []codeexecutor.PutFile{put}); err != nil {
		return workspaceReplaceContentOutput{}, err
	}
	return workspaceReplaceContentOutput{
		Path: rel, Replacements: num, TotalMatches: total, Changed: true,
		BytesWritten: len(replaced),
	}, nil
}

func listWorkspaceEntries(
	ctx context.Context,
	eng codeexecutor.Engine,
	ws codeexecutor.Workspace,
	dir string,
) ([]string, []string, error) {
	pattern := path.Join(dir, "**")
	files, err := eng.FS().Collect(ctx, ws, []string{pattern})
	if err != nil {
		return nil, nil, err
	}
	fileSet := map[string]struct{}{}
	folderSet := map[string]struct{}{}
	for _, f := range files {
		name := strings.TrimSpace(f.Name)
		if name == "" || name == dir {
			continue
		}
		rel := strings.TrimPrefix(name, dir+"/")
		head, _, ok := strings.Cut(rel, "/")
		if !ok {
			fileSet[path.Join(dir, head)] = struct{}{}
			continue
		}
		folderSet[path.Join(dir, head)] = struct{}{}
	}
	outFiles := mapKeys(fileSet)
	outFolders := mapKeys(folderSet)
	return outFiles, outFolders, nil
}

func mapKeys(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

func sliceWorkspaceText(
	text string,
	startLine *int,
	numLines *int,
) (string, int, int, int, error) {
	lines := strings.Split(text, "\n")
	total := len(lines)
	if total == 0 {
		return "", 0, 0, 0, nil
	}
	start := 1
	if startLine != nil {
		start = *startLine
	}
	if start <= 0 || start > total {
		return "", 0, 0, total, fmt.Errorf("start_line out of range")
	}
	limit := maxReadLinesDefault
	if numLines != nil {
		limit = *numLines
	}
	if limit <= 0 {
		return "", 0, 0, total, fmt.Errorf("num_lines must be > 0")
	}
	end := start + limit - 1
	if end > total {
		end = total
	}
	return strings.Join(lines[start-1:end], "\n"), start, end, total, nil
}

var _ tool.Tool = (*WorkspaceReadFileTool)(nil)
var _ tool.CallableTool = (*WorkspaceReadFileTool)(nil)
var _ tool.Tool = (*WorkspaceWriteFileTool)(nil)
var _ tool.CallableTool = (*WorkspaceWriteFileTool)(nil)
var _ tool.Tool = (*WorkspaceReplaceContentTool)(nil)
var _ tool.CallableTool = (*WorkspaceReplaceContentTool)(nil)
var _ tool.Tool = (*WorkspaceListDirTool)(nil)
var _ tool.CallableTool = (*WorkspaceListDirTool)(nil)
