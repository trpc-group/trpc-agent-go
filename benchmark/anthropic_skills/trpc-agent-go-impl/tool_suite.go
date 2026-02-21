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
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
	"trpc.group/trpc-go/trpc-agent-go/session"
	skillrepo "trpc.group/trpc-go/trpc-agent-go/skill"
	skilltool "trpc.group/trpc-go/trpc-agent-go/tool/skill"
)

const (
	toolSessionID = "anthropic-skills-tool-bench"

	outPrefix = "out/bench"

	invariantsTimeoutSec = 30
	webCaseTimeoutSec    = 90
	execTimeoutSec       = 10 * 60

	toolHeartbeatInterval = 20 * time.Second
)

type skillRunArgs struct {
	Skill       string   `json:"skill"`
	Command     string   `json:"command"`
	OutputFiles []string `json:"output_files,omitempty"`
	Timeout     int      `json:"timeout,omitempty"`
}

type skillRunFile struct {
	Name    string `json:"name"`
	Content string `json:"content"`
}

type skillRunResult struct {
	Stdout      string         `json:"stdout"`
	Stderr      string         `json:"stderr"`
	ExitCode    int            `json:"exit_code"`
	TimedOut    bool           `json:"timed_out"`
	DurationMS  int            `json:"duration_ms"`
	OutputFiles []skillRunFile `json:"output_files"`
}

func runToolSuite(
	repo skillrepo.Repository,
	exec codeexecutor.CodeExecutor,
	withExec bool,
	onlySkill string,
	progress bool,
	debug bool,
) error {
	ctx := toolBenchContext(toolSessionID)
	rt := skilltool.NewRunTool(repo, exec)

	skills := repo.Summaries()
	sort.Slice(skills, func(i, j int) bool {
		return skills[i].Name < skills[j].Name
	})

	selected := make([]skillrepo.Summary, 0, len(skills))
	for _, s := range skills {
		if onlySkill == "" || s.Name == onlySkill {
			selected = append(selected, s)
		}
	}

	progressf(progress, "🧰 Tool Suite")
	progressf(progress, "  Skills: %d", len(selected))
	for i, s := range selected {
		progressf(
			progress,
			"📌 Skill [%d/%d]: %s",
			i+1,
			len(selected),
			s.Name,
		)
		if err := runSkillInvariants(
			ctx,
			repo,
			rt,
			s.Name,
			progress,
			debug,
		); err != nil {
			return err
		}
	}

	if !withExec {
		progressf(progress, "✅ Tool Suite PASS")
		return nil
	}
	progressf(progress, "  🧪 Extra Exec Cases")
	progressf(progress, "  - %s", skillWebappTesting)
	if err := runWebappTestingCase(ctx, rt, progress, debug); err != nil {
		return err
	}
	progressf(progress, "  - %s", skillCreator)
	if err := runSkillCreatorValidateCase(
		ctx,
		rt,
		progress,
		debug,
	); err != nil {
		return err
	}
	progressf(progress, "  - %s", skillPDF)
	if err := runPDFBoundingBoxesCase(ctx, rt, progress, debug); err != nil {
		return err
	}
	progressf(progress, "  - %s", skillDocx)
	if err := runDocxOOXMLCase(ctx, rt, progress, debug); err != nil {
		return err
	}
	progressf(progress, "  - %s", skillPPTX)
	if err := runPptxInventoryCase(ctx, rt, progress, debug); err != nil {
		return err
	}
	progressf(progress, "  - %s", skillMCPBuilder)
	if err := runMCPBuilderDepsCase(ctx, rt, progress, debug); err != nil {
		return err
	}
	progressf(progress, "  - %s", skillSlackGIF)
	if err := runSlackGIFCase(ctx, rt, progress, debug); err != nil {
		return err
	}
	progressf(progress, "✅ Tool Suite PASS")
	return nil
}

func toolBenchContext(sessionID string) context.Context {
	inv := agent.NewInvocation(
		agent.WithInvocationSession(&session.Session{
			AppName: defaultAppName,
			UserID:  defaultUserID,
			ID:      sessionID,
			State:   session.StateMap{},
		}),
	)
	return agent.NewInvocationContext(context.Background(), inv)
}

func runSkillInvariants(
	ctx context.Context,
	repo skillrepo.Repository,
	rt *skilltool.RunTool,
	skillName string,
	progress bool,
	debug bool,
) error {
	outFile := fmt.Sprintf("%s/%s_ok.txt", outPrefix, skillName)
	probe := probePath(repo, skillName)

	cmd := invariantsCommand(outFile, probe)
	out, err := runSkill(
		ctx,
		rt,
		skillName,
		cmd,
		[]string{outFile},
		invariantsTimeoutSec,
		progress,
		debug,
		"invariants",
	)
	if err != nil {
		return err
	}
	if out.ExitCode != 0 {
		return fmt.Errorf("%s invariants: exit_code=%d",
			skillName, out.ExitCode)
	}
	if len(out.OutputFiles) != 1 {
		return fmt.Errorf("%s invariants: want 1 file, got %d",
			skillName, len(out.OutputFiles))
	}
	if !strings.Contains(out.OutputFiles[0].Content, "OK") {
		return fmt.Errorf("%s invariants: missing OK", skillName)
	}
	return nil
}

func probePath(repo skillrepo.Repository, skillName string) string {
	if repo == nil {
		return ""
	}
	root, err := repo.Path(skillName)
	if err != nil {
		return ""
	}
	candidates := []string{
		"scripts",
		"templates",
		"examples",
		"reference",
		"references",
		"themes",
		"core",
		"ooxml",
	}
	for _, c := range candidates {
		p := filepath.Join(root, c)
		if _, err := os.Stat(p); err == nil {
			return c
		}
	}
	return ""
}

func invariantsCommand(outFile string, probe string) string {
	var sb strings.Builder
	sb.WriteString("set -e; ")
	sb.WriteString("test -L out; test -L work; test -L inputs; ")
	sb.WriteString("test -d ")
	sb.WriteString(shellQuote(venvDir))
	sb.WriteString("; ")
	if strings.TrimSpace(probe) != "" {
		sb.WriteString("test -e ")
		sb.WriteString(shellQuote(probe))
		sb.WriteString("; ")
	}
	sb.WriteString("mkdir -p ")
	sb.WriteString(shellQuote(filepath.Dir(outFile)))
	sb.WriteString("; printf OK > ")
	sb.WriteString(shellQuote(outFile))
	sb.WriteString("; printf OK > ")
	sb.WriteString(shellQuote(filepath.Join(venvDir, "bench.txt")))
	sb.WriteString("; set +e; (echo no >> ")
	sb.WriteString(skillDefinitionFile)
	sb.WriteString(") 2>/dev/null; ")
	sb.WriteString("code=$?; set -e; ")
	sb.WriteString("if [ \"$code\" -eq 0 ]; then exit 1; fi; ")
	sb.WriteString("printf OK")
	return sb.String()
}

func runWebappTestingCase(
	ctx context.Context,
	rt *skilltool.RunTool,
	progress bool,
	debug bool,
) error {
	port, err := freePort()
	if err != nil {
		return err
	}
	outFile := fmt.Sprintf("%s/%s_server_ok.txt",
		outPrefix, skillWebappTesting)

	cmd := webappTestingCommand(port, outFile)
	out, err := runSkill(
		ctx,
		rt,
		skillWebappTesting,
		cmd,
		[]string{outFile},
		webCaseTimeoutSec,
		progress,
		debug,
		"server probe",
	)
	if err != nil {
		return err
	}
	if out.ExitCode != 0 || len(out.OutputFiles) != 1 {
		return fmt.Errorf("webapp-testing: bad result")
	}
	if !strings.Contains(out.OutputFiles[0].Content, "OK") {
		return fmt.Errorf("webapp-testing: missing OK")
	}
	return nil
}

func webappTestingCommand(port int, outFile string) string {
	var sb strings.Builder
	sb.WriteString(pythonCmd)
	sb.WriteString(" scripts/with_server.py --server ")
	sb.WriteString(shellQuote(fmt.Sprintf(
		"%s -m http.server %d", pythonCmd, port,
	)))
	sb.WriteString(" --port ")
	sb.WriteString(fmt.Sprintf("%d", port))
	sb.WriteString(" -- bash -lc ")
	sb.WriteString(shellQuote(fmt.Sprintf(
		"printf OK > %s", outFile,
	)))
	return sb.String()
}

func runSkillCreatorValidateCase(
	ctx context.Context,
	rt *skilltool.RunTool,
	progress bool,
	debug bool,
) error {
	outFile := fmt.Sprintf("%s/%s_validate.txt",
		outPrefix, skillCreator)

	pipPath := filepath.Join(venvDir, "bin", "pip")
	pyPath := filepath.Join(venvDir, "bin", pythonCmd)

	cmd := pythonCmd + " -m venv " + venvDir + " && " +
		pipPath + " install --disable-pip-version-check " +
		"--no-input pyyaml && " +
		pyPath + " scripts/quick_validate.py " +
		"../" + skillWebappTesting + " > " + outFile

	out, err := runSkill(
		ctx,
		rt,
		skillCreator,
		cmd,
		[]string{outFile},
		execTimeoutSec,
		progress,
		debug,
		"quick_validate",
	)
	if err != nil {
		return err
	}
	if out.ExitCode != 0 || len(out.OutputFiles) != 1 {
		return fmt.Errorf("skill-creator: bad result")
	}
	if !strings.Contains(out.OutputFiles[0].Content, "Skill is valid!") {
		return fmt.Errorf("skill-creator: unexpected output")
	}
	return nil
}

func runPDFBoundingBoxesCase(
	ctx context.Context,
	rt *skilltool.RunTool,
	progress bool,
	debug bool,
) error {
	outFile := fmt.Sprintf("%s/%s_bbox.txt", outPrefix, skillPDF)
	fieldsFile := fmt.Sprintf("%s/%s_fields.json", outPrefix, skillPDF)

	jsonBody := `{"form_fields":[` +
		`{"description":"Name","page_number":1,` +
		`"label_bounding_box":[10,10,50,30],` +
		`"entry_bounding_box":[60,10,150,30]}` +
		`]}`

	cmd := "set -e; mkdir -p " + shellQuote(outPrefix) + "; " +
		"printf %s " + shellQuote(jsonBody) + " > " +
		shellQuote(fieldsFile) + "; " +
		pythonCmd + " scripts/check_bounding_boxes.py " +
		shellQuote(fieldsFile) + " > " + shellQuote(outFile)

	out, err := runSkill(
		ctx,
		rt,
		skillPDF,
		cmd,
		[]string{outFile},
		invariantsTimeoutSec,
		progress,
		debug,
		"check_bounding_boxes",
	)
	if err != nil {
		return err
	}
	if out.ExitCode != 0 || len(out.OutputFiles) != 1 {
		return fmt.Errorf("pdf: bad result")
	}
	if !strings.Contains(out.OutputFiles[0].Content,
		"SUCCESS: All bounding boxes are valid") {
		return fmt.Errorf("pdf: unexpected output")
	}
	return nil
}

func runDocxOOXMLCase(
	ctx context.Context,
	rt *skilltool.RunTool,
	progress bool,
	debug bool,
) error {
	outFile := fmt.Sprintf("%s/%s_ooxml.txt", outPrefix, skillDocx)
	docFile := fmt.Sprintf("%s/%s_hello.docx", outPrefix, skillDocx)
	unpacked := "work/unpacked"
	repacked := fmt.Sprintf("%s/%s_repacked.docx",
		outPrefix, skillDocx)

	pipPath := filepath.Join(venvDir, "bin", "pip")
	pyPath := filepath.Join(venvDir, "bin", pythonCmd)

	helloCode := "from docx import Document\n" +
		"d=Document(); d.add_paragraph('Hello'); " +
		"d.save(" + repr(docFile) + ")\n"

	cmd := "set -e; mkdir -p " + shellQuote(outPrefix) + "; " +
		pythonCmd + " -m venv " + venvDir + " && " +
		pipPath + " install --disable-pip-version-check " +
		"--no-input defusedxml python-docx && " +
		pyPath + " -c " + shellQuote(helloCode) + " && " +
		pyPath + " ooxml/scripts/unpack.py " +
		shellQuote(docFile) + " " + shellQuote(unpacked) + " && " +
		pyPath + " ooxml/scripts/pack.py " +
		shellQuote(unpacked) + " " + shellQuote(repacked) + " && " +
		pyPath + " ooxml/scripts/unpack.py " +
		shellQuote(repacked) + " " + shellQuote(unpacked+"2") +
		" >/dev/null 2>&1; " +
		"grep -q " + shellQuote(">Hello<") + " " +
		shellQuote(unpacked+"2/word/document.xml") + " && " +
		"printf OK > " + shellQuote(outFile)

	out, err := runSkill(
		ctx,
		rt,
		skillDocx,
		cmd,
		[]string{outFile},
		execTimeoutSec,
		progress,
		debug,
		"ooxml pack/unpack",
	)
	if err != nil {
		return err
	}
	if out.ExitCode != 0 || len(out.OutputFiles) != 1 {
		return fmt.Errorf("docx: bad result")
	}
	if !strings.Contains(out.OutputFiles[0].Content, "OK") {
		return fmt.Errorf("docx: missing OK")
	}
	return nil
}

func runPptxInventoryCase(
	ctx context.Context,
	rt *skilltool.RunTool,
	progress bool,
	debug bool,
) error {
	outFile := fmt.Sprintf("%s/%s_inventory.txt",
		outPrefix, skillPPTX)
	pptxFile := fmt.Sprintf("%s/%s_hello.pptx",
		outPrefix, skillPPTX)
	invFile := fmt.Sprintf("%s/%s_inventory.json",
		outPrefix, skillPPTX)

	pipPath := filepath.Join(venvDir, "bin", "pip")
	pyPath := filepath.Join(venvDir, "bin", pythonCmd)

	makePPTX := "from pptx import Presentation\n" +
		"prs=Presentation();\n" +
		"sl=prs.slides.add_slide(prs.slide_layouts[5]);\n" +
		"tx=sl.shapes.add_textbox(1,1,500,50);\n" +
		"tx.text_frame.text='Hello';\n" +
		"prs.save(" + repr(pptxFile) + ")\n"

	cmd := "set -e; mkdir -p " + shellQuote(outPrefix) + "; " +
		pythonCmd + " -m venv " + venvDir + " && " +
		pipPath + " install --disable-pip-version-check " +
		"--no-input defusedxml python-pptx pillow && " +
		pyPath + " -c " + shellQuote(makePPTX) + " && " +
		pyPath + " scripts/inventory.py " +
		shellQuote(pptxFile) + " " + shellQuote(invFile) +
		" >/dev/null 2>&1; " +
		"grep -q " + shellQuote("Hello") + " " +
		shellQuote(invFile) + " && " +
		"printf OK > " + shellQuote(outFile)

	out, err := runSkill(
		ctx,
		rt,
		skillPPTX,
		cmd,
		[]string{outFile},
		execTimeoutSec,
		progress,
		debug,
		"inventory",
	)
	if err != nil {
		return err
	}
	if out.ExitCode != 0 || len(out.OutputFiles) != 1 {
		return fmt.Errorf("pptx: bad result")
	}
	if !strings.Contains(out.OutputFiles[0].Content, "OK") {
		return fmt.Errorf("pptx: missing OK")
	}
	return nil
}

func runMCPBuilderDepsCase(
	ctx context.Context,
	rt *skilltool.RunTool,
	progress bool,
	debug bool,
) error {
	outFile := fmt.Sprintf("%s/%s_deps.txt",
		outPrefix, skillMCPBuilder)

	pipPath := filepath.Join(venvDir, "bin", "pip")
	pyPath := filepath.Join(venvDir, "bin", pythonCmd)

	cmd := "set -e; mkdir -p " + shellQuote(outPrefix) + "; " +
		pythonCmd + " -m venv " + venvDir + " && " +
		pipPath + " install --disable-pip-version-check " +
		"--no-input -r scripts/requirements.txt && " +
		pyPath + " -c " + shellQuote(
		"import anthropic, mcp; print('OK')",
	) + " > " + shellQuote(outFile)

	out, err := runSkill(
		ctx,
		rt,
		skillMCPBuilder,
		cmd,
		[]string{outFile},
		execTimeoutSec,
		progress,
		debug,
		"deps import check",
	)
	if err != nil {
		return err
	}
	if out.ExitCode != 0 || len(out.OutputFiles) != 1 {
		return fmt.Errorf("mcp-builder: bad result")
	}
	if !strings.Contains(out.OutputFiles[0].Content, "OK") {
		return fmt.Errorf("mcp-builder: missing OK")
	}
	return nil
}

func runSlackGIFCase(
	ctx context.Context,
	rt *skilltool.RunTool,
	progress bool,
	debug bool,
) error {
	outFile := fmt.Sprintf("%s/%s_gif.txt", outPrefix, skillSlackGIF)
	gifFile := fmt.Sprintf("%s/%s_emoji.gif", outPrefix, skillSlackGIF)

	pipPath := filepath.Join(venvDir, "bin", "pip")
	pyPath := filepath.Join(venvDir, "bin", pythonCmd)

	makeGIF := "from core.gif_builder import GIFBuilder\n" +
		"from PIL import Image\n" +
		"b=GIFBuilder(width=128,height=128,fps=10)\n" +
		"for _ in range(5):\n" +
		"  b.add_frame(Image.new('RGB',(128,128),(255,0,0)))\n" +
		"b.save(" + repr(gifFile) + ",num_colors=48," +
		"optimize_for_emoji=True)\n" +
		"from core.validators import is_slack_ready\n" +
		"print('OK' if is_slack_ready(" + repr(gifFile) +
		",is_emoji=True,verbose=False) else 'BAD')\n"

	cmd := "set -e; mkdir -p " + shellQuote(outPrefix) + "; " +
		pythonCmd + " -m venv " + venvDir + " && " +
		pipPath + " install --disable-pip-version-check " +
		"--no-input -r requirements.txt && " +
		pyPath + " -c " + shellQuote(makeGIF) + " > " +
		shellQuote(outFile)

	out, err := runSkill(
		ctx,
		rt,
		skillSlackGIF,
		cmd,
		[]string{outFile},
		execTimeoutSec,
		progress,
		debug,
		"gif builder",
	)
	if err != nil {
		return err
	}
	if out.ExitCode != 0 || len(out.OutputFiles) != 1 {
		return fmt.Errorf("slack-gif-creator: bad result")
	}
	if !strings.Contains(out.OutputFiles[0].Content, "OK") {
		return fmt.Errorf("slack-gif-creator: missing OK")
	}
	return nil
}

func runSkill(
	ctx context.Context,
	rt *skilltool.RunTool,
	skillName string,
	cmd string,
	outputFiles []string,
	timeoutSec int,
	progress bool,
	debug bool,
	caseName string,
) (skillRunResult, error) {
	args := skillRunArgs{
		Skill:       skillName,
		Command:     cmd,
		OutputFiles: outputFiles,
		Timeout:     timeoutSec,
	}
	enc, err := json.Marshal(args)
	if err != nil {
		return skillRunResult{}, err
	}
	progressf(progress, "  🔧 Tool Call: %s", toolSkillRun)
	progressf(progress, "    Skill: %s", skillName)
	if strings.TrimSpace(caseName) != "" {
		progressf(progress, "    Case: %s", caseName)
	}
	progressf(progress, "    Args:")
	printTextBlock(
		progress,
		debug,
		"      ",
		string(enc),
		defaultPreviewChars,
	)

	start := time.Now()
	var done chan struct{}
	if progress {
		done = make(chan struct{})
		go toolHeartbeat(done, skillName, caseName, start)
	}
	res, err := rt.Call(ctx, enc)
	if done != nil {
		close(done)
	}
	if err != nil {
		progressf(
			progress,
			"  ❌ Tool Error: %s (%s) after %s",
			toolSkillRun,
			skillName,
			time.Since(start).Truncate(time.Millisecond),
		)
		return skillRunResult{}, err
	}
	b, err := json.Marshal(res)
	if err != nil {
		return skillRunResult{}, err
	}
	progressf(progress, "  ✅ Tool Result [%s]:", toolSkillRun)
	printTextBlock(
		progress,
		debug,
		"    ",
		string(b),
		defaultPreviewChars,
	)
	var out skillRunResult
	if err := json.Unmarshal(b, &out); err != nil {
		return skillRunResult{}, err
	}
	progressf(
		progress,
		"  ✅ Result | Exit code: %d | Timed out: %t | %dms",
		out.ExitCode,
		out.TimedOut,
		out.DurationMS,
	)
	return out, nil
}

func toolHeartbeat(
	done <-chan struct{},
	skillName string,
	caseName string,
	start time.Time,
) {
	ticker := time.NewTicker(toolHeartbeatInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			label := skillName
			if strings.TrimSpace(caseName) != "" {
				label = skillName + ":" + caseName
			}
			progressf(
				true,
				"    ⏳ Running %s (%s elapsed)",
				label,
				time.Since(start).Truncate(time.Second),
			)
		case <-done:
			return
		}
	}
}

func shellQuote(s string) string {
	if s == "" {
		return "''"
	}
	q := strings.ReplaceAll(s, "'", "'\\''")
	return "'" + q + "'"
}

func repr(s string) string {
	return strings.ReplaceAll(fmt.Sprintf("%q", s), "`", "\\x60")
}
