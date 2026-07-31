// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
// Package config loads and validates code-review-agent YAML configuration.
package config

import (
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

// Config is the root configuration for the code review agent.
type Config struct {
	Mode          string           `yaml:"mode"`            // live | dry_run
	DryRunTimeout time.Duration    `yaml:"dry_run_timeout"` // max 2 min for dry-run
	Input         InputConfig      `yaml:"input"`           // input source configuration
	Output        OutputConfig     `yaml:"output"`          // report output configuration
	Executor      ExecutorConfig   `yaml:"executor"`        // sandbox executor configuration
	LLM           LLMConfig        `yaml:"llm"`             // LLM analyzer configuration
	Dedup         DedupConfig      `yaml:"dedup"`           // deduplication configuration
	Sanitize      SanitizeConfig   `yaml:"sanitize"`        // sensitive data redaction configuration
	Database      DatabaseConfig   `yaml:"database"`        // database backend configuration
	Skill         SkillConfig      `yaml:"skill"`           // skill loading configuration
	Permissions   PermissionConfig `yaml:"permissions"`     // permission policy configuration
	Telemetry     TelemetryConfig  `yaml:"telemetry"`       // telemetry configuration (reserved)
}

// InputConfig configures input source.
type InputConfig struct {
	Type    string `yaml:"type"`     // diff_file | diff_text | repo_path
	Source  string `yaml:"source"`   // file path or "stdin"
	BaseRef string `yaml:"base_ref"` // default "origin/main"
}

// OutputConfig configures report output.
type OutputConfig struct {
	Dir     string   `yaml:"dir"`     // output directory path, defaults to "./output"
	Formats []string `yaml:"formats"` // output formats: json, md
}

// ExecutorConfig configures the sandbox executor.
type ExecutorConfig struct {
	Type          string          `yaml:"type"`            // local | cube | container | e2b
	TimeoutSec    int             `yaml:"timeout_sec"`     // per-command timeout (seconds)
	MaxOutputMB   int             `yaml:"max_output_mb"`   // output size limit (MB)
	MaxArtifactMB int             `yaml:"max_artifact_mb"` // artifact file size limit (MB, default 10)
	EnvWhitelist  []string        `yaml:"env_whitelist"`   // allowed environment variable names for the sandbox
	Commands      []CommandConfig `yaml:"commands"`        // sandbox command definitions
}

// CommandConfig is a single sandbox command definition.
type CommandConfig struct {
	Name       string   `yaml:"name"`        // human-readable command label
	Cmd        string   `yaml:"cmd"`         // executable path or binary name
	Args       []string `yaml:"args"`        // command arguments
	TimeoutSec int      `yaml:"timeout_sec"` // per-command timeout override (seconds); 0 uses executor default
	RiskLevel  string   `yaml:"risk_level"`  // low|medium|high
}

// LLMConfig configures the LLM analyzer.
type LLMConfig struct {
	Provider         string  `yaml:"provider"`           // model provider, currently only "openai" (OpenAI-compatible)
	ModelName        string  `yaml:"model_name"`         // LLM model identifier (e.g., "gpt-4")
	APIKey           string  `yaml:"api_key"`            // API key; empty falls back to the provider env var (e.g., OPENAI_API_KEY)
	BaseURL          string  `yaml:"base_url"`           // custom endpoint for OpenAI-compatible APIs (optional)
	Temperature      float64 `yaml:"temperature"`        // response randomness (0.0–1.0)
	MaxTokens        int     `yaml:"max_tokens"`         // maximum tokens in the response
	SystemPromptPath string  `yaml:"system_prompt_path"` // "" = use SKILL.md default
}

// DedupConfig configures deduplication.
type DedupConfig struct {
	ConfidenceThreshold float64 `yaml:"confidence_threshold"`  // minimum confidence for dedup match, default 0.6
	MaxFindingsPerFile  int     `yaml:"max_findings_per_file"` // max findings per file, default 20
	MaxTotalFindings    int     `yaml:"max_total_findings"`    // max total findings, default 100
}

// SanitizeConfig configures sensitive data redaction.
type SanitizeConfig struct {
	Enabled     bool     `yaml:"enabled"`     // toggles sensitive data redaction
	Patterns    []string `yaml:"patterns"`    // regex patterns for redaction
	Replacement string   `yaml:"replacement"` // replacement text, default "***REDACTED***"
}

// DatabaseConfig configures database backend.
type DatabaseConfig struct {
	Driver string `yaml:"driver"` // sqlite | postgres | mysql
	DSN    string `yaml:"dsn"`    // data source name (connection string)
}

// SkillConfig configures skill loading.
type SkillConfig struct {
	Dir        string `yaml:"dir"`         // skill directory, default "skills/code-review"
	RulesGlob  string `yaml:"rules_glob"`  // glob pattern for rule files, default "rules/*.md"
	ScriptsDir string `yaml:"scripts_dir"` // scripts subdirectory, default "scripts"
}

// PermissionConfig configures permission policies.
type PermissionConfig struct {
	DefaultPolicy map[string]string `yaml:"default_policy"` // risk_level -> decision (allow|deny)
	Overrides     []PermOverride    `yaml:"overrides"`      // command-level permission exceptions
}

// PermOverride is a command-level permission override.
type PermOverride struct {
	Pattern  string `yaml:"pattern"`  // command pattern to match
	Decision string `yaml:"decision"` // allow | deny
	Reason   string `yaml:"reason"`   // justification for this override
}

// TelemetryConfig configures telemetry (reserved).
type TelemetryConfig struct {
	Enabled  bool   `yaml:"enabled"`  // toggles telemetry collection
	Exporter string `yaml:"exporter"` // otlp | stdout | none
	Endpoint string `yaml:"endpoint"` // telemetry export target URL
}

// Load reads and parses a YAML configuration file.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config %s: %w", path, err)
	}
	cfg := &Config{}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parse config %s: %w", path, err)
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	cfg.applyDefaults()
	return cfg, nil
}

// Validate checks configuration sanity. Returns error for hard failures
// (fail-fast principle), does not silently fall back to defaults.
// Validate checks configuration sanity. Returns error for hard failures
// (fail-fast principle), does not silently fall back to defaults.
func (c *Config) Validate() error {
	if c.Mode == "" {
		return fmt.Errorf("mode is required (live, dry_run, or rule_only)")
	}
	if c.Mode != "live" && c.Mode != "dry_run" && c.Mode != "rule_only" {
		return fmt.Errorf("mode must be 'live', 'dry_run', or 'rule_only', got %q", c.Mode)
	}
	if c.Input.Type == "" {
		return fmt.Errorf("input.type is required (diff_file, diff_text, or repo_path)")
	}
	if c.Database.Driver == "" {
		return fmt.Errorf("database.driver is required")
	}
	// Fail closed: only sqlite is implemented (storage.NewSQLite). Reject
	// other drivers here instead of falling through to a nil storage.
	if c.Database.Driver != "sqlite" {
		return fmt.Errorf("database.driver must be 'sqlite' (postgres/mysql not yet implemented), got %q", c.Database.Driver)
	}
	if c.Database.DSN == "" {
		return fmt.Errorf("database.dsn is required")
	}
	if c.Executor.Type == "" {
		return fmt.Errorf("executor.type is required")
	}
	supportedExecutors := map[string]bool{"local": true, "container": true, "cube": true}
	if !supportedExecutors[c.Executor.Type] {
		return fmt.Errorf("executor.type must be one of: local, container, cube; got %q", c.Executor.Type)
	}
	if c.LLM.ModelName == "" && c.Mode == "live" {
		return fmt.Errorf("llm.model_name is required in live mode")
	}
	// Only the openai (OpenAI-compatible) provider is wired up; fail closed
	// on anything else in live mode so a typo doesn't silently no-op.
	if c.Mode == "live" && c.LLM.Provider != "" && c.LLM.Provider != "openai" {
		return fmt.Errorf("llm.provider must be 'openai' (other providers not yet implemented), got %q", c.LLM.Provider)
	}
	return nil
}

func (c *Config) applyDefaults() {
	if c.LLM.Provider == "" {
		c.LLM.Provider = "openai"
	}
	if c.Input.BaseRef == "" {
		c.Input.BaseRef = "origin/main"
	}
	if c.Output.Dir == "" {
		c.Output.Dir = "./output"
	}
	if len(c.Output.Formats) == 0 {
		c.Output.Formats = []string{"json", "md"}
	}
	if c.Dedup.ConfidenceThreshold == 0 {
		c.Dedup.ConfidenceThreshold = 0.6
	}
	if c.Dedup.MaxFindingsPerFile == 0 {
		c.Dedup.MaxFindingsPerFile = 20
	}
	if c.Dedup.MaxTotalFindings == 0 {
		c.Dedup.MaxTotalFindings = 100
	}
	if c.Sanitize.Replacement == "" {
		c.Sanitize.Replacement = "***REDACTED***"
	}
	if c.Executor.TimeoutSec == 0 {
		c.Executor.TimeoutSec = 30
	}
	if c.Executor.MaxOutputMB == 0 {
		c.Executor.MaxOutputMB = 10
	}
	if c.Executor.MaxArtifactMB == 0 {
		c.Executor.MaxArtifactMB = 10
	}
	if c.Permissions.DefaultPolicy == nil {
		c.Permissions.DefaultPolicy = map[string]string{
			"low":    "allow",
			"medium": "allow",
			"high":   "deny",
		}
	}
	if c.Skill.Dir == "" {
		c.Skill.Dir = "./skills/code-review"
	}
	if c.Skill.RulesGlob == "" {
		c.Skill.RulesGlob = "rules/*.md"
	}
	if c.Skill.ScriptsDir == "" {
		c.Skill.ScriptsDir = "scripts"
	}
	if c.DryRunTimeout == 0 {
		c.DryRunTimeout = 2 * time.Minute
	}
}
