//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

type sandboxSourceTrust struct {
	MappingAllowed bool
	ModuleImports  map[string]string
}

var sandboxImportPathPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._~+/-]*$`)

// inspectSandboxSourceTrust scans the physical source files that the
// repository checks may analyze. Any inability to complete the scan keeps
// diagnostic mapping disabled for the caller.
func inspectSandboxSourceTrust(
	snapshotRoot string,
	diagnosticModules map[string]string,
) sandboxSourceTrust {
	trust := sandboxSourceTrust{
		ModuleImports: make(map[string]string),
	}
	root, ok := trustedSandboxSnapshotRoot(snapshotRoot)
	if !ok {
		return trust
	}

	moduleDirs, ok := sandboxDiagnosticModuleDirs(root, diagnosticModules)
	if !ok {
		return trust
	}
	if len(diagnosticModules) == 0 {
		if !collectSandboxModuleImports(root, root, trust.ModuleImports) {
			return trust
		}
	} else {
		for _, moduleDir := range moduleDirs {
			moduleImport, ok := readSandboxModuleImportPath(
				filepath.Join(root, filepath.FromSlash(moduleDir)),
			)
			if !ok {
				return trust
			}
			trust.ModuleImports[moduleDir] = moduleImport
		}
	}

	seenFiles := make(map[string]bool)
	for _, moduleDir := range moduleDirs {
		moduleRoot := root
		if moduleDir != "." {
			moduleRoot = filepath.Join(root, filepath.FromSlash(moduleDir))
		}
		complete, mappingAllowed := scanSandboxGoFiles(moduleRoot, seenFiles)
		if !complete {
			return trust
		}
		if !mappingAllowed {
			return trust
		}
	}
	trust.MappingAllowed = true
	return trust
}

func trustedSandboxSnapshotRoot(snapshotRoot string) (string, bool) {
	if strings.TrimSpace(snapshotRoot) == "" {
		return "", false
	}
	absolute, err := filepath.Abs(snapshotRoot)
	if err != nil {
		return "", false
	}
	if hasSymlink, inspectErr := pathHasSymlinkComponent(absolute); inspectErr != nil || hasSymlink {
		return "", false
	}
	info, err := os.Lstat(absolute)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", false
	}
	return absolute, true
}

func sandboxDiagnosticModuleDirs(
	snapshotRoot string,
	diagnosticModules map[string]string,
) ([]string, bool) {
	if len(diagnosticModules) == 0 {
		return []string{"."}, true
	}
	seen := make(map[string]bool, len(diagnosticModules))
	modules := make([]string, 0, len(diagnosticModules))
	for _, module := range diagnosticModules {
		if !isSafeSandboxModulePath(module) {
			return nil, false
		}
		module = path.Clean(module)
		moduleRoot := snapshotRoot
		if module != "." {
			moduleRoot = filepath.Join(snapshotRoot, filepath.FromSlash(module))
		}
		if !pathStaysWithin(snapshotRoot, moduleRoot) {
			return nil, false
		}
		if hasSymlink, inspectErr := pathHasSymlinkComponent(moduleRoot); inspectErr != nil || hasSymlink {
			return nil, false
		}
		info, err := os.Lstat(moduleRoot)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return nil, false
		}
		if seen[module] {
			continue
		}
		seen[module] = true
		modules = append(modules, module)
	}
	if len(modules) == 0 {
		return nil, false
	}
	// The module map is generated from sorted manifest records, but sort here
	// as well so direct parser callers get deterministic traversal.
	sort.Strings(modules)
	return modules, true
}

func collectSandboxModuleImports(
	snapshotRoot string,
	currentRoot string,
	imports map[string]string,
) bool {
	if imports == nil {
		return false
	}
	err := filepath.WalkDir(snapshotRoot, func(current string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() && entry.Name() == "vendor" {
			return filepath.SkipDir
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("snapshot path %q is a symlink", filepath.ToSlash(current))
		}
		if entry.IsDir() || entry.Name() != "go.mod" {
			return nil
		}
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() {
			return fmt.Errorf("module file %q is not regular", filepath.ToSlash(current))
		}
		relative, err := filepath.Rel(currentRoot, filepath.Dir(current))
		if err != nil {
			return err
		}
		moduleDir := path.Clean(filepath.ToSlash(relative))
		if moduleDir == "" {
			moduleDir = "."
		}
		moduleImport, ok := readSandboxModuleImportPath(filepath.Dir(current))
		if !ok {
			return fmt.Errorf("read module path from %q", filepath.ToSlash(current))
		}
		imports[moduleDir] = moduleImport
		return nil
	})
	return err == nil
}

func readSandboxModuleImportPath(moduleRoot string) (string, bool) {
	moduleFile := filepath.Join(moduleRoot, "go.mod")
	info, err := os.Lstat(moduleFile)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return "", false
	}
	data, err := os.ReadFile(moduleFile)
	if err != nil {
		return "", false
	}
	stripped, commentsOK := stripSandboxModuleComments(data)
	if !commentsOK {
		return "", false
	}
	moduleImport := ""
	for _, rawLine := range strings.Split(stripped, "\n") {
		line := strings.TrimSpace(strings.TrimSuffix(rawLine, "\r"))
		if line == "" {
			continue
		}
		if line != "module" && !strings.HasPrefix(line, "module ") &&
			!strings.HasPrefix(line, "module\t") {
			continue
		}
		rest := strings.TrimSpace(strings.TrimPrefix(line, "module"))
		if index := strings.Index(rest, "//"); index >= 0 {
			rest = strings.TrimSpace(rest[:index])
		}
		if !isSandboxImportPath(rest) || moduleImport != "" {
			return "", false
		}
		moduleImport = rest
	}
	return moduleImport, moduleImport != ""
}

// stripSandboxModuleComments removes Go-mod line and block comments while
// preserving newlines. Keeping the line structure lets the small module-path
// parser accept valid comments without mistaking a commented-out "module"
// directive for metadata.
func stripSandboxModuleComments(data []byte) (string, bool) {
	var stripped strings.Builder
	stripped.Grow(len(data))
	inLineComment := false
	inBlockComment := false
	for index := 0; index < len(data); index++ {
		current := data[index]
		if inLineComment {
			if current == '\n' {
				inLineComment = false
				stripped.WriteByte(current)
			} else {
				stripped.WriteByte(' ')
			}
			continue
		}
		if inBlockComment {
			if current == '*' && index+1 < len(data) && data[index+1] == '/' {
				stripped.WriteString("  ")
				index++
				inBlockComment = false
			} else if current == '\n' {
				stripped.WriteByte(current)
			} else {
				stripped.WriteByte(' ')
			}
			continue
		}
		if current == '/' && index+1 < len(data) && data[index+1] == '/' {
			stripped.WriteString("  ")
			index++
			inLineComment = true
			continue
		}
		if current == '/' && index+1 < len(data) && data[index+1] == '*' {
			stripped.WriteString("  ")
			index++
			inBlockComment = true
			continue
		}
		stripped.WriteByte(current)
	}
	return stripped.String(), !inBlockComment
}

func scanSandboxGoFiles(root string, seenFiles map[string]bool) (bool, bool) {
	if seenFiles == nil {
		seenFiles = make(map[string]bool)
	}
	err := filepath.WalkDir(root, func(current string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() && entry.Name() == "vendor" {
			return filepath.SkipDir
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("snapshot path %q is a symlink", filepath.ToSlash(current))
		}
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".go" {
			return nil
		}
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() {
			return fmt.Errorf("Go source %q is not regular", filepath.ToSlash(current))
		}
		absolute, err := filepath.Abs(current)
		if err != nil {
			return err
		}
		if seenFiles[absolute] {
			return nil
		}
		seenFiles[absolute] = true
		file, err := parser.ParseFile(
			token.NewFileSet(),
			current,
			nil,
			parser.ParseComments,
		)
		if err != nil || file == nil {
			return fmt.Errorf("parse Go source %q", filepath.ToSlash(current))
		}
		if sandboxFileHasLineDirective(file) {
			return errSandboxLineDirective
		}
		return nil
	})
	if err == errSandboxLineDirective {
		return true, false
	}
	return err == nil, true
}

var errSandboxLineDirective = errors.New("Go source contains a line directive")

func sandboxFileHasLineDirective(file *ast.File) bool {
	// This deliberately rejects legitimate generated sources that use line
	// directives: without a physical-source proof, mapping is not trustworthy.
	if file == nil {
		return false
	}
	for _, group := range file.Comments {
		for _, comment := range group.List {
			text := strings.TrimLeft(comment.Text, " \t\v\f")
			if sandboxLineDirectiveText(text) {
				return true
			}
		}
	}
	return false
}

func sandboxLineDirectiveText(text string) bool {
	text = strings.TrimLeft(text, " \t\v\f")
	if strings.HasPrefix(text, "//line") {
		return len(text) == len("//line") || isSandboxDirectiveBoundary(text[len("//line"):])
	}
	if strings.HasPrefix(text, "/*line") {
		return len(text) == len("/*line") || isSandboxDirectiveBoundary(text[len("/*line"):])
	}
	return false
}

func isSandboxDirectiveBoundary(value string) bool {
	if value == "" {
		return true
	}
	first := value[0]
	return first == ' ' || first == '\t' || first == ':' || first == '/' || first == '*'
}

func isSandboxImportPath(value string) bool {
	if value == "" || !sandboxImportPathPattern.MatchString(value) ||
		path.IsAbs(value) || strings.Contains(value, "//") {
		return false
	}
	clean := path.Clean(value)
	if clean != value {
		return false
	}
	for _, component := range strings.Split(value, "/") {
		if component == "" || component == "." || component == ".." {
			return false
		}
	}
	return true
}

func sandboxPackageHeaderAllowed(
	line string,
	activeModule string,
	moduleAuthenticationRequired bool,
	moduleImports map[string]string,
) bool {
	packagePath, testPath, ok := parseSandboxPackageHeader(line)
	if !ok || !isSandboxImportPath(packagePath) {
		return false
	}
	packageBase := strings.TrimSuffix(packagePath, "_test")
	if moduleAuthenticationRequired {
		moduleImport, ok := moduleImports[path.Clean(activeModule)]
		if !ok || !sandboxPackageWithinModule(moduleImport, packagePath, packageBase) {
			return false
		}
		if testPath != "" {
			return sandboxImportPathWithin(moduleImport, strings.TrimSuffix(testPath, ".test"))
		}
		return true
	}
	for _, moduleImport := range moduleImports {
		if sandboxPackageWithinModule(moduleImport, packagePath, packageBase) &&
			(testPath == "" || sandboxImportPathWithin(moduleImport, strings.TrimSuffix(testPath, ".test"))) {
			return true
		}
	}
	return false
}

func sandboxPackageWithinModule(moduleImport string, packagePath string, packageBase string) bool {
	return sandboxImportPathWithin(moduleImport, packagePath) ||
		sandboxImportPathWithin(moduleImport, packageBase)
}

// parseSandboxPackageHeader accepts the exact package banner shapes emitted by
// the Go tools: "# <import/path>", the bracket-only context form
// "# [<import/path>]", and an optional "[<import/path>.test]" suffix. The
// suffix also permits external test packages, whose displayed package path
// ends in _test while the test binary path does not.
func parseSandboxPackageHeader(line string) (string, string, bool) {
	trimmed := strings.TrimSpace(strings.TrimSuffix(line, "\r"))
	if !strings.HasPrefix(trimmed, "# ") {
		return "", "", false
	}
	rest := strings.TrimPrefix(trimmed, "# ")
	if rest == "" || strings.TrimSpace(rest) != rest || strings.ContainsAny(rest, "\t\r\n") {
		return "", "", false
	}
	// The go command may print a second, bracketed package context line
	// after the ordinary header (for example, "# [example.com/p]").
	// Treat it as a header only when the bracket contents are themselves a
	// trusted import path; arbitrary bracketed text remains unaccounted.
	if strings.HasPrefix(rest, "[") && strings.HasSuffix(rest, "]") {
		bracketPath := rest[1 : len(rest)-1]
		if !isSandboxImportPath(bracketPath) {
			return "", "", false
		}
		if strings.HasSuffix(bracketPath, ".test") {
			packagePath := strings.TrimSuffix(bracketPath, ".test")
			if !isSandboxImportPath(packagePath) {
				return "", "", false
			}
			return packagePath, bracketPath, true
		}
		return bracketPath, "", true
	}
	packagePath := rest
	testPath := ""
	if open := strings.Index(rest, " ["); open >= 0 {
		if open == 0 || !strings.HasSuffix(rest, "]") || strings.Contains(rest[open+2:len(rest)-1], "[") {
			return "", "", false
		}
		packagePath = rest[:open]
		testPath = rest[open+2 : len(rest)-1]
		if !strings.HasSuffix(testPath, ".test") || !isSandboxImportPath(testPath) {
			return "", "", false
		}
		testBase := strings.TrimSuffix(testPath, ".test")
		if testBase != packagePath && testBase != strings.TrimSuffix(packagePath, "_test") {
			return "", "", false
		}
	}
	if !isSandboxImportPath(packagePath) {
		return "", "", false
	}
	return packagePath, testPath, true
}

func sandboxImportPathWithin(moduleImport string, packagePath string) bool {
	return packagePath == moduleImport || strings.HasPrefix(packagePath, moduleImport+"/")
}
