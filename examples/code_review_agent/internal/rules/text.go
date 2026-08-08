//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package rules

import (
	"go/ast"
	"go/token"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/findings"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/review"
)

var literalAssignmentPattern = regexp.MustCompile("(?i)([a-z][a-z0-9_.-]{1,63})\\s*(?::=|=|:)\\s*[\\\"'`]([^\\\"'`]{4,})[\\\"'`]")

func runTextRules(files []changedFile) []findings.Candidate {
	filtered := make([]changedFile, 0, len(files))
	for _, file := range files {
		if !file.isGo {
			filtered = append(filtered, file)
		}
	}
	return runLiteralSecretRules(filtered)
}

func runLiteralSecretRules(files []changedFile) []findings.Candidate {
	candidates := make([]findings.Candidate, 0)
	for _, file := range files {
		if file.deleted || file.isTest {
			continue
		}
		lines := make([]int, 0, len(file.added))
		for line := range file.added {
			lines = append(lines, line)
		}
		sort.Ints(lines)
		for _, line := range lines {
			matches := literalAssignmentPattern.FindAllStringSubmatch(file.added[line], -1)
			counts := make(map[string]int)
			for _, match := range matches {
				if len(match) != 3 || !sensitiveIdentifier(match[1]) || placeholderSecret(match[2]) {
					continue
				}
				name := normalizeAnchor(match[1])
				counts[name]++
				anchor := stableAnchor("lit", name, strconv.Itoa(counts[name]))
				candidates = append(candidates, newCandidate(
					RuleHardcodedSecret,
					review.SeverityHigh,
					review.ConfidenceHigh,
					"security",
					file,
					line,
					anchor,
					"Hard-coded credential in changed configuration",
					"An added line assigns a non-placeholder literal to a sensitive identifier; the value is omitted from evidence.",
					"Load the credential from an approved secret provider and rotate the exposed value.",
				))
			}
		}
	}
	return candidates
}

func sensitiveIdentifier(identifier string) bool {
	words := identifierWords(identifier)
	if len(words) == 0 {
		return false
	}
	joined := strings.Join(words, "_")
	for _, safe := range []string{
		"password_policy", "password_validator", "password_length",
		"token_count", "token_type", "token_endpoint", "token_limit",
		"secret_name", "secret_provider", "secret_reference",
	} {
		if strings.Contains(joined, safe) {
			return false
		}
	}
	for _, word := range words {
		switch word {
		case "password", "passwd", "pwd", "secret", "token", "apikey", "credential":
			return true
		}
	}
	for index := 0; index+1 < len(words); index++ {
		pair := words[index] + "_" + words[index+1]
		if pair == "api_key" || pair == "access_token" ||
			pair == "client_secret" || pair == "private_key" {
			return true
		}
	}
	return false
}

func identifierWords(value string) []string {
	var words []string
	var current []rune
	flush := func() {
		if len(current) != 0 {
			words = append(words, strings.ToLower(string(current)))
			current = current[:0]
		}
	}
	var previousLower bool
	for _, character := range value {
		if !unicode.IsLetter(character) && !unicode.IsDigit(character) {
			flush()
			previousLower = false
			continue
		}
		if unicode.IsUpper(character) && previousLower {
			flush()
		}
		current = append(current, character)
		previousLower = unicode.IsLower(character) || unicode.IsDigit(character)
	}
	flush()
	return words
}

func placeholderSecret(value string) bool {
	normalized := strings.ToLower(strings.TrimSpace(value))
	if normalized == "" || strings.HasPrefix(normalized, "${") || strings.HasPrefix(normalized, "{{") {
		return true
	}
	for _, placeholder := range []string{
		"changeme", "change-me", "placeholder", "replace-me", "redacted",
		"example", "dummy", "your-api-key", "your-token", "<secret>",
	} {
		if normalized == placeholder {
			return true
		}
	}
	return false
}

func missingTestCandidates(fileSet *token.FileSet, units []sourceUnit) []findings.Candidate {
	tests := make(map[testScope]bool)
	for index := range units {
		unit := &units[index]
		if !unit.isTest || unit.deleted || !hasChangedTestFunction(fileSet, unit) {
			continue
		}
		packageName := unit.parsed.Name.Name
		tests[testScope{
			layer:       unit.layer,
			dir:         path.Dir(unit.path),
			packageName: packageName,
		}] = true
	}

	candidates := make([]findings.Candidate, 0)
	for index := range units {
		unit := &units[index]
		if !productionGoFile(unit) {
			continue
		}
		packageName := unit.parsed.Name.Name
		directory := path.Dir(unit.path)
		hasTest := tests[testScope{layer: unit.layer, dir: directory, packageName: packageName}] ||
			tests[testScope{layer: unit.layer, dir: directory, packageName: packageName + "_test"}]
		if hasTest {
			continue
		}
		line := firstSemanticAddedLine(unit)
		if line == 0 {
			continue
		}
		candidates = append(candidates, newCandidate(
			RuleMissingTests,
			review.SeverityLow,
			review.ConfidenceMedium,
			"testing",
			unit.changedFile,
			line,
			"file:test-gap",
			"Production change has no related test change",
			"This production Go file has semantic additions, but the same layer and package have no changed Test, Benchmark, or Fuzz function.",
			"Add or update focused tests for the changed behavior, or document why existing coverage is sufficient.",
		))
	}
	return candidates
}

type testScope struct {
	layer       review.ChangeLayer
	dir         string
	packageName string
}

func hasChangedTestFunction(fileSet *token.FileSet, unit *sourceUnit) bool {
	for _, declaration := range unit.parsed.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || function.Body == nil || !validTestFunction(function, unit.imports) {
			continue
		}
		if functionHasSemanticAddedLine(fileSet, function, unit.semanticAdded) {
			return true
		}
	}
	return false
}

func validTestFunction(function *ast.FuncDecl, imports importResolver) bool {
	if function.Recv != nil || function.Type.Results != nil ||
		function.Type.Params == nil || len(function.Type.Params.List) != 1 {
		return false
	}
	parameter := function.Type.Params.List[0]
	if len(parameter.Names) != 1 {
		return false
	}
	pointer, ok := parameter.Type.(*ast.StarExpr)
	if !ok {
		return false
	}
	selector, ok := pointer.X.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	root, ok := selector.X.(*ast.Ident)
	if !ok || imports[root.Name] != "testing" {
		return false
	}
	tests := []struct {
		prefix   string
		typeName string
	}{
		{prefix: "Test", typeName: "T"},
		{prefix: "Benchmark", typeName: "B"},
		{prefix: "Fuzz", typeName: "F"},
	}
	for _, test := range tests {
		if strings.HasPrefix(function.Name.Name, test.prefix) &&
			len(function.Name.Name) > len(test.prefix) && selector.Sel.Name == test.typeName {
			return true
		}
	}
	return false
}

func functionHasSemanticAddedLine(
	fileSet *token.FileSet,
	function *ast.FuncDecl,
	semantic map[int]bool,
) bool {
	start := fileSet.Position(function.Pos()).Line
	end := fileSet.Position(function.End()).Line
	for line := start; line <= end; line++ {
		if semantic[line] {
			return true
		}
	}
	return false
}

func productionGoFile(unit *sourceUnit) bool {
	if !unit.isGo || unit.isTest || unit.deleted || len(unit.added) == 0 {
		return false
	}
	cleaned := path.Clean(unit.path)
	if strings.Contains("/"+cleaned+"/", "/vendor/") ||
		strings.Contains("/"+cleaned+"/", "/testdata/") ||
		strings.HasSuffix(cleaned, ".pb.go") ||
		strings.HasSuffix(cleaned, "_generated.go") {
		return false
	}
	prefix := string(unit.source)
	if len(prefix) > 512 {
		prefix = prefix[:512]
	}
	return !strings.Contains(prefix, "Code generated ")
}

func firstSemanticAddedLine(unit *sourceUnit) int {
	lines := make([]int, 0, len(unit.semanticAdded))
	for line := range unit.semanticAdded {
		lines = append(lines, line)
	}
	if len(lines) == 0 {
		return 0
	}
	sort.Ints(lines)
	return lines[0]
}
