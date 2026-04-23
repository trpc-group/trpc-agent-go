//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package claudecode

import (
	"path/filepath"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// NewToolSet constructs a Claude Code-compatible toolset.
func NewToolSet(opts ...Option) (tool.ToolSet, error) {
	options := toolSetOptions{
		name: defaultToolSetName,
		webFetch: WebFetchOptions{
			AllowAll: true,
		},
	}
	for _, opt := range opts {
		opt(&options)
	}
	baseDir := strings.TrimSpace(options.baseDir)
	if baseDir == "" {
		baseDir = "."
	}
	baseAbs, err := filepath.Abs(baseDir)
	if err != nil {
		return nil, err
	}
	runtime := newToolRuntime(baseAbs, options.maxFileSize)
	cc := &compositeToolSet{
		name: options.name,
	}
	if strings.TrimSpace(cc.name) == "" {
		cc.name = defaultToolSetName
	}
	if err := appendCoreTools(cc, runtime, options.readOnly); err != nil {
		return nil, err
	}
	if err := appendWebTools(cc, options); err != nil {
		return nil, err
	}
	return cc, nil
}

func newToolRuntime(baseAbs string, maxFileSize int64) *runtime {
	return &runtime{
		baseDir:     baseAbs,
		maxFileSize: maxFileSize,
		fileState: &fileState{
			views: map[string]fileView{},
		},
		taskState: &taskState{
			tasks: map[string]*backgroundTask{},
		},
	}
}

func appendCoreTools(cc *compositeToolSet, rt *runtime, readOnly bool) error {
	coreTools := []func(*runtime) (tool.Tool, error){
		newBashTool,
		newTaskStopTool,
		newTaskOutputTool,
		newReadTool,
		newGlobTool,
		newGrepTool,
	}
	for _, buildTool := range coreTools {
		builtTool, err := buildTool(rt)
		if err != nil {
			return err
		}
		cc.tools = append(cc.tools, builtTool)
	}
	if readOnly {
		return nil
	}
	writeTool, err := newWriteTool(rt)
	if err != nil {
		return err
	}
	editTool, err := newEditTool(rt)
	if err != nil {
		return err
	}
	notebookEditTool, err := newNotebookEditTool(rt)
	if err != nil {
		return err
	}
	cc.tools = append(cc.tools, writeTool, editTool, notebookEditTool)
	return nil
}

func appendWebTools(cc *compositeToolSet, options toolSetOptions) error {
	webFetchTool, err := newWebFetchTool(options.webFetch)
	if err != nil {
		return err
	}
	webSearchTool, err := newWebSearchTool(options.webSearch)
	if err != nil {
		return err
	}
	cc.tools = append(cc.tools, webFetchTool, webSearchTool)
	return nil
}
