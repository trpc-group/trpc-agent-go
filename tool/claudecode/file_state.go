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
	"fmt"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"
)

func readLocalFileSnapshot(absPath string, maxFileSize int64) (localFileSnapshot, error) {
	info, err := os.Stat(absPath)
	if err != nil {
		if os.IsNotExist(err) {
			return localFileSnapshot{Path: absPath}, nil
		}
		return localFileSnapshot{}, fmt.Errorf("stat file %q: %w", absPath, err)
	}
	if info.IsDir() {
		return localFileSnapshot{}, fmt.Errorf("target path %q is a directory", absPath)
	}
	if maxFileSize > 0 && info.Size() > maxFileSize {
		return localFileSnapshot{}, fmt.Errorf("file %q exceeds max size of %d bytes", absPath, maxFileSize)
	}
	raw, err := os.ReadFile(absPath)
	if err != nil {
		return localFileSnapshot{}, fmt.Errorf("read file %q: %w", absPath, err)
	}
	content, encoding, err := decodeTextBytes(raw)
	if err != nil {
		return localFileSnapshot{}, fmt.Errorf("decode file %q: %w", absPath, err)
	}
	mediaType := mime.TypeByExtension(strings.ToLower(filepath.Ext(absPath)))
	if mediaType == "" {
		mediaType = http.DetectContentType(raw)
	}
	return localFileSnapshot{
		Exists:       true,
		Path:         absPath,
		Raw:          raw,
		Content:      content,
		Mode:         info.Mode(),
		Timestamp:    info.ModTime().UnixMilli(),
		Encoding:     encoding,
		LineEnding:   detectLineEnding(raw),
		MediaType:    mediaType,
		OriginalSize: info.Size(),
	}, nil
}

func ensureWriteAllowed(
	absPath string,
	snapshot localFileSnapshot,
	state *fileState,
) error {
	view, ok := state.views[absPath]
	if !ok || view.IsPartialView {
		return fmt.Errorf("File has not been read yet. Read it first before writing to it.")
	}
	if snapshot.Timestamp > view.Timestamp {
		isFullView := view.Offset == nil && view.Limit == nil && strings.TrimSpace(view.Pages) == ""
		if !isFullView || snapshot.Content != view.Content {
			return fmt.Errorf("File has been modified since read, either by the user or by a linter. Read it again before attempting to write it.")
		}
	}
	return nil
}

func writeLocalFile(
	absPath string,
	content string,
	mode os.FileMode,
	encoding string,
	lineEnding string,
) error {
	parentDir := filepath.Dir(absPath)
	if err := os.MkdirAll(parentDir, 0o755); err != nil {
		return fmt.Errorf("create directory for %q: %w", absPath, err)
	}
	fileMode := mode
	if fileMode == 0 {
		fileMode = 0o644
	}
	encoded, err := encodeTextBytes(content, encoding, lineEnding)
	if err != nil {
		return err
	}
	if err := os.WriteFile(absPath, encoded, fileMode); err != nil {
		return fmt.Errorf("write file %q: %w", absPath, err)
	}
	return nil
}

func storeReadView(
	state *fileState,
	absPath string,
	content string,
	timestamp int64,
	offset *int,
	limit *int,
	pages string,
	isPartial bool,
	fromRead bool,
) {
	state.views[absPath] = fileView{
		Content:       content,
		Timestamp:     timestamp,
		Offset:        offset,
		Limit:         limit,
		Pages:         pages,
		IsPartialView: isPartial,
		FromRead:      fromRead,
	}
}

func matchesReadView(
	view fileView,
	offset *int,
	limit *int,
	pages string,
) bool {
	if !view.FromRead {
		return false
	}
	if !intPtrsEqual(view.Offset, offset) {
		return false
	}
	if !intPtrsEqual(view.Limit, limit) {
		return false
	}
	return strings.TrimSpace(view.Pages) == strings.TrimSpace(pages)
}

func intPtrsEqual(left *int, right *int) bool {
	switch {
	case left == nil && right == nil:
		return true
	case left == nil || right == nil:
		return false
	default:
		return *left == *right
	}
}

func normalizeQuotes(raw string) string {
	replacer := strings.NewReplacer(
		"‘", "'",
		"’", "'",
		"“", "\"",
		"”", "\"",
	)
	return replacer.Replace(raw)
}

func findActualString(fileContent string, searchString string) string {
	if strings.Contains(fileContent, searchString) {
		return searchString
	}
	var builder strings.Builder
	for _, r := range searchString {
		switch r {
		case '\'':
			builder.WriteString("['‘’]")
		case '"':
			builder.WriteString("[\"“”]")
		default:
			builder.WriteString(regexp.QuoteMeta(string(r)))
		}
	}
	re, err := regexp.Compile(builder.String())
	if err != nil {
		return ""
	}
	return re.FindString(fileContent)
}

func preserveQuoteStyle(oldString string, actualOldString string, newString string) string {
	if oldString == actualOldString {
		return newString
	}
	hasDoubleQuotes := strings.Contains(actualOldString, "“") || strings.Contains(actualOldString, "”")
	hasSingleQuotes := strings.Contains(actualOldString, "‘") || strings.Contains(actualOldString, "’")
	result := newString
	if hasDoubleQuotes {
		result = applyCurlyDoubleQuotes(result)
	}
	if hasSingleQuotes {
		result = applyCurlySingleQuotes(result)
	}
	return result
}

func applyCurlyDoubleQuotes(raw string) string {
	chars := []rune(raw)
	out := make([]rune, 0, len(chars))
	for idx, r := range chars {
		if r != '"' {
			out = append(out, r)
			continue
		}
		if isOpeningQuote(chars, idx) {
			out = append(out, '“')
			continue
		}
		out = append(out, '”')
	}
	return string(out)
}

func applyCurlySingleQuotes(raw string) string {
	chars := []rune(raw)
	out := make([]rune, 0, len(chars))
	for idx, r := range chars {
		if r != '\'' {
			out = append(out, r)
			continue
		}
		prevIsLetter := idx > 0 && unicode.IsLetter(chars[idx-1])
		nextIsLetter := idx+1 < len(chars) && unicode.IsLetter(chars[idx+1])
		if prevIsLetter && nextIsLetter {
			out = append(out, '’')
			continue
		}
		if isOpeningQuote(chars, idx) {
			out = append(out, '‘')
			continue
		}
		out = append(out, '’')
	}
	return string(out)
}

func isOpeningQuote(chars []rune, idx int) bool {
	if idx == 0 {
		return true
	}
	prev := chars[idx-1]
	return unicode.IsSpace(prev) || strings.ContainsRune("([{", prev)
}

func editLocalFile(
	absPath string,
	in editInput,
	runtime *runtime,
) (editOutput, error) {
	snapshot, err := readLocalFileSnapshot(absPath, maxEditableFileSize)
	if err != nil {
		return editOutput{}, err
	}
	if !snapshot.Exists {
		if in.OldString != "" {
			return editOutput{}, fmt.Errorf("File does not exist: %s", relativePath(runtime.currentBaseDir(), absPath))
		}
		if err := writeLocalFile(absPath, in.NewString, 0, "utf8", "\n"); err != nil {
			return editOutput{}, err
		}
		current, err := readLocalFileSnapshot(absPath, runtime.maxFileSize)
		if err != nil {
			return editOutput{}, err
		}
		storeReadView(runtime.fileState, absPath, current.Content, current.Timestamp, nil, nil, "", false, false)
		return writeOutputToEditOutput(absPath, in, nil, in.NewString), nil
	}
	if strings.HasSuffix(strings.ToLower(absPath), ".ipynb") {
		return editOutput{}, fmt.Errorf("File is a Jupyter Notebook. Use the %s tool to edit this file.", toolNotebookEdit)
	}
	if isProbablyBinary(snapshot.Raw) {
		return editOutput{}, fmt.Errorf("This tool cannot edit binary files.")
	}
	if in.OldString == in.NewString {
		return editOutput{}, fmt.Errorf("No changes to make: old_string and new_string are exactly the same.")
	}
	if err := ensureWriteAllowed(absPath, snapshot, runtime.fileState); err != nil {
		return editOutput{}, err
	}
	if in.OldString == "" {
		if strings.TrimSpace(snapshot.Content) != "" {
			return editOutput{}, fmt.Errorf("Cannot create new file - file already exists.")
		}
		if err := writeLocalFile(absPath, in.NewString, snapshot.Mode, snapshot.Encoding, snapshot.LineEnding); err != nil {
			return editOutput{}, err
		}
		current, err := readLocalFileSnapshot(absPath, runtime.maxFileSize)
		if err != nil {
			return editOutput{}, err
		}
		storeReadView(runtime.fileState, absPath, current.Content, current.Timestamp, nil, nil, "", false, false)
		return writeOutputToEditOutput(absPath, in, &snapshot.Content, in.NewString), nil
	}
	actualOldString := findActualString(snapshot.Content, in.OldString)
	if actualOldString == "" {
		return editOutput{}, fmt.Errorf("String to replace not found in file.\nString: %s", in.OldString)
	}
	actualNewString := preserveQuoteStyle(in.OldString, actualOldString, in.NewString)
	matchCount := strings.Count(snapshot.Content, actualOldString)
	if matchCount > 1 && !in.ReplaceAll {
		return editOutput{}, fmt.Errorf("Found %d matches of the string to replace, but replace_all is false. To replace all occurrences, set replace_all to true. To replace only one occurrence, please provide more context to uniquely identify the instance.\nString: %s", matchCount, in.OldString)
	}
	replacements := 1
	if in.ReplaceAll {
		replacements = -1
	}
	updated := strings.Replace(snapshot.Content, actualOldString, actualNewString, replacements)
	if err := writeLocalFile(absPath, updated, snapshot.Mode, snapshot.Encoding, snapshot.LineEnding); err != nil {
		return editOutput{}, err
	}
	current, err := readLocalFileSnapshot(absPath, runtime.maxFileSize)
	if err != nil {
		return editOutput{}, err
	}
	storeReadView(runtime.fileState, absPath, current.Content, current.Timestamp, nil, nil, "", false, false)
	return editOutput{
		FilePath:        absPath,
		OldString:       in.OldString,
		NewString:       in.NewString,
		OriginalFile:    snapshot.Content,
		StructuredPatch: buildStructuredPatch(snapshot.Content, updated),
		UserModified:    false,
		ReplaceAll:      in.ReplaceAll,
	}, nil
}

func writeOutputToEditOutput(absPath string, in editInput, oldContent *string, newContent string) editOutput {
	originalFile := ""
	if oldContent != nil {
		originalFile = *oldContent
	}
	return editOutput{
		FilePath:        absPath,
		OldString:       in.OldString,
		NewString:       in.NewString,
		OriginalFile:    originalFile,
		StructuredPatch: buildStructuredPatch(originalFile, newContent),
		UserModified:    false,
		ReplaceAll:      in.ReplaceAll,
	}
}
