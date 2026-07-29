//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package regression

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/status"
)

func TestWriteReportsCreatesReadableJSONAndMarkdown(t *testing.T) {
	report := completeReport(t)
	outputDir := t.TempDir()
	require.NoError(t, WriteReports(outputDir, report))

	jsonData, err := os.ReadFile(filepath.Join(outputDir, jsonReportName))
	require.NoError(t, err)
	var decoded Report
	require.NoError(t, json.Unmarshal(jsonData, &decoded))
	assert.Equal(t, SchemaVersion, decoded.SchemaVersion)
	assert.Equal(t, report.Decision, decoded.Decision)
	assert.True(t, decoded.Usage.TokenUsageAvailable)

	markdown, err := os.ReadFile(filepath.Join(outputDir, markdownReportName))
	require.NoError(t, err)
	assert.Contains(t, string(markdown), "Selected candidate decision: ACCEPT")
	assert.Contains(t, string(markdown), "Attempt 1")
	assert.Contains(t, string(markdown), "Token usage available: true")

	if runtime.GOOS != "windows" {
		for _, name := range []string{jsonReportName, markdownReportName} {
			info, statErr := os.Stat(filepath.Join(outputDir, name))
			require.NoError(t, statErr)
			assert.Equal(t, os.FileMode(fileMode), info.Mode().Perm())
		}
	}
}

func TestWriteReportsOverwritesExistingReports(t *testing.T) {
	report := completeReport(t)
	outputDir := t.TempDir()
	require.NoError(t, WriteReports(outputDir, report))
	report.Run.Seed = 99
	require.NoError(t, WriteReports(outputDir, report))
	jsonData, err := os.ReadFile(filepath.Join(outputDir, jsonReportName))
	require.NoError(t, err)
	assert.Contains(t, string(jsonData), `"seed":99`)
}

func TestWriteReportsValidatesArguments(t *testing.T) {
	report := completeReport(t)
	require.ErrorContains(t, WriteReports(" ", report), "output directory is empty")
	require.ErrorContains(t, WriteReports(t.TempDir(), nil), "report is nil")
	blockingFile := filepath.Join(t.TempDir(), "not-a-directory")
	require.NoError(t, os.WriteFile(blockingFile, []byte("blocked"), fileMode))
	require.ErrorContains(t, WriteReports(blockingFile, report), "create output directory")
}

func TestWriteReportsRollsBackNewPairWhenJSONPublishFails(t *testing.T) {
	outputDir := t.TempDir()
	ops := reportOpsWithRenameFailures(map[int]error{2: errors.New("forced JSON publish failure")})

	err := writeReports(outputDir, completeReport(t), ops)
	require.ErrorContains(t, err, "publish JSON report")
	assertReportFileMissing(t, outputDir, jsonReportName)
	assertReportFileMissing(t, outputDir, markdownReportName)
	assertNoReportTransactionFiles(t, outputDir)
}

func TestWriteReportsRestoresExistingPairWhenJSONPublishFails(t *testing.T) {
	outputDir := t.TempDir()
	report := completeReport(t)
	require.NoError(t, WriteReports(outputDir, report))
	oldJSON := readReportFile(t, outputDir, jsonReportName)
	oldMarkdown := readReportFile(t, outputDir, markdownReportName)
	report.Run.Seed = 99
	ops := reportOpsWithRenameFailures(map[int]error{4: errors.New("forced JSON publish failure")})

	err := writeReports(outputDir, report, ops)
	require.ErrorContains(t, err, "publish JSON report")
	assert.Equal(t, oldJSON, readReportFile(t, outputDir, jsonReportName))
	assert.Equal(t, oldMarkdown, readReportFile(t, outputDir, markdownReportName))
	assertNoReportTransactionFiles(t, outputDir)
}

func TestWriteReportsRestoresFirstBackupWhenSecondBackupFails(t *testing.T) {
	outputDir := t.TempDir()
	report := completeReport(t)
	require.NoError(t, WriteReports(outputDir, report))
	oldJSON := readReportFile(t, outputDir, jsonReportName)
	oldMarkdown := readReportFile(t, outputDir, markdownReportName)
	ops := reportOpsWithRenameFailures(map[int]error{2: errors.New("forced second backup failure")})

	err := writeReports(outputDir, report, ops)
	require.ErrorContains(t, err, "backup existing JSON report")
	assert.Equal(t, oldJSON, readReportFile(t, outputDir, jsonReportName))
	assert.Equal(t, oldMarkdown, readReportFile(t, outputDir, markdownReportName))
	assertNoReportTransactionFiles(t, outputDir)
}

func TestWriteReportsRestoresOnlyExistingJSONWhenPublishFails(t *testing.T) {
	outputDir := t.TempDir()
	oldJSON := []byte("old JSON\n")
	require.NoError(t, os.WriteFile(filepath.Join(outputDir, jsonReportName), oldJSON, fileMode))
	ops := reportOpsWithRenameFailures(map[int]error{3: errors.New("forced JSON publish failure")})

	err := writeReports(outputDir, completeReport(t), ops)
	require.ErrorContains(t, err, "publish JSON report")
	assert.Equal(t, oldJSON, readReportFile(t, outputDir, jsonReportName))
	assertReportFileMissing(t, outputDir, markdownReportName)
	assertNoReportTransactionFiles(t, outputDir)
}

func TestWriteReportsRestoresOnlyExistingMarkdownWhenPublishFails(t *testing.T) {
	outputDir := t.TempDir()
	oldMarkdown := []byte("old Markdown\n")
	require.NoError(t, os.WriteFile(
		filepath.Join(outputDir, markdownReportName), oldMarkdown, fileMode,
	))
	ops := reportOpsWithRenameFailures(map[int]error{3: errors.New("forced JSON publish failure")})

	err := writeReports(outputDir, completeReport(t), ops)
	require.ErrorContains(t, err, "publish JSON report")
	assert.Equal(t, oldMarkdown, readReportFile(t, outputDir, markdownReportName))
	assertReportFileMissing(t, outputDir, jsonReportName)
	assertNoReportTransactionFiles(t, outputDir)
}

func TestWriteReportsJoinsPublishAndRollbackFailures(t *testing.T) {
	outputDir := t.TempDir()
	report := completeReport(t)
	require.NoError(t, WriteReports(outputDir, report))
	oldJSON := readReportFile(t, outputDir, jsonReportName)
	oldMarkdown := readReportFile(t, outputDir, markdownReportName)
	report.Run.Seed = 99
	ops := reportOpsWithRenameFailures(map[int]error{
		4: errors.New("forced JSON publish failure"),
		6: errors.New("forced Markdown restore failure"),
	})

	err := writeReports(outputDir, report, ops)
	require.ErrorContains(t, err, "forced JSON publish failure")
	require.ErrorContains(t, err, "forced Markdown restore failure")
	assert.Equal(t, oldJSON, readReportFile(t, outputDir, jsonReportName))
	assertReportFileMissing(t, outputDir, markdownReportName)
	markdownBackups, globErr := filepath.Glob(
		filepath.Join(outputDir, "."+markdownReportName+".backup-*"),
	)
	require.NoError(t, globErr)
	require.Len(t, markdownBackups, 1)
	backupMarkdown, readErr := os.ReadFile(markdownBackups[0])
	require.NoError(t, readErr)
	assert.Equal(t, oldMarkdown, backupMarkdown)
}

func TestWriteReportsJoinsPublishedMarkdownRemovalFailure(t *testing.T) {
	outputDir := t.TempDir()
	ops := reportOpsWithRenameFailures(map[int]error{2: errors.New("forced JSON publish failure")})
	remove := ops.remove
	markdownPath := filepath.Join(outputDir, markdownReportName)
	ops.remove = func(path string) error {
		if filepath.Clean(path) == filepath.Clean(markdownPath) {
			return errors.New("forced Markdown rollback removal failure")
		}
		return remove(path)
	}

	err := writeReports(outputDir, completeReport(t), ops)
	require.ErrorContains(t, err, "forced JSON publish failure")
	require.ErrorContains(t, err, "forced Markdown rollback removal failure")
	assertReportFileMissing(t, outputDir, jsonReportName)
	assert.Contains(t, string(readReportFile(t, outputDir, markdownReportName)),
		"Selected candidate decision: ACCEPT")
	assertNoReportTransactionFiles(t, outputDir)
}

func TestWriteReportsJoinsStagedCleanupFailure(t *testing.T) {
	outputDir := t.TempDir()
	ops := reportOpsWithRenameFailures(map[int]error{2: errors.New("forced JSON publish failure")})
	remove := ops.remove
	ops.remove = func(path string) error {
		if strings.Contains(filepath.Base(path), ".tmp-") {
			return errors.New("forced staged cleanup failure")
		}
		return remove(path)
	}

	err := writeReports(outputDir, completeReport(t), ops)
	require.ErrorContains(t, err, "forced JSON publish failure")
	require.ErrorContains(t, err, "forced staged cleanup failure")
	assertReportFileMissing(t, outputDir, jsonReportName)
	assertReportFileMissing(t, outputDir, markdownReportName)
}

func TestWriteReportsKeepsCommittedPairWhenBackupCleanupFails(t *testing.T) {
	outputDir := t.TempDir()
	report := completeReport(t)
	require.NoError(t, WriteReports(outputDir, report))
	report.Run.Seed = 99
	ops := osReportFileOps
	ops.remove = func(path string) error {
		if strings.Contains(filepath.Base(path), ".backup-") {
			return errors.New("forced backup cleanup failure")
		}
		return os.Remove(path)
	}

	err := writeReports(outputDir, report, ops)
	require.ErrorContains(t, err, "artifacts committed but backup cleanup failed")
	assert.Contains(t, string(readReportFile(t, outputDir, jsonReportName)), `"seed":99`)
	assert.Contains(t, string(readReportFile(t, outputDir, markdownReportName)), "Seed: 99")
}

func TestWriteReportsRejectsNonRegularExistingTarget(t *testing.T) {
	outputDir := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(outputDir, jsonReportName), directoryMode))

	err := WriteReports(outputDir, completeReport(t))
	require.ErrorContains(t, err, "existing JSON report is not a regular file")
	assertReportFileMissing(t, outputDir, markdownReportName)
	assertNoReportTransactionFiles(t, outputDir)
}

func TestWritePromptAndReportsPublishesCompleteSet(t *testing.T) {
	promptDir := t.TempDir()
	promptPath := filepath.Join(promptDir, "baseline_prompt.txt")
	require.NoError(t, os.WriteFile(promptPath, []byte("baseline\n"), 0o600))
	outputDir := t.TempDir()
	report := completeWritebackReport(t, "candidate")

	require.NoError(t, WritePromptAndReports(
		promptPath, outputDir, "instruction", report,
	))
	assert.Equal(t, "candidate\n", string(readFile(t, promptPath)))
	assert.Contains(t, string(readReportFile(t, outputDir, jsonReportName)),
		`"shouldWriteBack":true`)
	assert.Contains(t, string(readReportFile(t, outputDir, markdownReportName)),
		"Should write back accepted prompt: true")
	if runtime.GOOS != "windows" {
		info, err := os.Stat(promptPath)
		require.NoError(t, err)
		assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
	}
	assertNoTransactionFiles(t, promptDir)
	assertNoReportTransactionFiles(t, outputDir)

	require.NoError(t, WritePromptAndReports(
		promptPath, outputDir, "instruction", report,
	))
	assert.Equal(t, "candidate\n", string(readFile(t, promptPath)))
	assertNoTransactionFiles(t, promptDir)
	assertNoReportTransactionFiles(t, outputDir)
}

func TestWritePromptAndReportsValidatesWritebackContract(t *testing.T) {
	promptPath := filepath.Join(t.TempDir(), "baseline_prompt.txt")
	require.NoError(t, os.WriteFile(promptPath, []byte("baseline\n"), 0o600))
	outputDir := t.TempDir()

	running := completeWritebackReport(t, "candidate")
	running.Run.Status = RunStatusRunning
	require.ErrorContains(t, WritePromptAndReports(
		promptPath, outputDir, "instruction", running,
	), "completed run")

	notAuthorized := completeWritebackReport(t, "candidate")
	notAuthorized.ShouldWriteBack = false
	require.ErrorContains(t, WritePromptAndReports(
		promptPath, outputDir, "instruction", notAuthorized,
	), "does not authorize")

	missingProfile := completeWritebackReport(t, "candidate")
	missingProfile.WritebackProfile = nil
	require.ErrorContains(t, WritePromptAndReports(
		promptPath, outputDir, "instruction", missingProfile,
	), "profile is nil")

	wrongSurface := completeWritebackReport(t, "candidate")
	require.ErrorContains(t, WritePromptAndReports(
		promptPath, outputDir, "other", wrongSurface,
	), "surface id does not match")

	emptyPrompt := completeWritebackReport(t, "candidate")
	emptyPrompt.WritebackProfile.Text = " "
	require.ErrorContains(t, WritePromptAndReports(
		promptPath, outputDir, "instruction", emptyPrompt,
	), "text is empty")

	missingPath := filepath.Join(t.TempDir(), "missing.txt")
	require.ErrorContains(t, WritePromptAndReports(
		missingPath, outputDir, "instruction",
		completeWritebackReport(t, "candidate"),
	), "accepted prompt does not exist")

	directoryPath := filepath.Join(t.TempDir(), "prompt")
	require.NoError(t, os.Mkdir(directoryPath, directoryMode))
	require.ErrorContains(t, WritePromptAndReports(
		directoryPath, outputDir, "instruction",
		completeWritebackReport(t, "candidate"),
	), "accepted prompt is not a regular file")

	conflictingPath := filepath.Join(outputDir, jsonReportName)
	require.NoError(t, os.WriteFile(conflictingPath, []byte("baseline\n"), 0o600))
	require.ErrorContains(t, WritePromptAndReports(
		conflictingPath, outputDir, "instruction",
		completeWritebackReport(t, "candidate"),
	), "target conflicts")
}

func TestWritePromptAndReportsRejectsHardlinkTargetAlias(t *testing.T) {
	outputDir := t.TempDir()
	promptPath := filepath.Join(outputDir, "baseline_prompt.txt")
	require.NoError(t, os.WriteFile(promptPath, []byte("baseline\n"), 0o600))
	if err := os.Link(
		promptPath,
		filepath.Join(outputDir, jsonReportName),
	); err != nil {
		t.Skipf("hardlinks are unavailable: %v", err)
	}

	err := WritePromptAndReports(
		promptPath,
		outputDir,
		"instruction",
		completeWritebackReport(t, "candidate"),
	)
	require.ErrorContains(t, err, "target conflicts")
	assert.Equal(t, "baseline\n", string(readFile(t, promptPath)))
}

func TestWritePromptAndReportsRejectsParentSymlinkTargetAlias(t *testing.T) {
	realOutputDir := t.TempDir()
	promptPath := filepath.Join(realOutputDir, jsonReportName)
	require.NoError(t, os.WriteFile(promptPath, []byte("baseline\n"), 0o600))
	aliasOutputDir := filepath.Join(t.TempDir(), "output-alias")
	if err := os.Symlink(realOutputDir, aliasOutputDir); err != nil {
		t.Skipf("directory symlinks are unavailable: %v", err)
	}

	err := WritePromptAndReports(
		promptPath,
		aliasOutputDir,
		"instruction",
		completeWritebackReport(t, "candidate"),
	)
	require.ErrorContains(t, err, "target conflicts")
	assert.Equal(t, "baseline\n", string(readFile(t, promptPath)))
}

func TestWritePromptAndReportsRollsBackCompleteSet(t *testing.T) {
	promptPath := filepath.Join(t.TempDir(), "baseline_prompt.txt")
	oldPrompt := []byte("baseline\n")
	require.NoError(t, os.WriteFile(promptPath, oldPrompt, 0o600))
	outputDir := t.TempDir()
	oldReport := completeWritebackReport(t, "old candidate")
	require.NoError(t, WritePromptAndReports(
		promptPath, outputDir, "instruction", oldReport,
	))
	oldPrompt = readFile(t, promptPath)
	oldJSON := readReportFile(t, outputDir, jsonReportName)
	oldMarkdown := readReportFile(t, outputDir, markdownReportName)
	newReport := completeWritebackReport(t, "new candidate")
	newReport.Run.Seed = 99
	ops := reportOpsWithRenameFailures(map[int]error{
		6: errors.New("forced JSON publish failure"),
	})

	err := writePromptAndReports(
		promptPath, outputDir, "instruction", newReport, ops,
	)
	require.ErrorContains(t, err, "forced JSON publish failure")
	assert.Equal(t, oldPrompt, readFile(t, promptPath))
	assert.Equal(t, oldJSON, readReportFile(t, outputDir, jsonReportName))
	assert.Equal(t, oldMarkdown, readReportFile(t, outputDir, markdownReportName))
	assertNoTransactionFiles(t, filepath.Dir(promptPath))
	assertNoReportTransactionFiles(t, outputDir)
}

func TestWritePromptAndReportsReportsRollbackFailure(t *testing.T) {
	promptDir := t.TempDir()
	promptPath := filepath.Join(promptDir, "baseline_prompt.txt")
	require.NoError(t, os.WriteFile(promptPath, []byte("baseline\n"), 0o600))
	outputDir := t.TempDir()
	require.NoError(t, WritePromptAndReports(
		promptPath, outputDir, "instruction",
		completeWritebackReport(t, "old candidate"),
	))
	ops := reportOpsWithRenameFailures(map[int]error{
		6: errors.New("forced JSON publish failure"),
		9: errors.New("forced prompt restore failure"),
	})

	err := writePromptAndReports(
		promptPath, outputDir, "instruction",
		completeWritebackReport(t, "new candidate"),
		ops,
	)
	require.ErrorContains(t, err, "forced JSON publish failure")
	require.ErrorContains(t, err, "forced prompt restore failure")
	_, statErr := os.Stat(promptPath)
	require.ErrorIs(t, statErr, os.ErrNotExist)
	backups, globErr := filepath.Glob(
		filepath.Join(promptDir, ".baseline_prompt.txt.backup-*"),
	)
	require.NoError(t, globErr)
	require.Len(t, backups, 1)
	assert.Equal(t, "old candidate\n", string(readFile(t, backups[0])))
}

func TestWritePromptAndReportsSerializesCompleteSets(t *testing.T) {
	promptPath := filepath.Join(t.TempDir(), "baseline_prompt.txt")
	require.NoError(t, os.WriteFile(promptPath, []byte("baseline\n"), 0o600))
	outputDir := t.TempDir()
	const writers = 8
	start := make(chan struct{})
	errs := make(chan error, writers)
	var wait sync.WaitGroup
	for i := 0; i < writers; i++ {
		report := completeWritebackReport(t, fmt.Sprintf("candidate-%d", i))
		report.Run.Seed = int64(i)
		report.Rounds[0].CandidatePrompt.Text = report.WritebackProfile.Text
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			errs <- WritePromptAndReports(
				promptPath, outputDir, "instruction", report,
			)
		}()
	}
	close(start)
	wait.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}

	var decoded Report
	require.NoError(t, json.Unmarshal(
		readReportFile(t, outputDir, jsonReportName),
		&decoded,
	))
	require.NotNil(t, decoded.WritebackProfile)
	assert.Equal(t, decoded.WritebackProfile.Text+"\n", string(readFile(t, promptPath)))
	markdown := readReportFile(t, outputDir, markdownReportName)
	assert.Contains(t, string(markdown), decoded.WritebackProfile.Text)
	assert.Contains(t, string(markdown), fmt.Sprintf("- Seed: %d", decoded.Run.Seed))
	assertNoTransactionFiles(t, filepath.Dir(promptPath))
	assertNoReportTransactionFiles(t, outputDir)
}

func TestWriteReportsSerializesConcurrentWriters(t *testing.T) {
	outputDir := t.TempDir()
	const writers = 8
	reports := make([]*Report, writers)
	for i := range reports {
		reports[i] = completeReport(t)
		reports[i].Run.Seed = int64(100 + i)
	}
	start := make(chan struct{})
	errs := make(chan error, writers)
	var wait sync.WaitGroup
	for _, report := range reports {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			errs <- WriteReports(outputDir, report)
		}()
	}
	close(start)
	wait.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}

	var decoded Report
	require.NoError(t, json.Unmarshal(readReportFile(t, outputDir, jsonReportName), &decoded))
	markdown := string(readReportFile(t, outputDir, markdownReportName))
	assert.Contains(t, markdown, fmt.Sprintf("Seed: %d", decoded.Run.Seed))
	assertNoReportTransactionFiles(t, outputDir)
}

func TestRenderMarkdownProtectsCandidatePrompt(t *testing.T) {
	report := completeReport(t)
	prompt := "first line\n```markdown\n## Injected Section\n- fake audit item\n```\nlast `value`"
	report.Rounds[0].CandidatePrompt.Text = prompt

	data, err := renderMarkdown(report)
	require.NoError(t, err)
	markdown := string(data)
	wantBlock := "- Candidate prompt:\n\n````\n" + prompt + "\n````\n"
	require.Contains(t, markdown, wantBlock)
	blockEnd := strings.Index(markdown, wantBlock) + len(wantBlock)
	usageStart := strings.Index(markdown, "## Usage")
	assert.Greater(t, usageStart, blockEnd)
}

func TestRenderMarkdownDistinguishesGateDelta(t *testing.T) {
	report := completeReport(t)
	report.Rounds[0].Delta.ScoreDelta = 0.2
	report.Rounds[0].RegressionGateDecision.ScoreDelta = 0

	data, err := renderMarkdown(report)
	require.NoError(t, err)
	markdown := string(data)
	assert.Contains(t, markdown, "Original baseline delta: 0.2000")
	assert.Contains(t, markdown, "Gate delta vs accepted baseline: 0.0000")
}

func TestRenderMarkdownSanitizesLineValues(t *testing.T) {
	report := completeReport(t)
	report.Decision.Reasons = []string{"decision\n## injected `heading` \\<tag> \\[link]"}
	report.Run.Error = "failure\r\n## injected error"
	report.Rounds[0].RegressionGateDecision.Reasons = []string{"gate\r## injected reason"}
	report.Rounds[0].Delta.Cases = []CaseDelta{{
		CaseID: "case\n## injected case", Kind: DeltaUnchanged,
	}}

	data, err := renderMarkdown(report)
	require.NoError(t, err)
	markdown := string(data)
	assert.Contains(t, markdown, "Decision reasons: decision ## injected \\`heading\\`")
	assert.Contains(t, markdown, `\\\<tag\>`)
	assert.Contains(t, markdown, `\\\[link\]`)
	assert.Contains(t, markdown, "Run error: failure ## injected error")
	assert.Contains(t, markdown, "Reasons: gate ## injected reason")
	assert.Contains(t, markdown, "- case ## injected case: unchanged")
	assert.NotContains(t, markdown, "\n## injected")
}

func TestMarkdownCodeBlock(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  string
	}{
		{name: "no backticks", value: "prompt", want: "```\nprompt\n```"},
		{name: "three backticks", value: "a```b", want: "````\na```b\n````"},
		{name: "four backticks", value: "a````b", want: "`````\na````b\n`````"},
		{name: "empty", value: "", want: "```\n\n```"},
		{name: "trailing newline", value: "prompt\n", want: "```\nprompt\n```"},
		{name: "trailing CRLF", value: "prompt\r\n", want: "```\nprompt\r\n```"},
		{name: "trailing CR", value: "prompt\r", want: "```\nprompt\r```"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assert.Equal(t, test.want, markdownCodeBlock(test.value))
		})
	}
}

func completeReport(t *testing.T) *Report {
	baseline := evaluationWithCases(caseWithMetric("a", 0.5, status.EvalStatusPassed))
	report, err := NewReport(RunMetadata{Seed: 42, Mode: "fake"}, baseline, baseline, AttributionResult{})
	require.NoError(t, err)
	require.NoError(t, AppendRound(report, completeRound(1, 0.7, true)))
	return report
}

func completeWritebackReport(t *testing.T, prompt string) *Report {
	t.Helper()
	report := completeReport(t)
	report.Run.Status = RunStatusCompleted
	report.Candidate.Text = prompt
	require.NoError(t, SetWriteback(
		report,
		PromptRecord{SurfaceID: "instruction", Text: "baseline"},
		PromptRecord{SurfaceID: "instruction", Text: prompt},
	))
	return report
}

func reportOpsWithRenameFailures(failures map[int]error) reportFileOps {
	ops := osReportFileOps
	renameCalls := 0
	ops.rename = func(oldPath, newPath string) error {
		renameCalls++
		if err := failures[renameCalls]; err != nil {
			return err
		}
		return os.Rename(oldPath, newPath)
	}
	return ops
}

func readReportFile(t *testing.T, outputDir, name string) []byte {
	t.Helper()
	return readFile(t, filepath.Join(outputDir, name))
}

func readFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	return data
}

func assertReportFileMissing(t *testing.T, outputDir, name string) {
	t.Helper()
	_, err := os.Stat(filepath.Join(outputDir, name))
	require.ErrorIs(t, err, os.ErrNotExist)
}

func assertNoReportTransactionFiles(t *testing.T, outputDir string) {
	t.Helper()
	assertNoTransactionFiles(t, outputDir)
}

func assertNoTransactionFiles(t *testing.T, directory string) {
	t.Helper()
	entries, err := os.ReadDir(directory)
	require.NoError(t, err)
	for _, entry := range entries {
		assert.NotContains(t, entry.Name(), ".tmp-")
		assert.NotContains(t, entry.Name(), ".backup-")
	}
}
