//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package skills

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/openclaw/internal/deps"
)

func TestParseFrontMatter_OpenClawMetadata(t *testing.T) {
	content := `---
name: coding-agent
description: "Test skill"
metadata:
  {
    "openclaw": { "requires": { "anyBins": ["codex"] } },
  }
---

# Body
`
	fm, err := parseFrontMatter(content)
	require.NoError(t, err)
	require.Equal(t, "coding-agent", fm.Name)
	require.Equal(t, "Test skill", fm.Description)

	meta, ok, err := parseOpenClawMetadata(fm)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, []string{"codex"}, meta.Requires.AnyBins)
}

func TestParseFrontMatter_OpenClawMetadata_MetadataAsString(t *testing.T) {
	content := `---
name: coding-agent
description: "Test skill"
metadata: '{"openclaw":{"requires":{"anyBins":["codex"]}}}'
---

# Body
`
	fm, err := parseFrontMatter(content)
	require.NoError(t, err)
	require.Equal(t, "coding-agent", fm.Name)

	meta, ok, err := parseOpenClawMetadata(fm)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, []string{"codex"}, meta.Requires.AnyBins)
}

func TestParseFrontMatter_OpenClawMetadata_InstallEntries(t *testing.T) {
	content := `---
name: coding-agent
description: "Test skill"
metadata:
  {
    "openclaw":
      {
        "requires":
          {
            "python":
              [
                {
                  "module": "pypdf",
                  "package": "pypdf",
                },
              ],
          },
        "install":
          [
            {
              "id": "brew",
              "kind": "brew",
              "formula": "poppler",
              "bins": ["pdftotext"],
            },
          ],
      },
  }
---

# Body
`
	fm, err := parseFrontMatter(content)
	require.NoError(t, err)

	meta, ok, err := parseOpenClawMetadata(fm)
	require.NoError(t, err)
	require.True(t, ok)
	require.Len(t, meta.Requires.Python, 1)
	require.Equal(t, "pypdf", meta.Requires.Python[0].Module)
	require.Len(t, meta.Install, 1)
	require.Equal(t, "brew", meta.Install[0].Kind)
	require.Equal(t, "poppler", meta.Install[0].Formula)
}

func TestParseFrontMatter_NoFrontMatter(t *testing.T) {
	_, err := parseFrontMatter("hello\n")
	require.True(t, errors.Is(err, errNoFrontMatter))
}

func TestParseFrontMatterFile_ReadError(t *testing.T) {
	root := t.TempDir()
	_, err := parseFrontMatterFile(filepath.Join(root, "missing.md"))
	require.Error(t, err)
}

func TestNormalizeStringAnyMap_MapAnyAny(t *testing.T) {
	in := map[any]any{
		"openclaw": map[any]any{
			"always": true,
		},
		1: "ignore",
	}
	out := normalizeStringAnyMap(in)
	require.Contains(t, out, "openclaw")
}

func TestParseOpenClawMetadata_NoMetadata(t *testing.T) {
	meta, ok, err := parseOpenClawMetadata(parsedFrontMatter{
		Name:     "x",
		Metadata: nil,
	})
	require.NoError(t, err)
	require.False(t, ok)
	require.Equal(t, openClawMetadata{}, meta)
}

func TestParseOpenClawMetadata_NoOpenClawKey(t *testing.T) {
	meta, ok, err := parseOpenClawMetadata(parsedFrontMatter{
		Name: "x",
		Metadata: map[string]any{
			"other": map[string]any{"a": 1},
		},
	})
	require.NoError(t, err)
	require.False(t, ok)
	require.Equal(t, openClawMetadata{}, meta)
}

func TestParseOpenClawMetadata_MarshalError(t *testing.T) {
	meta, ok, err := parseOpenClawMetadata(parsedFrontMatter{
		Name: "x",
		Metadata: map[string]any{
			openClawMetadataKey: marshalTextErr{},
		},
	})
	require.Error(t, err)
	require.False(t, ok)
	require.Equal(t, openClawMetadata{}, meta)
}

func TestParseOpenClawMetadata_UnmarshalError(t *testing.T) {
	meta, ok, err := parseOpenClawMetadata(parsedFrontMatter{
		Name: "x",
		Metadata: map[string]any{
			openClawMetadataKey: []string{"not a map"},
		},
	})
	require.Error(t, err)
	require.False(t, ok)
	require.Equal(t, openClawMetadata{}, meta)
}

func TestAsString_NonString(t *testing.T) {
	require.Empty(t, asString(123))
}

func TestRepository_GatesOnBins(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("exec lookpath + chmod differs on windows")
	}

	root := t.TempDir()
	writeSkill(t, root, "needsbin", `---
name: needsbin
description: test
metadata:
  {
    "openclaw": { "requires": { "bins": ["not-a-real-bin"] } },
  }
---

hello
`)

	r, err := NewRepository([]string{root})
	require.NoError(t, err)

	require.Empty(t, r.Summaries())
	_, err = r.Get("needsbin")
	require.Error(t, err)
	require.Contains(t, err.Error(), "disabled")
}

func TestRepository_AllowBundled_BlocksBundledSkill(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, root, "bundled", `---
name: bundled
description: test
---

x
`)

	r, err := NewRepository(
		[]string{root},
		WithBundledSkillsRoot(root),
		WithAllowBundled([]string{"other"}),
	)
	require.NoError(t, err)
	require.Empty(t, r.Summaries())

	_, err = r.Get("bundled")
	require.Error(t, err)
	require.Contains(t, err.Error(), "allow_bundled")
}

func TestRepository_AllowBundled_AllowsBySkillKey(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, root, "bundled", `---
name: bundled
description: test
metadata:
  { "openclaw": { "skillKey": "key1" } }
---

x
`)

	r, err := NewRepository(
		[]string{root},
		WithBundledSkillsRoot(root),
		WithAllowBundled([]string{"key1"}),
	)
	require.NoError(t, err)

	sums := r.Summaries()
	require.Len(t, sums, 1)
	require.Equal(t, "bundled", sums[0].Name)
}

func TestRepository_AllowBundled_DoesNotAffectExternalSkills(t *testing.T) {
	bundledRoot := t.TempDir()
	otherRoot := t.TempDir()

	writeSkill(t, bundledRoot, "bundled", `---
name: bundled
description: test
---

x
`)
	writeSkill(t, otherRoot, "external", `---
name: external
description: test
---

x
`)

	r, err := NewRepository(
		[]string{bundledRoot, otherRoot},
		WithBundledSkillsRoot(bundledRoot),
		WithAllowBundled([]string{"external"}),
	)
	require.NoError(t, err)

	sums := r.Summaries()
	require.Len(t, sums, 1)
	require.Equal(t, "external", sums[0].Name)
}

func TestEvaluateRequiredAnyBins(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("exec lookpath + chmod differs on windows")
	}

	dir := t.TempDir()
	bin := filepath.Join(dir, "mybin")
	require.NoError(t, os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755))
	t.Setenv("PATH", dir)

	require.Empty(t, evaluateRequiredAnyBins([]string{"mybin", "missing"}))
	require.NotEmpty(t, evaluateRequiredAnyBins([]string{"missing1"}))
}

func TestEvaluateRequiredEnv(t *testing.T) {
	t.Setenv("SKILLS_TEST_OK", "1")

	require.Empty(t, evaluateRequiredEnv(
		[]string{"SKILLS_TEST_OK"},
		"",
		SkillConfig{},
	))
	require.NotEmpty(t, evaluateRequiredEnv(
		[]string{"SKILLS_TEST_MISSING"},
		"",
		SkillConfig{},
	))
}

func TestEvaluateRequiredEnv_EmptyHostEnvIsMissing(t *testing.T) {
	t.Setenv("SKILLS_TEST_EMPTY", "")

	require.NotEmpty(t, evaluateRequiredEnv(
		[]string{"SKILLS_TEST_EMPTY"},
		"",
		SkillConfig{},
	))
}

func TestEvaluateRequiredEnv_BlockedKeyNotSatisfiedByConfig(t *testing.T) {
	os.Unsetenv(envLDPreload)

	reason := evaluateRequiredEnv(
		[]string{envLDPreload},
		"",
		SkillConfig{
			Env: map[string]string{
				envLDPreload: "x",
			},
		},
	)
	require.Contains(t, reason, envLDPreload)
}

func TestEvaluateRequiredEnv_SatisfiedByConfigEnv(t *testing.T) {
	os.Unsetenv("SKILLS_TEST_CFG_ENV")

	require.Empty(t, evaluateRequiredEnv(
		[]string{"SKILLS_TEST_CFG_ENV"},
		"",
		SkillConfig{
			Env: map[string]string{
				"SKILLS_TEST_CFG_ENV": "1",
			},
		},
	))
}

func TestEvaluateRequiredEnv_SatisfiedByAPIKeyForPrimaryEnv(t *testing.T) {
	os.Unsetenv("SKILLS_TEST_PRIMARY_ENV")

	require.Empty(t, evaluateRequiredEnv(
		[]string{"SKILLS_TEST_PRIMARY_ENV"},
		"SKILLS_TEST_PRIMARY_ENV",
		SkillConfig{
			APIKey: "k",
		},
	))
}

func TestNormalizeMetadata_InvalidString(t *testing.T) {
	require.Nil(t, normalizeMetadata("openclaw: ["))
}

func TestRepository_SkillKey_ConfigResolution(t *testing.T) {
	os.Unsetenv("SKILLS_TEST_SKILLKEY_ENV")

	root := t.TempDir()
	writeSkill(t, root, "needkey", `---
name: needkey
description: test
metadata:
  {
    "openclaw": {
      "skillKey": "key1",
      "primaryEnv": "SKILLS_TEST_SKILLKEY_ENV",
      "requires": { "env": ["SKILLS_TEST_SKILLKEY_ENV"] }
    }
  }
---

x
`)

	r, err := NewRepository(
		[]string{root},
		WithSkillConfigs(map[string]SkillConfig{
			"key1": {APIKey: "k"},
		}),
	)
	require.NoError(t, err)

	require.Len(t, r.Summaries(), 1)

	env, err := r.SkillRunEnv(context.Background(), "needkey")
	require.NoError(t, err)
	require.Equal(t, "k", env["SKILLS_TEST_SKILLKEY_ENV"])
}

func TestRepository_SkillConfigs_NormalizeKeyAndEnvKey(t *testing.T) {
	os.Unsetenv("SKILLS_TEST_TRIM_ENV")

	root := t.TempDir()
	writeSkill(t, root, "trim", `---
name: trim
description: test
metadata:
  { "openclaw": { "skillKey": "key1",
    "requires": { "env": ["SKILLS_TEST_TRIM_ENV"] } } }
---

x
`)

	r, err := NewRepository(
		[]string{root},
		WithSkillConfigs(map[string]SkillConfig{
			" key1 ": {
				Env: map[string]string{
					" SKILLS_TEST_TRIM_ENV ": " v ",
				},
			},
		}),
	)
	require.NoError(t, err)
	require.Len(t, r.Summaries(), 1)

	env, err := r.SkillRunEnv(context.Background(), "trim")
	require.NoError(t, err)
	require.Equal(t, "v", env["SKILLS_TEST_TRIM_ENV"])
}

func TestEvaluateOpenClawRequirements_Always(t *testing.T) {
	ok, reason := evaluateOpenClawRequirements(
		openClawMetadata{
			Always: true,
			Requires: openClawRequires{
				Bins: []string{"definitely-missing"},
			},
		},
		nil,
		SkillConfig{},
	)
	require.True(t, ok)
	require.Empty(t, reason)
}

func TestRepository_GetDisabledHasReason(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, root, "needenv", `---
name: needenv
description: test
metadata:
  { "openclaw": { "requires": { "env": ["SKILLS_TEST_NEEDENV"] } } }
---

x
`)

	r, err := NewRepository([]string{root})
	require.NoError(t, err)
	require.Empty(t, r.Summaries())

	_, err = r.Get("needenv")
	require.Error(t, err)
	require.True(t, strings.Contains(err.Error(), "missing env"))
}

func TestRepository_BaseDirSubstitution(t *testing.T) {
	root := t.TempDir()
	dir := writeSkill(t, root, "basedir", `---
name: basedir
description: test
---

run from {baseDir}
`)
	err := os.WriteFile(
		filepath.Join(dir, "DOC.md"),
		[]byte("doc {baseDir}\n"),
		0o644,
	)
	require.NoError(t, err)

	r, err := NewRepository([]string{root})
	require.NoError(t, err)

	s, err := r.Get("basedir")
	require.NoError(t, err)
	require.Contains(t, s.Body, dir)
	require.NotContains(t, s.Body, openClawBaseDirPlaceholder)

	require.Len(t, s.Docs, 1)
	require.Contains(t, s.Docs[0].Content, dir)
}

func TestRepository_Path(t *testing.T) {
	root := t.TempDir()
	dir := writeSkill(t, root, "p", `---
name: p
description: test
---

x
`)

	r, err := NewRepository([]string{root}, WithDebug(true))
	require.NoError(t, err)
	require.True(t, r.debug)

	got, err := r.Path("p")
	require.NoError(t, err)
	exp, err := filepath.EvalSymlinks(dir)
	require.NoError(t, err)
	require.Equal(t, exp, got)

	_, err = r.Path("missing")
	require.Error(t, err)
}

func TestRepository_PathDisabledHasReason(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, root, "needenv", `---
name: needenv
description: test
metadata:
  { "openclaw": { "requires": { "env": ["SKILLS_TEST_NEEDENV"] } } }
---

x
`)

	r, err := NewRepository([]string{root})
	require.NoError(t, err)

	_, err = r.Path("needenv")
	require.Error(t, err)
	require.Contains(t, err.Error(), "missing env")
}

func TestWithBundledSkillsRoot_Empty(t *testing.T) {
	r := &Repository{bundledRoot: "x"}
	WithBundledSkillsRoot(" ")(r)
	require.Empty(t, r.bundledRoot)
}

func TestWithBundledSkillsRoot_MissingPath(t *testing.T) {
	r := &Repository{}
	missing := filepath.Join(t.TempDir(), "missing")
	WithBundledSkillsRoot(missing)(r)
	require.Equal(t, missing, r.bundledRoot)
}

func TestRepository_resolveSkillConfig_FallbackToName(t *testing.T) {
	r := &Repository{
		skillConfigs: map[string]SkillConfig{
			"skill": {APIKey: "k"},
		},
	}
	cfg, ok := r.resolveSkillConfig("missing", "skill")
	require.True(t, ok)
	require.Equal(t, "k", cfg.APIKey)
}

func TestRepository_resolveSkillConfig_NilReceiver(t *testing.T) {
	var r *Repository
	_, ok := r.resolveSkillConfig("k", "n")
	require.False(t, ok)
}

func TestRepository_PrecedenceNoFallback(t *testing.T) {
	rootA := t.TempDir()
	rootB := t.TempDir()

	writeSkill(t, rootA, "dup", `---
name: dup
description: test
metadata:
  { "openclaw": { "os": ["win32"] } }
---

from A
`)
	writeSkill(t, rootB, "dup", `---
name: dup
description: test
---

from B
`)

	r, err := NewRepository([]string{rootA, rootB})
	require.NoError(t, err)

	// Higher-precedence skill (rootA) is ineligible on non-windows, so the
	// skill is excluded entirely (OpenClaw semantics: no fallback).
	if runtime.GOOS != "windows" {
		require.Empty(t, r.Summaries())
		_, err := r.Get("dup")
		require.Error(t, err)
		return
	}

	s, err := r.Get("dup")
	require.NoError(t, err)
	require.Contains(t, s.Body, "from A")
}

func TestEvaluateOpenClawOS_Mismatch(t *testing.T) {
	allow := []string{"darwin"}
	if runtime.GOOS == "darwin" {
		allow = []string{"linux"}
	}

	ok, reason := evaluateOpenClawOS(allow)
	require.False(t, ok)
	require.Contains(t, reason, "os mismatch")
	require.Contains(t, reason, allow[0])
}

func TestNormalizeOpenClawOS_Win32(t *testing.T) {
	require.Equal(t, "windows", normalizeOpenClawOS(" win32 "))
}

func TestEvaluateSkill_NoOpenClawMetadata_IsEligible(t *testing.T) {
	ok, reason := evaluateSkill(
		"demo",
		openClawMetadata{},
		false,
		nil,
		SkillConfig{},
		false,
		nil,
	)
	require.True(t, ok)
	require.Empty(t, reason)
}

func TestEvaluateSkill_DisabledByConfig(t *testing.T) {
	enabled := false
	ok, reason := evaluateSkill(
		"demo",
		openClawMetadata{},
		false,
		nil,
		SkillConfig{Enabled: &enabled},
		false,
		nil,
	)
	require.False(t, ok)
	require.Contains(t, reason, "config")
}

func TestRepository_GatesOnConfig(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, root, "needcfg", `---
name: needcfg
description: test
metadata:
  { "openclaw": { "requires": { "config": ["channels.discord.token"] } } }
---

x
`)

	r, err := NewRepository([]string{root})
	require.NoError(t, err)
	require.Empty(t, r.Summaries())

	_, err = r.Get("needcfg")
	require.Error(t, err)
	require.Contains(t, err.Error(), "missing config")
}

func TestRepository_ConfigSatisfied(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, root, "needcfg", `---
name: needcfg
description: test
metadata:
  { "openclaw": { "requires": { "config": ["channels.discord.token"] } } }
---

x
`)

	r, err := NewRepository(
		[]string{root},
		WithConfigKeys([]string{"channels.discord.token"}),
	)
	require.NoError(t, err)
	require.Len(t, r.Summaries(), 1)

	_, err = r.Get("needcfg")
	require.NoError(t, err)
}

func TestRepository_ConfigPrefixMatch(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, root, "needprefix", `---
name: needprefix
description: test
metadata:
  { "openclaw": { "requires": { "config": ["channels.discord"] } } }
---

x
`)

	r, err := NewRepository(
		[]string{root},
		WithConfigKeys([]string{"channels.discord.token"}),
	)
	require.NoError(t, err)
	require.Len(t, r.Summaries(), 1)
}

func TestRepository_GetEmptyName(t *testing.T) {
	r := &Repository{}
	_, err := r.Get(" ")
	require.Error(t, err)
	require.Contains(t, err.Error(), "empty skill name")
}

func TestRepository_GetDisabledNoReason(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, root, "p", `---
name: p
description: test
---

x
`)

	r, err := NewRepository([]string{root})
	require.NoError(t, err)

	_, err = r.Get("missing")
	require.Error(t, err)
	require.Equal(t, `skill "missing" is disabled`, err.Error())
}

func TestRepository_GetUsesBasePathWhenBaseDirEmpty(t *testing.T) {
	root := t.TempDir()
	dir := writeSkill(t, root, "basedir", `---
name: basedir
description: test
---

run from {baseDir}
`)
	r, err := NewRepository([]string{root})
	require.NoError(t, err)

	r.baseDirs["basedir"] = ""

	s, err := r.Get("basedir")
	require.NoError(t, err)
	require.Contains(t, s.Body, dir)
}

func TestRepository_SkillRunEnv_ConfigEnvAndAPIKey(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, root, "needkey", `---
name: needkey
description: test
metadata:
  { "openclaw": { "primaryEnv": "SKILLS_TEST_PRIMARY_ENV" } }
---

x
`)

	r, err := NewRepository(
		[]string{root},
		WithSkillConfigs(map[string]SkillConfig{
			"needkey": {
				APIKey: "k",
				Env: map[string]string{
					"SKILLS_TEST_ENV_A": "a",
				},
			},
		}),
	)
	require.NoError(t, err)

	env, err := r.SkillRunEnv(context.Background(), "needkey")
	require.NoError(t, err)
	require.Equal(t, "a", env["SKILLS_TEST_ENV_A"])
	require.Equal(t, "k", env["SKILLS_TEST_PRIMARY_ENV"])
}

func TestRepository_SkillRunEnv_PrimaryEnvNoOverride(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, root, "needkey", `---
name: needkey
description: test
metadata:
  { "openclaw": { "primaryEnv": "SKILLS_TEST_PRIMARY_ENV" } }
---

x
`)

	r, err := NewRepository(
		[]string{root},
		WithSkillConfigs(map[string]SkillConfig{
			"needkey": {
				APIKey: "k",
				Env: map[string]string{
					"SKILLS_TEST_PRIMARY_ENV": "from-env",
				},
			},
		}),
	)
	require.NoError(t, err)

	env, err := r.SkillRunEnv(context.Background(), "needkey")
	require.NoError(t, err)
	require.Equal(t, "from-env", env["SKILLS_TEST_PRIMARY_ENV"])
}

func TestRepository_SkillRunEnv_FiltersBlockedEnvKeys(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, root, "needkey", `---
name: needkey
description: test
---

x
`)

	r, err := NewRepository(
		[]string{root},
		WithSkillConfigs(map[string]SkillConfig{
			"needkey": {
				Env: map[string]string{
					envLDPreload: "x",
				},
			},
		}),
	)
	require.NoError(t, err)

	env, err := r.SkillRunEnv(context.Background(), "needkey")
	require.NoError(t, err)
	_, ok := env[envLDPreload]
	require.False(t, ok)
}

func TestListTool_ReturnsDisabledSkillsWithReasons(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeSkill(t, root, "ok", `---
name: ok
description: ok
---

# ok
`)
	writeSkill(t, root, "needsbin", `---
name: needsbin
description: needs bin
metadata:
  { "openclaw": { "requires": { "bins": ["definitely_missing_bin"] } } }
---

# needsbin
`)

	repo, err := NewRepository([]string{root})
	require.NoError(t, err)

	lt := NewListTool(repo)
	gotAny, err := lt.Call(context.Background(), []byte(`{}`))
	require.NoError(t, err)

	got, ok := gotAny.(listOutput)
	require.True(t, ok)
	require.Equal(t, 2, got.Total)
	require.Equal(t, 1, got.Enabled)
	require.Equal(t, 1, got.Disabled)

	byName := map[string]skillEntry{}
	for _, s := range got.Skills {
		byName[s.Name] = s
	}
	require.True(t, byName["ok"].Enabled)
	require.False(t, byName["needsbin"].Enabled)
	require.Contains(t, byName["needsbin"].Reason, "missing bins")
}

func TestListTool_Declaration(t *testing.T) {
	t.Parallel()

	lt := NewListTool(nil)
	decl := lt.Declaration()
	require.NotNil(t, decl)
	require.Equal(t, skillListToolName, decl.Name)
	require.NotNil(t, decl.InputSchema)
	require.NotNil(t, decl.InputSchema.Properties["mode"])
	require.NotNil(t, decl.OutputSchema)
}

func TestListTool_Call_InvalidArgsFails(t *testing.T) {
	t.Parallel()

	lt := NewListTool(nil)
	_, err := lt.Call(context.Background(), []byte("{"))
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid args")
}

func TestListTool_Call_NilRepoReturnsEmpty(t *testing.T) {
	t.Parallel()

	lt := NewListTool(nil)
	gotAny, err := lt.Call(context.Background(), []byte(`{}`))
	require.NoError(t, err)

	got, ok := gotAny.(listOutput)
	require.True(t, ok)
	require.Equal(t, 0, got.Total)
	require.Empty(t, got.Skills)
}

func TestListTool_ModeFiltering(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeSkill(t, root, "ok", `---
name: ok
description: ok
---

# ok
`)
	writeSkill(t, root, "metaonly", `---
name: metaonly
description: meta only
metadata:
  { "openclaw": { "emoji": "pin", "homepage": "https://example.com" } }
---

# metaonly
`)
	writeSkill(t, root, "needsbin", `---
name: needsbin
description: needs bin
metadata:
  { "openclaw": { "requires": { "bins": ["definitely_missing_bin"] } } }
---

# needsbin
`)

	repo, err := NewRepository([]string{root})
	require.NoError(t, err)

	lt := NewListTool(repo)

	gotAny, err := lt.Call(
		context.Background(),
		[]byte(`{"mode":"enabled"}`),
	)
	require.NoError(t, err)
	enabledOut := gotAny.(listOutput)
	require.Equal(t, 2, enabledOut.Total)
	require.Equal(t, 2, enabledOut.Enabled)
	require.Equal(t, 0, enabledOut.Disabled)
	for _, s := range enabledOut.Skills {
		require.True(t, s.Enabled, s.Name)
	}

	gotAny, err = lt.Call(
		context.Background(),
		[]byte(`{"mode":"disabled"}`),
	)
	require.NoError(t, err)
	disabledOut := gotAny.(listOutput)
	require.Equal(t, 1, disabledOut.Total)
	require.Equal(t, 0, disabledOut.Enabled)
	require.Equal(t, 1, disabledOut.Disabled)
	require.Len(t, disabledOut.Skills, 1)
	require.Equal(t, "needsbin", disabledOut.Skills[0].Name)
	require.False(t, disabledOut.Skills[0].Enabled)

	gotAny, err = lt.Call(
		context.Background(),
		[]byte(`{"mode":"unknown"}`),
	)
	require.NoError(t, err)
	allOut := gotAny.(listOutput)
	require.Equal(t, 3, allOut.Total)
	require.Equal(t, 2, allOut.Enabled)
	require.Equal(t, 1, allOut.Disabled)

	var metaonly skillEntry
	for _, s := range allOut.Skills {
		if s.Name == "metaonly" {
			metaonly = s
		}
	}
	require.NotEmpty(t, metaonly.Name)
	require.Equal(t, "pin", metaonly.Emoji)
	require.Equal(t, "https://example.com", metaonly.Homepage)
	require.Nil(t, metaonly.Requires)
}

func TestRepository_DependencySources(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeSkill(t, root, "doc", `---
name: doc
description: doc
metadata:
  {
    "openclaw":
      {
        "requires":
          {
            "python":
              [
                {
                  "module": "pypdf",
                  "package": "pypdf",
                },
              ],
          },
      },
  }
---

# doc
`)

	repo, err := NewRepository([]string{root})
	require.NoError(t, err)

	sources, err := repo.DependencySources([]string{"doc"})
	require.NoError(t, err)
	require.Len(t, sources, 1)
	require.Equal(t, "doc", sources[0].Name)
	require.Len(t, sources[0].Requires.Python, 1)
	require.Equal(t, "pypdf", sources[0].Requires.Python[0].Module)
}

func TestRepository_DependencySources_UnknownSkill(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeSkill(t, root, "doc", `---
name: doc
description: doc
---

# doc
`)

	repo, err := NewRepository([]string{root})
	require.NoError(t, err)

	_, err = repo.DependencySources([]string{"missing"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "unknown skill")
}

func TestRepository_HelperFunctions(t *testing.T) {
	t.Parallel()

	var nilRepo *Repository
	sources, err := nilRepo.DependencySources(nil)
	require.NoError(t, err)
	require.Nil(t, sources)

	require.True(t, containsSource([]deps.Source{{Name: "a"}}, "a"))
	require.False(t, containsSource([]deps.Source{{Name: "a"}}, "b"))
	require.True(t, containsString([]string{"a", "b"}, "b"))
	require.False(t, containsString([]string{"a", "b"}, "c"))
	require.Equal(
		t,
		[]string{"b", "a"},
		normalizeSkillNames([]string{" b ", "", "a", "b"}),
	)
}

func TestRepository_DependencySources_AllSkillsSorted(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeSkill(t, root, "b", `---
name: b
description: skill b
metadata:
  {
    "openclaw": {},
  }
---

# b
`)
	writeSkill(t, root, "a", `---
name: a
description: skill a
metadata:
  {
    "openclaw": {},
  }
---

# a
`)

	repo, err := NewRepository([]string{root})
	require.NoError(t, err)

	sources, err := repo.DependencySources(nil)
	require.NoError(t, err)
	require.Len(t, sources, 2)
	require.Equal(t, "a", sources[0].Name)
	require.Equal(t, "skill a", sources[0].Description)
	require.Equal(t, "b", sources[1].Name)
	require.Equal(t, "skill b", sources[1].Description)
}

func TestRepository_SkillRunEnv_NilAndBlank(t *testing.T) {
	t.Parallel()

	var nilRepo *Repository
	env, err := nilRepo.SkillRunEnv(context.Background(), "skill")
	require.NoError(t, err)
	require.Nil(t, env)

	repo := &Repository{}
	env, err = repo.SkillRunEnv(context.Background(), " ")
	require.NoError(t, err)
	require.Nil(t, env)
}

func TestBundledSkills_ParseFrontMatterAndMetadata(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	require.True(t, ok)

	root := filepath.Join(filepath.Dir(file), "..", "..", "skills")
	entries, err := os.ReadDir(root)
	require.NoError(t, err)

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		skillMd := filepath.Join(root, entry.Name(), skillFileName)
		data, err := os.ReadFile(skillMd)
		require.NoError(t, err, entry.Name())

		fm, err := parseFrontMatter(string(data))
		if err != nil {
			require.True(
				t,
				errors.Is(err, errNoFrontMatter),
				entry.Name(),
			)
			continue
		}

		_, _, err = parseOpenClawMetadata(fm)
		require.NoError(t, err, entry.Name())
	}
}

func writeSkill(t *testing.T, root, name, skillMd string) string {
	t.Helper()
	dir := filepath.Join(root, name)
	err := os.MkdirAll(dir, 0o755)
	require.NoError(t, err)
	err = os.WriteFile(
		filepath.Join(dir, skillFileName),
		[]byte(skillMd),
		0o644,
	)
	require.NoError(t, err)
	return dir
}

type marshalTextErr struct{}

func (marshalTextErr) MarshalText() ([]byte, error) {
	return nil, errors.New("boom")
}
