//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main is the main package for the skill example.
package main

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/ledongthuc/pdf"
	"github.com/xuri/excelize/v2"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
	localexec "trpc.group/trpc-go/trpc-agent-go/codeexecutor/local"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/planner/react"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/skill"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/arxivsearch"
	"trpc.group/trpc-go/trpc-agent-go/tool/file"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
	"trpc.group/trpc-go/trpc-agent-go/tool/webfetch/httpfetch"
	"trpc.group/trpc-go/trpc-agent-go/tool/wikipedia"
)

var (
	datasetPath = flag.String(
		"dataset",
		defaultDatasetPath,
		"Path to GAIA dataset",
	)
	dataDir = flag.String(
		"data-dir",
		defaultDataDir,
		"Directory containing data files",
	)
	outputPath = flag.String(
		"output",
		defaultOutputPath,
		"Path to output results",
	)
	maxTasks = flag.Int(
		"tasks",
		0,
		"Maximum number of tasks to run (0 means all)",
	)
	modelName = flag.String(
		"model",
		defaultModelName,
		"Model name to use",
	)
	specificTask = flag.String(
		"task-id",
		"",
		"Run a single task by task ID or 1-based index",
	)
)

const (
	defaultDatasetPath = "../data/gaia_2023_level1_validation.json"
	defaultDataDir     = "../data"
	defaultOutputPath  = "../results/trpc-agent-go.json"
	defaultModelName   = "deepseek-chat"

	maxDocChars = 100000
)

const truncatedSuffix = "\n\n... (Content truncated due to size limit)"

// Save the original working directory.
var originalDir string

var currencySymbolsToStrip = []string{
	"$",
	"€",
	"£",
	"¥",
	"₹",
	"¢",
	"₽",
	"₩",
	"฿",
}

func init() {
	var err error
	originalDir, err = os.Getwd()
	if err != nil {
		log.Fatalf("Failed to get current directory: %v", err)
	}
	ensureWhisperPython3()
}

func ensureWhisperPython3() {
	if canImportWhisperWithPython3() {
		return
	}
	if !canImportWhisperWithPython() {
		return
	}
	py, err := exec.LookPath("python")
	if err != nil {
		return
	}
	pyDir := filepath.Dir(py)
	if pyDir == "" {
		return
	}
	pathEnv := os.Getenv("PATH")
	sep := string(os.PathListSeparator)
	os.Setenv("PATH", pyDir+sep+pathEnv)
	log.Printf("Updated PATH to prefer python from: %s", pyDir)
}

// canImportWhisperWithPython3 checks if whisper can be imported using python3.
func canImportWhisperWithPython3() bool {
	c := exec.Command("python3", "-c", "import whisper")
	c.Stdout = io.Discard
	c.Stderr = io.Discard
	return c.Run() == nil
}

// canImportWhisperWithPython checks if whisper can be imported using python.
func canImportWhisperWithPython() bool {
	c := exec.Command("python", "-c", "import whisper")
	c.Stdout = io.Discard
	c.Stderr = io.Discard
	return c.Run() == nil
}

const envGAIADebugModel = "TRPC_AGENT_GAIA_DEBUG_MODEL"

func buildModelDebugCallbacks() *model.Callbacks {
	callbacks := model.NewCallbacks()

	var mu sync.Mutex
	callIndex := 0
	lastReqHash := ""

	callbacks.RegisterBeforeModel(
		func(
			ctx context.Context,
			args *model.BeforeModelArgs,
		) (*model.BeforeModelResult, error) {
			if os.Getenv(envGAIADebugModel) == "" {
				return &model.BeforeModelResult{}, nil
			}

			reqHash := hashModelRequest(args.Request)

			mu.Lock()
			callIndex++
			idx := callIndex
			prevHash := lastReqHash
			lastReqHash = reqHash
			mu.Unlock()

			msgCount, toolMsgCount, last := requestSummary(args.Request)
			suffix := ""
			if prevHash != "" && prevHash == reqHash {
				suffix = " unchanged"
			}

			log.Printf(
				"[debug] before_model call=%d msgs=%d "+
					"tool_msgs=%d hash=%s%s",
				idx,
				msgCount,
				toolMsgCount,
				reqHash,
				suffix,
			)
			if last != nil {
				log.Printf(
					"[debug] last_msg role=%s tool_id=%s "+
						"len=%d",
					last.Role,
					last.ToolID,
					len(last.Content),
				)
			}
			return &model.BeforeModelResult{}, nil
		},
	)

	return callbacks
}

func requestSummary(
	req *model.Request,
) (int, int, *model.Message) {
	if req == nil {
		return 0, 0, nil
	}
	toolMsgCount := 0
	for _, msg := range req.Messages {
		if msg.Role == model.RoleTool {
			toolMsgCount++
		}
	}
	if len(req.Messages) == 0 {
		return 0, toolMsgCount, nil
	}
	last := req.Messages[len(req.Messages)-1]
	return len(req.Messages), toolMsgCount, &last
}

func hashModelRequest(req *model.Request) string {
	if req == nil {
		return ""
	}

	h := sha256.New()
	for _, msg := range req.Messages {
		_, _ = io.WriteString(h, string(msg.Role))
		_, _ = io.WriteString(h, "\n")
		_, _ = io.WriteString(h, msg.ToolID)
		_, _ = io.WriteString(h, "\n")
		sum := sha256.Sum256([]byte(msg.Content))
		_, _ = h.Write(sum[:])
		_, _ = io.WriteString(h, "\n")
		for _, tc := range msg.ToolCalls {
			_, _ = io.WriteString(h, tc.Function.Name)
			_, _ = io.WriteString(h, "\n")
			argSum := sha256.Sum256(tc.Function.Arguments)
			_, _ = h.Write(argSum[:])
			_, _ = io.WriteString(h, "\n")
		}
	}

	out := hex.EncodeToString(h.Sum(nil))
	const maxLen = 12
	if len(out) > maxLen {
		return out[:maxLen]
	}
	return out
}

// GAIATask represents a single task from the dataset
type GAIATask struct {
	TaskID      string `json:"task_id"`
	Question    string `json:"Question"`
	Level       string `json:"Level"`
	FinalAnswer string `json:"Final answer"`
	FileName    string `json:"file_name"`
	FilePath    string `json:"file_path"`
}

// BenchmarkResult stores the result of a single task
type BenchmarkResult struct {
	TaskID          string        `json:"task_id"`
	Question        string        `json:"question"`
	Level           string        `json:"level"`
	PredictedAnswer string        `json:"predicted_answer"`
	GroundTruth     string        `json:"ground_truth"`
	Correct         bool          `json:"correct"`
	Steps           int           `json:"steps"`
	ExecutionTime   time.Duration `json:"execution_time_ms"`
	TokensUsed      int           `json:"tokens_used"`
	ToolCalls       int           `json:"tool_calls"`
	Error           string        `json:"error,omitempty"`
}

// SummaryResult stores aggregated results
type SummaryResult struct {
	Framework       string            `json:"framework"`
	TotalTasks      int               `json:"total_tasks"`
	CorrectCount    int               `json:"correct_count"`
	Accuracy        float64           `json:"accuracy"`
	AvgSteps        float64           `json:"avg_steps"`
	AvgTime         float64           `json:"avg_time_ms"`
	AvgTokens       float64           `json:"avg_tokens"`
	AvgToolCalls    float64           `json:"avg_tool_calls"`
	DetailedResults []BenchmarkResult `json:"detailed_results"`
}

// SystemPrompt is the system instruction used for GAIA evaluation.
const SystemPrompt = `You are an AI assistant designed to answer
questions accurately and concisely.

============================================================
REASONING PROTOCOL
============================================================

STEP 1: UNDERSTAND THE QUESTION
- Identify what is being asked (number, name, list, yes/no, etc.)
- Extract key constraints: time periods, conditions, specific requirements
- Recognize question type: calculation, retrieval, comparison, logic

STEP 2: PLAN YOUR APPROACH
- For CALCULATIONS: Break down into steps, verify formula
- For TIME-BASED queries: Note the exact time period required
  (as of YYYY, in MM/YYYY)
- For LISTS: Ensure you check ALL candidates, don't skip any
- For COMPARISONS: Gather data for all items being compared
- For LOGIC: State the rule or principle being applied

STEP 3: EXECUTE WITH VERIFICATION
- Gather information using appropriate tools
- For numbers: Double-check calculations (consider using multiple methods)
- For historical data: Verify you're using data from the CORRECT time period
- For lists: Maintain a checklist and verify completeness
- NEVER mix current data with historical requests

STEP 4: VALIDATE BEFORE ANSWERING
Checklist:
□ Did I understand the question correctly?
□ Did I use the right time period (if applicable)?
□ Did I check ALL items (for lists)?
□ Did I verify my calculations?
□ Is my answer in the correct format?
□ Did I avoid common errors (mixing dates, skipping items)?

============================================================
CRITICAL RULES
============================================================

FORMAT REQUIREMENTS:
✓ Always end with: FINAL ANSWER: <concise answer>
✓ Numbers: provide just the number (e.g., "42" not "42 units")
✓ Names: provide just the name (e.g., "John Smith")
✓ Lists: use consistent format (e.g., "A, B, C")
✓ NEVER include explanations in the final answer line

COMMON ERRORS TO AVOID:
❌ Using current data when asked about historical periods
❌ Skipping items when checking lists
❌ Calculation errors (verify with multiple approaches)
❌ Confusing similar names/locations
❌ Returning execution plan instead of final answer
❌ Outputting empty or malformed answers

SPECIAL HANDLING:
📊 CALCULATIONS: Show your work, verify results
📅 TIME-BASED: Explicitly state "using data from [time]"
📋 LISTS: Create checklist, verify ALL items checked
🔍 FILE ANALYSIS: Read files completely; don't skip sections.
    Note: Large files (PDF, PPTX) may be truncated; look for truncation
    messages.
🌐 WEB RESEARCH: Cross-verify information from multiple sources

============================================================
AVAILABLE TOOLS
============================================================

- web_search: DuckDuckGo HTML search (general web)
- web_fetch: Fetch one or multiple URLs (HTML/PDF/JSON/XML/text).
  Large PDFs/pages may be truncated.
- wikipedia_wikipedia_search: Wikipedia search
- arxiv_search_search: arXiv search
- read_document: Read local documents (PDF/XLSX/CSV/DOCX/TXT).
  Not for images/audio.
- file_list_file: List files under data-dir (relative paths only)
- file_search_file: Find files by pattern under data-dir
- file_search_content: Search within text files under data-dir
- skill_load / skill_run: Use skills for audio/image when available

Note: Large files (>100KB text) may be truncated to avoid token limits.
You'll see a truncation message if this happens.

============================================================
OUTPUT FORMAT
============================================================

When you have found the answer:

FINAL ANSWER: <your concise, precise answer>

Example formats:
- Number: "FINAL ANSWER: 42"
- Name: "FINAL ANSWER: Albert Einstein"
- List: "FINAL ANSWER: Alice, Bob, Carol"
- Yes/No: "FINAL ANSWER: Yes"

DO NOT include explanations, units, or additional text after "FINAL ANSWER:"`

func main() {
	flag.Parse()

	log.Printf("Starting GAIA Benchmark evaluation for trpc-agent-go")
	log.Printf("Dataset: %s", *datasetPath)
	log.Printf("Data directory: %s", *dataDir)
	log.Printf("Max tasks: %d", *maxTasks)

	// Load dataset
	tasks, err := loadDataset(*datasetPath)
	if err != nil {
		log.Fatalf("Failed to load dataset: %v", err)
	}

	// Filter by specific task if specified
	if *specificTask != "" {
		tasks = filterTasksByID(tasks, *specificTask)
		if len(tasks) == 0 {
			log.Fatalf("No task found matching: %s", *specificTask)
		}
		log.Printf("Running specific task: %s", *specificTask)
	} else if *maxTasks > 0 && len(tasks) > *maxTasks {
		tasks = tasks[:*maxTasks]
	}

	log.Printf("Loaded %d tasks", len(tasks))

	// Create agent
	gaiaAgent := createGAIAAgent()

	// Run benchmark
	results := runBenchmark(gaiaAgent, tasks)

	// Save results
	if err := saveResults(results, *outputPath); err != nil {
		log.Fatalf("Failed to save results: %v", err)
	}

	// Print summary
	printSummary(results)
}

func loadDataset(path string) ([]GAIATask, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read file: %w", err)
	}

	var tasks []GAIATask
	if err := json.Unmarshal(data, &tasks); err != nil {
		return nil, fmt.Errorf("unmarshal json: %w", err)
	}

	return tasks, nil
}

// filterTasksByID returns a single task by ID or 1-based index.
func filterTasksByID(tasks []GAIATask, idOrIndex string) []GAIATask {
	s := strings.TrimSpace(idOrIndex)
	if s == "" {
		return nil
	}

	if index, err := strconv.Atoi(s); err == nil {
		if index > 0 && index <= len(tasks) {
			log.Printf(
				"Found task by index: [%d/%d] %s",
				index,
				len(tasks),
				tasks[index-1].TaskID,
			)
			return []GAIATask{tasks[index-1]}
		}
	}

	for i, task := range tasks {
		if task.TaskID == s {
			log.Printf(
				"Found task by ID: [%d/%d] %s",
				i+1,
				len(tasks),
				task.TaskID,
			)
			return []GAIATask{task}
		}
	}

	return nil
}

// read_document tool.

// ReadFileRequest is the request for read_document.
type ReadFileRequest struct {
	FilePath string `json:"file_path" jsonschema:"description=Path to file"`
}

// ReadFileResponse is the response for read_document.
type ReadFileResponse struct {
	Content  string `json:"content"`
	FileType string `json:"file_type"`
	Error    string `json:"error,omitempty"`
}

// readFileContent reads local documents and extracts text.
//
// It also supports workspace:// and artifact:// file refs, so skill outputs
// can be passed across tools without guessing filesystem roots.
func readFileContent(
	ctx context.Context,
	req ReadFileRequest,
) (ReadFileResponse, error) {
	rawPath := strings.TrimSpace(req.FilePath)
	if rawPath == "" {
		return ReadFileResponse{Error: "file_path is empty"}, nil
	}

	content, _, handled, err := tryReadFileRef(ctx, rawPath)
	if handled {
		if err != nil {
			return ReadFileResponse{Error: err.Error()}, nil
		}
		return ReadFileResponse{
			Content:  content,
			FileType: "text",
		}, nil
	}

	cacheKey := rawPath
	filePath := stripInputsPrefix(rawPath)
	if strings.TrimSpace(filePath) == "" {
		return ReadFileResponse{Error: "file_path is empty"}, nil
	}
	if !filepath.IsAbs(filePath) &&
		!looksLikeSkillWorkspacePath(filePath) {
		absDataDir, err := filepath.Abs(*dataDir)
		if err != nil {
			return ReadFileResponse{
				Error: fmt.Sprintf("resolve data dir: %v", err),
			}, nil
		}
		filePath = filepath.Join(absDataDir, filePath)
	}

	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		if looksLikeSkillWorkspacePath(cacheKey) {
			if content, _, ok := lookupSkillRunOutputFileFromContext(
				ctx,
				cacheKey,
			); ok {
				return ReadFileResponse{
					Content:  content,
					FileType: "text",
				}, nil
			}
			return ReadFileResponse{
				Error: "workspace file is not exported; " +
					"use skill_run output_files content " +
					"or output_files[*].ref " +
					"(workspace://...)",
			}, nil
		}
		return ReadFileResponse{
			Error: fmt.Sprintf("file not found: %s", cacheKey),
		}, nil
	}

	ext := strings.ToLower(filepath.Ext(filePath))
	if isAudioExt(ext) {
		return ReadFileResponse{
			Error: "audio files are not supported by read_document; " +
				"use the whisper skill instead",
		}, nil
	}
	if isImageExt(ext) {
		return ReadFileResponse{
			Error: "image files are not supported by read_document; " +
				"use the ocr skill instead",
		}, nil
	}

	switch ext {
	case ".pdf":
		return readPDFFile(filePath)
	case ".xlsx", ".xls":
		return readExcelFile(filePath)
	case ".csv":
		return readCSVFile(filePath)
	case ".docx", ".pptx":
		return readDocxFile(filePath)
	case ".txt", ".json", ".xml", ".md", ".py", ".go", ".js", ".html", ".css":
		return readTextFile(filePath)
	default:
		// Fall back to plain text.
		return readTextFile(filePath)
	}
}

func looksLikeSkillWorkspacePath(p string) bool {
	s := filepath.ToSlash(strings.TrimSpace(p))
	return strings.HasPrefix(s, "out/") ||
		strings.HasPrefix(s, "work/") ||
		strings.HasPrefix(s, "skills/") ||
		strings.HasPrefix(s, "runs/")
}

func stripInputsPrefix(p string) string {
	s := filepath.ToSlash(strings.TrimSpace(p))
	if strings.HasPrefix(s, "inputs/") {
		return strings.TrimPrefix(s, "inputs/")
	}
	if strings.HasPrefix(s, "work/inputs/") {
		return strings.TrimPrefix(s, "work/inputs/")
	}
	return strings.TrimSpace(p)
}

// readPDFFile reads a PDF file and extracts plain text.
func readPDFFile(filePath string) (ReadFileResponse, error) {
	f, r, err := pdf.Open(filePath)
	if err != nil {
		return ReadFileResponse{
			Error: fmt.Sprintf("open PDF: %v", err),
		}, nil
	}
	defer f.Close()

	var buf bytes.Buffer
	totalPage := r.NumPage()

	for pageIndex := 1; pageIndex <= totalPage; pageIndex++ {
		page := r.Page(pageIndex)
		if page.V.IsNull() {
			continue
		}
		text, err := page.GetPlainText(nil)
		if err != nil {
			continue
		}

		if buf.Len()+len(text) > maxDocChars {
			remaining := maxDocChars - buf.Len()
			if remaining > 0 {
				if remaining > len(text) {
					remaining = len(text)
				}
				buf.WriteString(text[:remaining])
			}
			buf.WriteString(
				fmt.Sprintf(
					"\n\n... (Content truncated: %d/%d pages)",
					pageIndex-1,
					totalPage,
				),
			)
			break
		}

		buf.WriteString(text)
		buf.WriteString("\n")
	}

	content := buf.String()
	if len(content) > maxDocChars {
		content = content[:maxDocChars] + truncatedSuffix
	}

	return ReadFileResponse{
		Content:  content,
		FileType: "pdf",
	}, nil
}

// readExcelFile reads an Excel file and outputs a text representation.
// If the sheet contains background colors, the output includes color hints.
func readExcelFile(filePath string) (ReadFileResponse, error) {
	f, err := excelize.OpenFile(filePath)
	if err != nil {
		return ReadFileResponse{
			Error: fmt.Sprintf("open Excel file: %v", err),
		}, nil
	}
	defer f.Close()

	var b strings.Builder

	for _, sheet := range f.GetSheetList() {
		b.WriteString(fmt.Sprintf("=== Sheet: %s ===\n", sheet))

		rows, err := f.GetRows(sheet)
		if err != nil {
			continue
		}

		maxCols := maxRowLen(rows)
		hasColors, maxUsedCol := scanExcelSheet(f, sheet, rows, maxCols)

		if hasColors {
			writeExcelSheetWithColors(&b, f, sheet, rows, maxUsedCol)
		} else {
			writeExcelSheetPlain(&b, rows)
		}
		b.WriteString("\n")

		if b.Len() > maxDocChars {
			break
		}
	}

	content := b.String()
	if len(content) > maxDocChars {
		content = content[:maxDocChars] + truncatedSuffix
	}
	return ReadFileResponse{
		Content:  content,
		FileType: "excel",
	}, nil
}

const (
	excelARGBPrefix      = "FF"
	excelColorWhite      = "FFFFFF"
	excelColorWhiteShort = "FFFF"
	excelColorBlack      = "000000"
	excelScanExtraCols   = 10
)

const (
	excelColorNote = "Note: This Excel file contains background colors.\n"
	excelColorFmt  = "Format: Cell=Value[#RRGGBB] or Cell=#RRGGBB.\n\n"
)

func maxRowLen(rows [][]string) int {
	max := 0
	for _, r := range rows {
		if len(r) > max {
			max = len(r)
		}
	}
	return max
}

func scanExcelSheet(
	f *excelize.File,
	sheet string,
	rows [][]string,
	maxCols int,
) (bool, int) {
	hasColors := false
	maxUsedCol := 0

	limit := maxCols + excelScanExtraCols
	if limit < 1 {
		limit = 1
	}

	for rowIdx := range rows {
		for colIdx := 0; colIdx < limit; colIdx++ {
			cellName, ok := excelCellName(colIdx, rowIdx)
			if !ok {
				continue
			}
			cellValue, _ := f.GetCellValue(sheet, cellName)
			bg := excelCellBGColor(f, sheet, cellName)

			if cellValue != "" || bg != "" {
				if colIdx > maxUsedCol {
					maxUsedCol = colIdx
				}
			}
			if excelIsMeaningfulColor(bg) {
				hasColors = true
			}
		}
	}

	return hasColors, maxUsedCol
}

func excelCellName(colIdx int, rowIdx int) (string, bool) {
	colName, err := excelize.ColumnNumberToName(colIdx + 1)
	if err != nil {
		return "", false
	}
	return fmt.Sprintf("%s%d", colName, rowIdx+1), true
}

func excelCellBGColor(
	f *excelize.File,
	sheet string,
	cellName string,
) string {
	styleID, err := f.GetCellStyle(sheet, cellName)
	if err != nil || styleID == 0 {
		return ""
	}
	style, err := f.GetStyle(styleID)
	if err != nil || style == nil {
		return ""
	}
	if len(style.Fill.Color) == 0 {
		return ""
	}
	bg := strings.ToUpper(style.Fill.Color[0])
	return strings.TrimPrefix(bg, excelARGBPrefix)
}

func excelIsMeaningfulColor(color string) bool {
	c := strings.ToUpper(strings.TrimSpace(color))
	if c == "" {
		return false
	}
	return c != excelColorWhite && c != excelColorWhiteShort
}

func writeExcelSheetWithColors(
	b *strings.Builder,
	f *excelize.File,
	sheet string,
	rows [][]string,
	maxUsedCol int,
) {
	b.WriteString(excelColorNote)
	b.WriteString(excelColorFmt)

	for rowIdx := range rows {
		b.WriteString(fmt.Sprintf("Row %d: ", rowIdx+1))
		cells := make([]string, 0, maxUsedCol+1)

		for colIdx := 0; colIdx <= maxUsedCol; colIdx++ {
			cellName, ok := excelCellName(colIdx, rowIdx)
			if !ok {
				continue
			}
			cellValue, _ := f.GetCellValue(sheet, cellName)
			bg := excelCellBGColor(f, sheet, cellName)

			if cellValue != "" {
				if excelIsMeaningfulColor(bg) {
					cells = append(cells, fmt.Sprintf(
						"%s=%s[#%s]",
						cellName,
						cellValue,
						bg,
					))
				} else {
					cells = append(cells, fmt.Sprintf(
						"%s=%s",
						cellName,
						cellValue,
					))
				}
				continue
			}

			if excelIsMeaningfulColor(bg) && bg != excelColorBlack {
				cells = append(cells, fmt.Sprintf(
					"%s=#%s",
					cellName,
					bg,
				))
			}
		}

		if len(cells) > 0 {
			b.WriteString(strings.Join(cells, ", "))
		}
		b.WriteString("\n")
		if b.Len() > maxDocChars {
			return
		}
	}
}

func writeExcelSheetPlain(b *strings.Builder, rows [][]string) {
	for rowIdx, row := range rows {
		b.WriteString(fmt.Sprintf("Row %d: ", rowIdx+1))
		for colIdx, cell := range row {
			if colIdx > 0 {
				b.WriteString(" | ")
			}
			colName, err := excelize.ColumnNumberToName(colIdx + 1)
			if err != nil {
				continue
			}
			b.WriteString(fmt.Sprintf("%s=%s", colName, cell))
		}
		b.WriteString("\n")
		if b.Len() > maxDocChars {
			return
		}
	}
}

// readCSVFile reads a CSV file.
func readCSVFile(filePath string) (ReadFileResponse, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return ReadFileResponse{
			Error: fmt.Sprintf("open CSV file: %v", err),
		}, nil
	}
	defer f.Close()

	reader := csv.NewReader(f)
	reader.FieldsPerRecord = -1 // Allow variable field counts.

	var content strings.Builder
	rowIdx := 0

	for {
		record, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			// Fall back to plain text.
			return readTextFile(filePath)
		}

		rowIdx++
		content.WriteString(fmt.Sprintf("Row %d: ", rowIdx))
		for colIdx, cell := range record {
			if colIdx > 0 {
				content.WriteString(" | ")
			}
			content.WriteString(cell)
		}
		content.WriteString("\n")
	}

	return ReadFileResponse{
		Content:  content.String(),
		FileType: "csv",
	}, nil
}

// readDocxFile reads a DOCX/PPTX (Office ZIP) and extracts text.
func readDocxFile(filePath string) (ReadFileResponse, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return ReadFileResponse{
			Error: fmt.Sprintf("read Office file: %v", err),
		}, nil
	}

	content, err := extractDocxText(data)
	if err != nil {
		return ReadFileResponse{
			Error: fmt.Sprintf("extract Office text: %v", err),
		}, nil
	}

	if len(content) > maxDocChars {
		content = content[:maxDocChars] + truncatedSuffix
	}

	return ReadFileResponse{
		Content:  content,
		FileType: "docx",
	}, nil
}

// extractDocxText extracts text from an Office ZIP payload.
func extractDocxText(data []byte) (string, error) {
	zipReader, err := newZipReader(data)
	if err != nil {
		return "", err
	}

	documentPaths := []string{
		"word/document.xml", // DOCX
		"ppt/slides/",       // PPTX slides folder
	}

	var allText strings.Builder
	foundAny := false

	for _, file := range zipReader.File {
		isDocument := false
		for _, path := range documentPaths {
			if file.Name == path || strings.HasPrefix(file.Name, path) {
				isDocument = true
				break
			}
		}

		if !isDocument {
			continue
		}

		if !strings.HasSuffix(file.Name, ".xml") {
			continue
		}

		rc, err := file.Open()
		if err != nil {
			continue
		}

		xmlData, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			continue
		}

		text := extractTextFromXML(string(xmlData))
		if text != "" {
			if allText.Len() > 0 {
				allText.WriteString("\n\n")
			}
			// For PPTX, add slide markers.
			if strings.Contains(file.Name, "slide") {
				slideNum := extractSlideNumber(file.Name)
				allText.WriteString(
					fmt.Sprintf("=== Slide %s ===\n", slideNum),
				)
			}
			allText.WriteString(text)
			foundAny = true
		}
	}

	if !foundAny {
		return "", fmt.Errorf("no document content found in Office file")
	}

	return allText.String(), nil
}

// extractSlideNumber extracts a slide number from a slide XML path.
func extractSlideNumber(filename string) string {
	re := regexp.MustCompile(`slide(\d+)\.xml`)
	matches := re.FindStringSubmatch(filename)
	if len(matches) > 1 {
		return matches[1]
	}
	return "?"
}

// newZipReader creates a zip reader from an in-memory buffer.
func newZipReader(data []byte) (*zip.Reader, error) {
	reader := bytes.NewReader(data)
	return zip.NewReader(reader, int64(len(data)))
}

// extractTextFromXML extracts text from DOCX/PPTX XML documents.
func extractTextFromXML(xml string) string {
	var parts []string

	// DOCX: <w:t> tags.
	reWord := regexp.MustCompile(`<w:t[^>]*>([^<]*)</w:t>`)
	matchesWord := reWord.FindAllStringSubmatch(xml, -1)
	for _, match := range matchesWord {
		if len(match) > 1 && match[1] != "" {
			parts = append(parts, match[1])
		}
	}

	// PPTX: <a:t> tags.
	rePPT := regexp.MustCompile(`<a:t[^>]*>([^<]*)</a:t>`)
	matchesPPT := rePPT.FindAllStringSubmatch(xml, -1)
	for _, match := range matchesPPT {
		if len(match) > 1 && match[1] != "" {
			parts = append(parts, match[1])
		}
	}

	return strings.Join(parts, " ")
}

// readTextFile reads a plain text file.
func readTextFile(filePath string) (ReadFileResponse, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return ReadFileResponse{
			Error: fmt.Sprintf("read file: %v", err),
		}, nil
	}

	content := string(data)
	if len(content) > maxDocChars {
		content = content[:maxDocChars] + truncatedSuffix
	}
	return ReadFileResponse{
		Content:  content,
		FileType: "text",
	}, nil
}

// DuckDuckGo HTML search implementation for the "web_search" tool.

const (
	ddgFormQueryKey      = "q"
	ddgFormContentType   = "application/x-www-form-urlencoded"
	ddgHTMLSearchURL     = "https://html.duckduckgo.com/html/"
	ddgDefaultMaxResults = 5
	ddgHTTPTimeout       = 30 * time.Second
	httpPrefix           = "http://"
	httpsPrefix          = "https://"
)

const ddgUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) " +
	"AppleWebKit/537.36 (KHTML, like Gecko) " +
	"Chrome/120.0.0.0 Safari/537.36"

const (
	ddgLinkPattern = `class="result__a"[^>]*href="([^"]+)"[^>]*>` +
		`([^<]+)</a>`
	ddgSnippetPattern = `class="result__snippet"[^>]*>([^<]+)</a>`
)

// DDGSearchRequest is the request for web_search.
type DDGSearchRequest struct {
	Query      string `json:"query" jsonschema:"description=Query,required"`
	MaxResults int    `json:"max_results,omitempty" jsonschema:"description=Max"`
}

// DDGSearchResult is a single search result.
type DDGSearchResult struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Snippet string `json:"snippet"`
}

// DDGSearchResponse is the response for web_search.
type DDGSearchResponse struct {
	Query   string            `json:"query"`
	Results []DDGSearchResult `json:"results"`
	Summary string            `json:"summary"`
	Error   string            `json:"error,omitempty"`
}

func duckduckgoHTMLSearch(
	ctx context.Context,
	req DDGSearchRequest,
) (DDGSearchResponse, error) {
	query := strings.TrimSpace(req.Query)
	if query == "" {
		return DDGSearchResponse{Error: "query is required"}, nil
	}
	maxResults := req.MaxResults
	if maxResults <= 0 {
		maxResults = ddgDefaultMaxResults
	}

	formData := url.Values{}
	formData.Set(ddgFormQueryKey, query)

	body := strings.NewReader(formData.Encode())
	httpReq, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		ddgHTMLSearchURL,
		body,
	)
	if err != nil {
		return DDGSearchResponse{
			Error: fmt.Sprintf("create request: %v", err),
		}, nil
	}

	httpReq.Header.Set("Content-Type", ddgFormContentType)
	httpReq.Header.Set("User-Agent", ddgUserAgent)

	client := &http.Client{Timeout: ddgHTTPTimeout}
	resp, err := client.Do(httpReq)
	if err != nil {
		return DDGSearchResponse{
			Error: fmt.Sprintf("request failed: %v", err),
		}, nil
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return DDGSearchResponse{
			Error: fmt.Sprintf("HTTP error: %d", resp.StatusCode),
		}, nil
	}

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return DDGSearchResponse{
			Error: fmt.Sprintf("read response: %v", err),
		}, nil
	}

	results := parseDDGHTML(string(bodyBytes), maxResults)
	summary := fmt.Sprintf(
		"Found %d results for query '%s'",
		len(results),
		query,
	)
	return DDGSearchResponse{
		Query:   query,
		Results: results,
		Summary: summary,
	}, nil
}

func parseDDGHTML(html string, maxResults int) []DDGSearchResult {
	var results []DDGSearchResult

	linkRe := regexp.MustCompile(ddgLinkPattern)
	linkMatches := linkRe.FindAllStringSubmatch(html, -1)

	snippetRe := regexp.MustCompile(ddgSnippetPattern)
	snippetMatches := snippetRe.FindAllStringSubmatch(html, -1)

	for i, match := range linkMatches {
		if len(results) >= maxResults {
			break
		}
		if len(match) < 3 {
			continue
		}
		rawURL := match[1]
		title := cleanHTML(strings.TrimSpace(match[2]))
		rawURL = strings.ReplaceAll(rawURL, "&amp;", "&")

		if title == "" {
			continue
		}
		if !strings.HasPrefix(rawURL, httpPrefix) &&
			!strings.HasPrefix(rawURL, httpsPrefix) {
			continue
		}

		snippet := ""
		if i < len(snippetMatches) && len(snippetMatches[i]) > 1 {
			snippet = cleanHTML(snippetMatches[i][1])
		}

		results = append(results, DDGSearchResult{
			Title:   title,
			URL:     rawURL,
			Snippet: snippet,
		})
	}

	return results
}

func cleanHTML(s string) string {
	re := regexp.MustCompile(`<[^>]*>`)
	s = re.ReplaceAllString(s, "")

	s = strings.ReplaceAll(s, "&amp;", "&")
	s = strings.ReplaceAll(s, "&lt;", "<")
	s = strings.ReplaceAll(s, "&gt;", ">")
	s = strings.ReplaceAll(s, "&quot;", "\"")
	s = strings.ReplaceAll(s, "&#39;", "'")
	s = strings.ReplaceAll(s, "&#x27;", "'")
	s = strings.ReplaceAll(s, "&nbsp;", " ")
	return strings.TrimSpace(s)
}

func createGAIAAgent() agent.Agent {
	// Create model
	modelInstance := openai.New(*modelName)

	// DuckDuckGo HTML search tool (not the Instant Answer API).
	ddgSearchTool := function.NewFunctionTool(
		duckduckgoHTMLSearch,
		function.WithName("web_search"),
		function.WithDescription(
			"Search the web using DuckDuckGo HTML results. "+
				"Returns titles, URLs, and snippets.",
		),
	)

	// ArXiv search toolset.
	arxivToolSet, err := arxivsearch.NewToolSet()
	if err != nil {
		log.Printf("Warning: Failed to create arxiv search tool: %v", err)
	}

	// Wikipedia search toolset.
	wikipediaToolSet, err := wikipedia.NewToolSet()
	if err != nil {
		log.Printf("Warning: Failed to create wikipedia search tool: %v", err)
	}

	// Absolute data directory.
	absDataDir, err := filepath.Abs(*dataDir)
	if err != nil {
		log.Fatalf("Failed to get absolute path for data dir: %v", err)
	}

	// Create the file toolset for directory discovery only (list/search).
	// Reading should use read_document or skill_run output_files content.
	fileToolSet, err := file.NewToolSet(
		file.WithBaseDir(absDataDir),
		file.WithMaxFileSize(int64(maxDocChars)),
		file.WithReadFileEnabled(false),
		file.WithReadMultipleFilesEnabled(false),
		file.WithSaveFileEnabled(false),
		file.WithReplaceContentEnabled(false),
	)
	if err != nil {
		log.Fatalf("Failed to create file toolset: %v", err)
	}

	// read_document tool for local files (PDF/Excel/CSV/DOCX/TXT).
	readFileTool := function.NewFunctionTool(
		readFileContent,
		function.WithName("read_document"),
		function.WithDescription(
			"Read content from PDF, Excel (.xlsx/.xls), CSV, DOCX, "+
				"and text files. Does not support images or audio.",
		),
	)

	webFetchTool := httpfetch.NewTool()

	// GPT-5 uses a fixed temperature; keep the default (1.0).
	generationConfig := model.GenerationConfig{
		MaxTokens: intPtr(16384),
	}
	if !strings.Contains(*modelName, "gpt-5") {
		generationConfig.Temperature = floatPtr(0.0)
	}

	skillsRoot := filepath.Join(filepath.Dir(absDataDir), "skills")
	var skillRepo *skill.FSRepository
	if _, err := os.Stat(skillsRoot); err == nil {
		skillRepo, err = skill.NewFSRepository(skillsRoot)
		if err != nil {
			log.Printf("Warning: Failed to create skills repository: %v", err)
			skillRepo = nil
		} else {
			log.Printf("Loaded skills from: %s", skillsRoot)
		}
	} else {
		log.Printf("Skills directory not found: %s (skills disabled)", skillsRoot)
	}

	// Code executor for skills.
	var codeExec codeexecutor.CodeExecutor
	if skillRepo != nil {
		codeExec = localexec.New(
			localexec.WithWorkDir(
				filepath.Join(
					absDataDir,
					"..",
					"skill_workspaces",
				),
			),
			localexec.WithWorkspaceInputsHostBase(absDataDir),
		)
	}

	tools := []tool.Tool{ddgSearchTool, webFetchTool, readFileTool}
	toolSets := []tool.ToolSet{fileToolSet}
	if arxivToolSet != nil {
		toolSets = append(toolSets, arxivToolSet)
	}
	if wikipediaToolSet != nil {
		toolSets = append(toolSets, wikipediaToolSet)
	}

	agentOpts := []llmagent.Option{
		llmagent.WithModel(modelInstance),
		llmagent.WithTools(tools),
		llmagent.WithToolSets(toolSets),
		llmagent.WithGenerationConfig(generationConfig),
		llmagent.WithInstruction(SystemPrompt),
		llmagent.WithPlanner(react.New()),
	}
	if os.Getenv(envGAIADebugModel) != "" {
		agentOpts = append(
			agentOpts,
			llmagent.WithModelCallbacks(buildModelDebugCallbacks()),
		)
	}

	// Enable skills if configured.
	if skillRepo != nil && codeExec != nil {
		agentOpts = append(agentOpts,
			llmagent.WithSkills(skillRepo),
			llmagent.WithCodeExecutor(codeExec),
		)
		log.Printf("Agent Skills enabled")
	}

	return llmagent.New("gaia-agent", agentOpts...)
}

func runBenchmark(ag agent.Agent, tasks []GAIATask) SummaryResult {
	r := runner.NewRunner("gaia-runner", ag)

	results := make([]BenchmarkResult, 0, len(tasks))

	for i, task := range tasks {
		log.Printf("[%d/%d] Processing task: %s", i+1, len(tasks), task.TaskID)

		startTime := time.Now()
		result := runSingleTask(r, task)
		result.ExecutionTime = time.Since(startTime)

		results = append(results, result)

		log.Printf("  Result: correct=%v, steps=%d, time=%v",
			result.Correct, result.Steps, result.ExecutionTime)
		if result.PredictedAnswer != "" {
			log.Printf("  Predicted: %s", truncate(result.PredictedAnswer, 100))
		}
	}

	return calculateSummary("trpc-agent-go", results)
}

const audioAttachmentHint = `This is an audio file.
- Do NOT use read_document (it returns binary noise).
- Use the whisper skill (skill_load + skill_run) to transcribe it.
- Use the "Skill workspace path" shown above as the input path.
- Write the transcript under out/ (or $OUTPUT_DIR).
- Include the transcript file in output_files so content is inline.
- Answer using the transcript text.
- When listing items from the transcript, copy the exact wording verbatim.
  Keep all descriptors and spelling (do not paraphrase or shorten).
  Example: "freshly squeezed lemon juice" ≠ "fresh squeezed lemon juice".
  Example: "freshly squeezed lemon juice" ≠ "lemon juice".
  Example: "pure vanilla extract" ≠ "vanilla extract".`

const imageAttachmentHint = `This is an image file.
- Do NOT use read_document (it cannot read images).
- Use the ocr skill (skill_load + skill_run) to extract text.
- Use the "Skill workspace path" shown above as the input path.
- Write the OCR result under out/ (or $OUTPUT_DIR).
- Include the OCR output file in output_files so content is inline.
- Answer using the extracted text.
- When listing items from extracted text, copy the exact wording verbatim.
  Keep all descriptors (do not paraphrase or shorten).`

const defaultAttachmentHint = `Use the read_document tool to read this file.`

const skillWorkspacePathPrefix = "inputs/"

func attachmentHintForPath(filePath string) string {
	ext := strings.ToLower(filepath.Ext(filePath))
	if isAudioExt(ext) {
		return audioAttachmentHint
	}
	if isImageExt(ext) {
		return imageAttachmentHint
	}
	return defaultAttachmentHint
}

func isAudioExt(ext string) bool {
	switch ext {
	case ".aac", ".flac", ".m4a", ".mp3", ".ogg", ".wav", ".wma":
		return true
	default:
		return false
	}
}

func isImageExt(ext string) bool {
	switch ext {
	case ".bmp", ".gif", ".jpeg", ".jpg", ".png", ".tiff", ".webp":
		return true
	default:
		return false
	}
}

func runSingleTask(r runner.Runner, task GAIATask) BenchmarkResult {
	ctx := context.Background()

	result := BenchmarkResult{
		TaskID:      task.TaskID,
		Question:    task.Question,
		Level:       task.Level,
		GroundTruth: task.FinalAnswer,
	}

	log.Printf("  📝 Question: %s", truncate(task.Question, 150))
	log.Printf("  🎯 Ground Truth: %s", task.FinalAnswer)

	// Build the user prompt.
	var prompt strings.Builder
	prompt.WriteString("Please answer the following question:\n\n")
	prompt.WriteString(task.Question)

	// If there is an attachment, add a data-dir-relative path.
	//
	// Use relative paths in prompts because file tools reject absolute paths
	// and ".." traversal. read_document also accepts data-dir-relative paths.
	if task.FilePath != "" {
		rel := filepath.ToSlash(strings.TrimSpace(task.FilePath))
		absDataDir, err := filepath.Abs(*dataDir)
		if err != nil {
			log.Printf("Warning: data dir: %v", err)
		} else {
			absPath := filepath.Join(absDataDir, task.FilePath)
			if _, err := os.Stat(absPath); err == nil {
				ext := strings.ToLower(filepath.Ext(rel))
				prompt.WriteString("\n\nAttached file: ")
				prompt.WriteString(rel)
				prompt.WriteString("\n")
				prompt.WriteString("Data path: ")
				prompt.WriteString(rel)
				prompt.WriteString("\n")
				if isAudioExt(ext) || isImageExt(ext) {
					prompt.WriteString("Skill workspace path: ")
					prompt.WriteString(skillWorkspacePathPrefix)
					prompt.WriteString(rel)
					prompt.WriteString("\n")
				}
				prompt.WriteString(attachmentHintForPath(rel))
				log.Printf("  📎 Attached file: %s", absPath)
			}
		}
	}

	prompt.WriteString(
		"\n\nRemember to provide your final answer as: " +
			"FINAL ANSWER: <your answer>",
	)

	// Create user message
	userMessage := model.NewUserMessage(prompt.String())
	userID := "benchmark-user"
	sessionID := fmt.Sprintf("session-%s", task.TaskID)
	requestID := uuid.New().String()

	// Run the agent.
	eventChan, err := r.Run(
		ctx,
		userID,
		sessionID,
		userMessage,
		agent.WithRequestID(requestID),
	)
	if err != nil {
		result.Error = fmt.Sprintf("run agent: %v", err)
		return result
	}

	// Process events to count steps and tool calls
	// Keep only the last assistant message (excluding tool messages).
	var lastAssistantContent string
	var toolCalls int
	var steps int
	var currentToolCallNames []string

	for evt := range eventChan {
		var evtPtr *event.Event = evt

		if evtPtr.Error != nil {
			log.Printf("  ❌ Event Error: %s", evtPtr.Error.Message)
			result.Error = fmt.Sprintf("event error: %s", evtPtr.Error.Message)
			break
		}

		if evtPtr.Response != nil {
			// Count a step when we get usage.
			if evtPtr.Response.Usage != nil {
				steps++
				result.TokensUsed = evtPtr.Response.Usage.TotalTokens
				log.Printf("  📊 Step %d | Tokens: %d (in:%d out:%d)",
					steps,
					evtPtr.Response.Usage.TotalTokens,
					evtPtr.Response.Usage.PromptTokens,
					evtPtr.Response.Usage.CompletionTokens)
			}

			if len(evtPtr.Response.Choices) > 0 {
				choice := evtPtr.Response.Choices[0]

				// Tool call logging.
				if len(choice.Message.ToolCalls) > 0 {
					toolCalls += len(choice.Message.ToolCalls)
					log.Printf("  🔧 Tool Calls: %d", len(choice.Message.ToolCalls))
					currentToolCallNames = make([]string, 0, len(choice.Message.ToolCalls))
					for i, tc := range choice.Message.ToolCalls {
						log.Printf("    [%d] %s", i+1, tc.Function.Name)
						// Pretty-print JSON args for logs.
						var args map[string]any
						if err := json.Unmarshal(tc.Function.Arguments, &args); err == nil {
							argsJSON, err := json.MarshalIndent(
								args,
								"        ",
								"  ",
							)
							if err == nil {
								log.Printf("        Args: %s", string(argsJSON))
							}
						} else {
							log.Printf(
								"        Args: %s",
								truncate(
									string(tc.Function.Arguments),
									300,
								),
							)
						}
						currentToolCallNames = append(currentToolCallNames, tc.Function.Name)
					}
				}

				// Print tool results.
				if choice.Message.Role == "tool" && choice.Message.Content != "" {
					toolName := "unknown"
					if len(currentToolCallNames) > 0 {
						toolName = currentToolCallNames[0]
						currentToolCallNames = currentToolCallNames[1:]
					}

					log.Printf("  ✅ Tool Result [%s]:", toolName)
					content := choice.Message.Content
					if len(content) <= 500 {
						for _, line := range strings.Split(content, "\n") {
							if line != "" {
								log.Printf("      %s", line)
							}
						}
					} else {
						lines := strings.Split(content[:500], "\n")
						for _, line := range lines {
							if line != "" {
								log.Printf("      %s", line)
							}
						}
						log.Printf("      ... (truncated, total %d chars)", len(content))
					}
				}

				// Capture the final assistant message (no tool calls).
				isFinalAssistantMsg := choice.Message.Role ==
					"assistant" &&
					choice.Message.Content != "" &&
					len(choice.Message.ToolCalls) == 0
				if isFinalAssistantMsg {
					log.Printf("  💭 Agent Response:")
					content := choice.Message.Content
					if len(content) <= 500 {
						log.Printf("      %s", content)
					} else {
						log.Printf("      %s", truncate(content, 500))
					}
					lastAssistantContent = content
				}
			}
		}

		if evtPtr.IsFinalResponse() {
			log.Printf(
				"  ✅ Final response | Steps: %d, Tool calls: %d",
				steps,
				toolCalls,
			)
			break
		}
	}

	result.Steps = steps
	result.ToolCalls = toolCalls

	// Extract the predicted answer from the last assistant message.
	predictedAnswer := extractFinalAnswer(lastAssistantContent)
	result.PredictedAnswer = predictedAnswer

	log.Printf("  📤 Predicted: %s", predictedAnswer)

	// Verify the answer.
	result.Correct = verifyAnswer(predictedAnswer, result.GroundTruth)

	if result.Correct {
		log.Printf("  🎉 CORRECT!")
	} else {
		log.Printf("  ❌ INCORRECT (expected: %s)", result.GroundTruth)
	}

	return result
}

// extractFinalAnswer extracts the answer from the last assistant message.
func extractFinalAnswer(content string) string {
	patterns := []string{
		`(?i)FINAL\s*ANSWER\s*:\s*(.+?)(?:\n|$)`,
		`(?i)The\s+answer\s+is\s*:\s*(.+?)(?:\n|$)`,
		`(?i)Answer\s*:\s*(.+?)(?:\n|$)`,
	}

	for _, pattern := range patterns {
		re := regexp.MustCompile(pattern)
		matches := re.FindStringSubmatch(content)
		if len(matches) > 1 {
			answer := strings.TrimSpace(matches[1])
			answer = strings.Trim(answer, `"'`)
			answer = strings.TrimSpace(answer)
			return formatAnswer(answer)
		}
	}

	lines := strings.Split(content, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line != "" && !strings.HasPrefix(line, "#") {
			return formatAnswer(line)
		}
	}

	return ""
}

// formatAnswer formats an answer string for evaluation output.
func formatAnswer(answer string) string {
	for _, symbol := range currencySymbolsToStrip {
		answer = strings.ReplaceAll(answer, symbol, "")
	}

	commaPattern := regexp.MustCompile(`(\d),(\d)`)
	for commaPattern.MatchString(answer) {
		answer = commaPattern.ReplaceAllString(answer, "${1}${2}")
	}

	listPattern := regexp.MustCompile(`([a-zA-Z]),([a-zA-Z])`)
	for {
		newAnswer := listPattern.ReplaceAllString(answer, "${1}, ${2}")
		if newAnswer == answer {
			break
		}
		answer = newAnswer
	}

	decZeroRe := regexp.MustCompile(`\b(\d+)\.0+\b`)
	answer = decZeroRe.ReplaceAllString(answer, "${1}")

	decTailRe := regexp.MustCompile(`(\.\d*?)0+\b`)
	answer = decTailRe.ReplaceAllString(answer, "${1}")

	decSpaceRe := regexp.MustCompile(`(\d+)\.\s`)
	answer = decSpaceRe.ReplaceAllString(answer, "${1} ")

	decDotRe := regexp.MustCompile(`(\d+)\.$`)
	answer = decDotRe.ReplaceAllString(answer, "${1}")

	answer = strings.Join(strings.Fields(answer), " ")

	return strings.TrimSpace(answer)
}

// verifyAnswer checks whether the predicted answer matches the ground truth.
func verifyAnswer(predicted, groundTruth string) bool {
	predicted = normalizeAnswer(predicted)
	groundTruth = normalizeAnswer(groundTruth)

	if predicted == groundTruth {
		return true
	}

	if strings.Contains(predicted, groundTruth) {
		return true
	}

	if strings.Contains(groundTruth, predicted) && len(predicted) > 0 {
		return true
	}

	return false
}

// normalizeAnswer normalizes answers to make scoring more robust.
func normalizeAnswer(answer string) string {
	answer = strings.ToLower(answer)

	for _, symbol := range currencySymbolsToStrip {
		answer = strings.ReplaceAll(answer, symbol, "")
	}

	commaPattern := regexp.MustCompile(`(\d),(\d)`)
	for commaPattern.MatchString(answer) {
		answer = commaPattern.ReplaceAllString(answer, "${1}${2}")
	}

	listPattern := regexp.MustCompile(`([a-z0-9])\s*,\s*([a-z0-9])`)
	answer = listPattern.ReplaceAllString(answer, "${1}, ${2}")

	answer = strings.Join(strings.Fields(answer), " ")

	answer = strings.TrimRight(answer, ".,;:!?")

	decimalPattern := regexp.MustCompile(`(\d+\.\d*?)0+\b`)
	answer = decimalPattern.ReplaceAllString(answer, "${1}")

	trailingZeroPattern := regexp.MustCompile(`(\d+)\.0+\b`)
	answer = trailingZeroPattern.ReplaceAllString(answer, "${1}")

	return strings.TrimSpace(answer)
}

func calculateSummary(
	framework string,
	results []BenchmarkResult,
) SummaryResult {
	summary := SummaryResult{
		Framework:       framework,
		TotalTasks:      len(results),
		DetailedResults: results,
	}

	var totalSteps, totalTime, totalTokens, totalToolCalls int

	for _, r := range results {
		if r.Correct {
			summary.CorrectCount++
		}
		totalSteps += r.Steps
		totalTime += int(r.ExecutionTime.Milliseconds())
		totalTokens += r.TokensUsed
		totalToolCalls += r.ToolCalls
	}

	n := float64(len(results))
	summary.Accuracy = float64(summary.CorrectCount) / n * 100
	summary.AvgSteps = float64(totalSteps) / n
	summary.AvgTime = float64(totalTime) / n
	summary.AvgTokens = float64(totalTokens) / n
	summary.AvgToolCalls = float64(totalToolCalls) / n

	return summary
}

func saveResults(summary SummaryResult, path string) error {
	data, err := json.MarshalIndent(summary, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal json: %w", err)
	}

	// Ensure the path is absolute.
	absPath := path
	if !filepath.IsAbs(path) {
		absPath = filepath.Join(originalDir, path)
	}

	// Create the output directory.
	dir := filepath.Dir(absPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create results dir: %w", err)
	}

	if err := os.WriteFile(absPath, data, 0644); err != nil {
		return fmt.Errorf("write file: %w", err)
	}

	log.Printf("Results saved to: %s", absPath)
	return nil
}

func printSummary(summary SummaryResult) {
	fmt.Println("\n" + strings.Repeat("=", 60))
	fmt.Println("GAIA Benchmark Results - trpc-agent-go")
	fmt.Println(strings.Repeat("=", 60))
	fmt.Printf("Total Tasks:      %d\n", summary.TotalTasks)
	fmt.Printf("Correct Count:    %d\n", summary.CorrectCount)
	fmt.Printf("Accuracy:         %.2f%%\n", summary.Accuracy)
	fmt.Printf("Avg Steps:        %.2f\n", summary.AvgSteps)
	fmt.Printf("Avg Time:         %.2f ms\n", summary.AvgTime)
	fmt.Printf("Avg Tokens:       %.2f\n", summary.AvgTokens)
	fmt.Printf("Avg Tool Calls:   %.2f\n", summary.AvgToolCalls)
	fmt.Println(strings.Repeat("=", 60))
}

func intPtr(i int) *int {
	return &i
}

func floatPtr(f float64) *float64 {
	return &f
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
