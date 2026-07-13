// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.

package reviewinput

import (
	"fmt"
	"go/parser"
	"go/token"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"golang.org/x/mod/modfile"
)

// discoverGoPackages derives only the package facts needed for navigation and
// suggested sandbox commands. It parses package clauses and go.mod files
// directly instead of invoking `go list` against untrusted input on the host.
func discoverGoPackages(snapshot string, parsed parsedInput) (packages []GoPackage, err error) {
	seen := make(map[string]GoPackage)
	for _, file := range parsed.ChangedFiles {
		if !file.IsGo {
			continue
		}
		dir := path.Dir(file.Path)
		if snapshot == "" {
			seen[dir] = GoPackage{Directory: dir, Complete: false}
			continue
		}
		pkg, err := inspectGoPackage(snapshot, file)
		if err != nil {
			return nil, err
		}
		key := pkg.ModuleRoot + "\x00" + pkg.Directory
		seen[key] = pkg
	}
	packages = make([]GoPackage, 0, len(seen))
	for _, pkg := range seen {
		packages = append(packages, pkg)
	}
	sort.Slice(packages, func(i, j int) bool {
		if packages[i].ModuleRoot != packages[j].ModuleRoot {
			return packages[i].ModuleRoot < packages[j].ModuleRoot
		}
		return packages[i].Directory < packages[j].Directory
	})
	return packages, nil
}

// inspectGoPackage derives module-relative test and import paths from the
// post-change snapshot. Deleted Go files retain module context but skip package
// parsing because their source no longer exists in that tree.
func inspectGoPackage(snapshot string, changed ChangedFile) (pkg GoPackage, err error) {
	dir := path.Dir(changed.Path)
	absDir := filepath.Join(snapshot, filepath.FromSlash(dir))
	moduleRoot, modulePath, err := findModule(snapshot, absDir)
	if err != nil {
		return GoPackage{}, err
	}
	pkg = GoPackage{Directory: dir, ModulePath: modulePath, Complete: true}
	if moduleRoot != "" {
		relModule, err := filepath.Rel(snapshot, moduleRoot)
		if err != nil {
			return GoPackage{}, err
		}
		if relModule == "." {
			relModule = ""
		}
		pkg.ModuleRoot = filepath.ToSlash(relModule)
		relPkg, err := filepath.Rel(moduleRoot, absDir)
		if err != nil {
			return GoPackage{}, err
		}
		if relPkg == "." {
			relPkg = ""
		}
		pkg.ImportPath = strings.TrimSuffix(modulePath, "/")
		if relPkg != "" {
			pkg.ImportPath += "/" + filepath.ToSlash(relPkg)
		}
		pkg.SuggestedTestArg = "./" + filepath.ToSlash(relPkg)
		if relPkg == "" {
			pkg.SuggestedTestArg = "."
		}
	}

	if changed.Status != "deleted" {
		filePath := filepath.Join(snapshot, filepath.FromSlash(changed.Path))
		file, err := parser.ParseFile(token.NewFileSet(), filePath, nil, parser.PackageClauseOnly)
		if err != nil {
			return GoPackage{}, fmt.Errorf("parse package clause in %s: %w", changed.Path, err)
		}
		pkg.PackageName = file.Name.Name
	}
	return pkg, nil
}

// findModule walks upward only within the task-owned snapshot, which supports
// nested Go modules without allowing a changed path to inspect host parents.
func findModule(snapshot, start string) (root, modulePath string, err error) {
	snapshot, err = filepath.Abs(snapshot)
	if err != nil {
		return "", "", err
	}
	current, err := filepath.Abs(start)
	if err != nil {
		return "", "", err
	}
	for {
		if current != snapshot && !strings.HasPrefix(current, snapshot+string(filepath.Separator)) {
			return "", "", nil
		}
		data, readErr := os.ReadFile(filepath.Join(current, "go.mod"))
		if readErr == nil {
			return current, modfile.ModulePath(data), nil
		}
		if !os.IsNotExist(readErr) {
			return "", "", fmt.Errorf("read go.mod in %s: %w", current, readErr)
		}
		if current == snapshot {
			return "", "", nil
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", "", nil
		}
		current = parent
	}
}
