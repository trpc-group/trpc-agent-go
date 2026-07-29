//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package state defines the GraphAgent State key constants used by all nodes.
package state

// Input keys
const (
	StateKeyInputDiffFile = "input_diff_file"
	StateKeyInputDiffText = "input_diff_text"
	StateKeyInputRepoPath = "input_repo_path" // reserved
	StateKeyInputBaseRef  = "input_base_ref"
	StateKeyOutputDir     = "output_dir"
)

// Intermediate result keys
const (
	StateKeyTaskID              = "task_id"
	StateKeyFileChanges         = "file_changes"
	StateKeyAllowedCommands     = "allowed_commands"
	StateKeyPermissionDecisions = "permission_decisions"
	StateKeySandboxResults      = "sandbox_results"
	StateKeyRuleFindings        = "rule_findings"
	StateKeyLLMFindings         = "llm_findings"
	StateKeyFindings            = "findings"
	StateKeyWarnings            = "warnings"
)

// Config keys
const (
	StateKeyExecutorConfig = "executor_config"
	StateKeyLLMConfig      = "llm_config"
	StateKeyDedupConfig    = "dedup_config"
	StateKeySkillRules     = "skill_rules"
)

// Output keys
const (
	StateKeyJSONReportPath = "json_report_path"
	StateKeyMDReportPath   = "md_report_path"
	StateKeyStorageDone    = "storage_done"
)

// Node timing keys (each node writes its own timing, int64 milliseconds)
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
