//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package state defines the GraphAgent State key constants used by all nodes.
package state

// Input state keys — set by the CLI before graph execution.
const (
	// StateKeyInputDiffFile is the path to a unified diff file.
	StateKeyInputDiffFile = "input_diff_file"
	// StateKeyInputDiffText is the raw diff text (stdin or CLI flag).
	StateKeyInputDiffText = "input_diff_text"
	// StateKeyInputRepoPath is the git repository path for sandbox execution.
	StateKeyInputRepoPath = "input_repo_path"
	// StateKeyInputBaseRef is the base git ref for diff comparison (e.g., "origin/main").
	StateKeyInputBaseRef = "input_base_ref"
	// StateKeyOutputDir is the directory for JSON/Markdown report output.
	StateKeyOutputDir = "output_dir"
)

// Intermediate state keys — populated by graph nodes during execution.
const (
	// StateKeyTaskID uniquely identifies the current review task (UUID).
	StateKeyTaskID = "task_id"
	// StateKeyFileChanges holds parsed FileChange objects from DiffParser.
	StateKeyFileChanges = "file_changes"
	// StateKeyAllowedCommands holds SandboxCommand objects cleared by PermissionFilter.
	StateKeyAllowedCommands = "allowed_commands"
	// StateKeyPermissionDecisions records allow/deny/needs_human_review for each command.
	StateKeyPermissionDecisions = "permission_decisions"
	// StateKeySandboxResults holds SandboxResult objects from SandboxRunner.
	StateKeySandboxResults = "sandbox_results"
	// StateKeyRuleFindings holds findings produced by the RuleEngine.
	StateKeyRuleFindings = "rule_findings"
	// StateKeyLLMFindings holds findings produced by the LLMAnalyzer.
	StateKeyLLMFindings = "llm_findings"
	// StateKeyLLMErrors records LLM failures (no_model, llm_failure, mock_load).
	StateKeyLLMErrors = "llm_errors"
	// StateKeyFindings holds the final deduplicated findings from DedupEngine.
	StateKeyFindings = "findings"
	// StateKeyWarnings holds low-confidence findings flagged for human review.
	StateKeyWarnings = "warnings"
)

// Configuration state keys — set by the CLI before graph execution.
const (
	// StateKeyExecutorConfig holds the sandbox executor configuration.
	StateKeyExecutorConfig = "executor_config"
	// StateKeyLLMConfig holds the LLM analyzer configuration.
	StateKeyLLMConfig = "llm_config"
	// StateKeyDedupConfig holds the deduplication engine configuration.
	StateKeyDedupConfig = "dedup_config"
	// StateKeySkillRules holds the loaded Rule objects from skill rule files.
	StateKeySkillRules = "skill_rules"
)

// Output state keys — populated by ReportGenerator and StorageWriter.
const (
	// StateKeyJSONReportPath is the file path of the generated JSON report.
	StateKeyJSONReportPath = "json_report_path"
	// StateKeyMDReportPath is the file path of the generated Markdown report.
	StateKeyMDReportPath = "md_report_path"
	// StateKeyStorageDone indicates whether persistence completed successfully.
	StateKeyStorageDone = "storage_done"
)

// Node timing keys — each node writes its own elapsed time in milliseconds.
const (
	StateKeyNodeDiffParserMs       = "node_diffparser_ms"
	StateKeyNodePermissionFilterMs = "node_permissionfilter_ms"
	StateKeyNodeSandboxRunnerMs    = "node_sandboxrunner_ms"
	StateKeyNodeRuleEngineMs       = "node_ruleengine_ms"
	StateKeyNodeLLMAnalyzerMs      = "node_llmanalyzer_ms"
	StateKeyNodeDedupEngineMs      = "node_dedupengine_ms"
	StateKeyNodeReportGeneratorMs  = "node_reportgenerator_ms"
	StateKeyNodeStorageWriterMs    = "node_storagewriter_ms"
)
