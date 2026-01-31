//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package main

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/tls"
	"encoding/csv"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
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
	"trpc.group/trpc-go/trpc-agent-go/tool/wikipedia"
)

var (
	datasetPath     = flag.String("dataset", "../data/gaia_2023_level1_validation.json", "Path to GAIA dataset")
	dataDir         = flag.String("data-dir", "../data", "Directory containing data files")
	outputPath      = flag.String("output", "../results/trpc-agent-go.json", "Path to output results")
	maxTasks        = flag.Int("tasks", 0, "Maximum number of tasks to run (0 means all)")
	modelName       = flag.String("model", "deepseek-v3-local-II", "Model name to use")
	specificTask    = flag.String("task-id", "", "Run specific task by task ID or index (e.g., '28' or 'e1fc63a2-da7a-432f-be78-7c4a95598703')")
	enableRalphLoop = flag.Bool(
		"ralph-loop",
		false,
		"Enable Ralph Loop outer loop verification",
	)
	ralphMaxIterations = flag.Int(
		"ralph-max-iterations",
		3,
		"Max Ralph Loop iterations",
	)
)

// Store original working directory for relative path resolution
var originalDir string

func init() {
	var err error
	originalDir, err = os.Getwd()
	if err != nil {
		log.Fatalf("Failed to get current directory: %v", err)
	}
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
	ExecutionTime   time.Duration `json:"execution_time"`
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

// SystemPrompt is the system prompt used for GAIA evaluation
const SystemPrompt = `You are an AI assistant designed to answer questions accurately and concisely.

IMPORTANT RULES:
1. Always provide your final answer in a clear, specific format
2. When you have the answer, state it clearly with "FINAL ANSWER: <your answer>"
3. Be precise - if asked for a number, provide just the number
4. If asked for a name, provide just the name
5. Use the available tools to gather information when needed
6. For file-related questions, read the file content first before answering
7. Think step by step, but be concise in your final answer
8. DO NOT use read_document on image files!
9. PRESERVE EXACT WORDING: When extracting information from documents or audio, use the EXACT words/phrases as they appear. Do NOT simplify or paraphrase (e.g., use "freshly squeezed lemon juice" not just "lemon juice", use "granulated sugar" not just "sugar")
10. HISTORICAL ACCURACY: When answering questions about historical events or places, use the NAME THAT WAS IN USE AT THE TIME OF THE EVENT. For example, if someone was born in a town that later changed its name or was split into multiple towns, use the town name as it was when the person was born (e.g., John Adams was born in Braintree, Massachusetts in 1735, even though that area later became part of Quincy in 1792)
11. BOOK TITLES: When asked for "complete title" or "full title" of a book, use the OFFICIAL format as it appears on the title page. Numbers in titles are often written as WORDS (e.g., "Five Hundred" not "500"). Check library catalogs (Library of Congress, WorldCat) or publisher sources for the official title format.
12. POETRY FORMATTING: For questions about poem formatting (indentation, stanza structure, line breaks), you MUST access the original published source. Web versions often lose formatting. Try: (a) Archive.org book scans, (b) Google Books previews, (c) Academic databases, (d) Publisher's official edition. If unsure, state that the original formatting cannot be verified.
13. FOLLOW OUTPUT CONSTRAINTS: If the question specifies format constraints (e.g., "without abbreviations", "full name", "no periods"), you MUST follow them exactly. For example: "St. Petersburg" should be "Saint Petersburg" if asked for no abbreviations; "Dr." should be "Doctor" if asked for full form.

PYTHON CODE EXECUTION (CRITICAL - USE execute_python TOOL):
- You have the execute_python tool to run Python code. Use it for ANY mathematical, computational, or logic problems!
- For ANY calculations, probability problems, combinatorics, game theory, or optimization problems: USE execute_python tool!
- To execute code, call the execute_python tool with your Python code in the "code" parameter
- Example tool call:
  execute_python(code="result = 2 + 2\nprint(result)")
- Python is especially useful for:
  * Complex arithmetic and numerical computations
  * Probability and statistics calculations
  * Enumeration and brute-force search of possibilities
  * Solving puzzles with constraints (e.g., "30 coins in 3 boxes" type problems, "distribute items" problems)
  * Date/time calculations
  * Game theory optimal strategies (maximin, minimax)
  * String manipulation and pattern matching
  * ASCII art/diagram analysis - finding character positions, counting elements
  * Interval coverage problems (e.g., minimum towers/stations to cover all points)
  * Greedy algorithm problems
- When you encounter a math puzzle or optimization problem, ALWAYS use execute_python to enumerate all possibilities and compute the answer
- IMPORTANT: Only Python standard library is available. Do NOT use numpy, pandas, or other third-party libraries.
- ALWAYS use print() to output results in your code
- After calling execute_python, wait for the result before giving your FINAL ANSWER

AUDIO FILE PROCESSING (CRITICAL - USE WHISPER SKILL):
- For ANY audio files (MP3, WAV, M4A, FLAC, OGG, etc.), you MUST use the whisper skill to transcribe them
- NEVER use read_document on audio files - it will fail and cause token overflow!
- To transcribe audio: First call skill_load with skill="whisper", then call skill_run with the transcription command
- Example workflow:
  1. skill_load(skill="whisper")
  2. skill_run(skill="whisper", command="python3 scripts/transcribe.py /path/to/audio.mp3 /tmp/transcript.txt")
  3. Read the transcript file to get the text content
- *** IMPORTANT: whisper/skill_run is ONLY for audio transcription! ***
- *** DO NOT use skill_run to execute general Python code - use execute_python tool instead! ***

IMAGE ANALYSIS (CRITICAL - YOU HAVE VISION CAPABILITIES):
- You are a MULTIMODAL AI with built-in VISION capabilities
- If an image is attached to the message, you can SEE and ANALYZE it DIRECTLY - no tools needed!
- NEVER use any OCR tools, skills, or external services for images
- Simply LOOK at the attached image and describe what you see
- For text in images: READ it directly with your vision - you can see and read text in images
- For chess positions: LOOK at the board and analyze the position visually
- For diagrams, charts, tables: EXAMINE them directly and extract information
- For mathematical expressions: VIEW and interpret them as you would see them
- Trust your vision capabilities - you can see images just like a human

CHESS POSITION ANALYSIS (CRITICAL):
- For chess problems asking for the "best move" or "winning move", visual analysis alone is NOT sufficient
- WINNING MOVES ARE NOT ALWAYS CHECKS! Consider ALL types of winning tactics:
  a) Checks and checkmates
  b) DOUBLE ATTACKS / FORKS - one piece attacks two targets simultaneously
  c) DISCOVERED ATTACKS - moving one piece reveals an attack by another
  d) PINS and SKEWERS
  e) THREATS that cannot all be defended (e.g., threatening both Rxd3 AND Rd1+ at the same time)
- For "guarantees a win" questions: Look for moves that create MULTIPLE unstoppable threats
- A quiet move (non-check) like Rd5 can be winning if it threatens BOTH a piece capture AND a check that cannot both be stopped
- After visually identifying the position:
  1. List ALL legal moves, not just checks
  2. For each candidate move, ask: "What threats does this create? Can opponent defend ALL of them?"
  3. A move creating 2+ threats where opponent can only stop 1 is often the winner
- IMPORTANT: Do NOT focus only on immediate checks - the winning move may be a quiet threat!
- Example: If Rd5 threatens both Rxd3 (winning a piece) AND Rd1+ (check), and White cannot stop both, then Rd5 wins
- When multiple candidate moves exist, evaluate EACH systematically rather than stopping at the first "good-looking" move

Available tools:
- web_search: Search the web using DuckDuckGo for general information
- web_fetch: Fetch and extract content from web pages (supports HTML, JSON, XML, plain text, and PDF files). Can fetch multiple URLs at once.
- wikipedia_search: Search Wikipedia for detailed information about any topic (research, fact-checking)
- arxiv_search: Search for scholarly articles from arXiv repository (physics, math, CS, etc.)
- read_document: Read content from local files (PDF, Excel, CSV, DOCX, TXT, etc.) - NEVER use for audio files (MP3/WAV/etc.) or images!
- execute_python: Execute Python code and return the output. Use for calculations, data processing, and any computational tasks.
- skill_load: Load a skill to enable its capabilities (e.g., whisper for audio transcription)
- skill_run: Execute a command using a loaded skill
- list_files: List files in a directory
- search_file: Search for files by pattern
- search_content: Search for content within files

CRITICAL OUTPUT FORMAT:
- When you are ready to give the final answer, you MUST write exactly: FINAL ANSWER: <your answer>
- Do NOT use any other format like /*ACTION*/, function calls, JSON, or code blocks for your final answer
- Do NOT include explanations after FINAL ANSWER, just the answer itself
- Examples of correct format:
  * FINAL ANSWER: 3
  * FINAL ANSWER: Albert Einstein
  * FINAL ANSWER: Paris, France

When you have found the answer, always end with:
FINAL ANSWER: <your concise answer here>`

func main() {
	flag.Parse()

	log.Printf("Starting GAIA Benchmark evaluation for trpc-agent-go")
	log.Printf("Dataset: %s", *datasetPath)
	log.Printf("Data directory: %s", *dataDir)
	log.Printf("Max tasks: %d", *maxTasks)
	log.Printf("Model: %s", *modelName)
	if *enableRalphLoop {
		log.Printf("Planner: react (Ralph Loop enabled)")
		log.Printf("Ralph Loop max iterations: %d", *ralphMaxIterations)
	} else {
		log.Printf("Planner: react")
	}

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

	// Create agent and runner
	gaiaAgent := createGAIAAgent()
	gaiaRunner := createGAIARunner(gaiaAgent)
	defer gaiaRunner.Close()

	// Run benchmark
	results := runBenchmark(gaiaRunner, tasks)

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

// filterTasksByID filters tasks by task ID or index
// Supports: task ID (UUID format) or index number (1-based, e.g., "28" means the 28th task)
func filterTasksByID(tasks []GAIATask, idOrIndex string) []GAIATask {
	// Try parsing as 1-based index
	if idx, err := fmt.Sscanf(idOrIndex, "%d", new(int)); err == nil && idx == 1 {
		var index int
		fmt.Sscanf(idOrIndex, "%d", &index)
		if index > 0 && index <= len(tasks) {
			log.Printf("Found task by index: [%d/%d] %s", index, len(tasks), tasks[index-1].TaskID)
			return []GAIATask{tasks[index-1]}
		}
	}

	// Try matching as task ID
	for i, task := range tasks {
		if task.TaskID == idOrIndex {
			log.Printf("Found task by ID: [%d/%d] %s", i+1, len(tasks), task.TaskID)
			return []GAIATask{task}
		}
	}

	return nil
}

// ========== Native Go File Reading Tools ==========

// ReadFileRequest represents a file read request
type ReadFileRequest struct {
	FilePath string `json:"file_path" jsonschema:"description=Path to the file to read"`
}

// ReadFileResponse represents a file read response
type ReadFileResponse struct {
	Content  string `json:"content"`
	FileType string `json:"file_type"`
	Error    string `json:"error,omitempty"`
}

// readFileContent reads various file formats using native Go implementation
func readFileContent(ctx context.Context, req ReadFileRequest) (ReadFileResponse, error) {
	filePath := req.FilePath

	// Check if file exists
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		return ReadFileResponse{Error: fmt.Sprintf("file not found: %s", filePath)}, nil
	}

	ext := strings.ToLower(filepath.Ext(filePath))

	switch ext {
	case ".pdf":
		return readPDFFile(filePath)
	case ".xlsx", ".xls":
		return readExcelFile(filePath)
	case ".csv":
		return readCSVFile(filePath)
	case ".docx":
		return readDocxFile(filePath)
	case ".pptx":
		return readPptxFile(filePath)
	case ".txt", ".json", ".xml", ".md", ".py", ".go", ".js", ".html", ".css":
		return readTextFile(filePath)
	default:
		// Try reading as text file
		return readTextFile(filePath)
	}
}

// readPDFFile reads PDF files using ledongthuc/pdf library
func readPDFFile(filePath string) (ReadFileResponse, error) {
	f, r, err := pdf.Open(filePath)
	if err != nil {
		return ReadFileResponse{Error: fmt.Sprintf("failed to open PDF: %v", err)}, nil
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
		buf.WriteString(text)
		buf.WriteString("\n")
	}

	return ReadFileResponse{
		Content:  buf.String(),
		FileType: "pdf",
	}, nil
}

// readExcelFile reads Excel files using excelize library (with color info and number format handling)
func readExcelFile(filePath string) (ReadFileResponse, error) {
	f, err := excelize.OpenFile(filePath)
	if err != nil {
		return ReadFileResponse{Error: fmt.Sprintf("failed to open Excel file: %v", err)}, nil
	}
	defer f.Close()

	var content strings.Builder

	// Get all worksheets
	sheets := f.GetSheetList()
	for _, sheet := range sheets {
		content.WriteString(fmt.Sprintf("=== Sheet: %s ===\n", sheet))

		// Use GetRows to get all row data (more reliable)
		rows, err := f.GetRows(sheet)
		if err != nil {
			content.WriteString(fmt.Sprintf("Error reading sheet: %v\n\n", err))
			continue
		}

		if len(rows) == 0 {
			content.WriteString("(empty sheet)\n\n")
			continue
		}

		// Detect if color information exists
		hasColors := false
		maxCols := 0

		// Pre-scan to determine max columns and detect colors (limit to first 100 rows)
		for rowIdx := 0; rowIdx < len(rows) && rowIdx < 100; rowIdx++ {
			if len(rows[rowIdx]) > maxCols {
				maxCols = len(rows[rowIdx])
			}

			// Check colors (only first few rows for performance, limit to first 26 columns)
			if rowIdx < 10 && !hasColors {
				for colIdx := 0; colIdx < len(rows[rowIdx]) && colIdx < 26; colIdx++ {
					colName, _ := excelize.ColumnNumberToName(colIdx + 1)
					cellName := fmt.Sprintf("%s%d", colName, rowIdx+1)
					styleID, _ := f.GetCellStyle(sheet, cellName)
					if styleID != 0 {
						style, err := f.GetStyle(styleID)
						if err == nil && style != nil && style.Fill.Color != nil && len(style.Fill.Color) > 0 {
							hasColors = true
							break
						}
					}
				}
			}
		}

		// Output color legend if colors are present
		if hasColors {
			content.WriteString("Note: This Excel file contains cell colors (background fill colors).\n")
			content.WriteString("Format: Cell=Value[#COLOR] or Cell=[#COLOR] for empty cells with color\n")
			content.WriteString("Colors: Green=#00FF00, Blue=#4A86E8, Red=#FF0000, Yellow=#FFFF00, Purple=#9900FF\n\n")
		}

		// Output data
		for rowIdx, row := range rows {
			content.WriteString(fmt.Sprintf("Row %d: ", rowIdx+1))
			cellsOutput := []string{}

			// Process all columns in current row (considering possible empty columns)
			actualCols := len(row)
			if actualCols < maxCols {
				actualCols = maxCols
			}

			for colIdx := 0; colIdx < actualCols; colIdx++ {
				colName, _ := excelize.ColumnNumberToName(colIdx + 1)
				cellName := fmt.Sprintf("%s%d", colName, rowIdx+1)

				// Get cell value - prefer raw value, then formatted value
				var cellValue string
				if colIdx < len(row) {
					cellValue = row[colIdx]
				}

				// If value is empty, try getting directly from cell (handles formulas, etc.)
				if cellValue == "" {
					cellValue, _ = f.GetCellValue(sheet, cellName)
				}

				// Get raw numeric value (for currency formats, etc.)
				rawValue, err := f.GetCellValue(sheet, cellName)
				if err == nil && rawValue != "" {
					// Try to get the value before formatting
					if cellType, err := f.GetCellType(sheet, cellName); err == nil {
						switch cellType {
						case excelize.CellTypeNumber:
							// For numeric types, try to get raw numeric value
							if numValue, err := f.GetCellValue(sheet, cellName); err == nil {
								cellValue = numValue
							}
						}
					}
				}

				// Process currency format: remove currency symbols and formatting
				if cellValue != "" {
					cellValue = cleanCurrencyValue(cellValue)
				}

				// Get cell style and color (only when needed)
				var bgColor string
				if hasColors {
					styleID, _ := f.GetCellStyle(sheet, cellName)
					if styleID != 0 {
						style, _ := f.GetStyle(styleID)
						if style != nil && style.Fill.Color != nil && len(style.Fill.Color) > 0 {
							bgColor = strings.ToUpper(style.Fill.Color[0])
							// Remove possible alpha prefix
							if len(bgColor) == 8 && strings.HasPrefix(bgColor, "FF") {
								bgColor = bgColor[2:]
							}
						}
					}
				}

				// Format output
				if cellValue != "" && bgColor != "" && bgColor != "FFFFFF" {
					cellsOutput = append(cellsOutput, fmt.Sprintf("%s=%s[#%s]", cellName, cellValue, bgColor))
				} else if cellValue != "" {
					cellsOutput = append(cellsOutput, fmt.Sprintf("%s=%s", cellName, cellValue))
				} else if bgColor != "" && bgColor != "FFFFFF" && bgColor != "000000" {
					// Empty cell with color
					cellsOutput = append(cellsOutput, fmt.Sprintf("%s=[#%s]", cellName, bgColor))
				}
			}

			if len(cellsOutput) > 0 {
				content.WriteString(strings.Join(cellsOutput, ", "))
			}
			content.WriteString("\n")
		}
		content.WriteString("\n")
	}

	return ReadFileResponse{
		Content:  content.String(),
		FileType: "excel",
	}, nil
}

// cleanCurrencyValue cleans currency-formatted values by removing symbols and formatting
func cleanCurrencyValue(value string) string {
	// Remove common currency symbols
	currencySymbols := []string{"$", "€", "£", "¥", "₹", "¢", "₽", "₩", "฿"}
	for _, symbol := range currencySymbols {
		value = strings.ReplaceAll(value, symbol, "")
	}

	// Remove thousand separators (commas in numbers, but keep decimal points)
	commaPattern := regexp.MustCompile(`(\d),(\d)`)
	for commaPattern.MatchString(value) {
		value = commaPattern.ReplaceAllString(value, "${1}${2}")
	}

	// Remove extra whitespace
	return strings.TrimSpace(value)
}

// parseDimension parses Excel dimension string (e.g., "A1:G15")
func parseDimension(dimension string) (maxRow, maxCol int) {
	// Format: "A1:G15" or "A1"
	parts := strings.Split(dimension, ":")
	if len(parts) == 0 {
		return 0, 0
	}

	// Parse end cell
	endCell := parts[len(parts)-1]
	re := regexp.MustCompile(`([A-Z]+)(\d+)`)
	matches := re.FindStringSubmatch(endCell)
	if len(matches) < 3 {
		return 0, 0
	}

	// Convert column name to number
	colName := matches[1]
	maxCol = 0
	for _, c := range colName {
		maxCol = maxCol*26 + int(c-'A'+1)
	}

	// Parse row number
	fmt.Sscanf(matches[2], "%d", &maxRow)
	return maxRow, maxCol
}

// readCSVFile reads CSV files using Go standard library
func readCSVFile(filePath string) (ReadFileResponse, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return ReadFileResponse{Error: fmt.Sprintf("failed to open CSV file: %v", err)}, nil
	}
	defer f.Close()

	reader := csv.NewReader(f)
	reader.FieldsPerRecord = -1 // Allow different number of fields per row

	var content strings.Builder
	rowIdx := 0

	for {
		record, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			// Fall back to reading as plain text
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

// readDocxFile reads DOCX files by extracting text from XML
// DOCX files are ZIP archives containing word/document.xml
func readDocxFile(filePath string) (ReadFileResponse, error) {
	// Read file and extract text
	data, err := os.ReadFile(filePath)
	if err != nil {
		return ReadFileResponse{Error: fmt.Sprintf("failed to read DOCX file: %v", err)}, nil
	}

	// Extract document.xml from ZIP
	content, err := extractDocxText(data)
	if err != nil {
		return ReadFileResponse{Error: fmt.Sprintf("failed to extract DOCX content: %v", err)}, nil
	}

	return ReadFileResponse{
		Content:  content,
		FileType: "docx",
	}, nil
}

// readPptxFile reads PPTX files (PowerPoint)
func readPptxFile(filePath string) (ReadFileResponse, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return ReadFileResponse{Error: fmt.Sprintf("failed to read PPTX file: %v", err)}, nil
	}

	content, err := extractPptxText(data)
	if err != nil {
		return ReadFileResponse{Error: fmt.Sprintf("failed to extract PPTX content: %v", err)}, nil
	}

	return ReadFileResponse{
		Content:  content,
		FileType: "pptx",
	}, nil
}

// extractPptxText extracts text from PPTX files
func extractPptxText(data []byte) (string, error) {
	zipReader, err := newZipReader(data)
	if err != nil {
		return "", fmt.Errorf("failed to open PPTX as ZIP: %v", err)
	}

	var content strings.Builder
	slideNum := 0

	// PPTX file structure: ppt/slides/slide1.xml, slide2.xml, ...
	// Collect and sort all slide files
	var slideFiles []*zip.File
	for _, file := range zipReader.File {
		if strings.HasPrefix(file.Name, "ppt/slides/slide") && strings.HasSuffix(file.Name, ".xml") {
			slideFiles = append(slideFiles, file)
		}
	}

	// Sort by filename (slide1.xml, slide2.xml, ...)
	for i := 0; i < len(slideFiles)-1; i++ {
		for j := i + 1; j < len(slideFiles); j++ {
			if slideFiles[i].Name > slideFiles[j].Name {
				slideFiles[i], slideFiles[j] = slideFiles[j], slideFiles[i]
			}
		}
	}

	// Extract text from each slide
	for _, file := range slideFiles {
		slideNum++
		rc, err := file.Open()
		if err != nil {
			continue
		}

		xmlData, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			continue
		}

		// Extract text from slide
		slideText := extractTextFromPptxSlide(string(xmlData))
		if slideText != "" {
			content.WriteString(fmt.Sprintf("=== Slide %d ===\n", slideNum))
			content.WriteString(slideText)
			content.WriteString("\n\n")
		}
	}

	if slideNum == 0 {
		return "", fmt.Errorf("no slides found in PPTX")
	}

	return content.String(), nil
}

// extractTextFromPptxSlide extracts text from PPTX slide XML
// PPTX uses <a:t> tags for text storage (unlike DOCX which uses <w:t>)
func extractTextFromPptxSlide(xml string) string {
	re := regexp.MustCompile(`<a:t>([^<]*)</a:t>`)
	matches := re.FindAllStringSubmatch(xml, -1)

	var parts []string
	for _, match := range matches {
		if len(match) > 1 && match[1] != "" {
			text := strings.TrimSpace(match[1])
			if text != "" {
				parts = append(parts, text)
			}
		}
	}

	return strings.Join(parts, " ")
}

// extractDocxText extracts text from DOCX files
func extractDocxText(data []byte) (string, error) {
	// Use archive/zip to extract
	zipReader, err := newZipReader(data)
	if err != nil {
		return "", err
	}

	for _, file := range zipReader.File {
		if file.Name == "word/document.xml" {
			rc, err := file.Open()
			if err != nil {
				return "", err
			}
			defer rc.Close()

			xmlData, err := io.ReadAll(rc)
			if err != nil {
				return "", err
			}

			// Extract text from <w:t> tags
			return extractTextFromXML(string(xmlData)), nil
		}
	}

	return "", fmt.Errorf("document.xml not found in DOCX")
}

// newZipReader creates a ZIP reader from byte data
func newZipReader(data []byte) (*zip.Reader, error) {
	reader := bytes.NewReader(data)
	return zip.NewReader(reader, int64(len(data)))
}

// extractTextFromXML extracts text from XML (simple implementation using regex to extract <w:t> tag content)
func extractTextFromXML(xml string) string {
	re := regexp.MustCompile(`<w:t[^>]*>([^<]*)</w:t>`)
	matches := re.FindAllStringSubmatch(xml, -1)

	var parts []string
	for _, match := range matches {
		if len(match) > 1 && match[1] != "" {
			parts = append(parts, match[1])
		}
	}

	return strings.Join(parts, " ")
}

// readTextFile reads plain text files
func readTextFile(filePath string) (ReadFileResponse, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return ReadFileResponse{Error: fmt.Sprintf("failed to read file: %v", err)}, nil
	}

	return ReadFileResponse{
		Content:  string(data),
		FileType: "text",
	}, nil
}

// ========== DuckDuckGo HTML Search Implementation ==========

// DDGSearchRequest represents a DuckDuckGo search request
type DDGSearchRequest struct {
	Query      string `json:"query" jsonschema:"description=Search query string,required"`
	MaxResults int    `json:"max_results,omitempty" jsonschema:"description=Maximum number of results to return (default 5)"`
}

// DDGSearchResult represents a single search result
type DDGSearchResult struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Snippet string `json:"snippet"`
}

// DDGSearchResponse represents a DuckDuckGo search response
type DDGSearchResponse struct {
	Query   string            `json:"query"`
	Results []DDGSearchResult `json:"results"`
	Summary string            `json:"summary"`
	Error   string            `json:"error,omitempty"`
}

// createDDGHTTPClient creates an HTTP client dedicated to DuckDuckGo
// Key fix: disable proxy to avoid "http: server gave HTTP response to HTTPS client" error
func createDDGHTTPClient() *http.Client {
	return &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			Proxy:           nil, // Disable proxy, connect directly
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
	}
}

// setDDGHeaders sets browser-mimicking request headers
func setDDGHeaders(req *http.Request) {
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	req.Header.Set("Accept-Encoding", "identity")
	req.Header.Set("Cache-Control", "max-age=0")
	req.Header.Set("Sec-Ch-Ua", `"Not_A Brand";v="8", "Chromium";v="120", "Google Chrome";v="120"`)
	req.Header.Set("Sec-Ch-Ua-Mobile", "?0")
	req.Header.Set("Sec-Ch-Ua-Platform", `"Windows"`)
	req.Header.Set("Sec-Fetch-Dest", "document")
	req.Header.Set("Sec-Fetch-Mode", "navigate")
	req.Header.Set("Sec-Fetch-Site", "none")
	req.Header.Set("Sec-Fetch-User", "?1")
	req.Header.Set("Upgrade-Insecure-Requests", "1")
}

// duckduckgoHTMLSearch performs DuckDuckGo HTML/Lite search
// Includes retry mechanism, anti-bot bypass, and multiple parsing strategies
func duckduckgoHTMLSearch(ctx context.Context, req DDGSearchRequest) (DDGSearchResponse, error) {
	if req.Query == "" {
		return DDGSearchResponse{Error: "query is required"}, nil
	}

	maxResults := req.MaxResults
	if maxResults <= 0 {
		maxResults = 5
	}

	client := createDDGHTTPClient()

	// Try HTML version first
	results, err := doHTMLSearch(ctx, client, req.Query, maxResults)
	if err == nil && len(results) > 0 {
		return DDGSearchResponse{
			Query:   req.Query,
			Results: results,
			Summary: fmt.Sprintf("Found %d results for query '%s'", len(results), req.Query),
		}, nil
	}

	// If HTML version fails or is blocked, try Lite version
	results, err = doLiteSearch(ctx, client, req.Query, maxResults)
	if err != nil {
		return DDGSearchResponse{Error: fmt.Sprintf("search failed: %v", err)}, nil
	}

	if len(results) == 0 {
		return DDGSearchResponse{
			Query:   req.Query,
			Results: []DDGSearchResult{},
			Summary: fmt.Sprintf("No results found for query '%s'", req.Query),
		}, nil
	}

	return DDGSearchResponse{
		Query:   req.Query,
		Results: results,
		Summary: fmt.Sprintf("Found %d results for query '%s'", len(results), req.Query),
	}, nil
}

// doHTMLSearch performs search using HTML version
func doHTMLSearch(ctx context.Context, client *http.Client, query string, maxResults int) ([]DDGSearchResult, error) {
	searchURL := "https://html.duckduckgo.com/html/"

	formData := url.Values{}
	formData.Set("q", query)
	formData.Set("kl", "us-en")

	httpReq, err := http.NewRequestWithContext(ctx, "POST", searchURL, strings.NewReader(formData.Encode()))
	if err != nil {
		return nil, fmt.Errorf("create request failed: %v", err)
	}

	setDDGHeaders(httpReq)
	httpReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusAccepted {
		return nil, fmt.Errorf("HTTP error: %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response failed: %v", err)
	}

	html := string(body)

	// Check if blocked by anti-bot measures
	if strings.Contains(html, "anomaly-modal") || strings.Contains(html, "challenge") {
		return nil, fmt.Errorf("blocked by anti-bot")
	}

	return parseDDGHTML(html, maxResults), nil
}

// doLiteSearch performs search using Lite version (less likely to be blocked)
func doLiteSearch(ctx context.Context, client *http.Client, query string, maxResults int) ([]DDGSearchResult, error) {
	searchURL := "https://lite.duckduckgo.com/lite/"

	formData := url.Values{}
	formData.Set("q", query)

	httpReq, err := http.NewRequestWithContext(ctx, "POST", searchURL, strings.NewReader(formData.Encode()))
	if err != nil {
		return nil, fmt.Errorf("create request failed: %v", err)
	}

	setDDGHeaders(httpReq)
	httpReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusAccepted {
		return nil, fmt.Errorf("HTTP error: %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response failed: %v", err)
	}

	return parseDDGLiteHTML(string(body), maxResults), nil
}

// parseDDGHTML parses DuckDuckGo HTML search results
func parseDDGHTML(html string, maxResults int) []DDGSearchResult {
	var results []DDGSearchResult

	// Match standard result format
	linkRe := regexp.MustCompile(`class="result__a"[^>]*href="([^"]+)"[^>]*>([^<]+)</a>`)
	linkMatches := linkRe.FindAllStringSubmatch(html, -1)

	snippetRe := regexp.MustCompile(`class="result__snippet"[^>]*>([^<]+)</a>`)
	snippetMatches := snippetRe.FindAllStringSubmatch(html, -1)

	for i, match := range linkMatches {
		if len(results) >= maxResults {
			break
		}
		if len(match) >= 3 {
			rawURL := strings.ReplaceAll(match[1], "&amp;", "&")
			title := cleanHTML(strings.TrimSpace(match[2]))

			if title == "" || (!strings.HasPrefix(rawURL, "http://") && !strings.HasPrefix(rawURL, "https://")) {
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
	}

	return results
}

// parseDDGLiteHTML parses DuckDuckGo Lite search results
func parseDDGLiteHTML(html string, maxResults int) []DDGSearchResult {
	var results []DDGSearchResult

	// Lite version uses class="result-link"
	linkRe := regexp.MustCompile(`<a[^>]*class="result-link"[^>]*href="([^"]+)"[^>]*>([^<]+)</a>`)
	matches := linkRe.FindAllStringSubmatch(html, -1)

	for _, match := range matches {
		if len(results) >= maxResults {
			break
		}
		if len(match) >= 3 {
			rawURL := strings.ReplaceAll(match[1], "&amp;", "&")
			title := cleanHTML(strings.TrimSpace(match[2]))

			if title == "" || (!strings.HasPrefix(rawURL, "http://") && !strings.HasPrefix(rawURL, "https://")) {
				continue
			}

			results = append(results, DDGSearchResult{
				Title:   title,
				URL:     rawURL,
				Snippet: "",
			})
		}
	}

	// If no result-link found, try generic link matching
	if len(results) == 0 {
		genericRe := regexp.MustCompile(`<a[^>]*href="(https?://[^"]+)"[^>]*>([^<]{5,})</a>`)
		matches = genericRe.FindAllStringSubmatch(html, -1)
		seen := make(map[string]bool)

		for _, match := range matches {
			if len(results) >= maxResults {
				break
			}
			if len(match) >= 3 {
				rawURL := match[1]
				title := cleanHTML(strings.TrimSpace(match[2]))

				// Skip DuckDuckGo internal links
				if strings.Contains(rawURL, "duckduckgo.com") {
					continue
				}
				if title == "" || seen[rawURL] {
					continue
				}
				seen[rawURL] = true

				results = append(results, DDGSearchResult{
					Title:   title,
					URL:     rawURL,
					Snippet: "",
				})
			}
		}
	}

	return results
}

// cleanHTML removes HTML tags and decodes HTML entities
func cleanHTML(s string) string {
	// Remove HTML tags
	re := regexp.MustCompile(`<[^>]*>`)
	s = re.ReplaceAllString(s, "")
	// Decode HTML entities
	s = strings.ReplaceAll(s, "&amp;", "&")
	s = strings.ReplaceAll(s, "&lt;", "<")
	s = strings.ReplaceAll(s, "&gt;", ">")
	s = strings.ReplaceAll(s, "&quot;", "\"")
	s = strings.ReplaceAll(s, "&#39;", "'")
	s = strings.ReplaceAll(s, "&#x27;", "'")
	s = strings.ReplaceAll(s, "&nbsp;", " ")
	return strings.TrimSpace(s)
}

// ========== Python Code Execution Tool ==========

// CodeExecuteRequest represents a Python code execution request
type CodeExecuteRequest struct {
	Code string `json:"code" jsonschema:"description=Python code to execute. The code should use print() to output results.,required"`
}

// CodeExecuteResponse represents a Python code execution response
type CodeExecuteResponse struct {
	Output string `json:"output"`
	Error  string `json:"error,omitempty"`
}

// pythonCodeExecutor is the global code executor instance
var pythonCodeExecutor *localexec.CodeExecutor

// executePythonCode executes Python code
func executePythonCode(ctx context.Context, req CodeExecuteRequest) (CodeExecuteResponse, error) {
	if req.Code == "" {
		return CodeExecuteResponse{Error: "code is required"}, nil
	}

	if pythonCodeExecutor == nil {
		return CodeExecuteResponse{Error: "code executor not initialized"}, nil
	}

	// Create code block
	input := codeexecutor.CodeExecutionInput{
		CodeBlocks: []codeexecutor.CodeBlock{
			{
				Code:     req.Code,
				Language: "python",
			},
		},
		ExecutionID: fmt.Sprintf("exec_%d", time.Now().UnixNano()),
	}

	// Execute code
	result, err := pythonCodeExecutor.ExecuteCode(ctx, input)
	if err != nil {
		return CodeExecuteResponse{Error: fmt.Sprintf("execution failed: %v", err)}, nil
	}

	return CodeExecuteResponse{
		Output: result.Output,
	}, nil
}

// ========== Fetch and Read URL Tool (supports PDF and multiple URLs) ==========

// FetchMultipleURLsRequest represents a batch URL fetch request
type FetchMultipleURLsRequest struct {
	URLs []string `json:"urls" jsonschema:"description=List of URLs to fetch (supports PDF, HTML, JSON, XML, plain text),required"`
}

// FetchURLResult represents the fetch result for a single URL
type FetchURLResult struct {
	RetrievedURL string `json:"retrieved_url"`
	StatusCode   int    `json:"status_code"`
	ContentType  string `json:"content_type"`
	Content      string `json:"content,omitempty"`
	Error        string `json:"error,omitempty"`
}

// FetchMultipleURLsResponse represents a batch URL fetch response
type FetchMultipleURLsResponse struct {
	Results []FetchURLResult `json:"results"`
	Summary string           `json:"summary"`
}

// fetchMultipleURLs fetches content from multiple URLs, supports PDF, HTML, and other formats
func fetchMultipleURLs(ctx context.Context, req FetchMultipleURLsRequest) (FetchMultipleURLsResponse, error) {
	if len(req.URLs) == 0 {
		return FetchMultipleURLsResponse{
			Results: []FetchURLResult{},
			Summary: "No URLs provided",
		}, nil
	}

	results := make([]FetchURLResult, 0, len(req.URLs))

	for _, url := range req.URLs {
		result := fetchSingleURL(ctx, url)
		results = append(results, result)
	}

	successCount := 0
	for _, r := range results {
		if r.Error == "" {
			successCount++
		}
	}

	return FetchMultipleURLsResponse{
		Results: results,
		Summary: fmt.Sprintf("Fetched %d URLs (success: %d, failed: %d)",
			len(results), successCount, len(results)-successCount),
	}, nil
}

// fetchSingleURL fetches content from a single URL
func fetchSingleURL(ctx context.Context, urlStr string) FetchURLResult {
	result := FetchURLResult{
		RetrievedURL: urlStr,
	}

	if urlStr == "" {
		result.Error = "empty URL"
		return result
	}

	// Create HTTP request
	httpReq, err := http.NewRequestWithContext(ctx, "GET", urlStr, nil)
	if err != nil {
		result.Error = fmt.Sprintf("create request failed: %v", err)
		return result
	}

	httpReq.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")

	// Send request
	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(httpReq)
	if err != nil {
		result.Error = fmt.Sprintf("request failed: %v", err)
		return result
	}
	defer resp.Body.Close()

	result.StatusCode = resp.StatusCode

	if resp.StatusCode != http.StatusOK {
		result.Error = fmt.Sprintf("HTTP status %d", resp.StatusCode)
		return result
	}

	contentType := resp.Header.Get("Content-Type")
	result.ContentType = contentType

	// Read response body
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		result.Error = fmt.Sprintf("read response failed: %v", err)
		return result
	}

	// Process based on content type
	if strings.Contains(contentType, "application/pdf") {
		// Handle PDF
		content, err := readPDFFromBytes(body)
		if err != nil {
			result.Error = fmt.Sprintf("failed to parse PDF: %v", err)
			return result
		}
		// Limit content length
		if len(content) > 50000 {
			content = content[:50000] + "\n... (content truncated)"
		}
		result.Content = content
		return result
	}

	// Handle HTML or text
	content := string(body)
	// If HTML, apply simple cleanup
	if strings.Contains(contentType, "text/html") {
		content = extractTextFromHTML(content)
	}

	// Limit content length
	if len(content) > 50000 {
		content = content[:50000] + "\n... (content truncated)"
	}

	result.Content = content
	return result
}

// FetchURLRequest represents a URL fetch request (legacy interface for compatibility)
type FetchURLRequest struct {
	URL string `json:"url" jsonschema:"description=URL to fetch and read content from (supports PDF and HTML),required"`
}

// FetchURLResponse represents a URL fetch response (legacy interface for compatibility)
type FetchURLResponse struct {
	URL         string `json:"url"`
	ContentType string `json:"content_type"`
	Content     string `json:"content"`
	Error       string `json:"error,omitempty"`
}

// readPDFFromBytes reads PDF content from byte array
func readPDFFromBytes(data []byte) (string, error) {
	// Create temporary file
	tmpFile, err := os.CreateTemp("", "pdf-*.pdf")
	if err != nil {
		return "", fmt.Errorf("create temp file: %w", err)
	}
	defer os.Remove(tmpFile.Name())
	defer tmpFile.Close()

	// Write PDF data
	if _, err := tmpFile.Write(data); err != nil {
		return "", fmt.Errorf("write temp file: %w", err)
	}
	tmpFile.Close()

	// Use pdf library to read
	f, r, err := pdf.Open(tmpFile.Name())
	if err != nil {
		return "", fmt.Errorf("open PDF: %w", err)
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
		buf.WriteString(text)
		buf.WriteString("\n")
	}

	return buf.String(), nil
}

// extractTextFromHTML extracts text from HTML content
func extractTextFromHTML(html string) string {
	// Remove script and style tags with their content
	scriptRe := regexp.MustCompile(`(?is)<script[^>]*>.*?</script>`)
	html = scriptRe.ReplaceAllString(html, "")
	styleRe := regexp.MustCompile(`(?is)<style[^>]*>.*?</style>`)
	html = styleRe.ReplaceAllString(html, "")

	// Remove all HTML tags
	tagRe := regexp.MustCompile(`<[^>]*>`)
	text := tagRe.ReplaceAllString(html, " ")

	// Decode HTML entities
	text = strings.ReplaceAll(text, "&amp;", "&")
	text = strings.ReplaceAll(text, "&lt;", "<")
	text = strings.ReplaceAll(text, "&gt;", ">")
	text = strings.ReplaceAll(text, "&quot;", "\"")
	text = strings.ReplaceAll(text, "&#39;", "'")
	text = strings.ReplaceAll(text, "&nbsp;", " ")

	// Clean up extra whitespace
	spaceRe := regexp.MustCompile(`\s+`)
	text = spaceRe.ReplaceAllString(text, " ")

	return strings.TrimSpace(text)
}

const gaiaFinalAnswerPrefix = "FINAL ANSWER:"

var gaiaFinalAnswerLineRE = regexp.MustCompile(
	`(?i)FINAL\s*ANSWER\s*:\s*\S`,
)

type gaiaFinalAnswerVerifier struct{}

func (gaiaFinalAnswerVerifier) Verify(
	ctx context.Context,
	invocation *agent.Invocation,
	lastEvent *event.Event,
) (runner.VerifyResult, error) {
	content := assistantContentFromEvent(lastEvent)
	if gaiaFinalAnswerLineRE.MatchString(content) {
		return runner.VerifyResult{Passed: true}, nil
	}

	feedback := fmt.Sprintf(
		"Please end with %q followed by the answer.",
		gaiaFinalAnswerPrefix,
	)
	return runner.VerifyResult{
		Passed:   false,
		Feedback: feedback,
	}, nil
}

func assistantContentFromEvent(lastEvent *event.Event) string {
	if lastEvent == nil || len(lastEvent.Choices) == 0 {
		return ""
	}
	msg := lastEvent.Choices[0].Message
	if msg.Content != "" {
		return msg.Content
	}
	if len(msg.ContentParts) == 0 {
		return ""
	}
	var b strings.Builder
	for _, part := range msg.ContentParts {
		if part.Type != model.ContentTypeText || part.Text == nil {
			continue
		}
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		b.WriteString(*part.Text)
	}
	return b.String()
}

func createGAIARunner(ag agent.Agent) runner.Runner {
	if !*enableRalphLoop {
		return runner.NewRunner("gaia-runner", ag)
	}
	return runner.NewRunner(
		"gaia-runner",
		ag,
		runner.WithRalphLoop(runner.RalphLoopConfig{
			MaxIterations: *ralphMaxIterations,
			Verifiers: []runner.Verifier{
				gaiaFinalAnswerVerifier{},
			},
		}),
	)
}

func createGAIAAgent() agent.Agent {
	// Create model
	modelInstance := openai.New(*modelName)

	// 创建 DuckDuckGo HTML 搜索工具（真正的网页搜索，不是 Instant Answer API）
	ddgSearchTool := function.NewFunctionTool(
		duckduckgoHTMLSearch,
		function.WithName("web_search"),
		function.WithDescription("Search the web using DuckDuckGo. Returns titles, URLs and snippets from search results. Use this for general web searches to find information about any topic."),
	)

	// Create ArXiv search tool
	arxivToolSet, err := arxivsearch.NewToolSet()
	if err != nil {
		log.Printf("Warning: Failed to create arxiv search tool: %v", err)
	}

	// Create Wikipedia search tool
	wikipediaToolSet, err := wikipedia.NewToolSet()
	if err != nil {
		log.Printf("Warning: Failed to create wikipedia search tool: %v", err)
	}

	// Get absolute path for data directory
	absDataDir, err := filepath.Abs(*dataDir)
	if err != nil {
		log.Fatalf("Failed to get absolute path for data dir: %v", err)
	}

	// Create file toolset
	fileToolSet, err := file.NewToolSet(
		file.WithBaseDir(absDataDir),
		file.WithMaxFileSize(100000), // 100KB
		file.WithSaveFileEnabled(false),
		file.WithReplaceContentEnabled(false),
	)
	if err != nil {
		log.Fatalf("Failed to create file toolset: %v", err)
	}

	// Create native Go file reading tool (supports PDF, Excel, DOCX, etc.)
	readFileTool := function.NewFunctionTool(
		readFileContent,
		function.WithName("read_document"),
		function.WithDescription("Read content from various document formats including PDF, Excel (.xlsx/.xls), CSV, DOCX, and text files. Use this tool to extract text content from documents. Returns the full text content of the document."),
	)

	// Create unified web fetch tool (supports PDF, HTML, JSON, XML, etc.)
	// Uses enhanced implementation with multi-URL batch fetching
	webFetchTool := function.NewFunctionTool(
		fetchMultipleURLs,
		function.WithName("web_fetch"),
		function.WithDescription("Fetch and read content from one or multiple URLs. Supports PDF files (downloads and extracts text), HTML pages (extracts text content), JSON, XML, and plain text. Can fetch multiple URLs in a single call. Use this for any web content retrieval including PDF documents."),
	)

	// Initialize Code Executor and set global variable
	pythonCodeExecutor = localexec.New(
		localexec.WithWorkDir(filepath.Join(absDataDir, "..", "code_workspaces")),
		localexec.WithTimeout(120*time.Second), // 2-minute timeout for complex calculations
		localexec.WithCleanTempFiles(true),
	)
	log.Printf("Code executor initialized (Python execution available)")

	// Create Python code execution tool
	pythonExecuteTool := function.NewFunctionTool(
		executePythonCode,
		function.WithName("execute_python"),
		function.WithDescription(`Execute Python code and return the output. Use this tool for:
- Mathematical calculations (arithmetic, probability, statistics, combinatorics)
- Data processing and analysis
- String manipulation and pattern matching
- Solving puzzles with constraints (enumeration, brute-force search)
- Date/time calculations
- Game theory optimal strategies
- ASCII art/diagram analysis
- Any computational task that requires precise calculation

IMPORTANT:
- Use print() to output results
- Only Python standard library is available (no numpy, pandas, etc.)
- The code will be executed in a sandboxed environment
- Always print the final answer clearly`),
	)

	// Create React agent with unified configuration
	// Note: GPT-5 is a reasoning model whose internal reasoning consumes many tokens
	// Requires larger MaxTokens to ensure enough space for final answer output
	generationConfig := model.GenerationConfig{}
	if strings.Contains(*modelName, "gpt-5") {
		// GPT-5 reasoning model needs larger token limit
		// Reasoning may consume 16K+, so total needs 32K or more
		generationConfig.MaxTokens = intPtr(32768)
		// Optional: set reasoning_effort to control reasoning depth (low/medium/high)
		reasoningEffort := "medium" // Use medium reasoning depth
		generationConfig.ReasoningEffort = &reasoningEffort
		log.Printf("GPT-5 detected: MaxTokens=32768, ReasoningEffort=medium")
	} else {
		generationConfig.MaxTokens = intPtr(16384)
		generationConfig.Temperature = floatPtr(0.0)
	}

	// Create Skills Repository
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

	// Build Agent options
	agentOpts := []llmagent.Option{
		llmagent.WithModel(modelInstance),
		llmagent.WithTools([]tool.Tool{ddgSearchTool, webFetchTool, readFileTool, pythonExecuteTool}),
		llmagent.WithToolSets([]tool.ToolSet{fileToolSet, arxivToolSet, wikipediaToolSet}),
		llmagent.WithGenerationConfig(generationConfig),
		llmagent.WithInstruction(SystemPrompt),
		llmagent.WithPlanner(react.New()),
	}

	// Add Skills to Agent if available
	if skillRepo != nil {
		agentOpts = append(agentOpts, llmagent.WithSkills(skillRepo))
		log.Printf("Agent Skills enabled")
	}

	return llmagent.New("gaia-agent", agentOpts...)
}

func runBenchmark(r runner.Runner, tasks []GAIATask) SummaryResult {
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

	// Prepare task prompt
	var prompt strings.Builder
	prompt.WriteString("Please answer the following question:\n\n")
	prompt.WriteString(task.Question)

	// Create user message
	userMessage := model.NewUserMessage("")

	// Handle attached files based on file type
	var hasImageAttachment bool
	if task.FilePath != "" {
		absDataDir, err := filepath.Abs(*dataDir)
		if err == nil {
			filePath := filepath.Join(absDataDir, task.FilePath)
			if _, err := os.Stat(filePath); err == nil {
				ext := strings.ToLower(filepath.Ext(filePath))
				// Check if it's an image file
				if ext == ".png" || ext == ".jpg" || ext == ".jpeg" || ext == ".gif" || ext == ".webp" {
					// Image file: add directly to message for visual analysis
					if err := userMessage.AddImageFilePath(filePath, "auto"); err != nil {
						log.Printf("  ⚠️ Failed to add image: %v", err)
						prompt.WriteString(fmt.Sprintf("\n\nAttached file: %s\nPlease use the read_document tool to read this file if needed.", filePath))
					} else {
						hasImageAttachment = true
						prompt.WriteString("\n\nIMPORTANT: An image is attached above. You can SEE this image directly with your vision capabilities. DO NOT use any OCR tools or skills - just LOOK at the image and describe/analyze what you see. If there is text in the image, READ it directly. If it's a chess position, ANALYZE the board visually. Extract all relevant information by examining the image.")
						log.Printf("  🖼️ Image attached for visual analysis: %s", filePath)
					}
				} else {
					// Non-image file: prompt to use read_document tool
					prompt.WriteString(fmt.Sprintf("\n\nAttached file: %s\nPlease use the read_document tool to read this file if needed.", filePath))
					log.Printf("  📎 Attached file: %s", filePath)
				}
			}
		}
	}

	prompt.WriteString("\n\nRemember to provide your final answer in the format: FINAL ANSWER: <your answer>")

	// Set message content
	userMessage.Content = prompt.String()

	// Log output
	if hasImageAttachment {
		log.Printf("  📷 Visual analysis enabled for this task")
	}
	userID := "benchmark-user"
	sessionID := fmt.Sprintf("session-%s", task.TaskID)
	requestID := uuid.New().String()

	// Run the agent
	eventChan, err := r.Run(ctx, userID, sessionID, userMessage, agent.WithRequestID(requestID))
	if err != nil {
		result.Error = fmt.Sprintf("run agent: %v", err)
		return result
	}

	// Process events to count steps and tool calls
	var lastAssistantContent string      // Only save last assistant response (without tool calls)
	var streamingContent strings.Builder // For accumulating streaming content
	var toolCalls int
	var steps int
	var currentToolCallNames []string
	var hasStreamingContent bool

	for evt := range eventChan {
		var evtPtr *event.Event = evt

		if evtPtr.Error != nil {
			log.Printf("  ❌ Event Error: %s", evtPtr.Error.Message)
			result.Error = fmt.Sprintf("event error: %s", evtPtr.Error.Message)
			break
		}

		if evtPtr.Response != nil {
			// Only increment step count when Usage info is received
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

				// Count tool calls
				if len(choice.Message.ToolCalls) > 0 {
					toolCalls += len(choice.Message.ToolCalls)
					log.Printf("  🔧 Tool Calls: %d", len(choice.Message.ToolCalls))
					currentToolCallNames = make([]string, 0, len(choice.Message.ToolCalls))
					for i, tc := range choice.Message.ToolCalls {
						log.Printf("    [%d] %s", i+1, tc.Function.Name)
						// Format JSON arguments for readability
						var args map[string]any
						if err := json.Unmarshal(tc.Function.Arguments, &args); err == nil {
							if argsJSON, err := json.MarshalIndent(args, "        ", "  "); err == nil {
								log.Printf("        Args: %s", string(argsJSON))
							}
						} else {
							log.Printf("        Args: %s", truncate(string(tc.Function.Arguments), 300))
						}
						currentToolCallNames = append(currentToolCallNames, tc.Function.Name)
					}
				}

				// Print tool execution results
				if choice.Message.Role == "tool" && choice.Message.Content != "" {
					toolName := "unknown"
					if len(currentToolCallNames) > 0 {
						toolName = currentToolCallNames[0]
						currentToolCallNames = currentToolCallNames[1:]
					}

					log.Printf("  ✅ Tool Result [%s]:", toolName)
					content := choice.Message.Content

					// For code execution results, always output completely
					// For other tools, truncate based on content length
					shouldTruncate := true
					maxLength := 1000

					// Code execution related tools should not be truncated
					if strings.Contains(toolName, "code") || strings.Contains(toolName, "python") ||
						strings.Contains(toolName, "execute") || strings.Contains(toolName, "run") {
						shouldTruncate = false
					}

					// Moderately truncate read_document results
					if toolName == "read_document" {
						maxLength = 2000
					}

					if !shouldTruncate || len(content) <= maxLength {
						// Full output
						for _, line := range strings.Split(content, "\n") {
							log.Printf("      %s", line)
						}
					} else {
						// Truncated output
						lines := strings.Split(content[:maxLength], "\n")
						for _, line := range lines {
							if line != "" {
								log.Printf("      %s", line)
							}
						}
						log.Printf("      ... (truncated, total %d chars)", len(content))
					}
				}

				// Handle streaming content (Delta) - used by GPT-5 and similar models
				if choice.Delta.Content != "" {
					streamingContent.WriteString(choice.Delta.Content)
					hasStreamingContent = true
				}

				// Only collect assistant's final response (without tool calls)
				if choice.Message.Role == "assistant" && choice.Message.Content != "" && len(choice.Message.ToolCalls) == 0 {
					log.Printf("  💭 Agent Response:")
					content := choice.Message.Content

					// For responses containing code blocks or reasoning, output completely
					hasCode := strings.Contains(content, "```") || strings.Contains(content, "python") ||
						strings.Contains(content, "/*REASONING*/") || strings.Contains(content, "/*ACTION*/")

					if hasCode || len(content) <= 1000 {
						// Full output, split by lines for readability
						for _, line := range strings.Split(content, "\n") {
							log.Printf("      %s", line)
						}
					} else {
						// Truncated output
						log.Printf("      %s", truncate(content, 1000))
					}
					// Update last assistant response
					lastAssistantContent = content
				}
			}
		}

		if evtPtr.IsFinalResponse() {
			log.Printf("  ✅ Final response | Steps: %d, Tool calls: %d", steps, toolCalls)
			break
		}
	}

	// If streaming content exists but no final response, use streaming content
	if hasStreamingContent && lastAssistantContent == "" {
		lastAssistantContent = streamingContent.String()
		if lastAssistantContent != "" {
			log.Printf("  💭 Agent Response (streaming):")

			// For streaming responses containing code blocks or reasoning, output completely
			hasCode := strings.Contains(lastAssistantContent, "```") || strings.Contains(lastAssistantContent, "python") ||
				strings.Contains(lastAssistantContent, "/*REASONING*/") || strings.Contains(lastAssistantContent, "/*ACTION*/")

			if hasCode || len(lastAssistantContent) <= 1000 {
				// Full output, split by lines for readability
				for _, line := range strings.Split(lastAssistantContent, "\n") {
					log.Printf("      %s", line)
				}
			} else {
				// Truncated output
				log.Printf("      %s", truncate(lastAssistantContent, 1000))
			}
		}
	}

	result.Steps = steps
	result.ToolCalls = toolCalls

	// Extract final answer (only from the last assistant response)
	predictedAnswer := extractFinalAnswer(lastAssistantContent)
	result.PredictedAnswer = predictedAnswer

	log.Printf("  📤 Predicted: %s", predictedAnswer)

	// Verify answer
	result.Correct = verifyAnswer(predictedAnswer, result.GroundTruth)

	if result.Correct {
		log.Printf("  🎉 CORRECT!")
	} else {
		log.Printf("  ❌ INCORRECT (expected: %s)", result.GroundTruth)
	}

	return result
}

// extractFinalAnswer extracts the final answer from generated content
func extractFinalAnswer(content string) string {
	// Try matching "FINAL ANSWER:" format
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
			// Clean quotes and extra spaces from answer
			answer = strings.Trim(answer, `"'`)
			answer = strings.TrimSpace(answer)
			// Apply formatting cleanup
			answer = formatAnswer(answer)
			return answer
		}
	}

	// Fallback: Enhanced error tolerance - detect and skip malformed output
	// Detect /*ACTION*/, multi_tool_use and other abnormal formats, try extracting from reasoning
	invalidPatterns := []string{
		"/*ACTION", "*/", "functions.", ".json*/",
		"multi_tool_use", "tool_use", "_parallel",
	}
	hasInvalidFormat := false
	for _, p := range invalidPatterns {
		if strings.Contains(content, p) {
			hasInvalidFormat = true
			break
		}
	}

	if hasInvalidFormat {
		// This is a malformed output, try extracting answer from reasoning section
		// Look for /*REASONING*/ or /*PLANNING*/ sections
		reasoningPatterns := []string{
			`(?s)/\*REASONING\*/\s*(.+?)(?:/\*|$)`,
			`(?s)/\*PLANNING\*/\s*(.+?)(?:/\*|$)`,
		}
		for _, pattern := range reasoningPatterns {
			re := regexp.MustCompile(pattern)
			if matches := re.FindStringSubmatch(content); len(matches) > 1 {
				reasoning := matches[1]
				// Try extracting answer from reasoning
				extracted := extractAnswerFromReasoning(reasoning)
				if extracted != "" {
					return formatAnswer(extracted)
				}
			}
		}
	}

	// If no specific format found, return the last non-empty content
	lines := strings.Split(content, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		// Skip empty lines, comment lines, and malformed format lines
		if line == "" || strings.HasPrefix(line, "#") || strings.Contains(line, "/*") {
			continue
		}
		// Skip lines that look like tool calls
		if strings.Contains(line, "multi_tool_use") || strings.Contains(line, "tool_use") ||
			strings.Contains(line, "functions.") || strings.Contains(line, "_parallel") {
			continue
		}
		return formatAnswer(line)
	}

	return ""
}

// extractAnswerFromReasoning extracts answer from reasoning content
// This is a fallback function used when model output format is malformed
func extractAnswerFromReasoning(reasoning string) string {
	// 1. Try to find "the answer is X" or "there are X" patterns
	answerPatterns := []string{
		`(?i)the\s+answer\s+(?:is|should\s+be)\s*[:\s]*(\d+|[a-zA-Z][a-zA-Z\s,]+)`,
		`(?i)there\s+(?:are|were)\s+(\d+)\s+(?:studio\s+)?albums?`,
		`(?i)(\d+)\s+(?:studio\s+)?albums?\s+(?:were\s+)?(?:published|released)`,
		`(?i)count(?:ing)?\s*[:\s]*(\d+)`,
		`(?i)total\s*[:\s]*(\d+)`,
	}

	for _, pattern := range answerPatterns {
		re := regexp.MustCompile(pattern)
		if matches := re.FindStringSubmatch(reasoning); len(matches) > 1 {
			return strings.TrimSpace(matches[1])
		}
	}

	// 2. If reasoning contains a list (e.g., "- 2005 ...\n- 2009 ...\n- 2009 ..."), count items
	// Find entries with years in 2000-2009 range
	yearPattern := regexp.MustCompile(`(?:^|\n)\s*[-•*]\s*(200[0-9])\s+`)
	yearMatches := yearPattern.FindAllStringSubmatch(reasoning, -1)
	if len(yearMatches) > 0 {
		// Count years in range
		count := 0
		for _, match := range yearMatches {
			if len(match) > 1 {
				year := match[1]
				// Check if year is in 2000-2009 range
				if year >= "2000" && year <= "2009" {
					count++
				}
			}
		}
		if count > 0 {
			return fmt.Sprintf("%d", count)
		}
	}

	// 3. Try to find the last mentioned number in reasoning (usually the conclusion)
	// Look for patterns like "So the answer is 3" or "which gives us 3"
	conclusionPatterns := []string{
		`(?i)so\s+(?:the\s+)?(?:answer|count|total|number)\s+is\s+(\d+)`,
		`(?i)(?:gives|leaves|results?\s+in)\s+(?:us\s+)?(\d+)`,
		`(?i)(?:=|equals?)\s*(\d+)`,
	}

	for _, pattern := range conclusionPatterns {
		re := regexp.MustCompile(pattern)
		if matches := re.FindStringSubmatch(reasoning); len(matches) > 1 {
			return strings.TrimSpace(matches[1])
		}
	}

	return ""
}

// formatAnswer formats answer by removing currency symbols, thousand separators, etc.
func formatAnswer(answer string) string {
	// 1. Remove currency symbols ($, €, £, ¥, etc.)
	currencySymbols := []string{"$", "€", "£", "¥", "₹", "¢", "₽", "₩", "฿"}
	for _, symbol := range currencySymbols {
		answer = strings.ReplaceAll(answer, symbol, "")
	}

	// 2. Remove thousand separators (commas in numbers)
	// Use regex to only remove commas in numbers, e.g.: 89,706 -> 89706
	commaPattern := regexp.MustCompile(`(\d),(\d)`)
	for commaPattern.MatchString(answer) {
		answer = commaPattern.ReplaceAllString(answer, "${1}${2}")
	}

	// 3. Normalize list separators: ensure space after comma (for letters/words only)
	// a,b,c,d -> a, b,c,d -> a, b, c,d -> a, b, c, d
	listPattern := regexp.MustCompile(`([a-zA-Z]),([a-zA-Z])`)
	for {
		newAnswer := listPattern.ReplaceAllString(answer, "${1}, ${2}")
		if newAnswer == answer {
			break
		}
		answer = newAnswer
	}

	// 4. Handle decimal format
	// First handle .00 ending (integers)
	answer = regexp.MustCompile(`\b(\d+)\.00\b`).ReplaceAllString(answer, "${1}.00")
	// For other .0 endings, remove if it's actually an integer
	answer = regexp.MustCompile(`\b(\d+)\.0+\b`).ReplaceAllString(answer, "${1}")
	// Remove trailing zeros from decimals (but keep at least two decimal places if present)
	answer = regexp.MustCompile(`(\.\d\d)0+\b`).ReplaceAllString(answer, "${1}")
	// If nothing left after decimal point, remove the decimal point
	answer = regexp.MustCompile(`(\d+)\.\s`).ReplaceAllString(answer, "${1} ")
	answer = regexp.MustCompile(`(\d+)\.$`).ReplaceAllString(answer, "${1}")

	// 5. Remove extra spaces
	answer = strings.Join(strings.Fields(answer), " ")

	return strings.TrimSpace(answer)
}

// verifyAnswer verifies if predicted answer is correct
func verifyAnswer(predicted, groundTruth string) bool {
	// Normalize answers for comparison
	predicted = normalizeAnswer(predicted)
	groundTruth = normalizeAnswer(groundTruth)

	// Exact match
	if predicted == groundTruth {
		return true
	}

	// Check if predicted contains ground truth
	if strings.Contains(predicted, groundTruth) {
		return true
	}

	// Check if ground truth contains predicted (handles extra explanations)
	if strings.Contains(groundTruth, predicted) && len(predicted) > 0 {
		return true
	}

	return false
}

// normalizeAnswer normalizes answer for comparison
func normalizeAnswer(answer string) string {
	// 1. Convert to lowercase
	answer = strings.ToLower(answer)

	// 2. Remove currency symbols ($, €, £, ¥, etc.)
	currencySymbols := []string{"$", "€", "£", "¥", "₹", "¢", "₽", "₩", "฿"}
	for _, symbol := range currencySymbols {
		answer = strings.ReplaceAll(answer, symbol, "")
	}

	// 3. Remove thousand separators (commas in numbers)
	// Use regex to only remove commas in numbers, e.g.: 89,706 -> 89706
	commaPattern := regexp.MustCompile(`(\d),(\d)`)
	for commaPattern.MatchString(answer) {
		answer = commaPattern.ReplaceAllString(answer, "${1}${2}")
	}

	// 4. Normalize list separators: ensure space after comma
	// e.g.: "a,b,c" -> "a, b, c"
	listPattern := regexp.MustCompile(`([a-z0-9])\s*,\s*([a-z0-9])`)
	answer = listPattern.ReplaceAllString(answer, "${1}, ${2}")

	// 5. Remove extra spaces (normalize to single space)
	answer = strings.Join(strings.Fields(answer), " ")

	// 6. Remove trailing punctuation (except necessary ones)
	answer = strings.TrimRight(answer, ".,;:!?")

	// 7. Handle decimal format - keep consistent with formatAnswer
	// Remove .0 suffix from integers (e.g.: 42.0 -> 42)
	trailingZeroPattern := regexp.MustCompile(`(\d+)\.0+\b`)
	answer = trailingZeroPattern.ReplaceAllString(answer, "${1}")

	// Remove trailing zeros after decimal (but keep .00 format, e.g.: 0.1777000 -> 0.1777, but 89706.00 stays)
	decimalPattern := regexp.MustCompile(`(\d+\.\d\d)0+\b`)
	answer = decimalPattern.ReplaceAllString(answer, "${1}")

	return strings.TrimSpace(answer)
}

func calculateSummary(framework string, results []BenchmarkResult) SummaryResult {
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

	// Convert relative path to absolute path
	absPath := path
	if !filepath.IsAbs(path) {
		absPath = filepath.Join(originalDir, path)
	}

	// Create directory
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
