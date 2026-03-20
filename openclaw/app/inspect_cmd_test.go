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
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	ocdeps "trpc.group/trpc-go/trpc-agent-go/openclaw/internal/deps"
	ocskills "trpc.group/trpc-go/trpc-agent-go/openclaw/internal/skills"
	_ "trpc.group/trpc-go/trpc-agent-go/openclaw/plugins/telegram"
)

func TestRunInspect_DefaultIsPlugins(t *testing.T) {
	stdout, stderr := captureInspectOutput(t, func() {
		require.Equal(t, 0, runInspect(nil))
	})

	require.Empty(t, stderr)
	require.Contains(t, stdout, "Model types")
	require.Contains(t, stdout, "mock")
	require.Contains(t, stdout, "telegram")
}

func TestRunInspect_UnknownCommand(t *testing.T) {
	_, stderr := captureInspectOutput(t, func() {
		require.Equal(t, 2, runInspect([]string{"nope"}))
	})

	require.Contains(t, stderr, "unknown inspect command")
	require.Contains(t, stderr, "Usage:")
}

func TestRunInspect_ConfigKeys(t *testing.T) {
	cfgData, err := yaml.Marshal(map[string]any{
		"channels": []any{
			map[string]any{
				"type": telegramChannelType,
				"config": map[string]any{
					"token": "x",
				},
			},
		},
	})
	require.NoError(t, err)
	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(cfgPath, cfgData, 0o600))

	stdout, stderr := captureInspectOutput(t, func() {
		require.Equal(t, 0, runInspect([]string{
			inspectCmdConfigKeys,
			"-config",
			cfgPath,
			"-enable-openclaw-tools",
		}))
	})

	require.Empty(t, stderr)

	got := strings.Split(strings.TrimSpace(stdout), "\n")
	want := []string{
		"channels.telegram",
		"channels.telegram.token",
		"plugins.entries.telegram.config",
		"plugins.entries.telegram.config.token",
		"plugins.entries.telegram.enabled",
		"tools.cron",
		"tools.exec_command",
		"tools.kill_session",
		"tools.message",
		"tools.write_stdin",
	}
	require.Equal(t, want, got)
}

func TestRunInspect_Deps_WithSkill(t *testing.T) {
	root := t.TempDir()
	writeDepsSkill(t, root, "depskill", `---
name: depskill
description: deps skill
metadata:
  {
    "openclaw":
      {
        "requires":
          {
            "bins": ["definitely_missing_bin"],
            "python":
              [
                {
                  "module": "definitely_missing_python_module",
                  "package": "definitely-missing-python-package",
                },
              ],
          },
      },
  }
---

# depskill
`)

	stdout, stderr := captureInspectOutput(t, func() {
		require.Equal(t, 0, runInspect([]string{
			inspectCmdDeps,
			"-state-dir",
			t.TempDir(),
			"-skills-root",
			root,
			"-skill",
			"depskill",
		}))
	})

	require.Empty(t, stderr)
	require.Contains(t, stdout, "depskill")
	require.Contains(t, stdout, "definitely_missing_bin")
	require.Contains(t, stdout, "definitely_missing_python_module")
}

func TestRunBootstrapDeps_DryRun(t *testing.T) {
	root := t.TempDir()
	writeDepsSkill(t, root, "depskill", `---
name: depskill
description: deps skill
metadata:
  {
    "openclaw":
      {
        "requires":
          {
            "python":
              [
                {
                  "module": "definitely_missing_python_module",
                  "package": "definitely-missing-python-package",
                },
              ],
          },
      },
  }
---

# depskill
`)

	stdout, stderr := captureInspectOutput(t, func() {
		require.Equal(t, 0, runBootstrap([]string{
			bootstrapCmdDeps,
			"-state-dir",
			t.TempDir(),
			"-skills-root",
			root,
			"-skill",
			"depskill",
		}))
	})

	require.Empty(t, stderr)
	require.Contains(t, stdout, "Install Python packages")
	require.Contains(t, stdout, "definitely-missing-python-package")
}

func TestRunBootstrapDeps_WithSkillDoesNotAddDefaultProfiles(
	t *testing.T,
) {
	root := t.TempDir()
	writeDepsSkill(t, root, "skillonly", `---
name: skillonly
description: skill-only deps
metadata:
  {
    "openclaw":
      {
        "requires":
          {
            "bins": ["definitely_missing_bin"],
          },
      },
  }
---

# skillonly
`)

	stdout, stderr := captureInspectOutput(t, func() {
		require.Equal(t, 0, runBootstrap([]string{
			bootstrapCmdDeps,
			"-state-dir",
			t.TempDir(),
			"-skills-root",
			root,
			"-skill",
			"skillonly",
		}))
	})

	require.Empty(t, stderr)
	require.Contains(t, stdout, "Selected: skillonly")
	require.Contains(t, stdout, "Plan: nothing to install")
	require.Contains(t, stdout, "Unresolved:")
	require.Contains(t, stdout, "definitely_missing_bin")
	require.NotContains(
		t,
		stdout,
		"definitely-missing-python-package",
	)
}

func TestRunBootstrapDeps_ApplyPrintsUnresolved(t *testing.T) {
	root := t.TempDir()
	writeDepsSkill(t, root, "skillonly", `---
name: skillonly
description: skill-only deps
metadata:
  {
    "openclaw":
      {
        "requires":
          {
            "bins": ["definitely_missing_bin"],
          },
      },
  }
---

# skillonly
`)

	stdout, stderr := captureInspectOutput(t, func() {
		require.Equal(t, 0, runBootstrap([]string{
			bootstrapCmdDeps,
			"-state-dir",
			t.TempDir(),
			"-skills-root",
			root,
			"-skill",
			"skillonly",
			"-apply",
		}))
	})

	require.Empty(t, stderr)
	require.Contains(t, stdout, "Unresolved:")
	require.Contains(t, stdout, "definitely_missing_bin")
}

func TestBuildPlanForSources_OfficialSkillMetadataBuilds(
	t *testing.T,
) {
	root := filepath.Join("..", "skills")
	repo, err := ocskills.NewRepository([]string{root})
	require.NoError(t, err)

	sources, err := repo.DependencySources(nil)
	require.NoError(t, err)
	require.NotEmpty(t, sources)

	_, err = ocdeps.BuildPlanForSources(
		t.TempDir(),
		nil,
		sources,
	)
	require.NoError(t, err)
}

func TestRunBootstrap_HelpAndUnknownCommand(t *testing.T) {
	stdout, stderr := captureInspectOutput(t, func() {
		require.Equal(t, 2, runBootstrap(nil))
		require.Equal(t, 2, runBootstrap([]string{"unknown"}))
	})

	require.Empty(t, stdout)
	require.Contains(t, stderr, "Usage:")
	require.Contains(t, stderr, "unknown bootstrap command")
}

func TestRunInspect_Deps_JSONOutput(t *testing.T) {
	root := t.TempDir()
	writeDepsSkill(t, root, "depskill", `---
name: depskill
description: deps skill
metadata:
  {
    "openclaw":
      {
        "requires":
          {
            "python":
              [
                {
                  "module": "definitely_missing_python_module",
                  "package": "definitely-missing-python-package",
                },
              ],
          },
      },
  }
---

# depskill
`)

	stdout, stderr := captureInspectOutput(t, func() {
		require.Equal(t, 0, runInspect([]string{
			inspectCmdDeps,
			"-json",
			"-state-dir",
			t.TempDir(),
			"-skills-root",
			root,
			"-skill",
			"depskill",
		}))
	})

	require.Empty(t, stderr)

	var report ocdeps.Report
	require.NoError(t, json.Unmarshal([]byte(stdout), &report))
	require.True(t, ocdeps.HasMissing(report))
	require.NotEmpty(t, report.Sources)
}

func TestRunBootstrapDeps_JSONOutput(t *testing.T) {
	root := t.TempDir()
	writeDepsSkill(t, root, "depskill", `---
name: depskill
description: deps skill
metadata:
  {
    "openclaw":
      {
        "requires":
          {
            "python":
              [
                {
                  "module": "definitely_missing_python_module",
                  "package": "definitely-missing-python-package",
                },
              ],
          },
      },
  }
---

# depskill
`)

	stdout, stderr := captureInspectOutput(t, func() {
		require.Equal(t, 0, runBootstrap([]string{
			bootstrapCmdDeps,
			"-json",
			"-state-dir",
			t.TempDir(),
			"-skills-root",
			root,
			"-skill",
			"depskill",
		}))
	})

	require.Empty(t, stderr)

	var plan ocdeps.Plan
	require.NoError(t, json.Unmarshal([]byte(stdout), &plan))
	require.NotEmpty(t, plan.Steps)
	require.Contains(t, plan.Steps[0].CommandLine, "-m venv")
}

func TestDepsCommandHelpers(t *testing.T) {
	opts, code, err := parseDepsCommandOptions(
		subcmdInspect,
		inspectCmdDeps,
		nil,
		false,
	)
	require.NoError(t, err)
	require.Equal(t, 0, code)
	require.Equal(
		t,
		strings.Join(ocdeps.DefaultProfiles(), ","),
		opts.Profiles,
	)

	_, code, err = parseDepsCommandOptions(
		subcmdInspect,
		inspectCmdDeps,
		[]string{"extra"},
		false,
	)
	require.Error(t, err)
	require.Equal(t, 2, code)

	lines := toolDepsStartupLines(&ocdeps.Report{
		Missing: ocdeps.Missing{
			Bins: []string{"pdftotext"},
		},
	})
	require.Len(t, lines, 2)
	require.Contains(t, lines[0].text, "pdftotext")
	require.Contains(t, lines[1].text, "bootstrap deps")
	require.Nil(t, toolDepsStartupLines(&ocdeps.Report{}))
	require.Nil(t, toolDepsStartupLines(nil))

	stdout, stderr := captureInspectOutput(t, func() {
		printApplyResult(ocdeps.ApplyResult{
			Steps: []ocdeps.StepResult{
				{
					Step:     ocdeps.Step{Label: "install"},
					Status:   "applied",
					ExitCode: 0,
				},
				{
					Step:   ocdeps.Step{Label: "defer"},
					Status: "deferred",
					Error:  "needs sudo",
				},
				{
					Step:     ocdeps.Step{Label: "fail"},
					Status:   "failed",
					ExitCode: 3,
					Error:    "boom",
				},
			},
		})
		printToolchain(ocdeps.Toolchain{})
		require.Equal(t, 0, printJSON(map[string]string{"ok": "yes"}))
		require.Equal(
			t,
			1,
			printJSON(map[string]any{"bad": make(chan int)}),
		)
	})

	require.Contains(t, stdout, "Applied:")
	require.Contains(t, stdout, "install (exit=0)")
	require.Contains(t, stdout, "Deferred:")
	require.Contains(t, stdout, "defer")
	require.Contains(t, stdout, "needs sudo")
	require.Contains(t, stdout, "Failed:")
	require.Contains(t, stdout, "fail (exit=3)")
	require.Contains(t, stdout, "boom")
	require.Contains(t, stdout, "Python: not found")
	require.Contains(t, stdout, "\"ok\": \"yes\"")
	require.Contains(t, stderr, "unsupported type")

	require.Equal(t, "missing", statusText(false, ""))
	require.Equal(t, "found: /bin/tool", statusText(true, "/bin/tool"))
	require.Equal(
		t,
		"found: a, b",
		anyBinStatusText(ocdeps.AnyBinStatus{
			Satisfied: true,
			Found: []ocdeps.BinStatus{
				{Name: "a"},
				{Name: "b"},
			},
		}),
	)
	require.Equal(
		t,
		"bins=a; anyBins=b|c; python=mod",
		formatMissing(ocdeps.Missing{
			Bins: []string{"a"},
			AnyBins: [][]string{
				{"b", "c"},
			},
			Python: []ocdeps.PythonPackage{
				{Module: "mod"},
			},
		}),
	)
}

func captureInspectOutput(
	t *testing.T,
	fn func(),
) (stdout string, stderr string) {
	t.Helper()

	oldStdout := os.Stdout
	oldStderr := os.Stderr

	outR, outW, err := os.Pipe()
	require.NoError(t, err)
	errR, errW, err := os.Pipe()
	require.NoError(t, err)

	os.Stdout = outW
	os.Stderr = errW
	t.Cleanup(func() {
		os.Stdout = oldStdout
		os.Stderr = oldStderr
	})

	fn()

	require.NoError(t, outW.Close())
	require.NoError(t, errW.Close())

	out, err := io.ReadAll(outR)
	require.NoError(t, err)
	errOut, err := io.ReadAll(errR)
	require.NoError(t, err)

	return string(out), string(errOut)
}

func writeDepsSkill(
	t *testing.T,
	root string,
	name string,
	body string,
) {
	t.Helper()

	dir := filepath.Join(root, name)
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, "SKILL.md"),
		[]byte(body),
		0o644,
	))
}

func TestResolveSkillConfigKeys_IncludesPluginAndYAMLKeys(t *testing.T) {
	myChanConfig := mustYAMLNode(t, `
enabled: true
token: x
list:
  - 0
  - 1
emptymap: {}
emptyseq: []
"": true
`)
	providerConfig := mustYAMLNode(t, "x: 1\n")
	toolSetConfig := mustYAMLNode(t, "- true\n")

	opts := runOptions{
		EnableOpenClawTools: true,
		EnableLocalExec:     true,
		Channels: []pluginSpec{
			{Type: "  MyChan ", Config: myChanConfig},
			{Type: "  ", Config: myChanConfig},
		},
		ToolProviders: []pluginSpec{
			{Type: "Provider", Config: providerConfig},
		},
		ToolSets: []pluginSpec{
			{Type: "ToolSet", Config: toolSetConfig},
		},
	}

	keys := resolveSkillConfigKeys(opts)

	require.Contains(t, keys, "channels.mychan")
	require.Contains(t, keys, "channels.mychan.enabled")
	require.Contains(t, keys, "channels.mychan.token")
	require.Contains(t, keys, "channels.mychan.list")
	require.NotContains(t, keys, "channels.mychan.emptymap")
	require.NotContains(t, keys, "channels.mychan.emptyseq")

	require.Contains(t, keys, "plugins.entries.mychan.enabled")
	require.Contains(t, keys, "plugins.entries.mychan.config")
	require.Contains(t, keys, "plugins.entries.mychan.config.enabled")
	require.Contains(t, keys, "plugins.entries.mychan.config.token")
	require.Contains(t, keys, "plugins.entries.mychan.config.list")

	require.Contains(t, keys, "tools.providers.provider")
	require.Contains(t, keys, "tools.providers.provider.x")
	require.Contains(t, keys, "plugins.entries.provider.enabled")
	require.Contains(t, keys, "plugins.entries.provider.config")

	require.Contains(t, keys, "tools.toolsets.toolset")
	require.Contains(t, keys, "plugins.entries.toolset.enabled")

	require.Contains(t, keys, "tools.exec_command")
	require.Contains(t, keys, "tools.write_stdin")
	require.Contains(t, keys, "tools.kill_session")
	require.Contains(t, keys, "tools.message")
	require.Contains(t, keys, "tools.cron")
	require.Contains(t, keys, "tools.local_exec")
}

func TestAddYAMLConfigKeys_RejectsFalseyMapping(t *testing.T) {
	set := map[string]struct{}{}
	node := mustYAMLNode(t, `
a: false
b: 0
c: 0.0
`)
	require.False(t, addYAMLConfigKeys(set, "root", node))
	require.Empty(t, set)
}

func TestAddYAMLConfigKeys_CoversEdgeCases(t *testing.T) {
	set := map[string]struct{}{}
	require.False(t, addYAMLConfigKeys(set, "", &yaml.Node{}))
	require.False(t, addYAMLConfigKeys(set, "root", nil))
	require.False(t,
		addYAMLConfigKeys(set, "root", &yaml.Node{Kind: yaml.DocumentNode}),
	)
	require.False(t,
		addYAMLConfigKeys(set, "root", &yaml.Node{Kind: 123}),
	)
}

func TestAddYAMLConfigKeys_AliasUsesTargetNode(t *testing.T) {
	set := map[string]struct{}{}
	aliasTarget := &yaml.Node{
		Kind:  yaml.ScalarNode,
		Tag:   "!!bool",
		Value: "true",
	}
	aliasNode := &yaml.Node{
		Kind:  yaml.AliasNode,
		Alias: aliasTarget,
	}

	require.True(t, addYAMLConfigKeys(set, "root", aliasNode))
	_, ok := set["root"]
	require.True(t, ok)
}

func TestAddConfigKey_IgnoresNilAndBlank(t *testing.T) {
	addConfigKey(nil, "x")

	set := map[string]struct{}{}
	addConfigKey(set, "  ")
	require.Empty(t, set)

	addConfigKey(set, "  a ")
	_, ok := set["a"]
	require.True(t, ok)
}

func TestIsTruthyScalar_RejectsNilAndInvalidValues(t *testing.T) {
	require.False(t, isTruthyScalar(nil))
	require.False(t, isTruthyScalar(&yaml.Node{
		Kind:  yaml.ScalarNode,
		Tag:   "!!bool",
		Value: "   ",
	}))
	require.False(t, isTruthyScalar(&yaml.Node{
		Kind:  yaml.ScalarNode,
		Tag:   "!!int",
		Value: "nope",
	}))
	require.False(t, isTruthyScalar(&yaml.Node{
		Kind:  yaml.ScalarNode,
		Tag:   "!!float",
		Value: "nope",
	}))
}

func mustYAMLNode(t *testing.T, raw string) *yaml.Node {
	t.Helper()

	var node yaml.Node
	require.NoError(t, yaml.Unmarshal([]byte(raw), &node))
	return &node
}
