//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package safety

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

const maxPolicyBytes = 1 << 20

const (
	currentSchemaVersion = "v1"
	defaultPolicyID      = "default"
)

var domainNamePattern = regexp.MustCompile(
	`^[a-z0-9](?:[a-z0-9.-]*[a-z0-9])?$`,
)

var policyIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

// Policy configures the checks performed by Scanner. Built-in protections for
// shell bypasses, dangerous deletion, sensitive paths, privilege escalation,
// and secret exposure cannot be disabled by this policy.
type Policy struct {
	// SchemaVersion identifies the policy schema. LoadPolicy requires "v1";
	// NewScanner defaults an empty value to "v1" for programmatic policies.
	SchemaVersion string `json:"schema_version,omitempty" yaml:"schema_version,omitempty"`
	// PolicyID is a stable deployment-defined policy name. LoadPolicy requires
	// it; NewScanner defaults an empty value to "default".
	PolicyID string `json:"policy_id,omitempty" yaml:"policy_id,omitempty"`
	// AllowedCommands is an optional executable allowlist. Once non-empty,
	// every command segment must match an entry.
	AllowedCommands []string `json:"allowed_commands,omitempty" yaml:"allowed_commands,omitempty"`
	// DeniedCommands rejects matching executable names or basenames.
	DeniedCommands []string `json:"denied_commands,omitempty" yaml:"denied_commands,omitempty"`
	// ForbiddenPaths contains case-insensitive doublestar path patterns.
	ForbiddenPaths []string `json:"forbidden_paths,omitempty" yaml:"forbidden_paths,omitempty"`
	// AllowedNetworkDomains lists exact hosts and explicit wildcard subdomains.
	AllowedNetworkDomains []string `json:"allowed_network_domains,omitempty" yaml:"allowed_network_domains,omitempty"`
	// NetworkCommands adds executable names whose arguments contain network targets.
	NetworkCommands []string `json:"network_commands,omitempty" yaml:"network_commands,omitempty"`
	// MaxTimeoutSeconds rejects larger explicit timeout requests. Zero disables the check.
	MaxTimeoutSeconds int `json:"max_timeout_seconds,omitempty" yaml:"max_timeout_seconds,omitempty"`
	// MaxOutputBytes rejects larger explicit output-limit requests. Zero disables the check.
	MaxOutputBytes int `json:"max_output_bytes,omitempty" yaml:"max_output_bytes,omitempty"`
	// MaxSleepSeconds rejects larger literal sleep commands. Zero disables the check.
	MaxSleepSeconds int `json:"max_sleep_seconds,omitempty" yaml:"max_sleep_seconds,omitempty"`
	// MaxConcurrency rejects recognized larger parallelism arguments. Zero disables the check.
	MaxConcurrency int `json:"max_concurrency,omitempty" yaml:"max_concurrency,omitempty"`
	// AllowedEnvVars lists exact call-level environment names or suffix-wildcard prefixes.
	AllowedEnvVars []string `json:"allowed_env_vars,omitempty" yaml:"allowed_env_vars,omitempty"`
	// ToolProfiles maps model-visible tool names to non-standard argument schemas.
	ToolProfiles map[string]ToolProfile `json:"tool_profiles,omitempty" yaml:"tool_profiles,omitempty"`
}

// ToolProfile maps a model-visible tool schema to safety scan fields. Field
// names may use dot-separated object paths. Empty fields use the conventional
// names for the selected backend.
type ToolProfile struct {
	// Backend identifies the execution boundary for this profile.
	Backend Backend `json:"backend" yaml:"backend"`
	// CommandField locates a shell command string.
	CommandField string `json:"command_field,omitempty" yaml:"command_field,omitempty"`
	// ArgumentsField locates an array of literal argv values.
	ArgumentsField string `json:"arguments_field,omitempty" yaml:"arguments_field,omitempty"`
	// WorkingDirectoryField locates the requested working directory.
	WorkingDirectoryField string `json:"working_directory_field,omitempty" yaml:"working_directory_field,omitempty"`
	// EnvironmentField locates an object of call-level environment overrides.
	EnvironmentField string `json:"environment_field,omitempty" yaml:"environment_field,omitempty"`
	// TimeoutSecondsField locates an integer timeout in seconds.
	TimeoutSecondsField string `json:"timeout_seconds_field,omitempty" yaml:"timeout_seconds_field,omitempty"`
	// OutputBytesField locates an integer requested output limit in bytes.
	OutputBytesField string `json:"output_bytes_field,omitempty" yaml:"output_bytes_field,omitempty"`
	// BackgroundField locates a background-execution boolean.
	BackgroundField string `json:"background_field,omitempty" yaml:"background_field,omitempty"`
	// PTYField locates a PTY-allocation boolean.
	PTYField string `json:"pty_field,omitempty" yaml:"pty_field,omitempty"`
	// CodeBlocksField locates code blocks containing language and code strings.
	CodeBlocksField string `json:"code_blocks_field,omitempty" yaml:"code_blocks_field,omitempty"`
}

// LoadPolicy decodes one strict YAML or JSON policy document from r. File
// policies must declare schema_version "v1" and a non-empty policy_id.
func LoadPolicy(r io.Reader) (Policy, error) {
	if r == nil {
		return Policy{}, errors.New("nil policy reader")
	}
	data, err := io.ReadAll(io.LimitReader(r, maxPolicyBytes+1))
	if err != nil {
		return Policy{}, fmt.Errorf("read policy: %w", err)
	}
	if len(data) > maxPolicyBytes {
		return Policy{}, fmt.Errorf("policy exceeds %d bytes", maxPolicyBytes)
	}
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	var policy Policy
	if err := dec.Decode(&policy); err != nil {
		return Policy{}, fmt.Errorf("decode policy: %w", err)
	}
	if strings.TrimSpace(policy.SchemaVersion) == "" {
		return Policy{}, errors.New("schema_version is required")
	}
	if strings.TrimSpace(policy.PolicyID) == "" {
		return Policy{}, errors.New("policy_id is required")
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		if err == nil {
			return Policy{}, errors.New("policy must contain one document")
		}
		return Policy{}, fmt.Errorf("decode trailing policy document: %w", err)
	}
	return normalizePolicy(policy)
}

// LoadPolicyFile loads one strict YAML or JSON policy document from path.
func LoadPolicyFile(path string) (Policy, error) {
	f, err := os.Open(path)
	if err != nil {
		return Policy{}, fmt.Errorf("open policy: %w", err)
	}
	defer f.Close()
	return LoadPolicy(f)
}

func normalizePolicy(policy Policy) (Policy, error) {
	policy.SchemaVersion = strings.TrimSpace(policy.SchemaVersion)
	if policy.SchemaVersion == "" {
		policy.SchemaVersion = currentSchemaVersion
	}
	if policy.SchemaVersion != currentSchemaVersion {
		return Policy{}, fmt.Errorf("unsupported policy schema_version %q", policy.SchemaVersion)
	}
	policy.PolicyID = strings.TrimSpace(policy.PolicyID)
	if policy.PolicyID == "" {
		policy.PolicyID = defaultPolicyID
	}
	if !policyIDPattern.MatchString(policy.PolicyID) {
		return Policy{}, fmt.Errorf("invalid policy_id %q", policy.PolicyID)
	}
	if policy.MaxTimeoutSeconds < 0 || policy.MaxOutputBytes < 0 ||
		policy.MaxSleepSeconds < 0 || policy.MaxConcurrency < 0 {
		return Policy{}, errors.New("policy limits must not be negative")
	}
	policy.AllowedCommands = cleanStrings(policy.AllowedCommands, false)
	policy.DeniedCommands = cleanStrings(policy.DeniedCommands, false)
	policy.ForbiddenPaths = cleanStrings(policy.ForbiddenPaths, false)
	policy.NetworkCommands = cleanStrings(policy.NetworkCommands, true)
	policy.AllowedEnvVars = cleanStrings(policy.AllowedEnvVars, true)

	domains := make([]string, 0, len(policy.AllowedNetworkDomains))
	for _, domain := range cleanStrings(policy.AllowedNetworkDomains, true) {
		domain = strings.TrimSuffix(domain, ".")
		if err := validateDomainPattern(domain); err != nil {
			return Policy{}, err
		}
		domains = append(domains, domain)
	}
	policy.AllowedNetworkDomains = domains

	profiles := make(map[string]ToolProfile, len(policy.ToolProfiles))
	for name, profile := range policy.ToolProfiles {
		name = strings.TrimSpace(name)
		if name == "" {
			return Policy{}, errors.New("tool profile name must not be empty")
		}
		normalizedProfile := profile
		if err := validateProfile(&normalizedProfile); err != nil {
			return Policy{}, fmt.Errorf("tool profile %q: %w", name, err)
		}
		profiles[name] = normalizedProfile
	}
	if len(profiles) == 0 {
		profiles = nil
	}
	policy.ToolProfiles = profiles
	return policy, nil
}

func policyRevision(policy Policy) (string, error) {
	data, err := json.Marshal(policy)
	if err != nil {
		return "", fmt.Errorf("encode normalized policy: %w", err)
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:]), nil
}

func cleanStrings(values []string, lower bool) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if lower {
			value = strings.ToLower(value)
		}
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func validateDomainPattern(domain string) error {
	if domain == "" {
		return errors.New("allowed network domain must not be empty")
	}
	wildcard := strings.HasPrefix(domain, "*.")
	host := strings.TrimPrefix(domain, "*.")
	if net.ParseIP(host) != nil && !wildcard {
		return nil
	}
	if strings.ContainsAny(host, "/:@") || strings.Contains(host, "*") ||
		strings.Contains(host, "..") || !domainNamePattern.MatchString(host) ||
		(wildcard && !strings.Contains(host, ".")) {
		return fmt.Errorf("invalid allowed network domain %q", domain)
	}
	return nil
}

func validateProfile(profile *ToolProfile) error {
	switch profile.Backend {
	case BackendUnknown, BackendGeneric, BackendWorkspace, BackendHost,
		BackendCodeExecutor:
	default:
		return fmt.Errorf("unknown backend %q", profile.Backend)
	}
	fields := []*string{
		&profile.CommandField,
		&profile.ArgumentsField,
		&profile.WorkingDirectoryField,
		&profile.EnvironmentField,
		&profile.TimeoutSecondsField,
		&profile.OutputBytesField,
		&profile.BackgroundField,
		&profile.PTYField,
		&profile.CodeBlocksField,
	}
	for _, field := range fields {
		*field = strings.TrimSpace(*field)
		if strings.HasPrefix(*field, ".") || strings.HasSuffix(*field, ".") ||
			strings.Contains(*field, "..") {
			return fmt.Errorf("invalid field path %q", *field)
		}
	}
	return nil
}
