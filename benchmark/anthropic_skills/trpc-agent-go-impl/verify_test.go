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
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	skillrepo "trpc.group/trpc-go/trpc-agent-go/skill"
)

func TestVerifyBrandLandingOutputs_OK(t *testing.T) {
	root := t.TempDir()
	skillDir := filepath.Join(root, skillBrandGuide)
	require.NoError(t, os.MkdirAll(skillDir, 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(skillDir, skillDefinitionFile),
		[]byte(testBrandSkillMD()),
		0o644,
	))

	repo, err := skillrepo.NewFSRepository(root)
	require.NoError(t, err)

	ws := t.TempDir()
	require.NoError(t, os.MkdirAll(
		filepath.Join(ws, filepath.Dir(fileBrandTokens)),
		0o755,
	))
	require.NoError(t, os.MkdirAll(
		filepath.Join(ws, filepath.Dir(fileBrandIndexHTML)),
		0o755,
	))

	require.NoError(t, os.WriteFile(
		filepath.Join(ws, fileBrandTokens),
		[]byte(testBrandTokensJSON()),
		0o644,
	))
	require.NoError(t, os.WriteFile(
		filepath.Join(ws, fileBrandStylesCSS),
		[]byte(testBrandStylesCSS()),
		0o644,
	))
	require.NoError(t, os.WriteFile(
		filepath.Join(ws, fileBrandIndexHTML),
		[]byte(testBrandIndexHTML()),
		0o644,
	))

	require.NoError(t, verifyBrandLandingOutputs(repo, ws))
}

func testBrandSkillMD() string {
	return `---
name: brand-guidelines
description: test
---

# Brand Guidelines

## Colors

- Dark: ` + "`#141413`" + ` - Primary text and dark backgrounds
- Light: ` + "`#faf9f5`" + ` - Light backgrounds and text on dark
- Mid Gray: ` + "`#b0aea5`" + ` - Secondary elements
- Light Gray: ` + "`#e8e6dc`" + ` - Subtle backgrounds

- Orange: ` + "`#d97757`" + ` - Primary accent
- Blue: ` + "`#6a9bcc`" + ` - Secondary accent
- Green: ` + "`#788c5d`" + ` - Tertiary accent
`
}

func testBrandTokensJSON() string {
	return `{
  "dark": "141413",
  "light": "#faf9f5",
  "mid_gray": "#b0aea5",
  "light_gray": "#e8e6dc",
  "orange": "#d97757",
  "blue": "#6a9bcc",
  "green": "#788c5d"
}`
}

func testBrandStylesCSS() string {
	return `:root{
  --brand-dark:#141413;
  --brand-light:#FAF9F5;
  --brand-mid:#B0AEA5;
  --brand-light-gray:#E8E6DC;
  --brand-orange:#D97757;
  --brand-blue:#6A9BCC;
  --brand-green:#788C5D;
}
`
}

func testBrandIndexHTML() string {
	return `<!doctype html>
<html lang="en">
  <head>
    <meta charset="utf-8" />
    <title>Bench Landing</title>
    <link rel="stylesheet" href="styles.css" />
  </head>
  <body>
    <h1>Bench Landing</h1>
  </body>
</html>
`
}
