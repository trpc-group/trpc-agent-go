//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package toolcache provides per-invocation caches for tools.
//
// It is used to share skill_run output_files (inline content) across tools
// without relying on host filesystem paths.
package toolcache

import (
	"context"
	"slices"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
)

const stateKeySkillRunOutputFiles = "tool:skill_run:output_files"

type cachedSkillRunFile struct {
	Content  string
	MIMEType string
}

// SkillRunOutputFile is a read-only view of an exported skill_run output.
// It is safe to pass across tools because it contains inline content.
type SkillRunOutputFile struct {
	Name     string
	Content  string
	MIMEType string
}

// StoreSkillRunOutputFilesFromContext stores skill_run output_files into the
// invocation state carried by ctx. It is a no-op when ctx has no invocation.
func StoreSkillRunOutputFilesFromContext(
	ctx context.Context,
	files []codeexecutor.File,
) {
	inv, ok := agent.InvocationFromContext(ctx)
	if !ok || inv == nil {
		return
	}
	StoreSkillRunOutputFiles(inv, files)
}

// StoreSkillRunOutputFiles stores skill_run output_files into inv so other
// tools can look them up by name later.
func StoreSkillRunOutputFiles(
	inv *agent.Invocation,
	files []codeexecutor.File,
) {
	if inv == nil || len(files) == 0 {
		return
	}

	merged := make(map[string]cachedSkillRunFile, len(files))
	if existing, ok := inv.GetState(stateKeySkillRunOutputFiles); ok {
		if m, ok := existing.(map[string]cachedSkillRunFile); ok {
			for k, v := range m {
				merged[k] = v
			}
		}
	}

	for _, f := range files {
		name := strings.TrimSpace(f.Name)
		if name == "" {
			continue
		}
		merged[name] = cachedSkillRunFile{
			Content:  f.Content,
			MIMEType: f.MIMEType,
		}
	}

	if len(merged) == 0 {
		return
	}
	inv.SetState(stateKeySkillRunOutputFiles, merged)
}

// LookupSkillRunOutputFileFromContext looks up an exported skill_run output
// file by name from the invocation in ctx.
func LookupSkillRunOutputFileFromContext(
	ctx context.Context,
	name string,
) (string, string, bool) {
	inv, ok := agent.InvocationFromContext(ctx)
	if !ok || inv == nil {
		return "", "", false
	}
	return LookupSkillRunOutputFile(inv, name)
}

// LookupSkillRunOutputFile looks up an exported skill_run output file by name
// from inv. It returns (content, mime, ok).
func LookupSkillRunOutputFile(
	inv *agent.Invocation,
	name string,
) (string, string, bool) {
	if inv == nil {
		return "", "", false
	}
	n := strings.TrimSpace(name)
	if n == "" {
		return "", "", false
	}

	v, ok := inv.GetState(stateKeySkillRunOutputFiles)
	if !ok {
		return "", "", false
	}
	m, ok := v.(map[string]cachedSkillRunFile)
	if !ok {
		return "", "", false
	}
	f, ok := m[n]
	if !ok {
		return "", "", false
	}
	return f.Content, f.MIMEType, true
}

// SkillRunOutputFilesFromContext returns a stable list of exported skill_run
// output files from the invocation in ctx.
func SkillRunOutputFilesFromContext(
	ctx context.Context,
) []SkillRunOutputFile {
	inv, ok := agent.InvocationFromContext(ctx)
	if !ok || inv == nil {
		return nil
	}
	return SkillRunOutputFiles(inv)
}

// SkillRunOutputFiles returns a stable list of exported skill_run output
// files from inv.
func SkillRunOutputFiles(
	inv *agent.Invocation,
) []SkillRunOutputFile {
	if inv == nil {
		return nil
	}
	v, ok := inv.GetState(stateKeySkillRunOutputFiles)
	if !ok {
		return nil
	}
	m, ok := v.(map[string]cachedSkillRunFile)
	if !ok || len(m) == 0 {
		return nil
	}

	out := make([]SkillRunOutputFile, 0, len(m))
	for name, f := range m {
		out = append(out, SkillRunOutputFile{
			Name:     name,
			Content:  f.Content,
			MIMEType: f.MIMEType,
		})
	}
	slices.SortFunc(out, func(a, b SkillRunOutputFile) int {
		return strings.Compare(a.Name, b.Name)
	})
	return out
}
