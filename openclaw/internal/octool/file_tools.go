//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package octool

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	pdfpkg "github.com/ledongthuc/pdf"
	"github.com/xuri/excelize/v2"
	docreader "trpc.group/trpc-go/trpc-agent-go/knowledge/document/reader"
	docxreader "trpc.group/trpc-go/trpc-agent-go/knowledge/document/reader/docx"
	"trpc.group/trpc-go/trpc-agent-go/tool"

	"trpc.group/trpc-go/trpc-agent-go/openclaw/internal/uploads"
)

const (
	toolReadDocument    = "read_document"
	toolReadSpreadsheet = "read_spreadsheet"

	errReadPathRequired       = "path is required when no upload is present"
	errDocumentUnsupported    = "unsupported document type"
	errSpreadsheetUnsupported = "unsupported spreadsheet type"
	errSpreadsheetSheetEmpty  = "spreadsheet has no sheets"

	docKindPDF  = "pdf"
	docKindDOCX = "docx"
	docKindText = "text"

	sheetKindXLSX = "xlsx"
	sheetKindCSV  = "csv"

	defaultReadDocumentChars = 6_000
	defaultReadSheetChars    = 4_000
	defaultSheetPreviewRows  = 20

	schemaTypeObject = "object"
	schemaTypeString = "string"
	schemaTypeNumber = "number"
	schemaTypeArray  = "array"
)

type readDocumentTool struct {
	uploads *uploads.Store
}

type readSpreadsheetTool struct {
	uploads *uploads.Store
}

type readDocumentInput struct {
	Path     string `json:"path,omitempty"`
	Page     *int   `json:"page,omitempty"`
	MaxChars *int   `json:"max_chars,omitempty"`
}

type readDocumentResult struct {
	Path      string `json:"path"`
	Kind      string `json:"kind"`
	Title     string `json:"title"`
	PageCount int    `json:"page_count,omitempty"`
	Page      *int   `json:"page,omitempty"`
	Text      string `json:"text"`
	Truncated bool   `json:"truncated,omitempty"`
}

type readSpreadsheetInput struct {
	Path     string `json:"path,omitempty"`
	Sheet    string `json:"sheet,omitempty"`
	Row      *int   `json:"row,omitempty"`
	StartRow *int   `json:"start_row,omitempty"`
	EndRow   *int   `json:"end_row,omitempty"`
	MaxChars *int   `json:"max_chars,omitempty"`
}

type spreadsheetRow struct {
	Index  int      `json:"index"`
	Values []string `json:"values,omitempty"`
}

type readSpreadsheetResult struct {
	Path      string           `json:"path"`
	Kind      string           `json:"kind"`
	Title     string           `json:"title"`
	Sheet     string           `json:"sheet,omitempty"`
	StartRow  int              `json:"start_row,omitempty"`
	EndRow    int              `json:"end_row,omitempty"`
	RowCount  int              `json:"row_count,omitempty"`
	Rows      []spreadsheetRow `json:"rows,omitempty"`
	Text      string           `json:"text"`
	Truncated bool             `json:"truncated,omitempty"`
}

func NewReadDocumentTool(
	stores ...*uploads.Store,
) tool.Tool {
	return &readDocumentTool{
		uploads: firstUploadStore(stores),
	}
}

func NewReadSpreadsheetTool(
	stores ...*uploads.Store,
) tool.Tool {
	return &readSpreadsheetTool{
		uploads: firstUploadStore(stores),
	}
}

func firstUploadStore(stores []*uploads.Store) *uploads.Store {
	if len(stores) == 0 {
		return nil
	}
	return stores[0]
}

func (t *readDocumentTool) Declaration() *tool.Declaration {
	return &tool.Declaration{
		Name: toolReadDocument,
		Description: "Read a chat document from a stable local path. " +
			"Use this for PDFs, DOCX files, and plain text-like " +
			"documents already present in the chat instead of " +
			"calling exec_command to inspect upload paths.",
		InputSchema: &tool.Schema{
			Type: schemaTypeObject,
			Properties: map[string]*tool.Schema{
				"path": {
					Type: schemaTypeString,
					Description: "Optional document path or host ref. " +
						"If omitted, the latest matching upload " +
						"is used.",
				},
				"page": {
					Type: schemaTypeNumber,
					Description: "Optional 1-based PDF page number. " +
						"Only valid for PDF files.",
				},
				"max_chars": {
					Type: schemaTypeNumber,
					Description: "Optional maximum number of " +
						"characters to return.",
				},
			},
		},
	}
}

func (t *readDocumentTool) Call(
	ctx context.Context,
	args []byte,
) (any, error) {
	var in readDocumentInput
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid args: %w", err)
	}

	env := uploadEnvFromContext(ctx, t.uploads)
	path, kind, err := resolveDocumentPath(in.Path, env)
	if err != nil {
		return nil, err
	}

	maxChars := resolvedMaxChars(in.MaxChars, defaultReadDocumentChars)
	text, pageCount, err := readDocumentText(path, kind, in.Page)
	if err != nil {
		return nil, err
	}
	text, truncated := truncateText(text, maxChars)

	return readDocumentResult{
		Path:      path,
		Kind:      kind,
		Title:     filepath.Base(path),
		PageCount: pageCount,
		Page:      normalizedPositive(in.Page),
		Text:      text,
		Truncated: truncated,
	}, nil
}

func (t *readSpreadsheetTool) Declaration() *tool.Declaration {
	return &tool.Declaration{
		Name: toolReadSpreadsheet,
		Description: "Read tabular chat uploads such as XLSX and " +
			"CSV files. Use this instead of exec_command when the " +
			"user asks for rows, sheets, or table excerpts.",
		InputSchema: &tool.Schema{
			Type: schemaTypeObject,
			Properties: map[string]*tool.Schema{
				"path": {
					Type: schemaTypeString,
					Description: "Optional spreadsheet path or " +
						"host ref. If omitted, the latest upload " +
						"is used.",
				},
				"sheet": {
					Type: schemaTypeString,
					Description: "Optional worksheet name. " +
						"Defaults to the first sheet.",
				},
				"row": {
					Type: schemaTypeNumber,
					Description: "Optional 1-based row number to " +
						"read.",
				},
				"start_row": {
					Type: schemaTypeNumber,
					Description: "Optional 1-based range start " +
						"row.",
				},
				"end_row": {
					Type:        schemaTypeNumber,
					Description: "Optional 1-based range end row.",
				},
				"max_chars": {
					Type: schemaTypeNumber,
					Description: "Optional maximum number of " +
						"characters to return.",
				},
			},
		},
	}
}

func (t *readSpreadsheetTool) Call(
	ctx context.Context,
	args []byte,
) (any, error) {
	var in readSpreadsheetInput
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid args: %w", err)
	}

	env := uploadEnvFromContext(ctx, t.uploads)
	path, kind, err := resolveSpreadsheetPath(in.Path, env)
	if err != nil {
		return nil, err
	}

	rows, sheetName, err := readSpreadsheetRows(path, kind, in.Sheet)
	if err != nil {
		return nil, err
	}

	selected, startRow, endRow, err := selectSpreadsheetRows(rows, in)
	if err != nil {
		return nil, err
	}

	text := formatSpreadsheetRows(selected)
	maxChars := resolvedMaxChars(in.MaxChars, defaultReadSheetChars)
	text, truncated := truncateText(text, maxChars)

	return readSpreadsheetResult{
		Path:      path,
		Kind:      kind,
		Title:     filepath.Base(path),
		Sheet:     sheetName,
		StartRow:  startRow,
		EndRow:    endRow,
		RowCount:  len(rows),
		Rows:      selected,
		Text:      text,
		Truncated: truncated,
	}, nil
}

func resolveDocumentPath(
	rawPath string,
	env map[string]string,
) (string, string, error) {
	path := strings.TrimSpace(rawPath)
	if path == "" {
		path = strings.TrimSpace(env[envLastPDFPath])
	}
	if path == "" {
		path = strings.TrimSpace(env[envLastUploadPath])
	}
	if path == "" {
		return "", "", errors.New(errReadPathRequired)
	}

	resolved, err := resolveInputPath(path, env)
	if err != nil {
		return "", "", err
	}

	kind := documentKindFromPath(resolved)
	if kind == "" {
		return "", "", fmt.Errorf("%s: %s",
			errDocumentUnsupported, filepath.Ext(resolved))
	}
	return resolved, kind, nil
}

func resolveSpreadsheetPath(
	rawPath string,
	env map[string]string,
) (string, string, error) {
	path := strings.TrimSpace(rawPath)
	if path == "" {
		path = strings.TrimSpace(env[envLastUploadPath])
	}
	if path == "" {
		return "", "", errors.New(errReadPathRequired)
	}

	resolved, err := resolveInputPath(path, env)
	if err != nil {
		return "", "", err
	}

	kind := spreadsheetKindFromPath(resolved)
	if kind == "" {
		return "", "", fmt.Errorf("%s: %s",
			errSpreadsheetUnsupported, filepath.Ext(resolved))
	}
	return resolved, kind, nil
}

func resolveInputPath(
	rawPath string,
	env map[string]string,
) (string, error) {
	path := strings.TrimSpace(rawPath)
	if path == "" {
		return "", errors.New(errReadPathRequired)
	}
	if resolved, ok := uploads.PathFromHostRef(path); ok {
		path = resolved
	}
	if resolved, ok := resolveUploadContextPath(path, env); ok {
		path = resolved
	}

	localPath, err := resolveWorkdir(path)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(localPath)
	if err != nil {
		return "", fmt.Errorf("stat path: %w", err)
	}
	if info.IsDir() {
		return "", fmt.Errorf("path is a directory: %s", localPath)
	}
	return localPath, nil
}

func resolveUploadContextPath(
	path string,
	env map[string]string,
) (string, bool) {
	path = strings.TrimSpace(path)
	if path == "" || filepath.IsAbs(path) || path == "~" ||
		strings.HasPrefix(path, "~/") {
		return "", false
	}

	if matched := matchRecentUploadPath(path, env); matched != "" {
		return matched, true
	}

	dir := strings.TrimSpace(env[envSessionUploadsDir])
	if dir == "" {
		return "", false
	}
	candidate := filepath.Join(dir, path)
	if info, err := os.Stat(candidate); err == nil &&
		info != nil && !info.IsDir() {
		return candidate, true
	}
	return "", false
}

func matchRecentUploadPath(
	rawPath string,
	env map[string]string,
) string {
	rawPath = strings.TrimSpace(rawPath)
	if rawPath == "" {
		return ""
	}

	var recent []execUploadMeta
	if raw := strings.TrimSpace(env[envRecentUploadsJSON]); raw != "" {
		if err := json.Unmarshal([]byte(raw), &recent); err == nil {
			for _, item := range recent {
				if uploadMatchesReference(item, rawPath) {
					return item.Path
				}
			}
		}
	}

	latest := execUploadMeta{
		Name:    strings.TrimSpace(env[envLastUploadName]),
		Path:    strings.TrimSpace(env[envLastUploadPath]),
		HostRef: strings.TrimSpace(env[envLastUploadHostRef]),
	}
	if uploadMatchesReference(latest, rawPath) {
		return latest.Path
	}
	return ""
}

func uploadMatchesReference(
	item execUploadMeta,
	rawPath string,
) bool {
	for _, candidate := range uploadReferenceCandidates(item) {
		if candidate == rawPath {
			return true
		}
	}
	return false
}

func uploadReferenceCandidates(item execUploadMeta) []string {
	var out []string
	out = appendUniqueTrimmed(out, item.Name)
	out = appendUniqueTrimmed(out, item.Path)
	out = appendUniqueTrimmed(out, filepath.Base(item.Path))
	out = appendUniqueTrimmed(out, item.HostRef)
	if hostPath, ok := uploads.PathFromHostRef(item.HostRef); ok {
		out = appendUniqueTrimmed(out, hostPath)
		out = appendUniqueTrimmed(out, filepath.Base(hostPath))
	}
	return out
}

func appendUniqueTrimmed(values []string, value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return values
	}
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

func documentKindFromPath(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".pdf":
		return docKindPDF
	case ".docx", ".doc":
		return docKindDOCX
	case ".txt", ".md", ".markdown", ".json", ".csv",
		".yaml", ".yml", ".log":
		return docKindText
	default:
		return ""
	}
}

func spreadsheetKindFromPath(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".xlsx", ".xls", ".xlsm":
		return sheetKindXLSX
	case ".csv":
		return sheetKindCSV
	default:
		return ""
	}
}

func readDocumentText(
	path string,
	kind string,
	page *int,
) (string, int, error) {
	switch kind {
	case docKindPDF:
		return readPDFText(path, page)
	case docKindDOCX:
		if normalizedPositive(page) != nil {
			return "", 0,
				errors.New("page is only supported for PDF files")
		}
		text, err := readDOCXText(path)
		return text, 0, err
	case docKindText:
		if normalizedPositive(page) != nil {
			return "", 0,
				errors.New("page is only supported for PDF files")
		}
		text, err := readTextFile(path)
		return text, 0, err
	default:
		return "", 0, fmt.Errorf("%s: %s",
			errDocumentUnsupported, kind)
	}
}

func readPDFText(
	path string,
	page *int,
) (string, int, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", 0, fmt.Errorf("open pdf: %w", err)
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return "", 0, fmt.Errorf("stat pdf: %w", err)
	}

	reader, err := pdfpkg.NewReader(file, info.Size())
	if err != nil {
		return "", 0, fmt.Errorf("read pdf: %w", err)
	}

	pageCount := reader.NumPage()
	selectedPage := normalizedPositive(page)
	if selectedPage != nil {
		if *selectedPage > pageCount {
			return "", 0, fmt.Errorf(
				"page %d exceeds page count %d",
				*selectedPage,
				pageCount,
			)
		}
		return pdfPageText(reader, *selectedPage), pageCount, nil
	}

	var builder strings.Builder
	for pageIndex := 1; pageIndex <= pageCount; pageIndex++ {
		text := pdfPageText(reader, pageIndex)
		if strings.TrimSpace(text) == "" {
			continue
		}
		if builder.Len() > 0 {
			builder.WriteString("\n")
		}
		builder.WriteString(text)
	}
	return builder.String(), pageCount, nil
}

func pdfPageText(
	reader *pdfpkg.Reader,
	pageIndex int,
) string {
	if reader == nil {
		return ""
	}
	page := reader.Page(pageIndex)
	if page.V.IsNull() {
		return ""
	}
	text, err := page.GetPlainText(nil)
	if err != nil {
		return ""
	}
	return text
}

func readDOCXText(path string) (string, error) {
	rdr := docxreader.New(docreader.WithChunk(false))
	docs, err := rdr.ReadFromFile(path)
	if err != nil {
		return "", fmt.Errorf("read docx: %w", err)
	}

	parts := make([]string, 0, len(docs))
	for _, doc := range docs {
		if doc == nil {
			continue
		}
		text := strings.TrimSpace(doc.Content)
		if text == "" {
			continue
		}
		parts = append(parts, text)
	}
	return strings.Join(parts, "\n\n"), nil
}

func readTextFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read file: %w", err)
	}
	return string(data), nil
}

func readSpreadsheetRows(
	path string,
	kind string,
	sheet string,
) ([][]string, string, error) {
	switch kind {
	case sheetKindCSV:
		rows, err := readCSVRows(path)
		return rows, "", err
	case sheetKindXLSX:
		return readWorkbookRows(path, sheet)
	default:
		return nil, "", fmt.Errorf("%s: %s",
			errSpreadsheetUnsupported, kind)
	}
}

func readCSVRows(path string) ([][]string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open csv: %w", err)
	}
	defer file.Close()

	reader := csv.NewReader(file)
	rows, err := reader.ReadAll()
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("read csv: %w", err)
	}
	return rows, nil
}

func readWorkbookRows(
	path string,
	sheet string,
) ([][]string, string, error) {
	workbook, err := excelize.OpenFile(path)
	if err != nil {
		return nil, "", fmt.Errorf("open spreadsheet: %w", err)
	}
	defer func() {
		_ = workbook.Close()
	}()

	sheets := workbook.GetSheetList()
	if len(sheets) == 0 {
		return nil, "", errors.New(errSpreadsheetSheetEmpty)
	}

	selected := strings.TrimSpace(sheet)
	if selected == "" {
		selected = sheets[0]
	}

	rows, err := workbook.GetRows(selected)
	if err != nil {
		return nil, "", fmt.Errorf("read sheet %q: %w", selected, err)
	}
	return rows, selected, nil
}

func selectSpreadsheetRows(
	rows [][]string,
	in readSpreadsheetInput,
) ([]spreadsheetRow, int, int, error) {
	totalRows := len(rows)
	if totalRows == 0 {
		return nil, 0, 0, nil
	}

	startRow, endRow, err := spreadsheetRange(totalRows, in)
	if err != nil {
		return nil, 0, 0, err
	}

	selected := make([]spreadsheetRow, 0, endRow-startRow+1)
	for rowIndex := startRow; rowIndex <= endRow; rowIndex++ {
		values := sanitizeSpreadsheetCells(rows[rowIndex-1])
		selected = append(selected, spreadsheetRow{
			Index:  rowIndex,
			Values: values,
		})
	}
	return selected, startRow, endRow, nil
}

func spreadsheetRange(
	totalRows int,
	in readSpreadsheetInput,
) (int, int, error) {
	row := normalizedPositive(in.Row)
	if row != nil {
		if *row > totalRows {
			return 0, 0, fmt.Errorf(
				"row %d exceeds row count %d",
				*row,
				totalRows,
			)
		}
		return *row, *row, nil
	}

	start := 1
	if v := normalizedPositive(in.StartRow); v != nil {
		start = *v
	}
	if start > totalRows {
		return 0, 0, fmt.Errorf(
			"start_row %d exceeds row count %d",
			start,
			totalRows,
		)
	}

	end := minInt(totalRows, defaultSheetPreviewRows)
	if start > 1 {
		end = start
	}
	if v := normalizedPositive(in.EndRow); v != nil {
		end = *v
	}
	if end < start {
		return 0, 0,
			fmt.Errorf("end_row %d is smaller than start_row %d",
				end, start)
	}
	if end > totalRows {
		end = totalRows
	}
	if v := normalizedPositive(in.StartRow); v != nil &&
		normalizedPositive(in.EndRow) == nil {
		end = minInt(totalRows, start+defaultSheetPreviewRows-1)
	}
	return start, end, nil
}

func sanitizeSpreadsheetCells(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	out := make([]string, 0, len(values))
	for _, value := range values {
		sanitized := strings.ReplaceAll(value, "\n", " ")
		sanitized = strings.TrimSpace(sanitized)
		out = append(out, sanitized)
	}
	return out
}

func formatSpreadsheetRows(rows []spreadsheetRow) string {
	if len(rows) == 0 {
		return ""
	}
	lines := make([]string, 0, len(rows))
	for _, row := range rows {
		lines = append(lines, formatSpreadsheetRow(row))
	}
	return strings.Join(lines, "\n")
}

func formatSpreadsheetRow(row spreadsheetRow) string {
	return "row " + strconv.Itoa(row.Index) + ": " +
		strings.Join(row.Values, "\t")
}

func truncateText(text string, maxChars int) (string, bool) {
	if maxChars <= 0 {
		return text, false
	}
	runes := []rune(text)
	if len(runes) <= maxChars {
		return text, false
	}
	return string(runes[:maxChars]), true
}

func normalizedPositive(value *int) *int {
	if value == nil || *value <= 0 {
		return nil
	}
	out := *value
	return &out
}

func resolvedMaxChars(value *int, fallback int) int {
	if value == nil || *value <= 0 {
		return fallback
	}
	return *value
}

func minInt(a int, b int) int {
	if a < b {
		return a
	}
	return b
}

var _ tool.CallableTool = (*readDocumentTool)(nil)
var _ tool.CallableTool = (*readSpreadsheetTool)(nil)
