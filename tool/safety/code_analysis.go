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
	"go/ast"
	goparser "go/parser"
	"go/token"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

var (
	pythonFromImportPattern = regexp.MustCompile(
		`(?m)^[[:space:]]*from[[:space:]]+([A-Za-z_][A-Za-z0-9_.]*)[[:space:]]+import[[:space:]]+([A-Za-z_][A-Za-z0-9_]*)(?:[[:space:]]+as[[:space:]]+([A-Za-z_][A-Za-z0-9_]*))?`,
	)
	pythonImportPattern = regexp.MustCompile(
		`(?m)^[[:space:]]*import[[:space:]]+([A-Za-z_][A-Za-z0-9_.]*)(?:[[:space:]]+as[[:space:]]+([A-Za-z_][A-Za-z0-9_]*))?`,
	)
	codeAliasAssignmentPattern = regexp.MustCompile(
		`(?m)\b([A-Za-z_][A-Za-z0-9_]*)[[:space:]]*=[[:space:]]*([A-Za-z_][A-Za-z0-9_.]*)\b`,
	)
	codeCallNamePattern = regexp.MustCompile(
		`\b([A-Za-z_][A-Za-z0-9_.]*)[[:space:]]*\(`,
	)
	pythonWhilePattern = regexp.MustCompile(
		`(?m)^[[:space:]]*while[[:space:]]+([^:\r\n]+)[[:space:]]*:`,
	)
	numericComparisonPattern = regexp.MustCompile(
		`^[[:space:]]*([-+]?(?:[0-9]+(?:\.[0-9]*)?|\.[0-9]+)(?:e[-+]?[0-9]+)?)[[:space:]]*(==|!=|<=|>=|<|>)[[:space:]]*([-+]?(?:[0-9]+(?:\.[0-9]*)?|\.[0-9]+)(?:e[-+]?[0-9]+)?)[[:space:]]*$`,
	)
	jsWhilePattern = regexp.MustCompile(
		`(?m)\bwhile[[:space:]]*\([[:space:]]*([^()\r\n]+)[[:space:]]*\)`,
	)
	jsEndlessForPattern = regexp.MustCompile(
		`(?m)\bfor[[:space:]]*\([[:space:]]*;[[:space:]]*;[[:space:]]*\)`,
	)
	jsFSDeleteAliasPattern = regexp.MustCompile(
		`(?m)\b([A-Za-z_$][A-Za-z0-9_$]*)[[:space:]]*=[[:space:]]*require[[:space:]]*\([[:space:]]*["'](?:node:)?fs["'][[:space:]]*\)[[:space:]]*\.[[:space:]]*(?:rmSync|rm|unlinkSync|rmdirSync)[[:space:]]*;?`,
	)
	jsRequireAliasPattern = regexp.MustCompile(
		`(?m)\b(?:const|let|var)[[:space:]]+([A-Za-z_$][A-Za-z0-9_$]*)[[:space:]]*=[[:space:]]*require[[:space:]]*\([[:space:]]*["'](?:node:)?([^"']+)["'][[:space:]]*\)`,
	)
	jsRequirePropertyAliasPattern = regexp.MustCompile(
		`(?m)\b(?:const|let|var)[[:space:]]+([A-Za-z_$][A-Za-z0-9_$]*)[[:space:]]*=[[:space:]]*require[[:space:]]*\([[:space:]]*["'](?:node:)?([^"']+)["'][[:space:]]*\)[[:space:]]*\.[[:space:]]*([A-Za-z_$][A-Za-z0-9_$]*)`,
	)
	jsNamedImportPattern = regexp.MustCompile(
		`(?m)\bimport[[:space:]]*\{[[:space:]]*([A-Za-z_$][A-Za-z0-9_$]*)(?:[[:space:]]+as[[:space:]]+([A-Za-z_$][A-Za-z0-9_$]*))?[[:space:]]*\}[[:space:]]*from[[:space:]]*["'](?:node:)?([^"']+)["']`,
	)
	jsFSDeleteDestructurePattern = regexp.MustCompile(
		`(?m)\{[^}\r\n]*\b(?:rmSync|rm|unlinkSync|unlink|rmdirSync|rmdir)\b[^}\r\n]*\}[[:space:]]*=[[:space:]]*require[[:space:]]*\([[:space:]]*["'](?:node:)?fs["'][[:space:]]*\)`,
	)
	jsFSDeleteNamedImportPattern = regexp.MustCompile(
		`(?m)\bimport[[:space:]]*\{[^}\r\n]*\b(?:rmSync|rm|unlinkSync|unlink|rmdirSync|rmdir)\b[^}\r\n]*\}[[:space:]]*from[[:space:]]*["'](?:node:)?fs["']`,
	)
	jsRiskyDestructurePattern = regexp.MustCompile(
		`(?m)(?:\{[^}\r\n]+\}[[:space:]]*=[[:space:]]*require[[:space:]]*\([[:space:]]*["'](?:node:)?(?:fs|http|https|net|tls|child_process|worker_threads)["'][[:space:]]*\)|\bimport[[:space:]]*\{[^}\r\n]+\}[[:space:]]*from[[:space:]]*["'](?:node:)?(?:fs|http|https|net|tls|child_process|worker_threads)["'])`,
	)
)

func (s *Scanner) scanLanguageAwareCode(block codeBlock) []Finding {
	switch normalizeCodeLanguage(block.language) {
	case "python", "py":
		return s.scanPythonCode(block.code)
	case "javascript", "js", "node", "typescript", "ts":
		return s.scanJavaScriptCode(block.code)
	case "go", "golang":
		return s.scanGoCode(block.code)
	default:
		return nil
	}
}

func (s *Scanner) scanPythonCode(code string) []Finding {
	aliases := pythonImportAliases(code)
	extendCodeAliases(code, aliases)
	constants := foldedStringConstants(code)

	var findings []Finding
	if pythonHasRiskyWildcardImport(code) {
		findings = append(findings, finding(
			ruleCodePolicy,
			DecisionAsk,
			RiskHigh,
			"Python code wildcard-imports a module with execution, filesystem, or network capabilities",
			"Use explicit imports so safety-relevant calls can be resolved.",
		))
	}
	for _, match := range codeCallNamePattern.FindAllStringSubmatchIndex(code, -1) {
		if len(match) < 4 {
			continue
		}
		name := resolveCodeAlias(code[match[2]:match[3]], aliases)
		directConstant, hasDirectConstant :=
			readConcatenatedStringAt(code, match[1])
		switch {
		case pythonDeleteCall(name):
			findings = append(findings, codeDeleteFinding())
		case pythonProcessCall(name):
			findings = append(findings, codeProcessFinding())
		case pythonNetworkCall(name):
			networkConstants := constants
			if hasDirectConstant {
				networkConstants = []string{directConstant}
			}
			findings = append(
				findings,
				s.codeNetworkFindings(networkConstants)...,
			)
		case name == "open" || name == "builtins.open" ||
			name == "os.open" || name == "pathlib.Path":
			if !hasDirectConstant {
				findings = append(
					findings,
					finding(
						ruleCodePolicy,
						DecisionAsk,
						RiskHigh,
						"code opens a path that cannot be statically verified",
						"Use a literal non-sensitive path or require human review for dynamic file access.",
					),
				)
				continue
			}
			if path := matchSensitivePath(
				directConstant,
				s.policy.ForbiddenPaths,
			); path != "" {
				findings = append(findings, codeSensitivePathFinding(path))
			}
		case pythonRiskyUnresolvedCall(name):
			findings = append(findings, codeUnresolvedCapabilityFinding(
				"Python",
				name,
			))
		}
	}
	if pythonHasTruthyConstantLoop(code) {
		findings = append(findings, codeInfiniteLoopFinding())
	}
	return findings
}

func pythonRiskyUnresolvedCall(name string) bool {
	lower := strings.ToLower(name)
	for _, prefix := range []string{
		"ctypes.", "importlib.", "marshal.", "pickle.",
		"shutil.", "socket.", "subprocess.", "urllib.request.",
	} {
		if strings.HasPrefix(lower, prefix) {
			return true
		}
	}
	if strings.HasPrefix(lower, "os.") {
		for _, safePrefix := range []string{
			"os.path.", "os.getenv", "os.getpid", "os.getcwd",
		} {
			if strings.HasPrefix(lower, safePrefix) {
				return false
			}
		}
		return true
	}
	return false
}

func pythonImportAliases(code string) map[string]string {
	aliases := make(map[string]string)
	for _, match := range pythonFromImportPattern.FindAllStringSubmatch(
		code,
		-1,
	) {
		local := match[2]
		if match[3] != "" {
			local = match[3]
		}
		aliases[local] = match[1] + "." + match[2]
	}
	for _, match := range pythonImportPattern.FindAllStringSubmatch(
		code,
		-1,
	) {
		local := strings.Split(match[1], ".")[0]
		if match[2] != "" {
			local = match[2]
		}
		aliases[local] = match[1]
	}
	extendPythonImportAliases(code, aliases)
	return aliases
}

func extendPythonImportAliases(
	code string,
	aliases map[string]string,
) {
	for _, rawLine := range strings.Split(code, "\n") {
		line := strings.TrimSpace(strings.SplitN(rawLine, "#", 2)[0])
		if strings.HasPrefix(line, "from ") {
			body := strings.TrimSpace(strings.TrimPrefix(line, "from "))
			module, imported, ok := strings.Cut(body, " import ")
			if !ok {
				continue
			}
			imported = strings.Trim(imported, "() ")
			for _, item := range strings.Split(imported, ",") {
				parts := strings.Fields(strings.TrimSpace(item))
				if len(parts) == 0 || parts[0] == "*" {
					continue
				}
				local := parts[0]
				if len(parts) == 3 && parts[1] == "as" {
					local = parts[2]
				}
				aliases[local] = strings.TrimSpace(module) + "." + parts[0]
			}
			continue
		}
		if !strings.HasPrefix(line, "import ") {
			continue
		}
		for _, item := range strings.Split(
			strings.TrimPrefix(line, "import "),
			",",
		) {
			parts := strings.Fields(strings.TrimSpace(item))
			if len(parts) == 0 {
				continue
			}
			local := strings.Split(parts[0], ".")[0]
			if len(parts) == 3 && parts[1] == "as" {
				local = parts[2]
			}
			aliases[local] = parts[0]
		}
	}
}

func pythonHasRiskyWildcardImport(code string) bool {
	for _, rawLine := range strings.Split(code, "\n") {
		line := strings.TrimSpace(strings.SplitN(rawLine, "#", 2)[0])
		if !strings.HasSuffix(line, " import *") {
			continue
		}
		module := strings.TrimSpace(strings.TrimSuffix(
			strings.TrimPrefix(line, "from "),
			" import *",
		))
		for _, prefix := range []string{
			"ctypes", "importlib", "os", "shutil", "socket",
			"subprocess", "urllib.request",
		} {
			if module == prefix || strings.HasPrefix(module, prefix+".") {
				return true
			}
		}
	}
	return false
}

func extendCodeAliases(code string, aliases map[string]string) {
	for iteration := 0; iteration < 4; iteration++ {
		changed := false
		for _, match := range codeAliasAssignmentPattern.FindAllStringSubmatchIndex(
			code,
			-1,
		) {
			if len(match) < 6 {
				continue
			}
			afterSource := skipCodeSpace(code, match[5])
			if afterSource < len(code) && code[afterSource] == '(' {
				// This is a function call result, not a function alias.
				continue
			}
			local := code[match[2]:match[3]]
			source := code[match[4]:match[5]]
			target := resolveCodeAlias(source, aliases)
			if aliases[local] == target {
				continue
			}
			aliases[local] = target
			changed = true
		}
		if !changed {
			return
		}
	}
}

func resolveCodeAlias(name string, aliases map[string]string) string {
	first, rest, found := strings.Cut(name, ".")
	resolved, ok := aliases[first]
	if !ok {
		return name
	}
	if found {
		return resolved + "." + rest
	}
	return resolved
}

func pythonDeleteCall(name string) bool {
	switch strings.ToLower(name) {
	case "shutil.rmtree", "os.remove", "os.unlink", "os.rmdir",
		"os.removedirs":
		return true
	default:
		return false
	}
}

func pythonProcessCall(name string) bool {
	lower := strings.ToLower(name)
	return lower == "os.system" ||
		lower == "os.popen" ||
		strings.HasPrefix(lower, "subprocess.")
}

func pythonNetworkCall(name string) bool {
	lower := strings.ToLower(name)
	return strings.HasPrefix(lower, "requests.") ||
		strings.HasPrefix(lower, "httpx.") ||
		lower == "urllib.request.urlopen" ||
		lower == "socket.create_connection" ||
		lower == "socket.connect"
}

func pythonHasTruthyConstantLoop(code string) bool {
	for _, match := range pythonWhilePattern.FindAllStringSubmatch(code, -1) {
		if len(match) != 2 {
			continue
		}
		condition := strings.TrimSpace(match[1])
		if constantConditionTrue(condition) {
			return true
		}
	}
	return false
}

func constantConditionTrue(condition string) bool {
	condition = strings.TrimSpace(condition)
	if strings.EqualFold(condition, "true") {
		return true
	}
	if value, err := strconv.ParseFloat(condition, 64); err == nil {
		return value != 0
	}
	if value, next, ok := readCodeString(condition, 0); ok &&
		next == len(condition) {
		return value != ""
	}
	match := numericComparisonPattern.FindStringSubmatch(condition)
	if len(match) != 4 {
		return false
	}
	left, leftErr := strconv.ParseFloat(match[1], 64)
	right, rightErr := strconv.ParseFloat(match[3], 64)
	if leftErr != nil || rightErr != nil {
		return false
	}
	switch match[2] {
	case "==":
		return left == right
	case "!=":
		return left != right
	case "<":
		return left < right
	case "<=":
		return left <= right
	case ">":
		return left > right
	case ">=":
		return left >= right
	default:
		return false
	}
}

func (s *Scanner) scanJavaScriptCode(code string) []Finding {
	var findings []Finding
	constants := foldedStringConstants(code)
	aliases := javaScriptAliases(code)
	findings = append(findings, javaScriptDestructureFindings(code)...)
	extendCodeAliases(code, aliases)
	findings = append(
		findings,
		s.scanJavaScriptCalls(code, constants, aliases)...,
	)
	if javaScriptHasTruthyConstantLoop(code) {
		findings = append(findings, codeInfiniteLoopFinding())
	}
	return findings
}

func javaScriptAliases(code string) map[string]string {
	aliases := make(map[string]string)
	for _, match := range jsRequireAliasPattern.FindAllStringSubmatch(
		code,
		-1,
	) {
		aliases[match[1]] = match[2]
	}
	for _, match := range jsRequirePropertyAliasPattern.FindAllStringSubmatch(
		code,
		-1,
	) {
		aliases[match[1]] = match[2] + "." + match[3]
	}
	for _, match := range jsNamedImportPattern.FindAllStringSubmatch(
		code,
		-1,
	) {
		local := match[1]
		if match[2] != "" {
			local = match[2]
		}
		aliases[local] = match[3] + "." + match[1]
	}
	for _, match := range jsFSDeleteAliasPattern.FindAllStringSubmatch(
		code,
		-1,
	) {
		aliases[match[1]] = "fs.rmSync"
	}
	return aliases
}

func javaScriptDestructureFindings(code string) []Finding {
	if jsFSDeleteDestructurePattern.MatchString(code) ||
		jsFSDeleteNamedImportPattern.MatchString(code) {
		return []Finding{codeDeleteFinding()}
	}
	if jsRiskyDestructurePattern.MatchString(code) {
		return []Finding{finding(
			ruleCodePolicy,
			DecisionAsk,
			RiskHigh,
			"JavaScript code destructures a capability-bearing module that cannot be classified completely",
			"Use a direct qualified API call or require human review.",
		)}
	}
	return nil
}

func (s *Scanner) scanJavaScriptCalls(
	code string,
	constants []string,
	aliases map[string]string,
) []Finding {
	var findings []Finding
	for _, match := range codeCallNamePattern.FindAllStringSubmatchIndex(code, -1) {
		if len(match) < 4 {
			continue
		}
		name := strings.ToLower(resolveCodeAlias(
			code[match[2]:match[3]],
			aliases,
		))
		directConstant, hasDirectConstant :=
			readConcatenatedStringAt(code, match[1])
		findings = append(findings, s.javaScriptCallFindings(
			name,
			directConstant,
			hasDirectConstant,
			constants,
		)...)
	}
	return findings
}

func (s *Scanner) javaScriptCallFindings(
	name string,
	directConstant string,
	hasDirectConstant bool,
	constants []string,
) []Finding {
	switch {
	case strings.HasPrefix(name, "fs.") &&
		(strings.Contains(name, "rm") ||
			strings.Contains(name, "unlink") ||
			strings.Contains(name, "rmdir")):
		return []Finding{codeDeleteFinding()}
	case strings.HasPrefix(name, "fs.") &&
		(strings.Contains(name, "readfile") ||
			strings.HasSuffix(name, ".open") ||
			strings.HasSuffix(name, ".opensync")):
		return s.javaScriptReadFindings(directConstant, hasDirectConstant)
	case name == "fetch" ||
		strings.HasPrefix(name, "axios.") ||
		strings.HasPrefix(name, "http.") ||
		strings.HasPrefix(name, "https.") ||
		strings.HasPrefix(name, "net."):
		return s.codeNetworkFindings(constants)
	case strings.HasPrefix(name, "child_process."):
		return []Finding{codeProcessFinding()}
	case javaScriptRiskyUnresolvedCall(name):
		return []Finding{codeUnresolvedCapabilityFinding("JavaScript", name)}
	default:
		return nil
	}
}

func (s *Scanner) javaScriptReadFindings(
	directConstant string,
	hasDirectConstant bool,
) []Finding {
	if !hasDirectConstant {
		return []Finding{finding(
			ruleCodePolicy,
			DecisionAsk,
			RiskHigh,
			"code reads a path that cannot be statically verified",
			"Use a literal non-sensitive path or require human review for dynamic file access.",
		)}
	}
	path := matchSensitivePath(directConstant, s.policy.ForbiddenPaths)
	if path == "" {
		return nil
	}
	return []Finding{codeSensitivePathFinding(path)}
}

func javaScriptHasTruthyConstantLoop(code string) bool {
	if jsEndlessForPattern.MatchString(code) {
		return true
	}
	for _, match := range jsWhilePattern.FindAllStringSubmatch(code, -1) {
		if len(match) == 2 && constantConditionTrue(match[1]) {
			return true
		}
	}
	return false
}

func javaScriptRiskyUnresolvedCall(name string) bool {
	for _, prefix := range []string{
		"bun.", "child_process.", "deno.", "fs.", "http.",
		"https.", "net.", "tls.", "worker_threads.",
	} {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

func (s *Scanner) scanGoCode(code string) []Finding {
	file, err := goparser.ParseFile(
		token.NewFileSet(),
		"codeexec.go",
		code,
		goparser.AllErrors,
	)
	if err != nil || file == nil {
		return []Finding{finding(
			ruleCodePolicy,
			s.policy.ParseFailureAction,
			RiskMedium,
			"Go code could not be parsed safely",
			"Fix the Go syntax or require human review before execution.",
		)}
	}

	imports := goImportAliases(file)
	callAliases := goCallAliases(file, imports)
	var findings []Finding
	if goHasRiskyDotImport(file) {
		findings = append(findings, finding(
			ruleCodePolicy,
			DecisionAsk,
			RiskHigh,
			"Go code dot-imports a package with execution, filesystem, or network capabilities",
			"Use an explicit package qualifier so safety-relevant calls can be resolved.",
		))
	}
	ast.Inspect(file, func(node ast.Node) bool {
		switch typed := node.(type) {
		case *ast.ForStmt:
			conditionIsTrue := typed.Cond == nil
			if ident, ok := typed.Cond.(*ast.Ident); ok &&
				ident.Name == "true" {
				conditionIsTrue = true
			}
			if conditionIsTrue {
				findings = append(
					findings,
					codeInfiniteLoopFinding(),
				)
			}
		case *ast.CallExpr:
			name := goCallName(typed.Fun, imports, callAliases)
			switch {
			case goDeleteCall(name):
				findings = append(findings, codeDeleteFinding())
			case goProcessCall(name):
				findings = append(findings, codeProcessFinding())
			case goNetworkCall(name):
				constants := goCallStringConstants(typed.Args)
				findings = append(
					findings,
					s.codeNetworkFindings(constants)...,
				)
			case goFileReadCall(name):
				constants := goCallStringConstants(typed.Args)
				if len(constants) == 0 {
					findings = append(findings, finding(
						ruleCodePolicy,
						DecisionAsk,
						RiskHigh,
						"Go code reads a path that cannot be statically verified",
						"Use a literal non-sensitive path or require human review for dynamic file access.",
					))
				} else if path := firstSensitiveConstant(
					constants,
					s.policy.ForbiddenPaths,
				); path != "" {
					findings = append(
						findings,
						codeSensitivePathFinding(path),
					)
				}
			case goRiskyUnresolvedCall(name):
				findings = append(findings, codeUnresolvedCapabilityFinding(
					"Go",
					name,
				))
			}
		}
		return true
	})
	return findings
}

func goHasRiskyDotImport(file *ast.File) bool {
	for _, spec := range file.Imports {
		if spec.Name == nil || spec.Name.Name != "." {
			continue
		}
		path, err := strconv.Unquote(spec.Path.Value)
		if err == nil && goRiskyPackage(path) {
			return true
		}
	}
	return false
}

func goImportAliases(file *ast.File) map[string]string {
	aliases := make(map[string]string)
	for _, spec := range file.Imports {
		path, err := strconv.Unquote(spec.Path.Value)
		if err != nil {
			continue
		}
		name := filepath.Base(path)
		if spec.Name != nil && spec.Name.Name != "." &&
			spec.Name.Name != "_" {
			name = spec.Name.Name
		}
		aliases[name] = path
	}
	return aliases
}

func goCallAliases(
	file *ast.File,
	imports map[string]string,
) map[string]string {
	aliases := make(map[string]string)
	ast.Inspect(file, func(node ast.Node) bool {
		switch typed := node.(type) {
		case *ast.AssignStmt:
			for i, left := range typed.Lhs {
				if i >= len(typed.Rhs) {
					continue
				}
				ident, ok := left.(*ast.Ident)
				if !ok {
					continue
				}
				if name := goCallName(
					typed.Rhs[i],
					imports,
					aliases,
				); name != "" {
					aliases[ident.Name] = name
				}
			}
		case *ast.ValueSpec:
			for i, ident := range typed.Names {
				if i >= len(typed.Values) {
					continue
				}
				if name := goCallName(
					typed.Values[i],
					imports,
					aliases,
				); name != "" {
					aliases[ident.Name] = name
				}
			}
		}
		return true
	})
	return aliases
}

func goCallName(
	expr ast.Expr,
	imports map[string]string,
	aliases map[string]string,
) string {
	switch typed := expr.(type) {
	case *ast.Ident:
		if alias := aliases[typed.Name]; alias != "" {
			return alias
		}
		return typed.Name
	case *ast.SelectorExpr:
		left := goCallName(typed.X, imports, aliases)
		if imported := imports[left]; imported != "" {
			left = imported
		}
		if left == "" {
			return typed.Sel.Name
		}
		return left + "." + typed.Sel.Name
	default:
		return ""
	}
}

func goDeleteCall(name string) bool {
	switch name {
	case "os.Remove", "os.RemoveAll":
		return true
	default:
		return false
	}
}

func goProcessCall(name string) bool {
	return strings.HasPrefix(name, "os/exec.")
}

func goNetworkCall(name string) bool {
	return strings.HasPrefix(name, "net.Dial") ||
		strings.HasPrefix(name, "net/http.Get") ||
		strings.HasPrefix(name, "net/http.Post") ||
		strings.HasPrefix(name, "net/http.NewRequest")
}

func goFileReadCall(name string) bool {
	switch name {
	case "os.Open", "os.OpenFile", "os.ReadFile":
		return true
	default:
		return false
	}
}

func goRiskyUnresolvedCall(name string) bool {
	for _, prefix := range []string{
		"net.", "net/http.", "net/url.", "os.", "os/exec.",
		"plugin.", "reflect.", "syscall.", "unsafe.",
	} {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

func goRiskyPackage(name string) bool {
	for _, prefix := range []string{
		"net", "os", "plugin", "reflect", "syscall", "unsafe",
	} {
		if name == prefix || strings.HasPrefix(name, prefix+"/") {
			return true
		}
	}
	return false
}

func goCallStringConstants(args []ast.Expr) []string {
	var constants []string
	for _, arg := range args {
		if value, ok := evalGoString(arg); ok {
			constants = append(constants, value)
		}
	}
	return constants
}

func evalGoString(expr ast.Expr) (string, bool) {
	switch typed := expr.(type) {
	case *ast.BasicLit:
		if typed.Kind != token.STRING {
			return "", false
		}
		value, err := strconv.Unquote(typed.Value)
		return value, err == nil
	case *ast.BinaryExpr:
		if typed.Op != token.ADD {
			return "", false
		}
		left, leftOK := evalGoString(typed.X)
		right, rightOK := evalGoString(typed.Y)
		return left + right, leftOK && rightOK
	default:
		return "", false
	}
}

const (
	maxFoldedConstants   = 512
	maxFoldedConstantLen = 4 << 10
)

func foldedStringConstants(code string) []string {
	var constants []string
	for offset := 0; offset < len(code); {
		if len(constants) >= maxFoldedConstants {
			return constants
		}
		value, next, ok := readCodeString(code, offset)
		if !ok {
			offset++
			continue
		}
		combined := value
		if len(value) <= maxFoldedConstantLen {
			constants = append(constants, value)
		}
		cursor := next
		for {
			cursor = skipCodeSpace(code, cursor)
			if cursor >= len(code) || code[cursor] != '+' {
				break
			}
			cursor = skipCodeSpace(code, cursor+1)
			nextValue, after, nextOK := readCodeString(
				code,
				cursor,
			)
			if !nextOK {
				break
			}
			if len(combined)+len(nextValue) > maxFoldedConstantLen ||
				len(constants) >= maxFoldedConstants {
				break
			}
			combined += nextValue
			constants = append(constants, combined)
			cursor = after
		}
		offset = next
	}
	return constants
}

func readCodeString(
	code string,
	offset int,
) (string, int, bool) {
	if offset >= len(code) ||
		(code[offset] != '\'' && code[offset] != '"') {
		return "", offset, false
	}
	quote := code[offset]
	var value strings.Builder
	for i := offset + 1; i < len(code); i++ {
		if code[i] == quote {
			return value.String(), i + 1, true
		}
		if code[i] == '\\' && i+1 < len(code) {
			i++
			value.WriteByte(code[i])
			continue
		}
		value.WriteByte(code[i])
	}
	return "", offset, false
}

func skipCodeSpace(code string, offset int) int {
	for offset < len(code) {
		switch code[offset] {
		case ' ', '\t', '\r', '\n':
			offset++
		default:
			return offset
		}
	}
	return offset
}

func readConcatenatedStringAt(
	code string,
	offset int,
) (string, bool) {
	offset = skipCodeSpace(code, offset)
	value, next, ok := readCodeString(code, offset)
	if !ok {
		return "", false
	}
	combined := value
	for {
		next = skipCodeSpace(code, next)
		if next >= len(code) || code[next] != '+' {
			return combined, true
		}
		next = skipCodeSpace(code, next+1)
		part, after, partOK := readCodeString(code, next)
		if !partOK {
			return "", false
		}
		combined += part
		next = after
	}
}

func firstSensitiveConstant(
	constants []string,
	configured []string,
) string {
	for _, constant := range constants {
		if path := matchSensitivePath(constant, configured); path != "" {
			return path
		}
	}
	return ""
}

func (s *Scanner) codeNetworkFindings(
	constants []string,
) []Finding {
	var hosts []string
	for _, constant := range constants {
		host := networkTargetHost(constant, true)
		if plausibleNetworkHost(host) {
			hosts = append(hosts, host)
		}
	}
	if len(hosts) == 0 {
		return []Finding{finding(
			ruleCodePolicy,
			DecisionAsk,
			RiskHigh,
			"code performs a network operation whose destination cannot be statically verified",
			"Use a literal allowlisted destination or require human review for dynamic network access.",
		)}
	}
	for _, host := range hosts {
		if !hostAllowed(host, s.policy.NetworkAllowlist) {
			return []Finding{finding(
				ruleNetwork,
				DecisionDeny,
				RiskHigh,
				"code network operation targets non-allowlisted host: "+host,
				"Add the domain to network_allowlist only after reviewing data exfiltration risk.",
			)}
		}
	}
	return nil
}

func codeDeleteFinding() Finding {
	return finding(
		ruleDangerousDelete,
		DecisionDeny,
		RiskCritical,
		"code invokes a filesystem deletion API",
		"Delete only explicit workspace-relative files after human review.",
	)
}

func codeProcessFinding() Finding {
	return finding(
		ruleCommandPolicy,
		DecisionDeny,
		RiskHigh,
		"code invokes a child process or shell execution API",
		"Use a directly supported, auditable operation instead of launching a nested command interpreter.",
	)
}

func codeInfiniteLoopFinding() Finding {
	return finding(
		ruleInfiniteLoop,
		DecisionDeny,
		RiskHigh,
		"code contains an obvious loop with a constant true condition",
		"Use a bounded loop with an explicit iteration limit, deadline, or cancellation condition.",
	)
}

func codeSensitivePathFinding(path string) Finding {
	return finding(
		ruleSensitivePath,
		DecisionDeny,
		RiskCritical,
		"code references forbidden path or credential marker: "+path,
		"Remove credential access and pass required values through approved secret handling.",
	)
}

func codeUnresolvedCapabilityFinding(language string, name string) Finding {
	return finding(
		ruleCodePolicy,
		DecisionAsk,
		RiskHigh,
		language+" code invokes a capability that cannot be classified safely: "+name,
		"Use a directly supported API with literal arguments or require human review.",
	)
}
