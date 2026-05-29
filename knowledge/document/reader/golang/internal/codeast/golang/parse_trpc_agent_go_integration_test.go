//go:build integration

//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package golang

import (
	"context"
	"fmt"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"sync"
	"testing"
	"time"

	"golang.org/x/tools/go/packages"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/internal/codeast"
)

const (
	defaultIntegrationRepoURL  = "https://github.com/trpc-group/trpc-agent-go"
	defaultIntegrationRoutines = 300
)

type parseProfile struct {
	moduleDir string
	packages  int
	files     int
	nodes     int
	edges     int
	load      time.Duration
	extract   time.Duration
	analyze   time.Duration
	total     time.Duration
	fallback  bool
}

func TestParseTrpcAgentGoRepositoryIntegration(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git not found: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	repoURL := envString("TRPC_AGENT_GO_PARSE_REPO_URL", defaultIntegrationRepoURL)
	branch := os.Getenv("TRPC_AGENT_GO_PARSE_BRANCH")
	routines := envInt("TRPC_AGENT_GO_PARSE_ROUTINES", defaultIntegrationRoutines)

	tmpDir := t.TempDir()
	repoDir := filepath.Join(tmpDir, "trpc-agent-go")

	cloneStart := time.Now()
	if err := cloneRepository(ctx, repoURL, branch, repoDir); err != nil {
		t.Fatalf("clone repository: %v", err)
	}
	cloneElapsed := time.Since(cloneStart)

	scanStart := time.Now()
	goFiles, err := countGoFiles(repoDir)
	if err != nil {
		t.Fatalf("count go files: %v", err)
	}
	scanElapsed := time.Since(scanStart)

	parser := NewParser(WithConcurrency(routines))
	runtime.GC()
	var before runtime.MemStats
	runtime.ReadMemStats(&before)
	parseStart := time.Now()
	profiles, result, err := profileParseDirectory(parser, repoDir)
	parseElapsed := time.Since(parseStart)
	var after runtime.MemStats
	runtime.ReadMemStats(&after)
	if err != nil {
		t.Fatalf("parse repository: %v", err)
	}
	if result == nil || len(result.Nodes) == 0 {
		t.Fatalf("parse repository returned no nodes")
	}

	sort.Slice(profiles, func(i, j int) bool {
		return profiles[i].total > profiles[j].total
	})
	loadTotal, extractTotal, analyzeTotal, fallbackCount := aggregateProfiles(profiles)
	moduleRoutines, perModuleRoutines := parser.moduleConcurrency(len(profiles))

	t.Logf(
		"summary repo=%s branch=%q routines=%d module_routines=%d per_module_routines=%d clone=%s scan=%s parse=%s modules=%d go_files=%d nodes=%d edges=%d heap_alloc_delta=%s total_alloc_delta=%s",
		repoURL,
		branch,
		routines,
		moduleRoutines,
		perModuleRoutines,
		cloneElapsed.Truncate(time.Millisecond),
		scanElapsed.Truncate(time.Millisecond),
		parseElapsed.Truncate(time.Millisecond),
		len(profiles),
		goFiles,
		len(result.Nodes),
		len(result.Edges),
		formatBytes(int64(after.Alloc)-int64(before.Alloc)),
		formatBytes(int64(after.TotalAlloc)-int64(before.TotalAlloc)),
	)
	t.Logf(
		"stage_totals load=%s extract=%s analyze=%s fallback_modules=%d",
		loadTotal.Truncate(time.Millisecond),
		extractTotal.Truncate(time.Millisecond),
		analyzeTotal.Truncate(time.Millisecond),
		fallbackCount,
	)

	top := len(profiles)
	if top > 10 {
		top = 10
	}
	for i := 0; i < top; i++ {
		profile := profiles[i]
		t.Logf(
			"module[%02d] path=%s total=%s load=%s extract=%s analyze=%s packages=%d files=%d nodes=%d edges=%d fallback=%t",
			i+1,
			relPath(repoDir, profile.moduleDir),
			profile.total.Truncate(time.Millisecond),
			profile.load.Truncate(time.Millisecond),
			profile.extract.Truncate(time.Millisecond),
			profile.analyze.Truncate(time.Millisecond),
			profile.packages,
			profile.files,
			profile.nodes,
			profile.edges,
			profile.fallback,
		)
	}
}

func cloneRepository(ctx context.Context, repoURL, branch, repoDir string) error {
	args := []string{"clone", "--depth", "1"}
	if branch != "" {
		args = append(args, "--branch", branch)
	}
	args = append(args, repoURL, repoDir)
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: %s", err, string(output))
	}
	return nil
}

func profileParseDirectory(p *Parser, dirPath string) ([]parseProfile, *codeast.Result, error) {
	absDir, err := filepath.Abs(dirPath)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get absolute path: %w", err)
	}
	modules, err := findGoModules(absDir)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to find go modules: %w", err)
	}
	if len(modules) == 0 {
		modules = []string{absDir}
	}

	moduleConcurrency, perModuleConcurrency := p.moduleConcurrency(len(modules))
	profiles, results, err := profileParseModules(p, modules, moduleConcurrency, perModuleConcurrency)
	if err != nil {
		return nil, nil, err
	}
	allNodes, allEdges, _ := mergeModuleResults(results)

	return profiles, &codeast.Result{
		File: &codeast.FileInfo{
			Name:     absDir,
			Language: codeast.LanguageGo,
			Package:  modulePathForDir(absDir, absDir),
		},
		Nodes: allNodes,
		Edges: allEdges,
	}, nil
}

func profileParseModules(
	p *Parser,
	modules []string,
	moduleConcurrency int,
	perModuleConcurrency int,
) ([]parseProfile, []*codeast.Result, error) {
	if moduleConcurrency <= 1 {
		profiles := make([]parseProfile, len(modules))
		results := make([]*codeast.Result, len(modules))
		moduleParser := p.withConcurrency(perModuleConcurrency)
		for i, moduleDir := range modules {
			profile, result, err := profileParseDirectoryModule(moduleParser, moduleDir)
			if err != nil {
				return nil, nil, err
			}
			profiles[i] = profile
			results[i] = result
		}
		return profiles, results, nil
	}

	type job struct {
		index int
		path  string
	}
	jobs := make(chan job)
	errCh := make(chan error, 1)
	profiles := make([]parseProfile, len(modules))
	results := make([]*codeast.Result, len(modules))
	var wg sync.WaitGroup
	for i := 0; i < moduleConcurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			moduleParser := p.withConcurrency(perModuleConcurrency)
			for item := range jobs {
				profile, result, err := profileParseDirectoryModule(moduleParser, item.path)
				if err != nil {
					select {
					case errCh <- err:
					default:
					}
					return
				}
				profiles[item.index] = profile
				results[item.index] = result
			}
		}()
	}
	for i, moduleDir := range modules {
		select {
		case err := <-errCh:
			close(jobs)
			wg.Wait()
			return nil, nil, err
		case jobs <- job{index: i, path: moduleDir}:
		}
	}
	close(jobs)
	wg.Wait()
	select {
	case err := <-errCh:
		return nil, nil, err
	default:
		return profiles, results, nil
	}
}

func profileParseDirectoryModule(p *Parser, moduleDir string) (parseProfile, *codeast.Result, error) {
	profile, result, err := profileParseDirectoryFull(p, moduleDir)
	if err == nil {
		return profile, result, nil
	}

	start := time.Now()
	result, directErr := p.parseDirectoryDirectAST(moduleDir)
	elapsed := time.Since(start)
	if directErr != nil {
		return parseProfile{}, nil, err
	}
	profile = parseProfile{
		moduleDir: moduleDir,
		files:     len(result.Nodes),
		nodes:     len(result.Nodes),
		edges:     len(result.Edges),
		total:     elapsed,
		fallback:  true,
	}
	return profile, result, nil
}

func profileParseDirectoryFull(p *Parser, moduleDir string) (parseProfile, *codeast.Result, error) {
	profile := parseProfile{moduleDir: moduleDir}
	totalStart := time.Now()
	fset := token.NewFileSet()
	cfg := &packages.Config{
		Context: context.Background(),
		Mode: packages.NeedName |
			packages.NeedFiles |
			packages.NeedCompiledGoFiles |
			packages.NeedSyntax |
			packages.NeedImports |
			packages.NeedTypes |
			packages.NeedTypesInfo |
			packages.NeedDeps,
		Dir:  moduleDir,
		Fset: fset,
	}

	loadStart := time.Now()
	pkgs, err := packages.Load(cfg, "./...")
	profile.load = time.Since(loadStart)
	if err != nil {
		return profile, nil, fmt.Errorf("failed to load packages from %s: %w", moduleDir, err)
	}
	if len(pkgs) == 0 {
		return profile, nil, fmt.Errorf("no packages loaded from %s", moduleDir)
	}

	extractStart := time.Now()
	var allNodes []*codeast.Node
	parsedPkgs := make([]*parsedPackage, 0, len(pkgs))
	for _, pkg := range pkgs {
		if pkg == nil || len(pkg.Syntax) == 0 {
			continue
		}
		parsed := parsedPackageFromPackages(pkg)
		nodes, err := p.extractor.Extract(&extractInput{pkg: parsed, fset: parsed.Fset})
		if err != nil {
			return profile, nil, err
		}
		profile.files += len(parsed.Syntax)
		allNodes = append(allNodes, nodes...)
		parsedPkgs = append(parsedPkgs, parsed)
	}
	profile.extract = time.Since(extractStart)
	if len(allNodes) == 0 {
		return profile, nil, fmt.Errorf("no nodes extracted from loaded packages in %s", moduleDir)
	}

	nodeSet := make(map[string]bool, len(allNodes))
	for _, node := range allNodes {
		if node == nil || node.ID == "" {
			continue
		}
		nodeSet[node.ID] = true
	}

	analyzeStart := time.Now()
	allEdges, err := p.analyzePackages(parsedPkgs, nodeSet)
	profile.analyze = time.Since(analyzeStart)
	if err != nil {
		return profile, nil, err
	}

	profile.packages = len(parsedPkgs)
	profile.nodes = len(allNodes)
	profile.edges = len(allEdges)
	profile.total = time.Since(totalStart)
	return profile, &codeast.Result{
		File: &codeast.FileInfo{
			Name:     moduleDir,
			Language: codeast.LanguageGo,
			Package:  modulePathForDir(moduleDir, moduleDir),
			Imports:  packageImports(parsedPkgs),
		},
		Nodes: allNodes,
		Edges: allEdges,
	}, nil
}

func countGoFiles(root string) (int, error) {
	var count int
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "vendor":
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(path) == ".go" {
			count++
		}
		return nil
	})
	return count, err
}

func envString(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func envInt(name string, fallback int) int {
	value := os.Getenv(name)
	if value == "" {
		return fallback
	}
	n, err := strconv.Atoi(value)
	if err != nil || n <= 0 {
		return fallback
	}
	return n
}

func relPath(root, path string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == "." {
		return "."
	}
	return filepath.ToSlash(rel)
}

func formatBytes(n int64) string {
	sign := ""
	if n < 0 {
		sign = "-"
		n = -n
	}
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%s%dB", sign, n)
	}
	div, exp := int64(unit), 0
	for value := n / unit; value >= unit; value /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%s%.1f%ciB", sign, float64(n)/float64(div), "KMGTPE"[exp])
}

func aggregateProfiles(profiles []parseProfile) (load, extract, analyze time.Duration, fallbackCount int) {
	for _, profile := range profiles {
		load += profile.load
		extract += profile.extract
		analyze += profile.analyze
		if profile.fallback {
			fallbackCount++
		}
	}
	return load, extract, analyze, fallbackCount
}
