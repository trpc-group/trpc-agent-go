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

func SkillRunOutputFilesFromContext(
	ctx context.Context,
) []SkillRunOutputFile {
	inv, ok := agent.InvocationFromContext(ctx)
	if !ok || inv == nil {
		return nil
	}
	return SkillRunOutputFiles(inv)
}

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
