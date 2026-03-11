//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package app

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"

	ocdeps "trpc.group/trpc-go/trpc-agent-go/openclaw/internal/deps"
	ocskills "trpc.group/trpc-go/trpc-agent-go/openclaw/internal/skills"
)

const subcmdBootstrap = "bootstrap"

const (
	bootstrapCmdDeps = "deps"
)

type depsCommandOptions struct {
	StateDir           string
	Profiles           string
	Skills             string
	JSON               bool
	Apply              bool
	SkillsRoot         string
	SkillsExtraDirs    string
	SkillsAllowBundled string
}

func runBootstrap(args []string) int {
	if len(args) == 0 {
		printBootstrapUsage()
		return 2
	}

	cmd := strings.ToLower(strings.TrimSpace(args[0]))
	switch cmd {
	case bootstrapCmdDeps:
		return runBootstrapDeps(args[1:])
	case "", "help", "-h", "--help":
		printBootstrapUsage()
		return 2
	default:
		fmt.Fprintf(os.Stderr, "unknown bootstrap command: %s\n", cmd)
		printBootstrapUsage()
		return 2
	}
}

func runInspectDeps(args []string) int {
	opts, code, err := parseDepsCommandOptions(
		subcmdInspect,
		inspectCmdDeps,
		args,
		false,
	)
	if err != nil {
		if err != flag.ErrHelp {
			fmt.Fprintln(os.Stderr, err)
		}
		return code
	}

	stateDir, sources, names, err := resolveDepsSources(opts)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	report, err := ocdeps.Inspect(stateDir, sources)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	if opts.JSON {
		return printJSON(report)
	}
	printDepsReport(report, names)
	return 0
}

func runBootstrapDeps(args []string) int {
	opts, code, err := parseDepsCommandOptions(
		subcmdBootstrap,
		bootstrapCmdDeps,
		args,
		true,
	)
	if err != nil {
		if err != flag.ErrHelp {
			fmt.Fprintln(os.Stderr, err)
		}
		return code
	}

	stateDir, sources, names, err := resolveDepsSources(opts)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	plan, err := ocdeps.BuildPlanForSources(stateDir, names, sources)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	if opts.JSON {
		if !opts.Apply {
			return printJSON(plan)
		}
		result, err := ocdeps.ApplyPlan(context.Background(), plan)
		if err != nil {
			_ = printJSON(result)
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		return printJSON(result)
	}

	printDepsPlan(plan)
	if !opts.Apply {
		return 0
	}

	result, err := ocdeps.ApplyPlan(context.Background(), plan)
	printApplyResult(result)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}

func parseDepsCommandOptions(
	top string,
	cmd string,
	args []string,
	allowApply bool,
) (depsCommandOptions, int, error) {
	fs := flag.NewFlagSet(top+" "+cmd, flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	var opts depsCommandOptions
	fs.StringVar(
		&opts.StateDir,
		flagStateDir,
		"",
		"State dir for managed toolchain",
	)
	fs.StringVar(
		&opts.Profiles,
		"profile",
		"",
		"Dependency profiles (comma-separated)",
	)
	fs.StringVar(
		&opts.Skills,
		"skill",
		"",
		"Skill names (comma-separated)",
	)
	fs.BoolVar(
		&opts.JSON,
		"json",
		false,
		"Print machine-readable JSON output",
	)
	fs.StringVar(
		&opts.SkillsRoot,
		"skills-root",
		"",
		"Skills root directory override",
	)
	fs.StringVar(
		&opts.SkillsExtraDirs,
		"skills-extra-dirs",
		"",
		"Extra skills roots (comma-separated)",
	)
	fs.StringVar(
		&opts.SkillsAllowBundled,
		flagSkillsAllowBundled,
		"",
		"Comma-separated allowlist of bundled skills",
	)
	if allowApply {
		fs.BoolVar(
			&opts.Apply,
			"apply",
			false,
			"Execute planned install commands",
		)
	}

	if err := fs.Parse(args); err != nil {
		return depsCommandOptions{}, 2, err
	}
	if len(fs.Args()) > 0 {
		return depsCommandOptions{}, 2, unexpectedArgsError(fs.Args())
	}
	if strings.TrimSpace(opts.Profiles) == "" &&
		strings.TrimSpace(opts.Skills) == "" {
		opts.Profiles = strings.Join(ocdeps.DefaultProfiles(), ",")
	}
	return opts, 0, nil
}

func resolveDepsSources(
	opts depsCommandOptions,
) (string, []ocdeps.Source, []string, error) {
	stateDir, err := resolveStateDir(opts.StateDir)
	if err != nil {
		return "", nil, nil, err
	}

	profileNames := splitCSV(opts.Profiles)
	skillNames := splitCSV(opts.Skills)

	profileSources, err := ocdeps.SourcesForProfiles(profileNames)
	if err != nil {
		return "", nil, nil, err
	}
	skillSources, err := resolveSkillDependencySources(stateDir, opts)
	if err != nil {
		return "", nil, nil, err
	}

	sources := append(profileSources, skillSources...)
	names := make([]string, 0, len(profileNames)+len(skillNames))
	names = append(names, profileNames...)
	names = append(names, skillNames...)
	return stateDir, ocdeps.MergeSources(sources...), names, nil
}

func resolveSkillDependencySources(
	stateDir string,
	opts depsCommandOptions,
) ([]ocdeps.Source, error) {
	names := splitCSV(opts.Skills)
	if len(names) == 0 {
		return nil, nil
	}

	cwd, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	cfg := agentConfig{
		SkillsRoot:         strings.TrimSpace(opts.SkillsRoot),
		SkillsExtraDirs:    splitCSV(opts.SkillsExtraDirs),
		SkillsAllowBundled: splitCSV(opts.SkillsAllowBundled),
		StateDir:           stateDir,
	}
	repo, err := ocskills.NewRepository(
		resolveSkillRoots(cwd, cfg),
		ocskills.WithBundledSkillsRoot(
			filepath.Join(cwd, appName, defaultSkillsDir),
		),
		ocskills.WithAllowBundled(cfg.SkillsAllowBundled),
	)
	if err != nil {
		return nil, err
	}
	return repo.DependencySources(names)
}

func toolDepsStartupLines(report *ocdeps.Report) []startupLogLine {
	if report == nil || !ocdeps.HasMissing(*report) {
		return nil
	}
	missing := formatMissing(report.Missing)
	if missing == "" {
		return nil
	}
	command := "openclaw bootstrap deps --profile " +
		strings.Join(ocdeps.DefaultProfiles(), ",") +
		" --apply"
	return []startupLogLine{
		{
			warn: true,
			text: "OpenClaw toolchain is missing optional dependencies: " +
				missing,
		},
		{
			text: "Suggested command: " + command,
		},
	}
}

func printDepsReport(
	report ocdeps.Report,
	names []string,
) {
	fmt.Fprintf(
		os.Stdout,
		"Platform: %s/%s\n",
		report.Platform.GOOS,
		report.Platform.GOARCH,
	)
	if report.Platform.PackageManager != "" {
		fmt.Fprintf(
			os.Stdout,
			"Package manager: %s\n",
			report.Platform.PackageManager,
		)
	}
	printToolchain(report.Toolchain)
	if len(names) > 0 {
		fmt.Fprintf(
			os.Stdout,
			"Selected: %s\n",
			strings.Join(names, ", "),
		)
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "\nSOURCE\tTYPE\tNAME\tSTATUS")
	for _, source := range report.Sources {
		for _, bin := range source.Bins {
			fmt.Fprintf(
				w,
				"%s\tbin\t%s\t%s\n",
				source.Name,
				bin.Name,
				statusText(bin.Found, bin.Path),
			)
		}
		for _, any := range source.AnyBins {
			fmt.Fprintf(
				w,
				"%s\tany-bin\t%s\t%s\n",
				source.Name,
				strings.Join(any.Names, " | "),
				anyBinStatusText(any),
			)
		}
		for _, pkg := range source.Python {
			fmt.Fprintf(
				w,
				"%s\tpython\t%s\t%s\n",
				source.Name,
				pkg.Module,
				statusText(pkg.Found, pkg.Package),
			)
		}
	}
	_ = w.Flush()

	if ocdeps.HasMissing(report) {
		fmt.Fprintf(os.Stdout, "\nMissing: %s\n", formatMissing(report.Missing))
	}
}

func printDepsPlan(plan ocdeps.Plan) {
	fmt.Fprintf(
		os.Stdout,
		"Platform: %s/%s\n",
		plan.Platform.GOOS,
		plan.Platform.GOARCH,
	)
	if plan.Platform.PackageManager != "" {
		fmt.Fprintf(
			os.Stdout,
			"Package manager: %s\n",
			plan.Platform.PackageManager,
		)
	}
	printToolchain(plan.Toolchain)
	if len(plan.Profiles) > 0 {
		fmt.Fprintf(
			os.Stdout,
			"Selected: %s\n",
			strings.Join(plan.Profiles, ", "),
		)
	}
	if len(plan.Steps) == 0 {
		fmt.Fprintln(os.Stdout, "Plan: nothing to install")
	} else {
		fmt.Fprintln(os.Stdout, "Plan:")
		for _, step := range plan.Steps {
			fmt.Fprintf(
				os.Stdout,
				"- %s\n  %s\n",
				step.Label,
				step.CommandLine,
			)
		}
	}
	if len(plan.Unresolved.Bins) > 0 ||
		len(plan.Unresolved.AnyBins) > 0 {
		fmt.Fprintf(
			os.Stdout,
			"Unresolved: %s\n",
			formatMissing(plan.Unresolved),
		)
	}
}

func printApplyResult(result ocdeps.ApplyResult) {
	if len(result.Steps) == 0 {
		return
	}
	fmt.Fprintln(os.Stdout, "Applied:")
	for _, step := range result.Steps {
		fmt.Fprintf(
			os.Stdout,
			"- %s (exit=%d)\n",
			step.Step.Label,
			step.ExitCode,
		)
	}
}

func printToolchain(toolchain ocdeps.Toolchain) {
	if toolchain.Python.Found {
		mode := "system"
		if toolchain.Python.Managed {
			mode = "managed"
		}
		fmt.Fprintf(
			os.Stdout,
			"Python: %s (%s)\n",
			toolchain.Python.Path,
			mode,
		)
		return
	}
	fmt.Fprintln(os.Stdout, "Python: not found")
}

func statusText(found bool, detail string) string {
	if found {
		if strings.TrimSpace(detail) == "" {
			return "found"
		}
		return "found: " + detail
	}
	if strings.TrimSpace(detail) == "" {
		return "missing"
	}
	return "missing: " + detail
}

func anyBinStatusText(status ocdeps.AnyBinStatus) string {
	if status.Satisfied && len(status.Found) > 0 {
		names := make([]string, 0, len(status.Found))
		for _, found := range status.Found {
			names = append(names, found.Name)
		}
		return "found: " + strings.Join(names, ", ")
	}
	return "missing"
}

func formatMissing(missing ocdeps.Missing) string {
	parts := make([]string, 0, 3)
	if len(missing.Bins) > 0 {
		parts = append(
			parts,
			"bins="+strings.Join(missing.Bins, ","),
		)
	}
	if len(missing.AnyBins) > 0 {
		groups := make([]string, 0, len(missing.AnyBins))
		for _, group := range missing.AnyBins {
			groups = append(groups, strings.Join(group, "|"))
		}
		parts = append(parts, "anyBins="+strings.Join(groups, ","))
	}
	if len(missing.Python) > 0 {
		modules := make([]string, 0, len(missing.Python))
		for _, pkg := range missing.Python {
			modules = append(modules, pkg.Module)
		}
		parts = append(parts, "python="+strings.Join(modules, ","))
	}
	return strings.Join(parts, "; ")
}

func printJSON(v any) int {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}

func printBootstrapUsage() {
	fmt.Fprintln(os.Stderr, "Usage:")
	fmt.Fprintln(
		os.Stderr,
		"  openclaw bootstrap deps [flags]",
	)
}
