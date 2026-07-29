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
	Mode        string            `yaml:"mode"`         // live | dry_run
	DryRunTimeout time.Duration   `yaml:"dry_run_timeout"` // max 2 min for dry-run
	Input       InputConfig       `yaml:"input"`
	Output      OutputConfig      `yaml:"output"`
	Executor    ExecutorConfig    `yaml:"executor"`
	LLM         LLMConfig         `yaml:"llm"`
	Dedup       DedupConfig       `yaml:"dedup"`
	Sanitize    SanitizeConfig    `yaml:"sanitize"`
	Database    DatabaseConfig    `yaml:"database"`
	Skill       SkillConfig       `yaml:"skill"`
	Permissions PermissionsConfig `yaml:"permissions"`
	Telemetry   TelemetryConfig   `yaml:"telemetry"`
}

// InputConfig configures input source.
type InputConfig struct {
	Type    string `yaml:"type"`    // diff_file | diff_text | repo_path
	Source  string `yaml:"source"`  // file path or "stdin"
	BaseRef string `yaml:"base_ref"` // default "origin/main"
}

// OutputConfig configures report output.
type OutputConfig struct {
	Dir     string   `yaml:"dir"`
	Formats []string `yaml:"formats"` // json, md
}

// ExecutorConfig configures the sandbox executor.
type ExecutorConfig struct {
	Type          string          `yaml:"type"`           // local | cube | container | e2b
	TimeoutSec    int             `yaml:"timeout_sec"`    // per-command timeout (seconds)
	MaxOutputMB   int             `yaml:"max_output_mb"`  // output size limit (MB)
	MaxArtifactMB int             `yaml:"max_artifact_mb"` // artifact file size limit (MB, default 10)
	EnvWhitelist  []string        `yaml:"env_whitelist"`
	Commands      []CommandConfig `yaml:"commands"`
}

// CommandConfig is a single sandbox command definition.
type CommandConfig struct {
	Name       string   `yaml:"name"`
	Cmd        string   `yaml:"cmd"`
	Args       []string `yaml:"args"`
	TimeoutSec int      `yaml:"timeout_sec"`
	RiskLevel  string   `yaml:"risk_level"` // low|medium|high
}

// LLMConfig configures the LLM analyzer.
type LLMConfig struct {
	ModelName        string  `yaml:"model_name"`
	Temperature      float64 `yaml:"temperature"`
	MaxTokens        int     `yaml:"max_tokens"`
	SystemPromptPath string  `yaml:"system_prompt_path"` // "" = use SKILL.md default
}

// DedupConfig configures deduplication.
type DedupConfig struct {
	ConfidenceThreshold float64 `yaml:"confidence_threshold"` // default 0.6
	MaxFindingsPerFile  int     `yaml:"max_findings_per_file"` // default 20
	MaxTotalFindings    int     `yaml:"max_total_findings"`    // default 100
}

// SanitizeConfig configures sensitive data redaction.
type SanitizeConfig struct {
	Enabled     bool     `yaml:"enabled"`
	Patterns    []string `yaml:"patterns"`
	Replacement string   `yaml:"replacement"` // default "***REDACTED***"
}

// DatabaseConfig configures database backend.
type DatabaseConfig struct {
	Driver string `yaml:"driver"` // sqlite | postgres | mysql
	DSN    string `yaml:"dsn"`
}

// SkillConfig configures skill loading.
type SkillConfig struct {
	Dir        string `yaml:"dir"`         // skills/code-review
	RulesGlob  string `yaml:"rules_glob"`  // "rules/*.md"
	ScriptsDir string `yaml:"scripts_dir"` // "scripts"
}

// PermissionsConfig configures permission policies.
type PermissionsConfig struct {
	DefaultPolicy map[string]string `yaml:"default_policy"` // risk_level -> decision
	Overrides     []PermOverride    `yaml:"overrides"`
}

// PermOverride is a command-level permission override.
type PermOverride struct {
	Pattern  string `yaml:"pattern"`
	Decision string `yaml:"decision"`
	Reason   string `yaml:"reason"`
}

// TelemetryConfig configures telemetry (reserved).
type TelemetryConfig struct {
	Enabled  bool   `yaml:"enabled"`
	Exporter string `yaml:"exporter"` // otlp | stdout | none
	Endpoint string `yaml:"endpoint"`
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
func (c *Config) Validate() error {
	if c.Mode == "" {
		return fmt.Errorf("mode is required (live or dry_run)")
	}
	if c.Mode != "live" && c.Mode != "dry_run" {
		return fmt.Errorf("mode must be 'live' or 'dry_run', got %q", c.Mode)
	}
	if c.Input.Type == "" {
		return fmt.Errorf("input.type is required (diff_file, diff_text, or repo_path)")
	}
	if c.Database.Driver == "" {
		return fmt.Errorf("database.driver is required")
	}
	if c.Database.DSN == "" {
		return fmt.Errorf("database.dsn is required")
	}
	if c.Executor.Type == "" {
		return fmt.Errorf("executor.type is required")
	}
	if c.LLM.ModelName == "" && c.Mode == "live" {
		return fmt.Errorf("llm.model_name is required in live mode")
	}
	return nil
}

func (c *Config) applyDefaults() {
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
