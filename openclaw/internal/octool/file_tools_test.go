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
	"archive/zip"
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/go-pdf/fpdf"
	"github.com/stretchr/testify/require"
	"github.com/xuri/excelize/v2"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/internal/uploads"
	sessionpkg "trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func newReadDocumentTool(
	stores ...*uploads.Store,
) tool.CallableTool {
	return NewReadDocumentTool(stores...).(tool.CallableTool)
}

func newReadSpreadsheetTool(
	stores ...*uploads.Store,
) tool.CallableTool {
	return NewReadSpreadsheetTool(stores...).(tool.CallableTool)
}

func TestReadDocumentTool_PDFPage(t *testing.T) {
	t.Parallel()

	pdfPath := createSamplePDF(t, []string{"Page 1", "Page 2"})
	tool := newReadDocumentTool()

	out, err := tool.Call(context.Background(), mustJSON(t, map[string]any{
		"path": pdfPath,
		"page": 2,
	}))
	require.NoError(t, err)

	res := out.(readDocumentResult)
	require.Equal(t, pdfPath, res.Path)
	require.Equal(t, docKindPDF, res.Kind)
	require.Equal(t, 2, res.PageCount)
	require.NotNil(t, res.Page)
	require.Equal(t, 2, *res.Page)
	require.Contains(t, res.Text, "Page 2")
}

func TestReadDocumentTool_DefaultsToLatestPDFUpload(t *testing.T) {
	t.Parallel()

	pdfPath := createSamplePDF(t, []string{"Latest PDF"})
	ctx := invocationContextWithUpload(
		t,
		pdfPath,
		"report.pdf",
		"application/pdf",
	)
	tool := newReadDocumentTool()

	out, err := tool.Call(ctx, mustJSON(t, map[string]any{}))
	require.NoError(t, err)

	res := out.(readDocumentResult)
	require.Equal(t, pdfPath, res.Path)
	require.Equal(t, docKindPDF, res.Kind)
	require.Contains(t, res.Text, "Latest PDF")
}

func TestReadSpreadsheetTool_RowSelection(t *testing.T) {
	t.Parallel()

	xlsxPath := createSampleWorkbook(t)
	tool := newReadSpreadsheetTool()

	out, err := tool.Call(context.Background(), mustJSON(t, map[string]any{
		"path": xlsxPath,
		"row":  4,
	}))
	require.NoError(t, err)

	res := out.(readSpreadsheetResult)
	require.Equal(t, xlsxPath, res.Path)
	require.Equal(t, sheetKindXLSX, res.Kind)
	require.Equal(t, 4, res.StartRow)
	require.Equal(t, 4, res.EndRow)
	require.Len(t, res.Rows, 1)
	require.Equal(t, 4, res.Rows[0].Index)
	require.Equal(t, []string{"r4c1", "r4c2"}, res.Rows[0].Values)
	require.Contains(t, res.Text, "row 4:")
}

func TestReadSpreadsheetTool_DefaultsToLatestUpload(t *testing.T) {
	t.Parallel()

	xlsxPath := createSampleWorkbook(t)
	ctx := invocationContextWithUpload(
		t,
		xlsxPath,
		"attachment.xlsx",
		"application/vnd.openxmlformats-"+
			"officedocument.spreadsheetml.sheet",
	)
	tool := newReadSpreadsheetTool()

	out, err := tool.Call(ctx, mustJSON(t, map[string]any{
		"row": 2,
	}))
	require.NoError(t, err)

	res := out.(readSpreadsheetResult)
	require.Equal(t, xlsxPath, res.Path)
	require.Len(t, res.Rows, 1)
	require.Equal(t, 2, res.Rows[0].Index)
	require.Equal(t, []string{"r2c1", "r2c2"}, res.Rows[0].Values)
}

func TestReadDocumentTool_Declaration(t *testing.T) {
	t.Parallel()

	decl := newReadDocumentTool().Declaration()
	require.Equal(t, toolReadDocument, decl.Name)
	require.Contains(t, decl.Description, "stable local path")
	require.Contains(t, decl.InputSchema.Properties, "path")
	require.Contains(t, decl.InputSchema.Properties, "page")
	require.Contains(t, decl.InputSchema.Properties, "max_chars")
}

func TestReadSpreadsheetTool_Declaration(t *testing.T) {
	t.Parallel()

	decl := newReadSpreadsheetTool().Declaration()
	require.Equal(t, toolReadSpreadsheet, decl.Name)
	require.Contains(t, decl.Description, "tabular chat uploads")
	require.Contains(t, decl.InputSchema.Properties, "sheet")
	require.Contains(t, decl.InputSchema.Properties, "row")
	require.Contains(t, decl.InputSchema.Properties, "start_row")
	require.Contains(t, decl.InputSchema.Properties, "end_row")
}

func TestReadDocumentTool_TextDocxAndErrors(t *testing.T) {
	t.Parallel()

	textPath := filepath.Join(t.TempDir(), "notes.txt")
	require.NoError(t, os.WriteFile(
		textPath,
		[]byte("alpha\nbeta\ngamma"),
		0o600,
	))

	docxPath := createSampleDOCX(t, "hello docx")
	tool := newReadDocumentTool()

	textOut, err := tool.Call(context.Background(), mustJSON(t, map[string]any{
		"path":      textPath,
		"max_chars": 5,
	}))
	require.NoError(t, err)

	textRes := textOut.(readDocumentResult)
	require.Equal(t, docKindText, textRes.Kind)
	require.True(t, textRes.Truncated)
	require.Equal(t, "alpha", textRes.Text)

	docxOut, err := tool.Call(context.Background(), mustJSON(t, map[string]any{
		"path": docxPath,
	}))
	require.NoError(t, err)

	docxRes := docxOut.(readDocumentResult)
	require.Equal(t, docKindDOCX, docxRes.Kind)
	require.Contains(t, docxRes.Text, "hello docx")

	_, err = tool.Call(context.Background(), mustJSON(t, map[string]any{
		"path": textPath,
		"page": 1,
	}))
	require.ErrorContains(t, err, "page is only supported")

	_, err = tool.Call(context.Background(), []byte("{"))
	require.ErrorContains(t, err, "invalid args")
}

func TestReadDocumentHelpers(t *testing.T) {
	t.Parallel()

	pdfPath := createSamplePDF(t, []string{"one", "two"})
	dirPath := t.TempDir()
	docxPath := createSampleDOCX(t, "docx text")
	logPath := filepath.Join(t.TempDir(), "events.log")
	binPath := filepath.Join(t.TempDir(), "bad.bin")
	require.NoError(t, os.WriteFile(logPath, []byte("line1"), 0o600))
	require.NoError(t, os.WriteFile(binPath, []byte("bin"), 0o600))

	path, kind, err := resolveDocumentPath("", map[string]string{
		envLastPDFPath: uploads.HostRef(pdfPath),
	})
	require.NoError(t, err)
	require.Equal(t, pdfPath, path)
	require.Equal(t, docKindPDF, kind)

	_, _, err = resolveDocumentPath(
		binPath,
		nil,
	)
	require.ErrorContains(t, err, errDocumentUnsupported)

	_, err = resolveInputPath(dirPath)
	require.ErrorContains(t, err, "directory")

	require.Equal(t, docKindDOCX, documentKindFromPath(docxPath))
	require.Equal(t, docKindText, documentKindFromPath(logPath))
	require.Equal(t, "", documentKindFromPath("bad.bin"))

	text, _, err := readDocumentText(logPath, docKindText, nil)
	require.NoError(t, err)
	require.Equal(t, "line1", text)

	_, _, err = readDocumentText(logPath, "other", nil)
	require.ErrorContains(t, err, errDocumentUnsupported)

	text, err = readDOCXText(docxPath)
	require.NoError(t, err)
	require.Contains(t, text, "docx text")

	_, _, err = readPDFText(pdfPath, intPtr(3))
	require.ErrorContains(t, err, "exceeds page count")

	require.Equal(t, "", pdfPageText(nil, 1))
}

func TestReadSpreadsheetTool_CSVAndErrors(t *testing.T) {
	t.Parallel()

	csvPath := filepath.Join(t.TempDir(), "sample.csv")
	require.NoError(t, os.WriteFile(
		csvPath,
		[]byte("c1,c2\nx,y\nm,n\n"),
		0o600,
	))

	tool := newReadSpreadsheetTool()
	out, err := tool.Call(context.Background(), mustJSON(t, map[string]any{
		"path":      csvPath,
		"start_row": 2,
		"end_row":   3,
		"max_chars": 8,
	}))
	require.NoError(t, err)

	res := out.(readSpreadsheetResult)
	require.Equal(t, sheetKindCSV, res.Kind)
	require.Equal(t, 3, res.RowCount)
	require.Equal(t, 2, res.StartRow)
	require.Equal(t, 3, res.EndRow)
	require.True(t, res.Truncated)

	_, err = tool.Call(context.Background(), mustJSON(t, map[string]any{
		"path": csvPath,
		"row":  9,
	}))
	require.ErrorContains(t, err, "row 9")

	_, err = tool.Call(context.Background(), []byte("{"))
	require.ErrorContains(t, err, "invalid args")
}

func TestSpreadsheetHelpers(t *testing.T) {
	t.Parallel()

	xlsxPath := createSampleWorkbook(t)
	csvPath := filepath.Join(t.TempDir(), "sheet.csv")
	require.NoError(t, os.WriteFile(
		csvPath,
		[]byte("h1,h2\na,b\nc,d\n"),
		0o600,
	))

	require.Equal(t, sheetKindXLSX, spreadsheetKindFromPath(xlsxPath))
	require.Equal(t, sheetKindCSV, spreadsheetKindFromPath(csvPath))
	require.Equal(t, "", spreadsheetKindFromPath("bad.txt"))

	rows, sheet, err := readSpreadsheetRows(xlsxPath, sheetKindXLSX, "")
	require.NoError(t, err)
	require.Equal(t, "Sheet1", sheet)
	require.Len(t, rows, 5)

	rows, sheet, err = readSpreadsheetRows(csvPath, sheetKindCSV, "")
	require.NoError(t, err)
	require.Empty(t, sheet)
	require.Len(t, rows, 3)

	_, _, err = readSpreadsheetRows(csvPath, "bad", "")
	require.ErrorContains(t, err, errSpreadsheetUnsupported)

	selected, startRow, endRow, err := selectSpreadsheetRows(
		[][]string{{"a", "b"}, {"c\nd", " e "}},
		readSpreadsheetInput{StartRow: intPtr(2)},
	)
	require.NoError(t, err)
	require.Equal(t, 2, startRow)
	require.Equal(t, 2, endRow)
	require.Equal(t, []string{"c d", "e"}, selected[0].Values)

	startRow, endRow, err = spreadsheetRange(
		5,
		readSpreadsheetInput{Row: intPtr(2)},
	)
	require.NoError(t, err)
	require.Equal(t, 2, startRow)
	require.Equal(t, 2, endRow)

	startRow, endRow, err = spreadsheetRange(
		5,
		readSpreadsheetInput{StartRow: intPtr(4)},
	)
	require.NoError(t, err)
	require.Equal(t, 4, startRow)
	require.Equal(t, 5, endRow)

	_, _, err = spreadsheetRange(
		3,
		readSpreadsheetInput{StartRow: intPtr(4)},
	)
	require.ErrorContains(t, err, "start_row 4")

	_, _, err = spreadsheetRange(
		5,
		readSpreadsheetInput{
			StartRow: intPtr(3),
			EndRow:   intPtr(2),
		},
	)
	require.ErrorContains(t, err, "smaller than")

	formatted := formatSpreadsheetRows([]spreadsheetRow{{
		Index:  2,
		Values: []string{"a", "b"},
	}})
	require.Equal(t, "row 2: a\tb", formatted)
	require.Equal(t, "", formatSpreadsheetRows(nil))
	require.Nil(t, sanitizeSpreadsheetCells(nil))

	truncated, ok := truncateText("abcdef", 3)
	require.True(t, ok)
	require.Equal(t, "abc", truncated)

	same, ok := truncateText("abc", 0)
	require.False(t, ok)
	require.Equal(t, "abc", same)

	require.Nil(t, normalizedPositive(intPtr(0)))
	require.Equal(t, 7, resolvedMaxChars(intPtr(7), 3))
	require.Equal(t, 3, resolvedMaxChars(nil, 3))
	require.Equal(t, 3, minInt(3, 8))
	require.Equal(t, 8, minInt(10, 8))
}

func createSamplePDF(t *testing.T, pages []string) string {
	t.Helper()

	doc := fpdf.New("P", "mm", "A4", "")
	doc.SetFont("Helvetica", "", 12)
	for _, pageText := range pages {
		doc.AddPage()
		doc.Cell(40, 10, pageText)
	}

	path := filepath.Join(t.TempDir(), "sample.pdf")
	require.NoError(t, doc.OutputFileAndClose(path))
	return path
}

func createSampleWorkbook(t *testing.T) string {
	t.Helper()

	book := excelize.NewFile()
	const sheet = "Sheet1"
	require.Equal(t, sheet, book.GetSheetName(0))
	for rowIndex := 1; rowIndex <= 5; rowIndex++ {
		require.NoError(t, book.SetCellStr(
			sheet,
			"A"+strconv.Itoa(rowIndex),
			"r"+strconv.Itoa(rowIndex)+"c1",
		))
		require.NoError(t, book.SetCellStr(
			sheet,
			"B"+strconv.Itoa(rowIndex),
			"r"+strconv.Itoa(rowIndex)+"c2",
		))
	}

	path := filepath.Join(t.TempDir(), "sample.xlsx")
	require.NoError(t, book.SaveAs(path))
	require.NoError(t, book.Close())
	return path
}

func createSampleDOCX(t *testing.T, text string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "sample.docx")
	file, err := os.Create(path)
	require.NoError(t, err)

	archive := zip.NewWriter(file)
	writeZipFile(t, archive, "[Content_Types].xml", strings.Join([]string{
		`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>`,
		`<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">`,
		`<Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>`,
		`<Default Extension="xml" ContentType="application/xml"/>`,
		`<Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/>`,
		`</Types>`,
	}, ""))
	writeZipFile(t, archive, "_rels/.rels", strings.Join([]string{
		`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>`,
		`<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">`,
		`<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/>`,
		`</Relationships>`,
	}, ""))
	writeZipFile(t, archive, "word/_rels/document.xml.rels", strings.Join([]string{
		`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>`,
		`<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"></Relationships>`,
	}, ""))
	writeZipFile(t, archive, "word/document.xml", strings.Join([]string{
		`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>`,
		`<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">`,
		`<w:body><w:p><w:r><w:t>` + text + `</w:t></w:r></w:p></w:body>`,
		`</w:document>`,
	}, ""))

	require.NoError(t, archive.Close())
	require.NoError(t, file.Close())
	return path
}

func writeZipFile(
	t *testing.T,
	archive *zip.Writer,
	name string,
	content string,
) {
	t.Helper()

	writer, err := archive.Create(name)
	require.NoError(t, err)
	_, err = writer.Write([]byte(content))
	require.NoError(t, err)
}

func intPtr(v int) *int {
	return &v
}

func invocationContextWithUpload(
	t *testing.T,
	path string,
	name string,
	mimeType string,
) context.Context {
	t.Helper()

	msg := model.Message{
		Role: model.RoleUser,
		ContentParts: []model.ContentPart{{
			Type: model.ContentTypeFile,
			File: &model.File{
				Name:     name,
				FileID:   "host://" + path,
				MimeType: mimeType,
			},
		}},
	}
	inv := agent.NewInvocation(
		agent.WithInvocationMessage(msg),
		agent.WithInvocationSession(
			sessionpkg.NewSession("app", "u1", "telegram:dm:u1:s1"),
		),
	)
	return agent.NewInvocationContext(context.Background(), inv)
}
