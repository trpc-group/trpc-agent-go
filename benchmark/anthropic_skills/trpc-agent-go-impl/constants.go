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

const (
	defaultSkillsRoot = "https://github.com/anthropics/skills/" +
		"archive/refs/heads/main.zip"

	defaultAppName = "anthropic-skills-bench"
	defaultUserID  = "bench"

	suiteTool        = "tool"
	suiteAgent       = "agent"
	suiteAll         = "all"
	suiteTokenReport = "token-report"
	suitePromptCache = "prompt-cache"

	envModelName     = "MODEL_NAME"
	fallbackModel    = "gpt-5"
	defaultCacheName = "skills_cache"
	defaultWorkName  = "skill_workspaces"

	pythonCmd = "python3"
	venvDir   = ".venv"

	skillDefinitionFile = "SKILL.md"
)

const (
	oceanDepthsThemeRelPath = "themes/ocean-depths.md"
	launchKitSlideTitle     = "Launch Kit"
)

const (
	toolSkillLoad      = "skill_load"
	toolSkillListDocs  = "skill_list_docs"
	toolSkillRun       = "skill_run"
	toolSkillSelectDoc = "skill_select_docs"
)

const (
	skillCreator        = "skill-creator"
	skillBrandGuide     = "brand-guidelines"
	skillInternalComms  = "internal-comms"
	skillMCPBuilder     = "mcp-builder"
	skillPDF            = "pdf"
	skillPPTX           = "pptx"
	skillDocx           = "docx"
	skillFrontendDesign = "frontend-design"
	skillSlackGIF       = "slack-gif-creator"
	skillTemplate       = "template-skill"
	skillThemeFactory   = "theme-factory"
	skillWebappTesting  = "webapp-testing"
)

const (
	scenarioBrandLanding       = "brand_landing_page"
	scenarioLaunchKit          = "launch_kit_deck"
	scenarioDocxToPPTX         = "docx_to_pptx_unpack"
	scenarioTemplateToValidate = "template_to_validate"
)

const (
	fileBrandTokens    = "out/brand_tokens.json"
	dirBrandLanding    = "out/brand_landing"
	fileBrandIndexHTML = dirBrandLanding + "/index.html"
	fileBrandStylesCSS = dirBrandLanding + "/styles.css"

	fileLaunchThemeSpecJSON = "out/launch_theme_spec.json"
	fileLaunchGIF           = "out/launch_emoji.gif"
	fileLaunchPPTX          = "out/launch_kit.pptx"

	fileDocxBench   = "out/docx_bench.docx"
	dirUnpackedDocx = "work/unpacked_docx_bench"

	dirBenchTemplate  = "out/bench_template_skill"
	fileBenchTemplate = dirBenchTemplate + "/SKILL.md"
)

const (
	markerDocxOK     = "BENCH_DOCX_OK"
	markerTemplateOK = "BENCH_TEMPLATE_SKILL_OK"
	markerValidateOK = "BENCH_TEMPLATE_VALIDATE_OK"
)
