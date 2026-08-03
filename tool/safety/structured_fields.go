//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

import (
	"context"
	"fmt"
	"path"
	"path/filepath"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

const maxStructuredExecutionEntries = 1024

func scanStructuredExecutionFields(
	ctx context.Context,
	req Request,
	policy Policy,
) []Match {
	if ctx.Err() != nil {
		return nil
	}
	matches := make([]Match, 0, 8)
	if structuredExecutionFieldsExceedBounds(req, policy) {
		return append(matches, newMatch(
			tool.PermissionActionAsk,
			RiskLevelHigh,
			"limits.structured_execution_fields",
			"structured execution fields exceed the safety scan budget",
			"Reduce staged inputs and output declarations or require human review.",
		))
	}
	matches = append(matches, scanSessionControls(req, policy)...)
	matches = append(matches, scanExecutionIdentity(req)...)
	matches = append(matches, scanSkillIdentity(req)...)
	if req.Skill != "" {
		matches = append(matches, scanTextHazards(req.Skill, policy)...)
	}
	if req.EditorText != "" {
		matches = append(matches, scanTextHazards(req.EditorText, policy)...)
	}
	for _, input := range req.Inputs {
		matches = append(matches, scanSkillInput(input, policy)...)
	}
	for _, output := range req.OutputFiles {
		matches = append(matches, scanOutputPath(output, policy)...)
	}
	if len(req.OutputFiles) > 0 {
		matches = append(matches, newMatch(
			tool.PermissionActionAsk,
			RiskLevelHigh,
			"skill.output.limits",
			"legacy skill output collection does not declare byte and file-count limits",
			"Use outputs with explicit max_files, max_file_bytes, and max_total_bytes values.",
		))
	}
	if req.Outputs != nil {
		if len(req.Outputs.Globs) == 0 {
			matches = append(matches, broadDefaultOutputMatch())
		}
		for _, output := range req.Outputs.Globs {
			matches = append(matches, scanOutputPath(output, policy)...)
		}
		matches = append(matches, scanOutputLimits(*req.Outputs, policy)...)
	}
	if req.SaveArtifacts || (req.Outputs != nil && req.Outputs.Save) {
		matches = append(matches, newMatch(
			tool.PermissionActionAsk,
			RiskLevelMedium,
			"artifact.persistence",
			"execution requests persistence outside the ephemeral workspace",
			"Review collected paths and apply artifact size, name, and retention limits.",
		))
		if req.ArtifactPrefix != "" {
			matches = append(matches, scanArtifactPrefix(req.ArtifactPrefix, policy)...)
		}
		if req.Outputs != nil && req.Outputs.NameTemplate != "" {
			matches = append(matches, scanArtifactPrefix(req.Outputs.NameTemplate, policy)...)
		}
	}
	return matches
}

func structuredExecutionFieldsExceedBounds(req Request, policy Policy) bool {
	if len(req.EditorText) > policy.Limits.MaxSessionInputBytes {
		return true
	}
	count := len(req.Inputs) + len(req.OutputFiles) + len(req.Env)
	if req.Outputs != nil {
		count += len(req.Outputs.Globs)
	}
	if count > maxStructuredExecutionEntries {
		return true
	}
	total := len(req.Skill) + len(req.ExecutionID) + len(req.SessionID) +
		len(req.EditorText) + len(req.ArtifactPrefix) + len(req.CWD)
	for key, value := range req.Env {
		total += len(key) + len(value)
		if total > policy.Limits.MaxScriptBytes {
			return true
		}
	}
	for _, input := range req.Inputs {
		total += len(input.From) + len(input.To) + len(input.Mode)
		if total > policy.Limits.MaxScriptBytes {
			return true
		}
	}
	return structuredExecutionRemainderExceedsBounds(req, policy, total)
}

func structuredExecutionRemainderExceedsBounds(
	req Request,
	policy Policy,
	total int,
) bool {
	for _, output := range req.OutputFiles {
		total += len(output)
		if total > policy.Limits.MaxScriptBytes {
			return true
		}
	}
	if req.Outputs != nil {
		total += len(req.Outputs.NameTemplate)
		for _, output := range req.Outputs.Globs {
			total += len(output)
			if total > policy.Limits.MaxScriptBytes {
				return true
			}
		}
	}
	return total > policy.Limits.MaxScriptBytes
}

func scanSessionControls(req Request, policy Policy) []Match {
	matches := make([]Match, 0, 3)
	isSessionControl := isSessionControlTool(req.ToolName)
	if isSessionControl && strings.TrimSpace(req.SessionID) == "" {
		matches = append(matches, newMatch(
			tool.PermissionActionAsk,
			RiskLevelHigh,
			"session.id",
			"session control request has no explicit session identifier",
			"Provide the reviewed target session before continuing.",
		))
	} else if isSessionControl && req.Backend != BackendHost {
		matches = append(matches, newMatch(
			tool.PermissionActionAsk,
			RiskLevelHigh,
			"session.ownership",
			"session control request cannot prove that the target belongs to this invocation",
			"Verify session ownership in trusted application context before polling, writing, or terminating it.",
		))
	}
	invalidYield := req.YieldMS != nil && *req.YieldMS < 0
	if invalidYield || req.PollLines < 0 {
		matches = append(matches, newMatch(
			tool.PermissionActionDeny,
			RiskLevelHigh,
			"limits.invalid",
			"session yield or poll limit is negative",
			"Use non-negative bounded session-control values.",
		))
		return matches
	}
	maximumYield := time.Duration(policy.Limits.MaxTimeoutSeconds) * time.Second
	if req.YieldMS != nil &&
		saturatedDuration(int64(*req.YieldMS), time.Millisecond) > maximumYield {
		matches = append(matches, newMatch(
			tool.PermissionActionAsk,
			RiskLevelHigh,
			"limits.session_yield",
			"session yield duration exceeds the policy timeout limit",
			"Reduce yield_ms or require explicit human review.",
		))
	}
	if req.PollLines > policy.Limits.MaxScriptLines {
		matches = append(matches, newMatch(
			tool.PermissionActionAsk,
			RiskLevelHigh,
			"limits.session_poll",
			"session poll line count exceeds the bounded review limit",
			"Reduce poll_lines and retain executor-side output truncation.",
		))
	}
	return matches
}

func scanExecutionIdentity(req Request) []Match {
	if normalizedToolName(req.ToolName) != "execute_code" ||
		strings.TrimSpace(req.ExecutionID) == "" {
		return nil
	}
	return []Match{newMatch(
		tool.PermissionActionAsk,
		RiskLevelHigh,
		"code.workspace_reuse",
		"explicit execution identifier can reuse process-scoped workspace state",
		"Use an invocation-owned execution identifier or verify ownership before reusing the workspace.",
	)}
}

func scanSkillIdentity(req Request) []Match {
	if !isSkillExecutionTool(req.ToolName) || strings.TrimSpace(req.Skill) != "" {
		return nil
	}
	return []Match{newMatch(
		tool.PermissionActionAsk,
		RiskLevelHigh,
		"skill.id",
		"skill execution request has no explicit skill identifier",
		"Provide the reviewed skill identifier before continuing.",
	)}
}

func isSkillExecutionTool(toolName string) bool {
	switch normalizedToolName(toolName) {
	case "skill_exec", "skill_run":
		return true
	default:
		return false
	}
}

func isSessionControlTool(toolName string) bool {
	switch normalizedToolName(toolName) {
	case "write_stdin", "kill_session",
		"skill_write_stdin", "skill_poll_session", "skill_kill_session",
		"workspace_write_stdin", "workspace_kill_session":
		return true
	default:
		return false
	}
}

func isWriteStdinTool(toolName string) bool {
	switch normalizedToolName(toolName) {
	case "write_stdin", "skill_write_stdin", "workspace_write_stdin":
		return true
	default:
		return false
	}
}

func scanSkillInput(input InputSpec, policy Policy) []Match {
	matches := make([]Match, 0, 4)
	if strings.TrimSpace(input.From) == "" {
		matches = append(matches, newMatch(
			tool.PermissionActionAsk,
			RiskLevelHigh,
			"skill.input.source",
			"skill input has no explicit source",
			"Provide a reviewed artifact, workspace, skill, or host reference.",
		))
	} else {
		matches = append(matches, scanTextHazards(input.From, policy)...)
		matches = append(matches, scanInputSource(input.From)...)
	}
	if input.To != "" {
		matches = append(matches, scanTextHazards(input.To, policy)...)
		if unsafeWorkspaceRelativePath(input.To) {
			matches = append(matches, newMatch(
				tool.PermissionActionDeny,
				RiskLevelHigh,
				"skill.input.destination",
				"skill input destination escapes or bypasses the workspace-relative boundary",
				"Stage the input under a reviewed workspace-relative destination.",
			))
		}
	}
	switch strings.ToLower(strings.TrimSpace(input.Mode)) {
	case "", "copy", "link":
	default:
		matches = append(matches, newMatch(
			tool.PermissionActionAsk,
			RiskLevelHigh,
			"skill.input.mode",
			fmt.Sprintf("skill input uses unsupported staging mode %q", input.Mode),
			"Use copy or link only after reviewing the workspace runtime semantics.",
		))
	}
	return matches
}

func scanInputSource(source string) []Match {
	lower := strings.ToLower(strings.TrimSpace(source))
	separator := strings.Index(lower, "://")
	if separator < 0 {
		return []Match{newMatch(
			tool.PermissionActionAsk,
			RiskLevelHigh,
			"skill.input.source",
			"skill input source does not use an explicit supported reference scheme",
			"Use artifact://, workspace://, skill://, or a reviewed host:// reference.",
		)}
	}
	scheme := lower[:separator]
	remainder := strings.TrimSpace(source[separator+3:])
	return scanInputSourceScheme(scheme, remainder)
}

func scanInputSourceScheme(scheme, remainder string) []Match {
	switch scheme {
	case "host":
		return []Match{newMatch(
			tool.PermissionActionAsk,
			RiskLevelHigh,
			"skill.input.host_source",
			"skill input reads from the host filesystem",
			"Prefer an artifact or workspace reference, or require explicit human approval.",
		)}
	case "artifact", "workspace", "skill":
		if remainder == "" || unsafeReferenceRemainder(remainder) {
			return []Match{newMatch(
				tool.PermissionActionDeny,
				RiskLevelHigh,
				"skill.input.source",
				"skill input source contains an empty, absolute, or traversing reference",
				"Use a scoped reference that cannot escape its declared source.",
			)}
		}
		return nil
	default:
		return []Match{newMatch(
			tool.PermissionActionAsk,
			RiskLevelHigh,
			"skill.input.source_scheme",
			fmt.Sprintf("skill input uses unsupported source scheme %q", scheme),
			"Use artifact://, workspace://, skill://, or a reviewed host:// reference.",
		)}
	}
}

func scanOutputPath(output string, policy Policy) []Match {
	if strings.TrimSpace(output) == "" {
		return []Match{newMatch(
			tool.PermissionActionAsk,
			RiskLevelMedium,
			"skill.output.path",
			"skill output contains an empty path or glob",
			"Declare a scoped workspace-relative output pattern.",
		)}
	}
	matches := scanTextHazards(output, policy)
	if unsafeWorkspaceRelativePath(output) {
		matches = append(matches, newMatch(
			tool.PermissionActionDeny,
			RiskLevelHigh,
			"skill.output.path",
			"skill output path escapes or bypasses the workspace-relative boundary",
			"Collect only reviewed workspace-relative output paths.",
		))
	}
	if isBroadOutputPattern(output) {
		matches = append(matches, newMatch(
			tool.PermissionActionAsk,
			RiskLevelHigh,
			"skill.output.broad_glob",
			"skill output pattern can collect the entire workspace",
			"Use a scoped output directory and a narrow file pattern.",
		))
	}
	return matches
}

func scanOutputLimits(outputs OutputSpec, policy Policy) []Match {
	if outputs.MaxFiles < 0 || outputs.MaxFileBytes < 0 ||
		outputs.MaxTotalBytes < 0 {
		return []Match{newMatch(
			tool.PermissionActionDeny,
			RiskLevelHigh,
			"skill.output.limits",
			"skill output collection contains a negative resource limit",
			"Use non-negative collection limits bounded by policy.",
		)}
	}
	if outputs.MaxFiles == 0 || outputs.MaxFileBytes == 0 ||
		outputs.MaxTotalBytes == 0 {
		return []Match{newMatch(
			tool.PermissionActionAsk,
			RiskLevelHigh,
			"skill.output.limits",
			"skill output collection relies on runtime defaults instead of explicit limits",
			"Set max_files, max_file_bytes, and max_total_bytes within policy.",
		)}
	}
	if outputs.MaxFiles > maxStructuredExecutionEntries {
		return []Match{newMatch(
			tool.PermissionActionAsk,
			RiskLevelHigh,
			"skill.output.limits",
			"skill output file count exceeds the safety review limit",
			"Reduce max_files and use a scoped output pattern.",
		)}
	}
	if outputs.MaxFileBytes > policy.Limits.MaxOutputBytes ||
		outputs.MaxTotalBytes > policy.Limits.MaxOutputBytes {
		return []Match{newMatch(
			tool.PermissionActionAsk,
			RiskLevelHigh,
			"skill.output.limits",
			"skill output collection exceeds the policy byte limit",
			"Reduce collection limits and keep process-output enforcement configured separately.",
		)}
	}
	return nil
}

func scanArtifactPrefix(prefix string, policy Policy) []Match {
	matches := scanTextHazards(prefix, policy)
	if unsafeWorkspaceRelativePath(prefix) {
		matches = append(matches, newMatch(
			tool.PermissionActionDeny,
			RiskLevelHigh,
			"artifact.name",
			"artifact prefix or name template is absolute, traversing, or uses a URI scheme",
			"Use a scoped relative artifact namespace.",
		))
	}
	return matches
}

func unsafeReferenceRemainder(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" || strings.Contains(value, "://") {
		return true
	}
	return unsafeWorkspaceRelativePath(value)
}

func unsafeWorkspaceRelativePath(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}
	if strings.ContainsRune(value, '\x00') || strings.Contains(value, "://") {
		return true
	}
	normalized := strings.ReplaceAll(value, "\\", "/")
	if strings.HasPrefix(normalized, "/") || filepath.IsAbs(value) ||
		hasWindowsDrivePrefix(normalized) {
		return true
	}
	cleaned := path.Clean(normalized)
	return cleaned == ".." || strings.HasPrefix(cleaned, "../")
}

func hasWindowsDrivePrefix(value string) bool {
	if len(value) < 2 || value[1] != ':' {
		return false
	}
	first := value[0]
	return first >= 'a' && first <= 'z' ||
		first >= 'A' && first <= 'Z'
}

func isBroadOutputPattern(value string) bool {
	value = strings.TrimSpace(strings.ReplaceAll(value, "\\", "/"))
	for strings.HasPrefix(value, "./") {
		value = strings.TrimPrefix(value, "./")
	}
	value = path.Clean(value)
	switch value {
	case ".", "*", "**", "**/*":
		return true
	}
	firstWildcard := strings.IndexAny(value, "*?[{")
	if firstWildcard < 0 {
		return false
	}
	literalRoot := strings.Trim(value[:firstWildcard], "/")
	return literalRoot == ""
}

func broadDefaultOutputMatch() Match {
	return newMatch(
		tool.PermissionActionAsk,
		RiskLevelHigh,
		"skill.output.broad_glob",
		"skill output collection omits globs and therefore uses the runtime default scope",
		"Declare a scoped workspace-relative output pattern.",
	)
}
