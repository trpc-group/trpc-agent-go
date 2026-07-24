//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	supportedPolicyVersion = 1

	defaultMaxTimeout     = 30 * time.Second
	defaultMaxOutputBytes = 1 << 20
	defaultMaxSleep       = 10 * time.Second
	defaultMaxConcurrency = 8

	minimumDomainLabels       = 2
	maximumLegacyIPv4Parts    = 4
	legacyIPv4TwoParts        = 2
	legacyIPv4ThreeParts      = 3
	legacyIPv4FourParts       = 4
	maximumIPv4Value          = 0xffffffff
	maximumIPv4Byte           = 0xff
	maximumIPv4TwoByteValue   = 0xffff
	maximumIPv4ThreeByteValue = 0xffffff
	maximumDomainNameLength   = 253
	maximumDomainLabelLength  = 63
	legacyIPv4ParseBitSize    = 32
)

var envKeyPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

var defaultAllowedCommands = []string{
	"go", "git", "echo", "pwd", "ls", "cat", "curl", "wget",
}

var defaultDeniedCommands = []string{
	"rm", "dd", "mkfs", "shutdown", "reboot", "nc", "ssh", "sudo",
}

var defaultDeniedPaths = []string{
	"~/.ssh", "~/.aws", "/etc", "/root", ".env",
	"credentials.json", "id_rsa", "id_ed25519",
}

var defaultAllowedEnv = []string{"LANG", "LC_ALL", "TMPDIR"}

// Policy is an immutable, compiled safety policy. Its fields are deliberately
// private so callers cannot mutate a policy after it has been validated.
type Policy struct {
	version int

	allowedCommands []string
	deniedCommands  []string
	denyAllCommands bool
	deniedPaths     []string
	allowedDomains  []string
	allowedEnv      []string

	maxTimeout     time.Duration
	maxOutputBytes int64
	maxSleep       time.Duration
	maxConcurrency int
}

type rawPolicy struct {
	Version     *int           `json:"version" yaml:"version"`
	Commands    rawCommands    `json:"commands" yaml:"commands"`
	Paths       rawPaths       `json:"paths" yaml:"paths"`
	Network     rawNetwork     `json:"network" yaml:"network"`
	Limits      rawLimits      `json:"limits" yaml:"limits"`
	Environment rawEnvironment `json:"environment" yaml:"environment"`
}

type rawCommands struct {
	Allowed *[]string `json:"allowed" yaml:"allowed"`
	Denied  *[]string `json:"denied" yaml:"denied"`
}

type rawPaths struct {
	Denied *[]string `json:"denied" yaml:"denied"`
}

type rawNetwork struct {
	AllowedDomains *[]string `json:"allowed_domains" yaml:"allowed_domains"`
}

type rawLimits struct {
	MaxTimeout     *string `json:"max_timeout" yaml:"max_timeout"`
	MaxOutputBytes *int64  `json:"max_output_bytes" yaml:"max_output_bytes"`
	MaxSleep       *string `json:"max_sleep" yaml:"max_sleep"`
	MaxConcurrency *int    `json:"max_concurrency" yaml:"max_concurrency"`
}

type rawEnvironment struct {
	Allowed *[]string `json:"allowed" yaml:"allowed"`
}

// DefaultPolicy returns a new copy of the built-in conservative policy.
func DefaultPolicy() Policy {
	return Policy{
		version:         supportedPolicyVersion,
		allowedCommands: cloneStrings(defaultAllowedCommands),
		deniedCommands:  cloneStrings(defaultDeniedCommands),
		deniedPaths:     cloneStrings(defaultDeniedPaths),
		allowedEnv:      cloneStrings(defaultAllowedEnv),
		maxTimeout:      defaultMaxTimeout,
		maxOutputBytes:  defaultMaxOutputBytes,
		maxSleep:        defaultMaxSleep,
		maxConcurrency:  defaultMaxConcurrency,
	}
}

// LoadPolicy strictly loads and compiles a YAML or JSON policy file.
func LoadPolicy(path string) (Policy, error) {
	if strings.TrimSpace(path) == "" {
		return Policy{}, errors.New("policy path is empty")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return Policy{}, fmt.Errorf("read policy %q: %w", path, err)
	}
	ext := strings.ToLower(filepath.Ext(path))
	var raw rawPolicy
	switch ext {
	case ".yaml", ".yml":
		err = decodeYAMLPolicy(data, &raw)
	case ".json":
		err = decodeJSONPolicy(data, &raw)
	default:
		return Policy{}, fmt.Errorf("unsupported policy extension %q", ext)
	}
	if err != nil {
		return Policy{}, fmt.Errorf("decode policy %q: %w", path, err)
	}
	policy, err := compilePolicy(raw)
	if err != nil {
		return Policy{}, fmt.Errorf("validate policy %q: %w", path, err)
	}
	return policy, nil
}

func decodeYAMLPolicy(data []byte, out *rawPolicy) error {
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(out); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("multiple YAML documents are not allowed")
		}
		return err
	}
	return nil
}

func decodeJSONPolicy(data []byte, out *rawPolicy) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(out); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("multiple JSON values are not allowed")
		}
		return err
	}
	return nil
}

func compilePolicy(raw rawPolicy) (Policy, error) {
	if raw.Version == nil {
		return Policy{}, errors.New("version is required")
	}
	if *raw.Version != supportedPolicyVersion {
		return Policy{}, fmt.Errorf("unsupported version %d", *raw.Version)
	}
	policy := DefaultPolicy()
	if err := compileCommands(raw.Commands, &policy); err != nil {
		return Policy{}, err
	}
	if err := compilePolicyLists(raw, &policy); err != nil {
		return Policy{}, err
	}
	if err := compileLimits(raw.Limits, &policy); err != nil {
		return Policy{}, err
	}
	return policy.clone(), nil
}

func compileCommands(raw rawCommands, policy *Policy) error {
	var err error
	if raw.Allowed != nil {
		policy.allowedCommands, err = cleanCommandList(
			"commands.allowed", *raw.Allowed, true,
		)
		if err != nil {
			return err
		}
		policy.denyAllCommands = len(policy.allowedCommands) == 0
	}
	if raw.Denied != nil {
		policy.deniedCommands, err = cleanCommandList(
			"commands.denied", *raw.Denied, false,
		)
		if err != nil {
			return err
		}
	}
	return rejectCommandConflicts(
		policy.allowedCommands, policy.deniedCommands,
	)
}

func compilePolicyLists(raw rawPolicy, policy *Policy) error {
	var err error
	if raw.Paths.Denied != nil {
		policy.deniedPaths, err = cleanUniqueList("paths.denied", *raw.Paths.Denied)
		if err != nil {
			return err
		}
	}
	if raw.Network.AllowedDomains != nil {
		policy.allowedDomains, err = cleanDomains(*raw.Network.AllowedDomains)
		if err != nil {
			return err
		}
	}
	if raw.Environment.Allowed != nil {
		policy.allowedEnv, err = cleanEnvKeys(*raw.Environment.Allowed)
		if err != nil {
			return err
		}
	}
	return nil
}

func compileLimits(raw rawLimits, policy *Policy) error {
	var err error
	if raw.MaxTimeout != nil {
		policy.maxTimeout, err = parsePositiveDuration("limits.max_timeout", *raw.MaxTimeout)
		if err != nil {
			return err
		}
	}
	if raw.MaxOutputBytes != nil {
		if *raw.MaxOutputBytes <= 0 {
			return errors.New("limits.max_output_bytes must be positive")
		}
		policy.maxOutputBytes = *raw.MaxOutputBytes
	}
	if raw.MaxSleep != nil {
		policy.maxSleep, err = parsePositiveDuration("limits.max_sleep", *raw.MaxSleep)
		if err != nil {
			return err
		}
	}
	if raw.MaxConcurrency != nil {
		if *raw.MaxConcurrency <= 0 {
			return errors.New("limits.max_concurrency must be positive")
		}
		policy.maxConcurrency = *raw.MaxConcurrency
	}
	return nil
}

func cleanUniqueList(name string, values []string) ([]string, error) {
	out := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			return nil, fmt.Errorf("%s contains an empty item", name)
		}
		if _, ok := seen[value]; ok {
			return nil, fmt.Errorf("%s contains duplicate item %q", name, value)
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out, nil
}

func cleanCommandList(
	name string,
	values []string,
	allow bool,
) ([]string, error) {
	out := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			return nil, fmt.Errorf("%s contains an empty item", name)
		}
		key := commandListKey(value, allow)
		if _, ok := seen[key]; ok {
			return nil, fmt.Errorf("%s contains duplicate item %q", name, value)
		}
		seen[key] = struct{}{}
		out = append(out, value)
	}
	return out, nil
}

func commandListKey(command string, allow bool) string {
	if allow && strings.ContainsAny(command, "/\\") {
		return "path:" + command
	}
	if allow && runtime.GOOS != "windows" {
		return "bare:" + command
	}
	return "normalized:" + normalizePolicyCommand(command)
}

func rejectCommandConflicts(allowed, denied []string) error {
	for _, command := range allowed {
		for _, deniedCommand := range denied {
			if deniedCommandMatches(deniedCommand, command) {
				return fmt.Errorf(
					"commands.allowed and commands.denied conflict on %q",
					command,
				)
			}
		}
	}
	return nil
}

func deniedCommandMatches(deniedCommand, command string) bool {
	deniedCommand = normalizePolicyCommand(deniedCommand)
	return deniedCommand == normalizePolicyCommand(command) ||
		deniedCommand == normalizePolicyCommand(policyCommandBase(command))
}

func policyCommandBase(command string) string {
	return path.Base(filepath.ToSlash(command))
}

func normalizePolicyCommand(command string) string {
	command = strings.ToLower(command)
	if runtime.GOOS != "windows" {
		return command
	}
	for _, ext := range []string{".exe", ".cmd", ".bat", ".com", ".ps1"} {
		if strings.HasSuffix(command, ext) {
			return strings.TrimSuffix(command, ext)
		}
	}
	return command
}

func cleanDomains(values []string) ([]string, error) {
	out := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		domain := strings.ToLower(strings.TrimSpace(value))
		if !validDomainPattern(domain) {
			return nil, fmt.Errorf("network.allowed_domains contains invalid domain %q", value)
		}
		if _, ok := seen[domain]; ok {
			return nil, fmt.Errorf("network.allowed_domains contains duplicate item %q", value)
		}
		seen[domain] = struct{}{}
		out = append(out, domain)
	}
	return out, nil
}

func validDomainPattern(value string) bool {
	if strings.HasPrefix(value, "*.") {
		value = strings.TrimPrefix(value, "*.")
	}
	if !validDomainName(value) {
		return false
	}
	if net.ParseIP(value) != nil || isLegacyIPv4Literal(value) {
		return false
	}
	labels := strings.Split(value, ".")
	if len(labels) < minimumDomainLabels {
		return false
	}
	if onlyDecimalDigits(labels[len(labels)-1]) {
		return false
	}
	for _, label := range labels {
		if !validDomainLabel(label) {
			return false
		}
	}
	return true
}

func validDomainName(value string) bool {
	return value != "" && len(value) <= maximumDomainNameLength &&
		!strings.ContainsAny(value, "/:@")
}

func validDomainLabel(label string) bool {
	if label == "" || len(label) > maximumDomainLabelLength ||
		label[0] == '-' || label[len(label)-1] == '-' {
		return false
	}
	for _, r := range label {
		if !validDomainRune(r) {
			return false
		}
	}
	return true
}

func validDomainRune(r rune) bool {
	return r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-'
}

func onlyDecimalDigits(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func isLegacyIPv4Literal(value string) bool {
	parts := strings.Split(value, ".")
	if len(parts) == 0 || len(parts) > maximumLegacyIPv4Parts {
		return false
	}
	values := make([]uint64, len(parts))
	for i, part := range parts {
		parsed, ok := parseIPv4Number(part)
		if !ok {
			return false
		}
		values[i] = parsed
	}
	switch len(values) {
	case 1:
		return values[0] <= maximumIPv4Value
	case legacyIPv4TwoParts:
		return values[0] <= maximumIPv4Byte &&
			values[1] <= maximumIPv4ThreeByteValue
	case legacyIPv4ThreeParts:
		return values[0] <= maximumIPv4Byte &&
			values[1] <= maximumIPv4Byte &&
			values[2] <= maximumIPv4TwoByteValue
	case legacyIPv4FourParts:
		for _, value := range values {
			if value > maximumIPv4Byte {
				return false
			}
		}
		return true
	default:
		return false
	}
}

func parseIPv4Number(value string) (uint64, bool) {
	if value == "" {
		return 0, false
	}
	base := 10
	digits := value
	if strings.HasPrefix(value, "0x") || strings.HasPrefix(value, "0X") {
		base = 16
		digits = value[2:]
		if digits == "" {
			return 0, false
		}
	} else if len(value) > 1 && value[0] == '0' {
		base = 8
		for _, r := range value[1:] {
			if r == '8' || r == '9' {
				base = 10
				break
			}
		}
	}
	parsed, err := strconv.ParseUint(digits, base, legacyIPv4ParseBitSize)
	return parsed, err == nil
}

func cleanEnvKeys(values []string) ([]string, error) {
	out, err := cleanUniqueList("environment.allowed", values)
	if err != nil {
		return nil, err
	}
	for _, key := range out {
		if !envKeyPattern.MatchString(key) {
			return nil, fmt.Errorf("environment.allowed contains invalid key %q", key)
		}
	}
	return out, nil
}

func parsePositiveDuration(name, value string) (time.Duration, error) {
	duration, err := time.ParseDuration(strings.TrimSpace(value))
	if err != nil {
		return 0, fmt.Errorf("%s: %w", name, err)
	}
	if duration <= 0 {
		return 0, fmt.Errorf("%s must be positive", name)
	}
	return duration, nil
}

func (p Policy) clone() Policy {
	p.allowedCommands = cloneStrings(p.allowedCommands)
	p.deniedCommands = cloneStrings(p.deniedCommands)
	p.deniedPaths = cloneStrings(p.deniedPaths)
	p.allowedDomains = cloneStrings(p.allowedDomains)
	p.allowedEnv = cloneStrings(p.allowedEnv)
	return p
}

func (p Policy) versionString() string {
	return strconv.Itoa(p.version)
}

func cloneStrings(values []string) []string {
	return append([]string(nil), values...)
}
