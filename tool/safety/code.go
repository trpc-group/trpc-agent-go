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
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
	"trpc.group/trpc-go/trpc-agent-go/internal/shellsafe"
)

const maxRawArgumentDepth = 32

var (
	pythonImportedBridgePattern = regexp.MustCompile(
		`(?i)(?:from\s+subprocess\s+import|import\s+subprocess)(?s:.*?)\b(?:run|call|popen)\s*\(`,
	)
	pythonOSAliasPattern = regexp.MustCompile(
		`(?m)\bimport\s+os\s+as\s+([A-Za-z_][A-Za-z0-9_]*)`,
	)
	pythonDynamicOSBridgePattern = regexp.MustCompile(
		`(?i)__import__\s*\(\s*["']os["']\s*\)\s*\.\s*(?:system|popen)\s*\(`,
	)
	pythonFromProcessPattern = regexp.MustCompile(
		`(?im)\bfrom\s+(os|subprocess)\s+import\s+` +
			`(system|popen|run|call)(?:\s+as\s+([A-Za-z_][A-Za-z0-9_]*))?`,
	)
	goImportedBridgePattern = regexp.MustCompile(
		`(?i)["']os/exec["'](?s:.*?)\b[A-Za-z_][A-Za-z0-9_]*\.Command(?:Context)?\s*\(`,
	)
	jsImportedBridgePattern = regexp.MustCompile(
		`(?i)(?:require\s*\(\s*["']child_process["']\s*\)|from\s+["']child_process["'])(?s:.*?)\b(?:exec|execSync|spawn|spawnSync)\s*\(`,
	)
	stdinDataCommands = map[string]struct{}{
		"base64": {}, "cat": {}, "cmp": {}, "cut": {}, "diff": {},
		"go": {}, "grep": {}, "head": {}, "hexdump": {}, "jq": {}, "od": {},
		"rg": {}, "sort": {}, "tail": {}, "tee": {}, "tr": {},
		"uniq": {}, "wc": {}, "xxd": {}, "yq": {},
	}
)

func scanCodeBlocks(policy Policy, blocks []codeexecutor.CodeBlock) []Finding {
	var findings []Finding
	for _, block := range blocks {
		language, understood := scannerLanguage(block.Language)
		if isShellLanguage(language) {
			findings = append(findings, scanExecution(policy, Request{
				Backend: BackendCodeExec,
				Command: block.Code,
			})...)
			continue
		}
		if !understood {
			findings = append(findings, newFinding(
				DecisionNeedsHumanReview, RiskHigh, "code.unsupported_language",
				"code language has no conservative safety scanner: "+strings.TrimSpace(block.Language),
				"review the code manually or use a language with a supported scanner",
			))
		}
		findings = append(findings, scanCodeResourceAbuse(language, block.Code)...)
		findings = append(findings, scanNotebookShellBridges(policy, language, block.Code)...)
		findings = append(findings, scanProcessBridge(policy, language, block.Code)...)
		findings = append(findings, scanCodeNetwork(policy, language, block.Code)...)
		findings = append(findings, scanCodePaths(policy, block.Code)...)
		findings = append(findings, scanSensitiveContent(block.Code)...)
	}
	return findings
}

func scannerLanguage(language string) (string, bool) {
	language = strings.ToLower(strings.TrimSpace(language))
	switch language {
	case "", "python", "py", "python3":
		return "python", true
	case "go", "golang":
		return "go", true
	case "javascript", "js", "typescript", "ts", "node":
		return "javascript", true
	case "bash", "sh", "shell", "zsh", "ash", "dash":
		return language, true
	default:
		return language, false
	}
}

func scanInlineInterpreters(policy Policy, segments [][]string) []Finding {
	var findings []Finding
	for _, argv := range segments {
		if len(argv) == 0 {
			continue
		}
		base := commandBase(argv[0])
		if isPythonInterpreter(base) {
			if module, rest, ok := interpreterModule(argv[1:]); ok {
				if module == "pip" {
					findings = append(findings, scanSegmentResources(
						policy, append([]string{"pip"}, rest...),
					)...)
				}
				findings = append(findings, inlineInterpreterFinding(base))
			}
			if payload, ok := interpreterPayload(argv[1:], "-c"); ok {
				findings = append(findings, scanCodeBlocks(policy, []codeexecutor.CodeBlock{{
					Language: "python", Code: payload,
				}})...)
				findings = append(findings, inlineInterpreterFinding(base))
			}
			continue
		}

		language, flags := inlineInterpreterSpec(base)
		for _, flag := range flags {
			payload, ok := interpreterPayload(argv[1:], flag)
			if !ok {
				continue
			}
			findings = append(findings, scanCodeBlocks(policy, []codeexecutor.CodeBlock{{
				Language: language, Code: payload,
			}})...)
			findings = append(findings, inlineInterpreterFinding(base))
			break
		}
	}
	return findings
}

func scanExecutableStdin(policy Policy, command, stdin string) []Finding {
	if stdin == "" {
		return nil
	}
	pipe, err := shellsafe.Parse(command)
	if err != nil || len(pipe.Commands) == 0 {
		return nil
	}
	language, executable := stdinProgram(pipe.Commands[0])
	if !executable {
		return nil
	}
	var findings []Finding
	if language != "" {
		findings = append(findings, scanCodeBlocks(
			policy,
			[]codeexecutor.CodeBlock{{Language: language, Code: stdin}},
		)...)
	}
	return append(findings, newFinding(
		DecisionNeedsHumanReview, RiskHigh, "code.stdin_program",
		"command consumes stdin as executable program content",
		"review stdin as code or use a non-executable data input",
	))
}

func stdinProgram(argv []string) (string, bool) {
	if len(argv) == 0 {
		return "", false
	}
	base := commandBase(argv[0])
	switch {
	case isPythonInterpreter(base):
		return "python", interpreterReadsStdin(argv[1:], "-c", "-m")
	case base == "node" || base == "nodejs":
		return "javascript", interpreterReadsStdin(
			argv[1:], "-e", "--eval", "-p", "--print", "-c", "--check",
		)
	case base == "ruby":
		return base, interpreterReadsStdin(argv[1:], "-e")
	case base == "perl":
		return base, interpreterReadsStdin(argv[1:], "-e", "-E")
	case base == "php":
		return base, interpreterReadsStdin(argv[1:], "-r")
	case base == "make" || base == "gmake":
		return "", optionReadsStdin(argv[1:], "-f", "--file", "--makefile")
	case base == "awk" || base == "gawk" || base == "mawk" || base == "nawk":
		return "", optionReadsStdin(argv[1:], "-f", "--file")
	case base == "sed":
		return "", optionReadsStdin(argv[1:], "-f", "--file")
	default:
		if _, dataOnly := stdinDataCommands[base]; dataOnly {
			return "", false
		}
		return "", true
	}
}

func interpreterReadsStdin(args []string, inlineFlags ...string) bool {
	if len(args) == 0 {
		return true
	}
	for _, arg := range args {
		if arg == "-" {
			return true
		}
	}
	for index, arg := range args {
		if arg == "--" {
			return index+1 == len(args)
		}
		for _, flag := range inlineFlags {
			if arg == flag || strings.HasPrefix(arg, flag+"=") ||
				!strings.HasPrefix(flag, "--") && strings.HasPrefix(arg, flag) &&
					len(arg) > len(flag) {
				return false
			}
		}
		if !strings.HasPrefix(arg, "-") {
			return false
		}
	}
	return true
}

func optionReadsStdin(args []string, options ...string) bool {
	for index, arg := range args {
		for _, option := range options {
			if arg == option && index+1 < len(args) && args[index+1] == "-" {
				return true
			}
			if strings.HasPrefix(option, "--") && arg == option+"=-" {
				return true
			}
			if !strings.HasPrefix(option, "--") && arg == option+"-" {
				return true
			}
		}
	}
	return false
}

func isPythonInterpreter(base string) bool {
	return base == "python" || base == "python3" || strings.HasPrefix(base, "python3.")
}

func inlineInterpreterSpec(base string) (string, []string) {
	switch base {
	case "node", "nodejs":
		return "javascript", []string{"-e", "--eval", "-p", "--print"}
	case "ruby":
		return "ruby", []string{"-e"}
	case "perl":
		return "perl", []string{"-e", "-E"}
	case "php":
		return "php", []string{"-r"}
	default:
		return "", nil
	}
}

func interpreterPayload(args []string, flag string) (string, bool) {
	for i, arg := range args {
		if arg == flag {
			if i+1 < len(args) {
				return args[i+1], true
			}
			return "", true
		}
		if strings.HasPrefix(flag, "--") && strings.HasPrefix(arg, flag+"=") {
			return strings.TrimPrefix(arg, flag+"="), true
		}
		if strings.HasPrefix(arg, flag) && len(arg) > len(flag) && strings.HasPrefix(flag, "-") &&
			!strings.HasPrefix(flag, "--") {
			return strings.TrimPrefix(arg, flag), true
		}
	}
	return "", false
}

func interpreterModule(args []string) (string, []string, bool) {
	for i, arg := range args {
		if arg == "-m" && i+1 < len(args) {
			return strings.ToLower(args[i+1]), args[i+2:], true
		}
		if strings.HasPrefix(arg, "-m") && len(arg) > 2 {
			return strings.ToLower(strings.TrimPrefix(arg, "-m")), args[i+1:], true
		}
	}
	return "", nil, false
}

func inlineInterpreterFinding(base string) Finding {
	return newFinding(
		DecisionNeedsHumanReview, RiskMedium, "code.inline_interpreter",
		"command executes inline code through "+base,
		"review inline code or use a narrowly scoped tool",
	)
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
	payload := strings.Join(processBridgeLiterals(language, code), " ")
	return processBridgeFindings(policy, payload,
		"code invokes a process or shell execution bridge")
}

func processBridgeFindings(policy Policy, payload, evidence string) []Finding {
	nested := scanExecution(policy, Request{Backend: BackendCodeExec, Command: payload})
	decision := DecisionNeedsHumanReview
	risk := RiskHigh
	if findingsDecision(nested) == DecisionDeny {
		decision = DecisionDeny
		risk = RiskCritical
	}
	findings := []Finding{newFinding(
		decision, risk, "code.process_bridge",
		evidence,
		"replace dynamic process execution with a narrowly scoped tool",
	)}
	return append(findings, nested...)
}

func processBridgeLiterals(language, code string) []string {
	literals := quotedLiterals(code)
	if language != "go" && language != "python" {
		return literals
	}
	filtered := literals[:0]
	for _, literal := range literals {
		if literal != "os/exec" && literal != "os" && literal != "subprocess" {
			filtered = append(filtered, literal)
		}
	}
	return filtered
}

func containsProcessBridge(language, code string) bool {
	lower := strings.ToLower(code)
	switch language {
	case "python", "py":
		if pythonImportedBridgePattern.MatchString(code) ||
			pythonDynamicOSBridgePattern.MatchString(code) || containsAny(lower,
			"subprocess.run(", "subprocess.call(", "subprocess.popen(",
			"os.system(", "os.popen(", "get_ipython().system(") {
			return true
		}
		if pythonFromImportInvokesProcess(code) {
			return true
		}
		for _, match := range executableSubmatches(pythonOSAliasPattern, code) {
			if len(match) > 1 && containsAny(lower,
				strings.ToLower(match[1])+".system(",
				strings.ToLower(match[1])+".popen(",
			) {
				return true
			}
		}
		return false
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

func pythonFromImportInvokesProcess(code string) bool {
	for _, match := range executableSubmatches(pythonFromProcessPattern, code) {
		if len(match) < 4 {
			continue
		}
		binding := match[2]
		if match[3] != "" {
			binding = match[3]
		}
		callPattern := regexp.MustCompile(`\b` + regexp.QuoteMeta(binding) + `\s*\(`)
		if len(executableSubmatches(callPattern, code)) > 0 {
			return true
		}
	}
	return false
}

func scanNotebookShellBridges(policy Policy, language, code string) []Finding {
	if language != "python" {
		return nil
	}
	findings := scanNotebookLineEscapes(policy, code)
	findings = append(findings, scanIPythonLineMagics(policy, code)...)
	return append(findings, scanIPythonCellMagics(policy, code)...)
}

func scanNotebookLineEscapes(policy Policy, code string) []Finding {
	var findings []Finding
	lines := strings.Split(code, "\n")
	lineOffset := 0
	for index, line := range lines {
		currentOffset := lineOffset
		lineOffset += len(line) + 1
		if !pythonLineExecutable(code, currentOffset) {
			continue
		}
		trimmed := strings.TrimSpace(line)
		payload := ""
		switch {
		case strings.HasPrefix(trimmed, "!!"):
			payload = strings.TrimSpace(strings.TrimPrefix(trimmed, "!!"))
		case strings.HasPrefix(trimmed, "!") && !strings.HasPrefix(trimmed, "!="):
			payload = strings.TrimSpace(strings.TrimPrefix(trimmed, "!"))
		case strings.HasPrefix(strings.ToLower(trimmed), "%system "):
			payload = strings.TrimSpace(trimmed[len("%system "):])
		case strings.EqualFold(trimmed, "%%bash") || strings.EqualFold(trimmed, "%%sh"):
			payload = strings.Join(lines[index+1:], "\n")
		}
		if payload == "" {
			continue
		}
		findings = append(findings, processBridgeFindings(
			policy, payload, "notebook shell escape executes an embedded command",
		)...)
		if strings.HasPrefix(strings.ToLower(trimmed), "%%") {
			break
		}
	}
	return findings
}

func scanIPythonLineMagics(policy Policy, code string) []Finding {
	var findings []Finding
	for _, args := range findCallArguments(code, "get_ipython().run_line_magic") {
		if len(args) < 2 {
			continue
		}
		magic := quotedLiterals(args[0])
		payload := quotedLiterals(args[1])
		if len(magic) != 1 || len(payload) != 1 ||
			!strings.EqualFold(magic[0], "system") {
			continue
		}
		findings = append(findings, processBridgeFindings(
			policy, payload[0], "IPython line magic executes an embedded command",
		)...)
	}
	return findings
}

func scanIPythonCellMagics(policy Policy, code string) []Finding {
	var findings []Finding
	for _, args := range findCallArguments(code, "get_ipython().run_cell_magic") {
		if len(args) < 3 {
			continue
		}
		magic := quotedLiterals(args[0])
		payload := quotedLiterals(args[2])
		if len(magic) != 1 || len(payload) != 1 ||
			!strings.EqualFold(magic[0], "bash") && !strings.EqualFold(magic[0], "sh") {
			continue
		}
		findings = append(findings, processBridgeFindings(
			policy, payload[0], "IPython cell magic executes an embedded shell command",
		)...)
	}
	return findings
}

type pythonLineState struct {
	quote   byte
	triple  bool
	escaped bool
	comment bool
}

func pythonLineExecutable(code string, position int) bool {
	var state pythonLineState
	for index := 0; index < position; index++ {
		current := code[index]
		if state.comment {
			if current == '\n' {
				state.comment = false
			}
			continue
		}
		if state.quote != 0 {
			index += state.consumeQuoted(code, index, position)
			continue
		}
		if current == '#' {
			state.comment = true
			continue
		}
		if current != '\'' && current != '"' {
			continue
		}
		state.quote = current
		if index+2 < position && code[index+1] == current && code[index+2] == current {
			state.triple = true
			index += 2
		}
	}
	return state.quote == 0 && !state.comment
}

func (s *pythonLineState) consumeQuoted(code string, index, position int) int {
	current := code[index]
	if s.escaped {
		s.escaped = false
		return 0
	}
	if current == '\\' {
		s.escaped = true
		return 0
	}
	if s.triple && current == s.quote && index+2 < position &&
		code[index+1] == s.quote && code[index+2] == s.quote {
		s.quote = 0
		s.triple = false
		return 2
	}
	if !s.triple && current == s.quote {
		s.quote = 0
	}
	return 0
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

func scanCodeNetwork(policy Policy, language, code string) []Finding {
	var findings []Finding
	for _, spec := range codeNetworkCallSpecs(language, code) {
		for _, args := range findCallArguments(code, spec.name) {
			destination, ok := spec.destination(args)
			if !ok {
				findings = append(findings, dynamicNetworkFinding())
				continue
			}
			destinationFindings, resolved := scanCodeDestination(policy, destination)
			findings = append(findings, destinationFindings...)
			if !resolved {
				findings = append(findings, dynamicNetworkFinding())
			}
		}
	}
	return findings
}

type codeNetworkCallSpec struct {
	name  string
	index int
	node  bool
}

func codeNetworkCallSpecs(language, code string) []codeNetworkCallSpec {
	switch language {
	case "python", "py":
		specs := []codeNetworkCallSpec{
			{name: "socket.create_connection", index: 0},
			{name: "socket.socket().connect", index: 0},
			{name: "urllib.request.urlopen", index: 0},
			{name: "requests.get", index: 0},
			{name: "requests.post", index: 0},
			{name: "httpx.get", index: 0},
			{name: "httpx.post", index: 0},
		}
		return append(specs, pythonAliasNetworkSpecs(code)...)
	case "go", "golang":
		specs := []codeNetworkCallSpec{
			{name: "net.Dial", index: 1},
			{name: "net.DialTimeout", index: 1},
			{name: "http.Get", index: 0},
			{name: "http.Post", index: 0},
			{name: "http.NewRequest", index: 1},
		}
		return append(specs, goAliasNetworkSpecs(code)...)
	case "javascript", "js", "typescript", "ts", "node":
		specs := []codeNetworkCallSpec{
			{name: "net.connect", node: true},
			{name: "net.createConnection", node: true},
			{name: "tls.connect", node: true},
			{name: "fetch", index: 0},
			{name: "http.get", index: 0},
			{name: "http.request", index: 0},
			{name: "https.get", index: 0},
			{name: "https.request", index: 0},
		}
		return append(specs, nodeAliasNetworkSpecs(code)...)
	default:
		return nil
	}
}

var (
	pythonModuleAliasPattern = regexp.MustCompile(
		`(?m)\bimport\s+(requests|httpx|socket)\s+as\s+([A-Za-z_][A-Za-z0-9_]*)`,
	)
	pythonFromAliasPattern = regexp.MustCompile(
		`(?m)\bfrom\s+(requests|httpx|socket)\s+import\s+(get|post|create_connection|socket)(?:\s+as\s+([A-Za-z_][A-Za-z0-9_]*))?`,
	)
	goNetAliasPattern = regexp.MustCompile(
		`(?m)\bimport\s+([A-Za-z_][A-Za-z0-9_]*|\.)\s+"net"`,
	)
	goImportGroupPattern     = regexp.MustCompile(`(?s)\bimport\s*\((.*?)\)`)
	goGroupedNetAliasPattern = regexp.MustCompile(
		`(?m)\b([A-Za-z_][A-Za-z0-9_]*|\.)\s+"net"`,
	)
	nodeModuleAliasPattern = regexp.MustCompile(
		`(?m)\b(?:const|let|var)\s+([A-Za-z_][A-Za-z0-9_]*)\s*=\s*require\(\s*["'](?:node:)?(?:net|tls)["']\s*\)`,
	)
	nodeImportAliasPattern = regexp.MustCompile(
		`(?m)\bimport\s+\*\s+as\s+([A-Za-z_][A-Za-z0-9_]*)\s+from\s+["'](?:node:)?(?:net|tls)["']`,
	)
	nodeDefaultImportAliasPattern = regexp.MustCompile(
		`(?m)\bimport\s+([A-Za-z_][A-Za-z0-9_]*)\s+from\s+["'](?:node:)?(?:net|tls)["']`,
	)
	nodeDestructuredAliasPattern = regexp.MustCompile(
		`(?m)\b(?:const|let|var)\s*\{\s*(connect|createConnection)(?:\s*:\s*([A-Za-z_][A-Za-z0-9_]*))?\s*\}\s*=\s*require\(\s*["'](?:node:)?net["']\s*\)`,
	)
	nodeNamedImportAliasPattern = regexp.MustCompile(
		`(?m)\bimport\s*\{\s*(connect|createConnection)(?:\s+as\s+([A-Za-z_][A-Za-z0-9_]*))?\s*\}\s*from\s*["'](?:node:)?net["']`,
	)
)

func pythonAliasNetworkSpecs(code string) []codeNetworkCallSpec {
	var specs []codeNetworkCallSpec
	for _, match := range executableSubmatches(pythonModuleAliasPattern, code) {
		module, alias := match[1], match[2]
		switch module {
		case "requests", "httpx":
			specs = append(specs,
				codeNetworkCallSpec{name: alias + ".get", index: 0},
				codeNetworkCallSpec{name: alias + ".post", index: 0},
			)
		case "socket":
			specs = append(specs,
				codeNetworkCallSpec{name: alias + ".create_connection", index: 0},
				codeNetworkCallSpec{name: alias + ".socket().connect", index: 0},
			)
		}
	}
	for _, match := range executableSubmatches(pythonFromAliasPattern, code) {
		if match[1] == "socket" && match[2] == "socket" {
			continue
		}
		name := match[2]
		if match[3] != "" {
			name = match[3]
		}
		specs = append(specs, codeNetworkCallSpec{name: name, index: 0})
	}
	return append(specs, pythonSocketObjectSpecs(code)...)
}

func pythonSocketObjectSpecs(code string) []codeNetworkCallSpec {
	constructors := []string{"socket.socket"}
	for _, match := range executableSubmatches(pythonModuleAliasPattern, code) {
		if match[1] == "socket" {
			constructors = append(constructors, match[2]+".socket")
		}
	}
	for _, match := range executableSubmatches(pythonFromAliasPattern, code) {
		if match[1] != "socket" || match[2] != "socket" {
			continue
		}
		name := match[2]
		if match[3] != "" {
			name = match[3]
		}
		constructors = append(constructors, name)
	}

	var objects []string
	for _, constructor := range constructors {
		constructorCall := regexp.QuoteMeta(constructor) + `\s*\([^()\n]*\)`
		patterns := []*regexp.Regexp{
			regexp.MustCompile(
				`(?m)\b([A-Za-z_][A-Za-z0-9_]*(?:\.[A-Za-z_][A-Za-z0-9_]*)?)\s*=\s*` +
					constructorCall,
			),
			regexp.MustCompile(
				`(?m)\bwith\s+` + constructorCall +
					`\s+as\s+([A-Za-z_][A-Za-z0-9_]*)\s*:`,
			),
		}
		for _, pattern := range patterns {
			for _, match := range executableSubmatches(pattern, code) {
				objects = appendUniqueString(objects, match[1])
			}
		}
	}
	for pass := 0; pass < 4; pass++ {
		before := len(objects)
		for _, object := range append([]string(nil), objects...) {
			pattern := regexp.MustCompile(
				`(?m)\b([A-Za-z_][A-Za-z0-9_]*)\s*=\s*` +
					regexp.QuoteMeta(object) + `\b`,
			)
			for _, match := range executableSubmatches(pattern, code) {
				objects = appendUniqueString(objects, match[1])
			}
		}
		if len(objects) == before {
			break
		}
	}

	specs := make([]codeNetworkCallSpec, 0, len(objects))
	for _, object := range objects {
		specs = append(specs, codeNetworkCallSpec{name: object + ".connect", index: 0})
	}
	return specs
}

func appendUniqueString(values []string, value string) []string {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

func goAliasNetworkSpecs(code string) []codeNetworkCallSpec {
	var specs []codeNetworkCallSpec
	for _, match := range executableSubmatches(goNetAliasPattern, code) {
		prefix := match[1] + "."
		if match[1] == "." {
			prefix = ""
		}
		specs = append(specs,
			codeNetworkCallSpec{name: prefix + "Dial", index: 1},
			codeNetworkCallSpec{name: prefix + "DialTimeout", index: 1},
		)
	}
	for _, group := range executableSubmatches(goImportGroupPattern, code) {
		if len(group) < 2 {
			continue
		}
		for _, match := range executableSubmatches(goGroupedNetAliasPattern, group[1]) {
			prefix := match[1] + "."
			if match[1] == "." {
				prefix = ""
			}
			specs = append(specs,
				codeNetworkCallSpec{name: prefix + "Dial", index: 1},
				codeNetworkCallSpec{name: prefix + "DialTimeout", index: 1},
			)
		}
	}
	return specs
}

func nodeAliasNetworkSpecs(code string) []codeNetworkCallSpec {
	var specs []codeNetworkCallSpec
	for _, pattern := range []*regexp.Regexp{
		nodeModuleAliasPattern, nodeImportAliasPattern, nodeDefaultImportAliasPattern,
	} {
		for _, match := range executableSubmatches(pattern, code) {
			alias := match[1]
			specs = append(specs,
				codeNetworkCallSpec{name: alias + ".connect", node: true},
				codeNetworkCallSpec{name: alias + ".createConnection", node: true},
			)
		}
	}
	for _, match := range executableSubmatches(nodeDestructuredAliasPattern, code) {
		alias := match[1]
		if match[2] != "" {
			alias = match[2]
		}
		specs = append(specs, codeNetworkCallSpec{name: alias, node: true})
	}
	for _, match := range executableSubmatches(nodeNamedImportAliasPattern, code) {
		alias := match[1]
		if match[2] != "" {
			alias = match[2]
		}
		specs = append(specs, codeNetworkCallSpec{name: alias, node: true})
	}
	return specs
}

func executableSubmatches(pattern *regexp.Regexp, code string) [][]string {
	indices := pattern.FindAllStringSubmatchIndex(code, -1)
	matches := make([][]string, 0, len(indices))
	for _, index := range indices {
		if len(index) < 2 || !codePositionExecutable(code, index[0]) {
			continue
		}
		match := make([]string, len(index)/2)
		for group := 0; group < len(match); group++ {
			start, end := index[group*2], index[group*2+1]
			if start >= 0 && end >= start {
				match[group] = code[start:end]
			}
		}
		matches = append(matches, match)
	}
	return matches
}

func (spec codeNetworkCallSpec) destination(args []string) (string, bool) {
	if !spec.node {
		if spec.index < 0 || spec.index >= len(args) {
			return "", false
		}
		return args[spec.index], true
	}
	if len(args) == 0 {
		return "", false
	}
	first := strings.TrimSpace(args[0])
	if strings.HasPrefix(first, "{") {
		if match := nodeHostPropertyPattern.FindStringSubmatch(first); len(match) == 2 {
			return match[1], true
		}
		if match := nodePathPropertyPattern.FindStringSubmatch(first); len(match) == 2 {
			return localPathExpression(match[1]), true
		}
		return "", false
	}
	if _, err := strconv.Atoi(first); err == nil {
		if len(args) < 2 {
			return "", false
		}
		return args[1], true
	}
	if len(args) == 1 && len(quotedLiterals(first)) == 1 {
		return localPathExpression(first), true
	}
	return first, true
}

var nodeHostPropertyPattern = regexp.MustCompile(
	`(?i)\bhost\s*:\s*("(?:\\.|[^"])*"|'(?:\\.|[^'])*')`,
)

var nodePathPropertyPattern = regexp.MustCompile(
	`(?i)\bpath\s*:\s*("(?:\\.|[^"])*"|'(?:\\.|[^'])*')`,
)

func localPathExpression(quoted string) string {
	literals := quotedLiterals(quoted)
	if len(literals) != 1 || isPathLike(literals[0]) {
		return quoted
	}
	return strconv.Quote("./" + literals[0])
}

// findCallArguments extracts balanced argument lists only from executable
// source positions. It intentionally stays conservative: malformed or dynamic
// calls are returned as unresolved and require review by the caller.
func findCallArguments(code, name string) [][]string {
	var calls [][]string
	for offset := 0; offset < len(code); {
		relative := strings.Index(code[offset:], name)
		if relative < 0 {
			break
		}
		start := offset + relative
		offset = start + len(name)
		if (start > 0 && isCodeIdentifierByte(code[start-1])) ||
			!codePositionExecutable(code, start) {
			continue
		}
		open := offset
		for open < len(code) && (code[open] == ' ' || code[open] == '\t' || code[open] == '\n') {
			open++
		}
		if open >= len(code) || code[open] != '(' {
			continue
		}
		contents, end, ok := balancedCallContents(code, open)
		if !ok {
			calls = append(calls, nil)
			continue
		}
		calls = append(calls, splitCallArguments(contents))
		offset = end
	}
	return calls
}

func codePositionExecutable(code string, position int) bool {
	var state codePositionState
	for i := 0; i < position; i++ {
		if state.consume(code, i, position) {
			i++
		}
	}
	return state.executable()
}

type codePositionState struct {
	quote        byte
	escaped      bool
	lineComment  bool
	blockComment bool
}

func (s *codePositionState) consume(code string, index, limit int) bool {
	current := code[index]
	if s.lineComment {
		s.consumeLineComment(current)
		return false
	}
	if s.blockComment {
		return s.consumeBlockComment(code, index, limit)
	}
	if s.quote != 0 {
		s.consumeQuote(current)
		return false
	}
	return s.consumeCode(code, index, limit)
}

func (s *codePositionState) consumeLineComment(current byte) {
	if current == '\n' {
		s.lineComment = false
	}
}

func (s *codePositionState) consumeBlockComment(
	code string,
	index, limit int,
) bool {
	if code[index] == '*' && index+1 < limit && code[index+1] == '/' {
		s.blockComment = false
		return true
	}
	return false
}

func (s *codePositionState) consumeQuote(current byte) {
	if s.escaped {
		s.escaped = false
		return
	}
	if current == '\\' {
		s.escaped = true
		return
	}
	if current == s.quote {
		s.quote = 0
	}
}

func (s *codePositionState) consumeCode(code string, index, limit int) bool {
	current := code[index]
	if current == '/' && index+1 < limit {
		if code[index+1] == '/' {
			s.lineComment = true
			return true
		}
		if code[index+1] == '*' {
			s.blockComment = true
			return true
		}
	}
	if current == '#' {
		s.lineComment = true
		return false
	}
	if current == '\'' || current == '"' || current == '`' {
		s.quote = current
	}
	return false
}

func (s codePositionState) executable() bool {
	return s.quote == 0 && !s.lineComment && !s.blockComment
}

func isCodeIdentifierByte(value byte) bool {
	return (value >= 'a' && value <= 'z') || (value >= 'A' && value <= 'Z') ||
		(value >= '0' && value <= '9') || value == '_'
}

func balancedCallContents(code string, open int) (string, int, bool) {
	depth := 0
	var quote byte
	escaped := false
	for i := open; i < len(code); i++ {
		current := code[i]
		if quote != 0 {
			if escaped {
				escaped = false
				continue
			}
			if current == '\\' {
				escaped = true
				continue
			}
			if current == quote {
				quote = 0
			}
			continue
		}
		if current == '\'' || current == '"' || current == '`' {
			quote = current
			continue
		}
		switch current {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return code[open+1 : i], i + 1, true
			}
		}
	}
	return "", len(code), false
}

func splitCallArguments(contents string) []string {
	var args []string
	start, depth := 0, 0
	var quote byte
	escaped := false
	for i := 0; i < len(contents); i++ {
		current := contents[i]
		if quote != 0 {
			if escaped {
				escaped = false
				continue
			}
			if current == '\\' {
				escaped = true
				continue
			}
			if current == quote {
				quote = 0
			}
			continue
		}
		if current == '\'' || current == '"' || current == '`' {
			quote = current
			continue
		}
		switch current {
		case '(', '[', '{':
			depth++
		case ')', ']', '}':
			depth--
		case ',':
			if depth == 0 {
				args = append(args, strings.TrimSpace(contents[start:i]))
				start = i + 1
			}
		}
	}
	if tail := strings.TrimSpace(contents[start:]); tail != "" {
		args = append(args, tail)
	}
	return args
}

func scanCodeDestination(policy Policy, expression string) ([]Finding, bool) {
	var findings []Finding
	recognized := false
	if explicitURLPattern.MatchString(expression) {
		findings = append(findings, scanNetworkText(policy, expression)...)
	}
	for _, literal := range quotedLiterals(expression) {
		if isFileURL(literal) {
			if filePath, ok := fileURLPath(literal); ok {
				if finding, denied := deniedPathFinding(policy.DeniedPaths, filePath); denied {
					findings = append(findings, finding)
				}
			}
			recognized = true
			continue
		}
		if _, ok := explicitHost(literal); ok {
			recognized = true
			continue
		}
		if host, ok := knownDestinationHost(literal); ok {
			recognized = true
			if finding, denied := networkDestinationFinding(policy, host); denied {
				findings = append(findings, finding)
			}
			continue
		}
		if isPathLike(literal) {
			recognized = true
		}
	}
	return findings, recognized && staticCodeDestination(expression)
}

func staticCodeDestination(expression string) bool {
	var quote byte
	escaped := false
	for i := 0; i < len(expression); i++ {
		current := expression[i]
		if quote != 0 {
			if escaped {
				escaped = false
				continue
			}
			if current == '\\' {
				escaped = true
				continue
			}
			if quote == '`' && current == '$' && i+1 < len(expression) && expression[i+1] == '{' {
				return false
			}
			if current == quote {
				quote = 0
			}
			continue
		}
		if current == '\'' || current == '"' || current == '`' {
			quote = current
			continue
		}
		if !staticDestinationSyntax(current) {
			return false
		}
	}
	return quote == 0
}

func staticDestinationSyntax(value byte) bool {
	return value == ' ' || value == '\t' || value == '\n' || value == '\r' ||
		(value >= '0' && value <= '9') || strings.ContainsRune("()[]{},:", rune(value))
}

func dynamicNetworkFinding() Finding {
	return newFinding(
		DecisionNeedsHumanReview, RiskMedium, "network.dynamic_destination",
		"network API destination is dynamic or could not be isolated",
		"use an explicit allowlisted destination or review the code",
	)
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
	if pathKey(parentKey) {
		if finding, denied := deniedPathFinding(policy.DeniedPaths, value); denied {
			return []Finding{finding}
		}
		return nil
	}
	if networkKey(parentKey) {
		if isFileURL(value) {
			if filePath, ok := fileURLPath(value); ok {
				if finding, denied := deniedPathFinding(policy.DeniedPaths, filePath); denied {
					return []Finding{finding}
				}
			}
			return nil
		}
		findings := scanNetworkText(policy, value)
		if _, ok := explicitHost(value); ok {
			return findings
		}
		if host, ok := knownDestinationHost(value); ok {
			if finding, denied := networkDestinationFinding(policy, host); denied {
				findings = append(findings, finding)
			}
			return findings
		}
		return append(findings, dynamicNetworkFinding())
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
	case "command", "commands", "cmd", "script", "scripts", "shell", "args", "argv":
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

func pathKey(key string) bool {
	key = normalizedArgumentKey(key)
	switch key {
	case "path", "paths", "file", "files", "file_name", "file_names",
		"filename", "filenames", "source", "sources":
		return true
	}
	return strings.HasSuffix(key, "_path") || strings.HasSuffix(key, "_paths") ||
		strings.HasSuffix(key, "_file") || strings.HasSuffix(key, "_files")
}

func normalizedArgumentKey(key string) string {
	key = strings.TrimSpace(key)
	var normalized strings.Builder
	for index := 0; index < len(key); index++ {
		current := key[index]
		if current == '-' {
			current = '_'
		}
		if current >= 'A' && current <= 'Z' {
			if normalized.Len() > 0 && key[index-1] != '_' && key[index-1] != '-' {
				normalized.WriteByte('_')
			}
			current += 'a' - 'A'
		}
		normalized.WriteByte(current)
	}
	return normalized.String()
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
