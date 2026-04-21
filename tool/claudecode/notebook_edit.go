//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package claudecode

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

type notebookEditInput struct {
	NotebookPath string `json:"notebook_path"`
	CellID       string `json:"cell_id,omitempty"`
	NewSource    string `json:"new_source"`
	CellType     string `json:"cell_type,omitempty"`
	EditMode     string `json:"edit_mode,omitempty"`
}

type notebookEditOutput struct {
	NewSource    string `json:"new_source"`
	CellID       string `json:"cell_id,omitempty"`
	CellType     string `json:"cell_type"`
	Language     string `json:"language"`
	EditMode     string `json:"edit_mode"`
	NotebookPath string `json:"notebook_path"`
	OriginalFile string `json:"original_file"`
	UpdatedFile  string `json:"updated_file"`
}

type notebookEditState struct {
	snapshot  localFileSnapshot
	notebook  map[string]any
	cells     []map[string]any
	editMode  string
	cellType  string
	language  string
	cellIndex int
}

func newNotebookEditTool(runtime *runtime) (tool.Tool, error) {
	return function.NewFunctionTool(
		func(_ context.Context, in notebookEditInput) (notebookEditOutput, error) {
			baseDir := runtime.currentBaseDir()
			_, absPath, err := normalizePath(baseDir, in.NotebookPath)
			if err != nil {
				return notebookEditOutput{}, err
			}
			runtime.fileState.mu.Lock()
			defer runtime.fileState.mu.Unlock()
			return editNotebook(absPath, in, runtime)
		},
		function.WithName(toolNotebookEdit),
		function.WithDescription(notebookEditDescription()),
	), nil
}

func editNotebook(
	absPath string,
	in notebookEditInput,
	runtime *runtime,
) (notebookEditOutput, error) {
	state, err := loadNotebookEditState(absPath, in, runtime)
	if err != nil {
		return notebookEditOutput{}, err
	}
	resultCellID, resultCellType, err := applyNotebookEdit(&state, in)
	if err != nil {
		return notebookEditOutput{}, err
	}
	state.notebook["cells"] = notebookCellsAny(state.cells)
	updatedContent, err := marshalNotebook(state.notebook)
	if err != nil {
		return notebookEditOutput{}, err
	}
	if err := writeLocalFile(absPath, updatedContent, state.snapshot.Mode, state.snapshot.Encoding, state.snapshot.LineEnding); err != nil {
		return notebookEditOutput{}, err
	}
	current, err := readLocalFileSnapshot(absPath, runtime.maxFileSize)
	if err != nil {
		return notebookEditOutput{}, err
	}
	storeReadView(runtime.fileState, absPath, current.Content, current.Timestamp, nil, nil, "", false, false)
	return notebookEditOutput{
		NewSource:    in.NewSource,
		CellID:       resultCellID,
		CellType:     resultCellType,
		Language:     state.language,
		EditMode:     state.editMode,
		NotebookPath: absPath,
		OriginalFile: state.snapshot.Content,
		UpdatedFile:  updatedContent,
	}, nil
}

func loadNotebookEditState(absPath string, in notebookEditInput, runtime *runtime) (notebookEditState, error) {
	if !strings.HasSuffix(strings.ToLower(absPath), ".ipynb") {
		return notebookEditState{}, fmt.Errorf("File must be a Jupyter notebook (.ipynb file).")
	}
	editMode := strings.TrimSpace(strings.ToLower(in.EditMode))
	if editMode == "" {
		editMode = "replace"
	}
	if editMode != "replace" && editMode != "insert" && editMode != "delete" {
		return notebookEditState{}, fmt.Errorf("Edit mode must be replace, insert, or delete.")
	}
	cellType, err := normalizeNotebookCellType(in.CellType)
	if err != nil {
		return notebookEditState{}, err
	}
	if editMode == "insert" && cellType == "" {
		return notebookEditState{}, fmt.Errorf("Cell type is required when using edit_mode=insert.")
	}
	snapshot, err := readLocalFileSnapshot(absPath, maxEditableFileSize)
	if err != nil {
		return notebookEditState{}, err
	}
	if !snapshot.Exists {
		return notebookEditState{}, fmt.Errorf("Notebook file does not exist.")
	}
	if err := ensureWriteAllowed(absPath, snapshot, runtime.fileState); err != nil {
		return notebookEditState{}, err
	}
	notebook, cells, err := parseNotebook(snapshot.Raw)
	if err != nil {
		return notebookEditState{}, fmt.Errorf("Notebook is not valid JSON.")
	}
	cellIndex, err := notebookCellIndex(cells, in.CellID)
	if err != nil {
		return notebookEditState{}, err
	}
	if in.CellID == "" && editMode != "insert" {
		return notebookEditState{}, fmt.Errorf("Cell ID must be specified when not inserting a new cell.")
	}
	if cellIndex > len(cells) {
		return notebookEditState{}, fmt.Errorf("Cell with index %d does not exist in notebook.", cellIndex)
	}
	if editMode == "replace" && cellIndex == len(cells) {
		editMode = "insert"
		if cellType == "" {
			cellType = "code"
		}
	}
	return notebookEditState{
		snapshot:  snapshot,
		notebook:  notebook,
		cells:     cells,
		editMode:  editMode,
		cellType:  cellType,
		language:  notebookLanguage(notebook),
		cellIndex: cellIndex,
	}, nil
}

func applyNotebookEdit(state *notebookEditState, in notebookEditInput) (string, string, error) {
	switch state.editMode {
	case "delete":
		return deleteNotebookCell(state, in)
	case "insert":
		return insertNotebookCell(state, in), state.cellType, nil
	default:
		return replaceNotebookCell(state, in)
	}
}

func deleteNotebookCell(state *notebookEditState, in notebookEditInput) (string, string, error) {
	if state.cellIndex >= len(state.cells) {
		return "", "", fmt.Errorf("Cell with ID %q not found in notebook.", strings.TrimSpace(in.CellID))
	}
	resultCellID := notebookResultCellID(state.cells[state.cellIndex], state.cellIndex, in.CellID)
	resultCellType := notebookCellType(state.cells[state.cellIndex], "code")
	state.cells = append(state.cells[:state.cellIndex], state.cells[state.cellIndex+1:]...)
	return resultCellID, resultCellType, nil
}

func insertNotebookCell(state *notebookEditState, in notebookEditInput) string {
	insertAt := 0
	if strings.TrimSpace(in.CellID) != "" {
		insertAt = state.cellIndex + 1
		if insertAt > len(state.cells) {
			insertAt = len(state.cells)
		}
	}
	newCell := newNotebookCell(state.cellType, in.NewSource, notebookSupportsCellIDs(state.notebook))
	resultCellID := notebookResultCellID(newCell, insertAt, "")
	state.cells = append(state.cells[:insertAt], append([]map[string]any{newCell}, state.cells[insertAt:]...)...)
	return resultCellID
}

func replaceNotebookCell(state *notebookEditState, in notebookEditInput) (string, string, error) {
	if state.cellIndex >= len(state.cells) {
		return "", "", fmt.Errorf("Cell with ID %q not found in notebook.", strings.TrimSpace(in.CellID))
	}
	target := state.cells[state.cellIndex]
	resultCellID := notebookResultCellID(target, state.cellIndex, in.CellID)
	if state.cellType == "" {
		state.cellType = notebookCellType(target, "code")
	}
	target["cell_type"] = state.cellType
	target["source"] = in.NewSource
	if state.cellType == "code" {
		target["execution_count"] = nil
		target["outputs"] = []any{}
		return resultCellID, state.cellType, nil
	}
	delete(target, "execution_count")
	delete(target, "outputs")
	return resultCellID, state.cellType, nil
}

func parseNotebook(raw []byte) (map[string]any, []map[string]any, error) {
	var notebook map[string]any
	if err := json.Unmarshal(raw, &notebook); err != nil {
		return nil, nil, err
	}
	rawCells, ok := notebook["cells"].([]any)
	if !ok {
		return nil, nil, fmt.Errorf("notebook cells are invalid")
	}
	cells := make([]map[string]any, 0, len(rawCells))
	for _, rawCell := range rawCells {
		cell, ok := rawCell.(map[string]any)
		if !ok {
			return nil, nil, fmt.Errorf("notebook cell is invalid")
		}
		cells = append(cells, cell)
	}
	return notebook, cells, nil
}

func notebookCellIndex(cells []map[string]any, cellID string) (int, error) {
	trimmed := strings.TrimSpace(cellID)
	if trimmed == "" {
		return 0, nil
	}
	for idx, cell := range cells {
		if value, ok := cell["id"].(string); ok && value == trimmed {
			return idx, nil
		}
	}
	if parsed, ok := parseNotebookCellID(trimmed); ok {
		return parsed, nil
	}
	return -1, fmt.Errorf("Cell with ID %q not found in notebook.", trimmed)
}

func parseNotebookCellID(raw string) (int, bool) {
	trimmed := strings.TrimSpace(raw)
	if strings.HasPrefix(trimmed, "cell-") {
		trimmed = strings.TrimPrefix(trimmed, "cell-")
	}
	value, err := strconv.Atoi(trimmed)
	if err != nil || value < 0 {
		return 0, false
	}
	return value, true
}

func normalizeNotebookCellType(raw string) (string, error) {
	trimmed := strings.TrimSpace(strings.ToLower(raw))
	if trimmed == "" {
		return "", nil
	}
	if trimmed != "code" && trimmed != "markdown" {
		return "", fmt.Errorf("Cell type must be code or markdown.")
	}
	return trimmed, nil
}

func notebookLanguage(notebook map[string]any) string {
	metadata, ok := notebook["metadata"].(map[string]any)
	if !ok {
		return "python"
	}
	languageInfo, ok := metadata["language_info"].(map[string]any)
	if !ok {
		return "python"
	}
	name, _ := languageInfo["name"].(string)
	if strings.TrimSpace(name) == "" {
		return "python"
	}
	return name
}

func notebookSupportsCellIDs(notebook map[string]any) bool {
	nbformat, _ := notebookInt(notebook["nbformat"])
	nbformatMinor, _ := notebookInt(notebook["nbformat_minor"])
	return nbformat > 4 || (nbformat == 4 && nbformatMinor >= 5)
}

func notebookInt(raw any) (int, bool) {
	switch value := raw.(type) {
	case float64:
		return int(value), true
	case int:
		return value, true
	default:
		return 0, false
	}
}

func newNotebookCell(cellType string, source string, includeID bool) map[string]any {
	cell := map[string]any{
		"cell_type": cellType,
		"metadata":  map[string]any{},
		"source":    source,
	}
	if includeID {
		cell["id"] = uuid.NewString()[:12]
	}
	if cellType == "code" {
		cell["execution_count"] = nil
		cell["outputs"] = []any{}
	}
	return cell
}

func notebookResultCellID(cell map[string]any, cellIndex int, fallback string) string {
	if value, ok := cell["id"].(string); ok && strings.TrimSpace(value) != "" {
		return value
	}
	if strings.TrimSpace(fallback) != "" {
		return strings.TrimSpace(fallback)
	}
	return fmt.Sprintf("cell-%d", cellIndex)
}

func notebookCellType(cell map[string]any, fallback string) string {
	value, _ := cell["cell_type"].(string)
	value = strings.TrimSpace(strings.ToLower(value))
	if value == "" {
		return fallback
	}
	return value
}

func notebookCellsAny(cells []map[string]any) []any {
	out := make([]any, 0, len(cells))
	for _, cell := range cells {
		out = append(out, cell)
	}
	return out
}

func marshalNotebook(notebook map[string]any) (string, error) {
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", " ")
	if err := encoder.Encode(notebook); err != nil {
		return "", err
	}
	return strings.TrimSuffix(buf.String(), "\n"), nil
}

func notebookEditDescription() string {
	return fmt.Sprintf(`Edit cells inside a Jupyter notebook.

Usage:
- Use this tool for .ipynb files instead of %s or %s when you want cell-aware edits.
- Always read the notebook with %s before editing it.
- Supports replace, insert, and delete operations on notebook cells.
- Use cell_id to target an existing cell. Insert operations can create a new cell and return the resulting cell ID.
- Notebook edits participate in the same stale-check protections as other file-writing tools.`, toolEdit, toolWrite, toolRead)
}
