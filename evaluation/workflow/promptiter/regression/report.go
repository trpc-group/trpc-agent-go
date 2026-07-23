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
	"sort"
	"strings"
)

const (
	// JSONReportName is the required machine-readable artifact name.
	JSONReportName = "optimization_report.json"
	// MarkdownReportName is the required human-readable artifact name.
	MarkdownReportName = "optimization_report.md"
)

// MarshalReportJSON serializes the report using stable indentation.
func MarshalReportJSON(report *Report) ([]byte, error) {
	if report == nil {
		return nil, errors.New("report is nil")
	}
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal report: %w", err)
	}
	return append(data, '\n'), nil
}

// RenderMarkdown renders the same immutable Report object used for JSON.
func RenderMarkdown(report *Report) (string, error) {
	if report == nil {
		return "", errors.New("report is nil")
	}
	var builder strings.Builder
	decisionText := "拒绝"
	if report.GateDecision.Accepted {
		decisionText = "接受"
	}
	fmt.Fprintf(&builder, "# PromptIter 优化回归报告\n\n")
	fmt.Fprintf(&builder, "- 结论：**%s候选 `%s`**\n", decisionText, markdownCell(report.CandidatePrompt.ID))
	fmt.Fprintf(&builder, "- 运行 ID：`%s`\n", markdownCell(report.RunID))
	fmt.Fprintf(&builder, "- 模式 / 随机种子：`%s` / `%d`\n", markdownCell(report.Mode), report.Seed)
	fmt.Fprintf(&builder, "- Baseline prompt SHA-256：`%s`\n", report.BaselinePrompt.SHA256)
	fmt.Fprintf(&builder, "- Candidate prompt SHA-256：`%s`\n", report.CandidatePrompt.SHA256)
	fmt.Fprintf(&builder, "- Candidate semantic SHA-256（不含 fake marker）：`%s`\n", report.CandidatePrompt.SemanticSHA256)
	fmt.Fprintf(&builder, "- 记录时间：%s — %s（墙钟 %d ms）\n\n", report.StartedAt.Format(timeLayout), report.CompletedAt.Format(timeLayout), report.WallTimeMS)

	bTrain := report.Baseline.Train.OverallScore
	cTrain := report.Candidate.Train.OverallScore
	bValidation := report.Baseline.Validation.OverallScore
	cValidation := report.Candidate.Validation.OverallScore
	builder.WriteString("## 分数摘要\n\n")
	builder.WriteString("| 数据集 | Baseline | Candidate | Delta | Baseline 通过数 | Candidate 通过数 |\n")
	builder.WriteString("|---|---:|---:|---:|---:|---:|\n")
	fmt.Fprintf(&builder, "| train | %.6f | %.6f | %+.6f | %d | %d |\n",
		bTrain, cTrain, cTrain-bTrain, report.Baseline.Train.PassedCases, report.Candidate.Train.PassedCases)
	fmt.Fprintf(&builder, "| validation | %.6f | %.6f | %+.6f | %d | %d |\n\n",
		bValidation, cValidation, cValidation-bValidation, report.Baseline.Validation.PassedCases, report.Candidate.Validation.PassedCases)

	trainBound, trainFallback := responseProvenanceCounts(report.Candidate.Train)
	validationBound, validationFallback := responseProvenanceCounts(report.Candidate.Validation)
	builder.WriteString("## 输出绑定审计\n\n")
	builder.WriteString("| 数据集 | Prompt 语义哈希绑定 | 已验证 Baseline fallback |\n")
	builder.WriteString("|---|---:|---:|\n")
	fmt.Fprintf(&builder, "| train | %d | %d |\n", trainBound, trainFallback)
	fmt.Fprintf(&builder, "| validation | %d | %d |\n\n", validationBound, validationFallback)

	writeCaseDeltaTable(&builder, "Train 逐 case 回归", report.Delta.Train.Cases)
	writeCaseDeltaTable(&builder, "Validation 逐 case 回归", report.Delta.Validation.Cases)

	builder.WriteString("## 接受门禁\n\n")
	builder.WriteString("| 检查 | 结果 | 实际值 | 条件 | 阈值 | 说明 |\n")
	builder.WriteString("|---|---|---:|---|---:|---|\n")
	for _, check := range report.GateDecision.Checks {
		status := "PASS"
		if !check.Passed {
			status = "FAIL"
		}
		fmt.Fprintf(&builder, "| %s | %s | %.6f | %s | %.6f | %s |\n",
			markdownCell(check.Name),
			status,
			check.Actual,
			markdownCell(check.Comparator),
			check.Limit,
			markdownCell(check.Reason),
		)
	}
	builder.WriteString("\n决策理由：\n\n")
	for _, reason := range report.GateDecision.Reasons {
		fmt.Fprintf(&builder, "- %s\n", reason)
	}
	builder.WriteString("\n")

	builder.WriteString("## Baseline 失败归因统计\n\n")
	builder.WriteString("| 分类 | 次数 |\n")
	builder.WriteString("|---|---:|\n")
	categories := make([]string, 0, len(report.FailureAttributionStats))
	for category := range report.FailureAttributionStats {
		categories = append(categories, string(category))
	}
	sort.Strings(categories)
	for _, category := range categories {
		fmt.Fprintf(&builder, "| %s | %d |\n", markdownCell(category), report.FailureAttributionStats[FailureCategory(category)])
	}
	if len(categories) == 0 {
		builder.WriteString("| none | 0 |\n")
	}
	builder.WriteString("\n")

	builder.WriteString("## 成本与时延\n\n")
	builder.WriteString("| Prompt | Model calls | Tool calls | Input tokens | Output tokens | Cost USD | Latency ms |\n")
	builder.WriteString("|---|---:|---:|---:|---:|---:|---:|\n")
	writeUsageRow(&builder, "baseline", report.CostLatencySummary.Baseline)
	writeUsageRow(&builder, "candidate", report.CostLatencySummary.Candidate)
	writeUsageDeltaRow(&builder, "delta", report.CostLatencySummary.Delta)
	writeUsageRow(&builder, "total run (baseline + all rounds)", report.CostLatencySummary.TotalRun)
	builder.WriteString("\n")

	builder.WriteString("## 优化轮次审计\n\n")
	builder.WriteString("| Round | Candidate | Train delta | Validation delta | 新增通过 | 新增失败 | Gate | Prompt SHA-256 |\n")
	builder.WriteString("|---:|---|---:|---:|---:|---:|---|---|\n")
	for _, round := range report.Rounds {
		status := "REJECT"
		if round.GateDecision.Accepted {
			status = "ACCEPT"
		}
		fmt.Fprintf(&builder, "| %d | %s | %+.6f | %+.6f | %d | %d | %s | `%s` |\n",
			round.Round,
			markdownCell(round.Candidate.ID),
			round.Delta.Train.ScoreDelta,
			round.Delta.Validation.ScoreDelta,
			round.Delta.Validation.NewPasses,
			round.Delta.Validation.NewFailures,
			status,
			round.Candidate.SHA256,
		)
	}
	builder.WriteString("\n轮次决策理由：\n\n")
	for _, round := range report.Rounds {
		fmt.Fprintf(
			&builder,
			"- Round %d `%s`：%s\n",
			round.Round,
			markdownCell(round.Candidate.ID),
			markdownCell(strings.Join(round.GateDecision.Reasons, "; ")),
		)
	}
	builder.WriteString("\n每轮完整 prompt、metric、trace、归因、delta、门禁与 usage 均保存在同目录 JSON 报告中。\n")
	return builder.String(), nil
}

func writeCaseDeltaTable(builder *strings.Builder, title string, cases []CaseDelta) {
	fmt.Fprintf(builder, "## %s\n\n", title)
	builder.WriteString("| Case | Baseline | Candidate | Delta | 状态 | 新增 hard fail | 关键 case |\n")
	builder.WriteString("|---|---:|---:|---:|---|---|---|\n")
	for _, caseDelta := range cases {
		fmt.Fprintf(builder, "| %s | %.6f | %.6f | %+.6f | %s | %t | %t |\n",
			markdownCell(caseDelta.CaseID),
			caseDelta.BaselineScore,
			caseDelta.CandidateScore,
			caseDelta.ScoreDelta,
			markdownCell(string(caseDelta.Outcome)),
			caseDelta.NewHardFail,
			caseDelta.Critical,
		)
	}
	builder.WriteString("\n")
}

// WriteReports atomically writes optimization_report.json and
// optimization_report.md to outputDir.
func WriteReports(report *Report, outputDir string) error {
	if outputDir == "" {
		return errors.New("output directory is empty")
	}
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return fmt.Errorf("create output directory: %w", err)
	}
	jsonData, err := MarshalReportJSON(report)
	if err != nil {
		return err
	}
	markdown, err := RenderMarkdown(report)
	if err != nil {
		return err
	}
	if err := writeReportPairAtomic(
		filepath.Join(outputDir, JSONReportName),
		jsonData,
		filepath.Join(outputDir, MarkdownReportName),
		[]byte(markdown),
	); err != nil {
		return fmt.Errorf("write report pair: %w", err)
	}
	return nil
}

const timeLayout = "2006-01-02T15:04:05.000Z07:00"

func writeUsageRow(builder *strings.Builder, name string, usage Usage) {
	fmt.Fprintf(builder, "| %s | %d | %d | %d | %d | %.6f | %d |\n",
		name,
		usage.ModelCalls,
		usage.ToolCalls,
		usage.InputTokens,
		usage.OutputTokens,
		usage.CostUSD,
		usage.LatencyMS,
	)
}

func writeUsageDeltaRow(builder *strings.Builder, name string, usage UsageDelta) {
	fmt.Fprintf(builder, "| %s | %+d | %+d | %+d | %+d | %+.6f | %+d |\n",
		name,
		usage.ModelCalls,
		usage.ToolCalls,
		usage.InputTokens,
		usage.OutputTokens,
		usage.CostUSD,
		usage.LatencyMS,
	)
}

func markdownCell(value string) string {
	value = strings.ReplaceAll(value, "|", "\\|")
	value = strings.ReplaceAll(value, "\r", " ")
	return strings.ReplaceAll(value, "\n", "<br>")
}

func responseProvenanceCounts(summary EvaluationSummary) (bound int, fallback int) {
	for _, evalCase := range summary.Cases {
		if evalCase.UsedFallback {
			fallback++
			continue
		}
		if evalCase.ResponsePromptSHA256 != "" {
			bound++
		}
	}
	return bound, fallback
}

func writeAtomic(path string, data []byte) (returnErr error) {
	directory := filepath.Dir(path)
	temp, err := os.CreateTemp(directory, ".optimization-report-*")
	if err != nil {
		return err
	}
	tempName := temp.Name()
	defer func() {
		if err := os.Remove(tempName); err != nil && !os.IsNotExist(err) && returnErr == nil {
			returnErr = err
		}
	}()
	if err := temp.Chmod(0o644); err != nil {
		_ = temp.Close()
		return err
	}
	if _, err := temp.Write(data); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(tempName, path)
}

type stagedReportFile struct {
	path        string
	tempPath    string
	backupPath  string
	hadOriginal bool
	installed   bool
}

func writeReportPairAtomic(jsonPath string, jsonData []byte, markdownPath string, markdownData []byte) (returnErr error) {
	files := []*stagedReportFile{
		{path: jsonPath},
		{path: markdownPath},
	}
	data := [][]byte{jsonData, markdownData}
	for _, file := range files {
		info, err := os.Stat(file.path)
		if err == nil && info.IsDir() {
			return fmt.Errorf("report destination %q is a directory", file.path)
		}
		if err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	defer func() {
		for _, file := range files {
			if file.tempPath != "" {
				_ = os.Remove(file.tempPath)
			}
			if file.backupPath != "" {
				_ = os.Remove(file.backupPath)
			}
		}
	}()
	for i, file := range files {
		tempPath, err := stageReportFile(filepath.Dir(file.path), data[i])
		if err != nil {
			return err
		}
		file.tempPath = tempPath
	}
	rollback := func() error {
		var rollbackErr error
		for i := len(files) - 1; i >= 0; i-- {
			file := files[i]
			if file.installed {
				if err := os.Remove(file.path); err != nil && !os.IsNotExist(err) {
					rollbackErr = errors.Join(rollbackErr, err)
				}
				file.installed = false
			}
			if file.hadOriginal && file.backupPath != "" {
				if err := os.Rename(file.backupPath, file.path); err != nil {
					rollbackErr = errors.Join(rollbackErr, err)
				} else {
					file.backupPath = ""
				}
			}
		}
		return rollbackErr
	}
	for _, file := range files {
		if _, err := os.Stat(file.path); err == nil {
			backupPath, err := reserveSiblingPath(filepath.Dir(file.path), ".optimization-report-backup-*")
			if err != nil {
				_ = rollback()
				return err
			}
			if err := os.Rename(file.path, backupPath); err != nil {
				_ = os.Remove(backupPath)
				_ = rollback()
				return err
			}
			file.backupPath = backupPath
			file.hadOriginal = true
		}
	}
	for _, file := range files {
		if err := os.Rename(file.tempPath, file.path); err != nil {
			rollbackErr := rollback()
			if rollbackErr != nil {
				return errors.Join(err, fmt.Errorf("rollback report pair: %w", rollbackErr))
			}
			return err
		}
		file.tempPath = ""
		file.installed = true
	}
	for _, file := range files {
		if file.backupPath != "" {
			if err := os.Remove(file.backupPath); err != nil {
				return err
			}
			file.backupPath = ""
		}
	}
	return nil
}

func stageReportFile(directory string, data []byte) (string, error) {
	temp, err := os.CreateTemp(directory, ".optimization-report-stage-*")
	if err != nil {
		return "", err
	}
	path := temp.Name()
	failed := true
	defer func() {
		_ = temp.Close()
		if failed {
			_ = os.Remove(path)
		}
	}()
	if err := temp.Chmod(0o644); err != nil {
		return "", err
	}
	if _, err := temp.Write(data); err != nil {
		return "", err
	}
	if err := temp.Sync(); err != nil {
		return "", err
	}
	if err := temp.Close(); err != nil {
		return "", err
	}
	failed = false
	return path, nil
}

func reserveSiblingPath(directory, pattern string) (string, error) {
	file, err := os.CreateTemp(directory, pattern)
	if err != nil {
		return "", err
	}
	path := file.Name()
	if err := file.Close(); err != nil {
		_ = os.Remove(path)
		return "", err
	}
	if err := os.Remove(path); err != nil {
		return "", err
	}
	return path, nil
}
