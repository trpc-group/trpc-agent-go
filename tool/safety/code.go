// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.

package safety

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
)

const maxRawArgumentDepth = 32

var (
	pythonImportedBridgePattern = regexp.MustCompile(
		`(?i)(?:from\s+subprocess\s+import|import\s+subprocess)(?s:.*?)\b(?:run|call|popen)\s*\(`,
	)
	goImportedBridgePattern = regexp.MustCompile(
		`(?i)["']os/exec["'](?s:.*?)\b[A-Za-z_][A-Za-z0-9_]*\.Command(?:Context)?\s*\(`,
	)
	jsImportedBridgePattern = regexp.MustCompile(
		`(?i)(?:require\s*\(\s*["']child_process["']\s*\)|from\s+["']child_process["'])(?s:.*?)\b(?:exec|execSync|spawn|spawnSync)\s*\(`,
	)
)

func scanCodeBlocks(policy Policy, blocks []codeexecutor.CodeBlock) []Finding {
	var findings []Finding
	for _, block := range blocks {
		language := strings.ToLower(strings.TrimSpace(block.Language))
		if isShellLanguage(language) {
			findings = append(findings, scanExecution(policy, Request{
				Backend: BackendCodeExec,
				Command: block.Code,
			})...)
			continue
		}
		findings = append(findings, scanCodeResourceAbuse(language, block.Code)...)
		findings = append(findings, scanProcessBridge(policy, language, block.Code)...)
		findings = append(findings, scanNetworkText(policy, block.Code)...)
		findings = append(findings, scanCodePaths(policy, block.Code)...)
		findings = append(findings, scanSensitiveContent(block.Code)...)
	}
	return findings
}

func isShellLanguage(language string) bool {
	switch language {
	case "bash", "sh", "shell", "zsh", "ash", "dash":
		return true
	default:
		return false
	}
}

func scanProcessBridge(policy Policy, language, code string) []Finding {
	if !containsProcessBridge(language, code) {
		return nil
	}
	payload := strings.Join(quotedLiterals(code), " ")
	nested := scanExecution(policy, Request{Backend: BackendCodeExec, Command: payload})
	decision := DecisionNeedsHumanReview
	risk := RiskHigh
	if findingsDecision(nested) == DecisionDeny {
		decision = DecisionDeny
		risk = RiskCritical
	}
	findings := []Finding{newFinding(
		decision, risk, "code.process_bridge",
		"code invokes a process or shell execution bridge",
		"replace dynamic process execution with a narrowly scoped tool",
	)}
	return append(findings, nested...)
}

func containsProcessBridge(language, code string) bool {
	lower := strings.ToLower(code)
	switch language {
	case "python", "py":
		return pythonImportedBridgePattern.MatchString(code) || containsAny(lower,
			"subprocess.run(", "subprocess.call(", "subprocess.popen(",
			"os.system(", "os.popen(")
	case "go", "golang":
		return goImportedBridgePattern.MatchString(code) ||
			containsAny(lower, "exec.command(", "exec.commandcontext(")
	case "javascript", "js", "typescript", "ts", "node":
		return jsImportedBridgePattern.MatchString(code) || containsAny(lower,
			"child_process.exec(", "child_process.execsync(",
			"child_process.spawn(", "child_process.spawnsync(")
	default:
		return false
	}
}

func quotedLiterals(code string) []string {
	var literals []string
	for index := 0; index < len(code); {
		if code[index] != '\'' && code[index] != '"' && code[index] != '`' {
			index++
			continue
		}
		quote := code[index]
		start := index
		index++
		escaped := false
		for index < len(code) {
			current := code[index]
			index++
			if escaped {
				escaped = false
				continue
			}
			if current == '\\' {
				escaped = true
				continue
			}
			if current != quote {
				continue
			}
			raw := code[start:index]
			if quote == '`' {
				literals = append(literals, strings.Trim(raw, "`"))
			} else if value, err := strconv.Unquote(raw); err == nil {
				literals = append(literals, value)
			} else {
				literals = append(literals, strings.Trim(raw, `"'`))
			}
			break
		}
	}
	return literals
}

func scanCodePaths(policy Policy, code string) []Finding {
	for _, literal := range quotedLiterals(code) {
		if finding, ok := deniedPathFinding(policy.DeniedPaths, literal); ok {
			return []Finding{finding}
		}
	}
	return nil
}

func scanRawArguments(policy Policy, raw json.RawMessage) []Finding {
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil
	}
	value, err := decodeRawValue(raw)
	if err != nil {
		return []Finding{newFinding(
			DecisionDeny, RiskHigh, "arguments.parse_error",
			"tool arguments are not valid JSON",
			"provide valid structured JSON arguments",
		)}
	}
	return walkRawValue(policy, value, "", 0)
}

func decodeRawValue(raw []byte) (any, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("unexpected trailing JSON value")
		}
		return nil, err
	}
	return value, nil
}

func walkRawValue(policy Policy, value any, parentKey string, depth int) []Finding {
	if depth > maxRawArgumentDepth {
		return []Finding{newFinding(
			DecisionDeny, RiskHigh, "arguments.max_depth",
			"tool arguments exceed the maximum nested depth",
			"reduce nested argument encoding",
		)}
	}
	switch typed := value.(type) {
	case map[string]any:
		return walkRawMap(policy, typed, depth)
	case []any:
		var findings []Finding
		for _, item := range typed {
			if text, ok := item.(string); ok && commandKey(parentKey) {
				findings = append(findings, scanExecution(policy, Request{Command: text})...)
			}
			findings = append(findings, walkRawValue(policy, item, parentKey, depth+1)...)
		}
		return findings
	case string:
		return walkRawString(policy, typed, parentKey, depth)
	default:
		return nil
	}
}

func walkRawMap(policy Policy, values map[string]any, depth int) []Finding {
	var findings []Finding
	if block, ok := codeBlockFromMap(values); ok {
		findings = append(findings, scanCodeBlocks(policy, []codeexecutor.CodeBlock{block})...)
	}
	if request, ok := requestFromRawMap(values); ok {
		findings = append(findings, scanExecution(policy, request)...)
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		findings = append(findings, walkRawValue(policy, values[key], key, depth+1)...)
	}
	return findings
}

func walkRawString(policy Policy, value, parentKey string, depth int) []Finding {
	trimmed := strings.TrimSpace(value)
	if strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[") {
		decoded, err := decodeRawValue([]byte(trimmed))
		if err != nil {
			return []Finding{newFinding(
				DecisionDeny, RiskHigh, "arguments.parse_error",
				"nested tool arguments are not valid JSON",
				"provide valid structured JSON arguments",
			)}
		}
		return walkRawValue(policy, decoded, parentKey, depth+1)
	}
	if parentKey == "" || commandKey(parentKey) {
		return scanExecution(policy, Request{Command: value})
	}
	if networkKey(parentKey) {
		findings := scanNetworkText(policy, value)
		if host, ok := schemelessHost(value); ok {
			if finding, denied := networkDestinationFinding(policy, host); denied {
				findings = append(findings, finding)
			}
		}
		return findings
	}
	return nil
}

func requestFromRawMap(values map[string]any) (Request, bool) {
	var request Request
	found := false
	if command, ok := stringMapValue(values, "command", "cmd", "script", "shell"); ok {
		request.Command = command
		found = true
	}
	if args, ok := stringSliceMapValue(values, "args", "argv"); ok {
		request.Args = args
		found = true
	}
	if cwd, ok := stringMapValue(values, "cwd", "working_directory", "workdir"); ok {
		request.Cwd = cwd
		found = true
	}
	if environment, ok := stringMapMapValue(values, "env", "environment"); ok {
		request.Env = environment
		found = true
	}
	if timeout, ok := intMapValue(values, "timeout_seconds", "timeout"); ok {
		request.TimeoutSeconds = timeout
		found = true
	}
	if output, ok := int64MapValue(values, "max_output_bytes"); ok {
		request.MaxOutputBytes = output
		found = true
	}
	if background, ok := boolMapValue(values, "background"); ok {
		request.Background = background
		found = true
	}
	if tty, ok := boolMapValue(values, "tty"); ok {
		request.TTY = tty
		found = true
	}
	return request, found
}

func codeBlockFromMap(values map[string]any) (codeexecutor.CodeBlock, bool) {
	code, codeOK := stringMapValue(values, "code")
	language, languageOK := stringMapValue(values, "language", "lang")
	return codeexecutor.CodeBlock{Code: code, Language: language}, codeOK && languageOK
}

func stringMapValue(values map[string]any, names ...string) (string, bool) {
	for _, name := range names {
		if value, ok := values[name].(string); ok {
			return value, true
		}
	}
	return "", false
}

func stringSliceMapValue(values map[string]any, names ...string) ([]string, bool) {
	for _, name := range names {
		items, ok := values[name].([]any)
		if !ok {
			continue
		}
		result := make([]string, 0, len(items))
		for _, item := range items {
			text, ok := item.(string)
			if !ok {
				return nil, false
			}
			result = append(result, text)
		}
		return result, true
	}
	return nil, false
}

func stringMapMapValue(values map[string]any, names ...string) (map[string]string, bool) {
	for _, name := range names {
		items, ok := values[name].(map[string]any)
		if !ok {
			continue
		}
		result := make(map[string]string, len(items))
		for key, item := range items {
			text, ok := item.(string)
			if !ok {
				return nil, false
			}
			result[key] = text
		}
		return result, true
	}
	return nil, false
}

func intMapValue(values map[string]any, names ...string) (int, bool) {
	value, ok := int64MapValue(values, names...)
	return int(value), ok
}

func int64MapValue(values map[string]any, names ...string) (int64, bool) {
	for _, name := range names {
		if value, ok := values[name].(json.Number); ok {
			parsed, err := value.Int64()
			return parsed, err == nil
		}
	}
	return 0, false
}

func boolMapValue(values map[string]any, names ...string) (bool, bool) {
	for _, name := range names {
		if value, ok := values[name].(bool); ok {
			return value, true
		}
	}
	return false, false
}

func commandKey(key string) bool {
	switch strings.ToLower(key) {
	case "command", "commands", "cmd", "script", "scripts", "shell", "argv":
		return true
	default:
		return false
	}
}

func networkKey(key string) bool {
	switch strings.ToLower(key) {
	case "url", "uri", "endpoint", "destination":
		return true
	default:
		return false
	}
}

func findingsDecision(findings []Finding) Decision {
	decision := DecisionAllow
	for _, finding := range findings {
		if decisionRank(finding.Decision) > decisionRank(decision) {
			decision = finding.Decision
		}
	}
	return decision
}

func containsAny(value string, candidates ...string) bool {
	for _, candidate := range candidates {
		if strings.Contains(value, candidate) {
			return true
		}
	}
	return false
}
