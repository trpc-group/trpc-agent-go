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
	"path/filepath"
	"strconv"
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
