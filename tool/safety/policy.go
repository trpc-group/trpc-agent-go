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

const supportedPolicyVersion = 1

var envKeyPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

var decimalIntegerPattern = regexp.MustCompile(`^-?(0|[1-9][0-9]*)$`)

type policyValueKind int

const (
	policyValueObject policyValueKind = iota
	policyValueStringList
	policyValueString
	policyValueInteger
)

var policyValueKinds = map[string]policyValueKind{
	"":                           policyValueObject,
	"version":                    policyValueInteger,
	"commands":                   policyValueObject,
	"commands.allowed":           policyValueStringList,
	"commands.denied":            policyValueStringList,
	"paths":                      policyValueObject,
	"paths.denied":               policyValueStringList,
	"network":                    policyValueObject,
	"network.allowed_domains":    policyValueStringList,
	"limits":                     policyValueObject,
	"limits.max_timeout":         policyValueString,
	"limits.max_output_bytes":    policyValueInteger,
	"limits.max_sleep":           policyValueString,
	"limits.max_concurrency":     policyValueInteger,
	"environment":                policyValueObject,
	"environment.allowed":        policyValueStringList,
	"actions":                    policyValueObject,
	"actions.parse_error":        policyValueString,
	"actions.unknown_language":   policyValueString,
	"actions.pipeline":           policyValueString,
	"actions.dependency_install": policyValueString,
	"actions.host_pty":           policyValueString,
	"actions.host_background":    policyValueString,
}

var policyObjectKeys = map[string]map[string]struct{}{
	"": stringSet(
		"version", "commands", "paths", "network", "limits",
		"environment", "actions",
	),
	"commands":    stringSet("allowed", "denied"),
	"paths":       stringSet("denied"),
	"network":     stringSet("allowed_domains"),
	"limits":      stringSet("max_timeout", "max_output_bytes", "max_sleep", "max_concurrency"),
	"environment": stringSet("allowed"),
	"actions": stringSet(
		"parse_error", "unknown_language", "pipeline",
		"dependency_install", "host_pty", "host_background",
	),
}

func stringSet(values ...string) map[string]struct{} {
	set := make(map[string]struct{}, len(values))
	for _, value := range values {
		set[value] = struct{}{}
	}
	return set
}

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

	parseErrorAction        Decision
	unknownLanguageAction   Decision
	pipelineAction          Decision
	dependencyInstallAction Decision
	hostPTYAction           Decision
	hostBackgroundAction    Decision
}

type rawPolicy struct {
	Version     *int           `json:"version" yaml:"version"`
	Commands    rawCommands    `json:"commands" yaml:"commands"`
	Paths       rawPaths       `json:"paths" yaml:"paths"`
	Network     rawNetwork     `json:"network" yaml:"network"`
	Limits      rawLimits      `json:"limits" yaml:"limits"`
	Environment rawEnvironment `json:"environment" yaml:"environment"`
	Actions     rawActions     `json:"actions" yaml:"actions"`
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

type rawActions struct {
	ParseError        *string `json:"parse_error" yaml:"parse_error"`
	UnknownLanguage   *string `json:"unknown_language" yaml:"unknown_language"`
	Pipeline          *string `json:"pipeline" yaml:"pipeline"`
	DependencyInstall *string `json:"dependency_install" yaml:"dependency_install"`
	HostPTY           *string `json:"host_pty" yaml:"host_pty"`
	HostBackground    *string `json:"host_background" yaml:"host_background"`
}

// DefaultPolicy returns a new copy of the built-in conservative policy.
func DefaultPolicy() Policy {
	return Policy{
		version:                 supportedPolicyVersion,
		allowedCommands:         cloneStrings(defaultAllowedCommands),
		deniedCommands:          cloneStrings(defaultDeniedCommands),
		deniedPaths:             cloneStrings(defaultDeniedPaths),
		allowedEnv:              cloneStrings(defaultAllowedEnv),
		maxTimeout:              30 * time.Second,
		maxOutputBytes:          1 << 20,
		maxSleep:                10 * time.Second,
		maxConcurrency:          8,
		parseErrorAction:        DecisionNeedsHumanReview,
		unknownLanguageAction:   DecisionNeedsHumanReview,
		pipelineAction:          DecisionAsk,
		dependencyInstallAction: DecisionAsk,
		hostPTYAction:           DecisionAsk,
		hostBackgroundAction:    DecisionDeny,
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
	var node yaml.Node
	nodeDecoder := yaml.NewDecoder(bytes.NewReader(data))
	if err := nodeDecoder.Decode(&node); err != nil {
		return err
	}
	if err := validateYAMLNode(&node, ""); err != nil {
		return err
	}
	var extra yaml.Node
	if err := nodeDecoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("multiple YAML documents are not allowed")
		}
		return err
	}

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

func validateYAMLNode(node *yaml.Node, fieldPath string) error {
	if err := validateYAMLNodeValue(node); err != nil {
		return err
	}
	if node.Kind == yaml.DocumentNode {
		if len(node.Content) != 1 {
			return errors.New("empty YAML document")
		}
		return validateYAMLNode(node.Content[0], fieldPath)
	}
	kind, ok := policyValueKinds[fieldPath]
	if !ok {
		return fmt.Errorf("unknown YAML field %q", fieldPath)
	}
	switch kind {
	case policyValueObject:
		return validateYAMLObject(node, fieldPath)
	case policyValueStringList:
		return validateYAMLStringList(node, fieldPath)
	case policyValueString:
		if node.Kind != yaml.ScalarNode || node.Tag != "!!str" {
			return fmt.Errorf("YAML field %q must be a string", fieldPath)
		}
	case policyValueInteger:
		if node.Kind != yaml.ScalarNode || node.Tag != "!!int" ||
			!decimalIntegerPattern.MatchString(node.Value) {
			return fmt.Errorf("YAML field %q must be a decimal integer", fieldPath)
		}
	}
	return nil
}

func validateYAMLNodeValue(node *yaml.Node) error {
	if node == nil {
		return errors.New("empty YAML document")
	}
	if node.Kind == yaml.AliasNode {
		return errors.New("YAML aliases are not allowed")
	}
	if node.Kind == yaml.ScalarNode && node.Tag == "!!null" {
		return errors.New("null policy values are not allowed")
	}
	return nil
}

func validateYAMLObject(node *yaml.Node, fieldPath string) error {
	if node.Kind != yaml.MappingNode {
		return fmt.Errorf("YAML field %q must be an object", fieldPath)
	}
	allowed := policyObjectKeys[fieldPath]
	seen := make(map[string]struct{}, len(node.Content)/2)
	for i := 0; i+1 < len(node.Content); i += 2 {
		key := node.Content[i]
		if key.Kind != yaml.ScalarNode || key.Tag != "!!str" {
			return errors.New("policy mapping keys must be strings")
		}
		if _, ok := seen[key.Value]; ok {
			return fmt.Errorf("duplicate YAML key %q", key.Value)
		}
		childPath := joinPolicyPath(fieldPath, key.Value)
		if _, ok := allowed[key.Value]; !ok {
			return fmt.Errorf("unknown YAML field %q", childPath)
		}
		seen[key.Value] = struct{}{}
		if err := validateYAMLNode(node.Content[i+1], childPath); err != nil {
			return err
		}
	}
	return nil
}

func validateYAMLStringList(node *yaml.Node, fieldPath string) error {
	if node.Kind != yaml.SequenceNode {
		return fmt.Errorf("YAML field %q must be a string list", fieldPath)
	}
	for _, child := range node.Content {
		if child.Kind != yaml.ScalarNode || child.Tag != "!!str" {
			return fmt.Errorf("YAML field %q must contain only strings", fieldPath)
		}
	}
	return nil
}

func joinPolicyPath(parent, child string) string {
	if parent == "" {
		return child
	}
	return parent + "." + child
}

func decodeJSONPolicy(data []byte, out *rawPolicy) error {
	if err := validateJSONDocument(data); err != nil {
		return err
	}
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

func validateJSONDocument(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := consumeJSONValue(decoder, ""); err != nil {
		return err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("multiple JSON values are not allowed")
		}
		return err
	}
	return nil
}

func consumeJSONValue(decoder *json.Decoder, fieldPath string) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	if token == nil {
		return errors.New("null policy values are not allowed")
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		return consumeJSONObject(decoder, fieldPath)
	case '[':
		return consumeJSONArray(decoder, fieldPath)
	default:
		return fmt.Errorf("unexpected JSON delimiter %q", delim)
	}
}

func consumeJSONObject(decoder *json.Decoder, fieldPath string) error {
	allowed, ok := policyObjectKeys[fieldPath]
	if !ok {
		return fmt.Errorf("JSON field %q must not be an object", fieldPath)
	}
	seen := make(map[string]struct{})
	for decoder.More() {
		keyToken, err := decoder.Token()
		if err != nil {
			return err
		}
		key, ok := keyToken.(string)
		if !ok {
			return errors.New("JSON object key is not a string")
		}
		if _, exists := seen[key]; exists {
			return fmt.Errorf("duplicate JSON key %q", key)
		}
		childPath := joinPolicyPath(fieldPath, key)
		if _, exists := allowed[key]; !exists {
			return fmt.Errorf("unknown JSON field %q", childPath)
		}
		seen[key] = struct{}{}
		if err := consumeJSONValue(decoder, childPath); err != nil {
			return err
		}
	}
	closing, err := decoder.Token()
	if err != nil {
		return err
	}
	if closing != json.Delim('}') {
		return errors.New("invalid JSON object closing delimiter")
	}
	return nil
}

func consumeJSONArray(decoder *json.Decoder, fieldPath string) error {
	for decoder.More() {
		if err := consumeJSONValue(decoder, fieldPath); err != nil {
			return err
		}
	}
	closing, err := decoder.Token()
	if err != nil {
		return err
	}
	if closing != json.Delim(']') {
		return errors.New("invalid JSON array closing delimiter")
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
	if err := compileActions(raw.Actions, &policy); err != nil {
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

func compileActions(raw rawActions, policy *Policy) error {
	actions := []struct {
		name   string
		raw    *string
		target *Decision
	}{
		{"actions.parse_error", raw.ParseError, &policy.parseErrorAction},
		{"actions.unknown_language", raw.UnknownLanguage, &policy.unknownLanguageAction},
		{"actions.pipeline", raw.Pipeline, &policy.pipelineAction},
		{"actions.dependency_install", raw.DependencyInstall, &policy.dependencyInstallAction},
		{"actions.host_pty", raw.HostPTY, &policy.hostPTYAction},
		{"actions.host_background", raw.HostBackground, &policy.hostBackgroundAction},
	}
	for _, action := range actions {
		decision, err := compileAction(action.name, action.raw, *action.target)
		if err != nil {
			return err
		}
		*action.target = decision
	}
	return nil
}

func compileAction(name string, raw *string, fallback Decision) (Decision, error) {
	if raw == nil {
		return fallback, nil
	}
	decision := Decision(strings.TrimSpace(*raw))
	switch decision {
	case DecisionDeny, DecisionAsk, DecisionNeedsHumanReview:
		return decision, nil
	case DecisionAllow:
		return "", fmt.Errorf("%s cannot be allow", name)
	default:
		return "", fmt.Errorf("%s has unknown decision %q", name, *raw)
	}
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
	if allow && runtime.GOOS == "linux" {
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
	if len(labels) < 2 {
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
	return value != "" && len(value) <= 253 &&
		!strings.ContainsAny(value, "/:@")
}

func validDomainLabel(label string) bool {
	if label == "" || len(label) > 63 ||
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
	if len(parts) == 0 || len(parts) > 4 {
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
		return values[0] <= 0xffffffff
	case 2:
		return values[0] <= 0xff && values[1] <= 0xffffff
	case 3:
		return values[0] <= 0xff && values[1] <= 0xff &&
			values[2] <= 0xffff
	case 4:
		for _, value := range values {
			if value > 0xff {
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
	parsed, err := strconv.ParseUint(digits, base, 32)
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
