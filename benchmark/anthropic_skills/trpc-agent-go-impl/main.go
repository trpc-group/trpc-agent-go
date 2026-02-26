//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	localexec "trpc.group/trpc-go/trpc-agent-go/codeexecutor/local"
	skillrepo "trpc.group/trpc-go/trpc-agent-go/skill"
)

func main() {
	var (
		flagSkillsRoot = flag.String(
			"skills-root",
			defaultSkillsRoot,
			"Skills root (dir or URL archive)",
		)
		flagProgress = flag.Bool(
			"progress",
			true,
			"Print progress to stderr",
		)
		flagCacheDir = flag.String(
			"skills-cache-dir",
			"",
			"Cache dir for URL roots (default: ../skills_cache)",
		)
		flagWorkRoot = flag.String(
			"work-root",
			"",
			"Local workspace root (default: ../skill_workspaces)",
		)
		flagSuite = flag.String(
			"suite",
			suiteAll,
			"tool | agent | all | token-report | prompt-cache",
		)
		flagModel = flag.String(
			"model",
			defaultModel(),
			"Model name for agent suite",
		)
		flagTokenReportAllDocs = flag.Bool(
			"token-report-all-docs",
			true,
			"In token-report suite, preload all docs for all skills",
		)
		flagSkipDocCases = flag.Bool(
			"skip-doc-cases",
			false,
			"Skip per-skill doc cases in agent suite",
		)
		flagWithExec = flag.Bool(
			"with-exec",
			true,
			"Run extra exec cases (pip installs, scripts)",
		)
		flagOnlySkill = flag.String(
			"only-skill",
			"",
			"Run only this skill name (optional)",
		)
		flagDebug = flag.Bool(
			"debug",
			false,
			"Print debug info on failures",
		)
	)
	flag.Parse()

	progress := *flagProgress
	cacheDir := resolveDefaultUnderParent(*flagCacheDir, defaultCacheName)
	setDefaultEnv(skillrepo.EnvSkillsCacheDir, cacheDir)

	workRoot := resolveDefaultUnderParent(*flagWorkRoot, defaultWorkName)
	toolWorkRoot := filepath.Join(workRoot, suiteTool)
	agentWorkRoot := filepath.Join(workRoot, suiteAgent)
	tokenWorkRoot := filepath.Join(workRoot, suiteTokenReport)
	cacheWorkRoot := filepath.Join(workRoot, suitePromptCache)

	suite := strings.ToLower(strings.TrimSpace(*flagSuite))
	if suite != suiteTool &&
		suite != suiteAgent &&
		suite != suiteAll &&
		suite != suiteTokenReport &&
		suite != suitePromptCache {
		must(fmt.Errorf("unknown suite: %q", suite), "flags")
	}

	progressf(progress,
		"Starting Anthropic Skills Benchmark for trpc-agent-go")
	progressf(progress, "Skills root: %s", *flagSkillsRoot)
	progressf(progress, "Skills cache: %s", cacheDir)
	progressf(progress, "Work root: %s", workRoot)
	progressf(
		progress,
		"Suite: %s | Model: %s | With exec: %t",
		suite,
		*flagModel,
		*flagWithExec,
	)

	repo, err := skillrepo.NewFSRepository(*flagSkillsRoot)
	must(err, "skills repo")
	progressf(progress, "Loaded skills: %d", len(repo.Summaries()))

	if suite == suiteTool || suite == suiteAll {
		exec := localexec.New(localexec.WithWorkDir(toolWorkRoot))
		must(
			runToolSuite(
				repo,
				exec,
				*flagWithExec,
				*flagOnlySkill,
				progress,
				*flagDebug,
			),
			"tool suite",
		)
	}

	if suite == suiteTokenReport {
		must(checkAgentEnv(*flagModel), "agent env")
		exec := localexec.New(localexec.WithWorkDir(tokenWorkRoot))
		must(
			runTokenReportSuite(
				repo,
				exec,
				tokenWorkRoot,
				*flagModel,
				*flagTokenReportAllDocs,
				*flagDebug,
				progress,
			),
			"token report suite",
		)
		fmt.Println("PASS")
		return
	}

	if suite == suitePromptCache {
		must(checkAgentEnv(*flagModel), "agent env")
		exec := localexec.New(localexec.WithWorkDir(cacheWorkRoot))
		must(
			runPromptCacheSuite(
				repo,
				exec,
				*flagModel,
				*flagDebug,
				progress,
			),
			"prompt cache suite",
		)
		fmt.Println("PASS")
		return
	}

	if suite == suiteAgent || suite == suiteAll {
		must(checkAgentEnv(*flagModel), "agent env")
		exec := localexec.New(localexec.WithWorkDir(agentWorkRoot))
		must(
			runAgentSuite(
				repo,
				exec,
				agentWorkRoot,
				*flagModel,
				*flagSkipDocCases,
				*flagWithExec,
				*flagOnlySkill,
				*flagDebug,
				progress,
			),
			"agent suite",
		)
	}

	fmt.Println("PASS")
}

func defaultModel() string {
	if v := strings.TrimSpace(os.Getenv(envModelName)); v != "" {
		return v
	}
	return fallbackModel
}
