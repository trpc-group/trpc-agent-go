//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package file

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"unicode/utf8"

	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
	"trpc.group/trpc-go/trpc-agent-go/internal/fileref"
	"trpc.group/trpc-go/trpc-agent-go/internal/toolcache"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

// readFileRequest represents the input for the read file operation.
type readFileRequest struct {
	FileName  string `json:"file_name" description:"Relative file path under base_directory, workspace:// or artifact:// file ref, or an absolute path under base_directory or a configured read-only root"`
	StartLine *int   `json:"start_line,omitempty" jsonschema:"description=Optional 1-based start line to begin reading from"`
	NumLines  *int   `json:"num_lines,omitempty" jsonschema:"description=Optional maximum number of lines to return"`
}

// readFileResponse represents the output from the read file operation.
type readFileResponse struct {
	BaseDirectory string `json:"base_directory"`
	FileName      string `json:"file_name"`
	Contents      string `json:"contents"`
	Message       string `json:"message"`
}

// readFile performs the read file operation.
func (f *fileToolSet) readFile(
	ctx context.Context,
	req *readFileRequest,
) (*readFileResponse, error) {
	rsp := &readFileResponse{
		BaseDirectory: f.baseDir,
		FileName:      "",
	}
	if req == nil {
		err := errors.New("request cannot be nil")
		rsp.Message = "Error: " + err.Error()
		return rsp, err
	}
	rsp.FileName = req.FileName

	// Validate the start line and number of lines.
	if err := validateReadFileRequest(req); err != nil {
		rsp.Message = fmt.Sprintf("Error: %v", err)
		return rsp, err
	}

	if ok, err := f.readFileFromRef(ctx, req, rsp); ok {
		return rsp, err
	}

	if err := f.readFileFromDiskOrCache(ctx, req, rsp); err != nil {
		return rsp, err
	}
	return rsp, nil
}

func validateReadFileRequest(req *readFileRequest) error {
	if req == nil {
		return errors.New("request cannot be nil")
	}
	if strings.TrimSpace(req.FileName) == "" {
		return errors.New("file name cannot be empty")
	}
	if req.StartLine != nil && *req.StartLine <= 0 {
		return fmt.Errorf("start line must be > 0: %v", *req.StartLine)
	}
	if req.NumLines != nil && *req.NumLines <= 0 {
		return fmt.Errorf("number of lines must be > 0: %v", *req.NumLines)
	}
	return nil
}

const (
	errNotTextFile     = "file is not a UTF-8 text file"
	errNotTextFileTmpl = "file is not a UTF-8 text file (mime: %s)"

	// utf8Replacement stands in for byte sequences that are not valid UTF-8.
	utf8Replacement = "\uFFFD"

	// invalidUTF8Note marks a read whose returned contents differ from the
	// source content because invalid UTF-8 was substituted.
	invalidUTF8Note = " (invalid UTF-8 replaced with U+FFFD)"
)

// rejectNonText reports content that is not a text file at all, and so cannot
// be repaired by substitution: a non-text MIME type, or an embedded NUL byte.
//
// Invalid UTF-8 on its own is not rejected. A handful of malformed bytes, such
// as a stray byte left by a non-UTF-8 editor or a truncated multi-byte rune,
// would otherwise make an entire readable file unreadable through this tool and
// leave a caller with no way to inspect it; sanitizeText repairs those instead.
//
// Empty content is accepted whatever the MIME type, as it always has been.
func rejectNonText(content string, mimeType string) error {
	if content == "" {
		return nil
	}
	if strings.TrimSpace(mimeType) != "" &&
		!codeexecutor.IsTextMIME(mimeType) {
		return notTextFileErr(mimeType)
	}
	if strings.IndexByte(content, 0) >= 0 {
		return notTextFileErr(mimeType)
	}
	return nil
}

// sanitizeText returns content with each run of invalid UTF-8 bytes replaced by
// U+FFFD, reporting whether any substitution was made.
//
// Callers run this on the slice they are about to return rather than on the
// whole file, so that size limits and line numbering stay measured in source
// bytes: U+FFFD is three bytes wide and so can be wider than what it replaces.
func sanitizeText(content string) (string, bool) {
	if utf8.ValidString(content) {
		return content, false
	}
	return strings.ToValidUTF8(content, utf8Replacement), true
}

func notTextFileErr(mimeType string) error {
	mt := strings.TrimSpace(mimeType)
	if mt == "" {
		return errors.New(errNotTextFile)
	}
	return fmt.Errorf(errNotTextFileTmpl, mt)
}

func (f *fileToolSet) readFileFromRef(
	ctx context.Context,
	req *readFileRequest,
	rsp *readFileResponse,
) (bool, error) {
	content, mimeType, handled, err := fileref.TryRead(ctx, req.FileName)
	if !handled {
		return false, nil
	}
	if err != nil {
		rsp.Message = fmt.Sprintf("Error: %v", err)
		return true, err
	}

	if err := rejectNonText(content, mimeType); err != nil {
		rsp.Message = fmt.Sprintf("Error: %v", err)
		return true, err
	}

	ref, _ := fileref.Parse(req.FileName)
	source := "ref"
	switch ref.Scheme {
	case fileref.SchemeWorkspace:
		source = fileref.WorkspacePrefix
	case fileref.SchemeArtifact:
		source = fileref.ArtifactPrefix
	}

	chunk, start, end, total, empty, err := f.sliceReadFile(req, content)
	if err != nil {
		rsp.Message = fmt.Sprintf("Error: %v", err)
		return true, err
	}
	chunk, replaced := sanitizeText(chunk)
	rsp.Contents = chunk
	if empty {
		rsp.Message = fmt.Sprintf(
			"Successfully read %s from %s, but file is empty",
			req.FileName,
			source,
		)
		return true, nil
	}
	rsp.Message = fmt.Sprintf(
		"Successfully read %s from %s, start line: %d, "+
			"end line: %d, total lines: %d",
		req.FileName,
		source,
		start,
		end,
		total,
	)
	if replaced {
		rsp.Message += invalidUTF8Note
	}
	return true, nil
}

func (f *fileToolSet) readFileFromDiskOrCache(
	ctx context.Context,
	req *readFileRequest,
	rsp *readFileResponse,
) error {
	filePath, err := f.resolveReadPath(req.FileName)
	if err != nil {
		rsp.Message = fmt.Sprintf("Error: %v", err)
		return err
	}
	stat, err := os.Stat(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			if ok, err := f.readFileFromCache(ctx, req, rsp); ok {
				return err
			}
		}
		rsp.Message = fmt.Sprintf(
			"Error: cannot access file '%s': %v. %s",
			req.FileName,
			err,
			f.missingFileHint(),
		)
		return fmt.Errorf(
			"accessing file '%s' under base directory '%s': %w",
			req.FileName,
			f.baseDir,
			err,
		)
	}
	if stat.IsDir() {
		rsp.Message = fmt.Sprintf(
			"Error: target path '%s' is a directory",
			req.FileName,
		)
		return fmt.Errorf(
			"target path '%s' is a directory",
			req.FileName,
		)
	}
	if stat.Size() > f.maxFileSize {
		if req.StartLine != nil || req.NumLines != nil {
			return f.readLargeFileRange(ctx, req, rsp, filePath)
		}
		rsp.Message = fmt.Sprintf(
			"Error: file is too large: %d > %d. Pass start_line/"+
				"num_lines to read a slice of it, or use "+
				"search_content to find the lines you need",
			stat.Size(),
			f.maxFileSize,
		)
		return fmt.Errorf(
			"file is too large: %d > %d",
			stat.Size(),
			f.maxFileSize,
		)
	}

	contents, err := os.ReadFile(filePath)
	if err != nil {
		rsp.Message = fmt.Sprintf("Error: cannot read file: %v", err)
		return fmt.Errorf("reading file: %w", err)
	}
	mimeType := http.DetectContentType(contents)
	if err := rejectNonText(string(contents), mimeType); err != nil {
		rsp.Message = fmt.Sprintf("Error: %v", err)
		return err
	}
	chunk, startLine, endLine, total, empty, err := f.sliceReadFile(
		req,
		string(contents),
	)
	if err != nil {
		rsp.Message = fmt.Sprintf("Error: %v", err)
		return err
	}
	chunk, replaced := sanitizeText(chunk)
	rsp.Contents = chunk
	if empty {
		rsp.Message = fmt.Sprintf(
			"Successfully read %s, but file is empty",
			req.FileName,
		)
		return nil
	}
	rsp.Message = fmt.Sprintf(
		"Successfully read %s, start line: %d, "+
			"end line: %d, total lines: %d",
		req.FileName,
		startLine,
		endLine,
		total,
	)
	if replaced {
		rsp.Message += invalidUTF8Note
	}
	return nil
}

func (f *fileToolSet) readFileFromCache(
	ctx context.Context,
	req *readFileRequest,
	rsp *readFileResponse,
) (bool, error) {
	content, mime, ok := toolcache.LookupSkillRunOutputFileFromContext(
		ctx,
		req.FileName,
	)
	if !ok {
		return false, nil
	}

	if err := rejectNonText(content, mime); err != nil {
		rsp.Message = fmt.Sprintf("Error: %v", err)
		return true, err
	}

	chunk, start, end, total, empty, err := f.sliceReadFile(req, content)
	if err != nil {
		rsp.Message = fmt.Sprintf("Error: %v", err)
		return true, err
	}
	chunk, replaced := sanitizeText(chunk)
	rsp.Contents = chunk
	if empty {
		rsp.Message = fmt.Sprintf(
			"Successfully read %s, but file is empty",
			req.FileName,
		)
		return true, nil
	}
	rsp.Message = fmt.Sprintf(
		"Loaded %s from a prior skill_run output_files "+
			"cache, start line: %d, end line: %d, "+
			"total lines: %d (mime: %s)",
		req.FileName,
		start,
		end,
		total,
		mime,
	)
	if replaced {
		rsp.Message += invalidUTF8Note
	}
	return true, nil
}

// readLargeFileRange serves a ranged read of a file too large to read whole.
// It streams the file line by line, so memory holds only the requested range,
// which itself must stay within maxFileSize — the limit protects what is
// returned to the model, not what the tool may look at. Once num_lines is
// satisfied the read stops rather than scanning to EOF for an exact line
// count, so the message then reports the total as a lower bound.
func (f *fileToolSet) readLargeFileRange(
	ctx context.Context,
	req *readFileRequest,
	rsp *readFileResponse,
	filePath string,
) error {
	file, err := os.Open(filePath)
	if err != nil {
		rsp.Message = fmt.Sprintf("Error: cannot read file: %v", err)
		return fmt.Errorf("reading file: %w", err)
	}
	defer file.Close()
	reader := bufio.NewReaderSize(file, 64*1024)
	head, _ := reader.Peek(512)
	mimeType := http.DetectContentType(head)
	if err := rejectNonText(string(head), mimeType); err != nil {
		rsp.Message = fmt.Sprintf("Error: %v", err)
		return err
	}

	start := 1
	if req.StartLine != nil {
		start = *req.StartLine
	}
	var lines []string
	var collected int64
	lineNo := 0
	endLine := 0
	rangeSatisfied := false
	for {
		if lineNo&1023 == 0 {
			if err := ctx.Err(); err != nil {
				rsp.Message = fmt.Sprintf("Error: %v", err)
				return err
			}
		}
		segment, rerr := reader.ReadString('\n')
		atEOF := errors.Is(rerr, io.EOF)
		if rerr != nil && !atEOF {
			rsp.Message = fmt.Sprintf("Error: cannot read file: %v", rerr)
			return fmt.Errorf("reading file: %w", rerr)
		}
		// Mirror strings.Split semantics: every segment is a line, including
		// the empty final segment after a trailing newline.
		lineNo++
		inRange := lineNo >= start &&
			(req.NumLines == nil || len(lines) < *req.NumLines)
		if inRange {
			line := strings.TrimSuffix(segment, "\n")
			collected += int64(len(line)) + 1
			if collected > f.maxFileSize {
				err := fmt.Errorf(
					"selected range is larger than %d bytes; "+
						"request fewer lines",
					f.maxFileSize,
				)
				rsp.Message = "Error: " + err.Error()
				return err
			}
			lines = append(lines, line)
			endLine = lineNo
			if req.NumLines != nil && len(lines) == *req.NumLines {
				rangeSatisfied = !atEOF
				break
			}
		}
		if atEOF {
			break
		}
	}
	total := lineNo
	if !rangeSatisfied && start > total {
		err := fmt.Errorf(
			"start line is out of range, start line: %d, "+
				"total lines: %d",
			start,
			total,
		)
		rsp.Message = "Error: " + err.Error()
		return err
	}
	chunk := strings.Join(lines, "\n")
	if err := rejectNonText(chunk, mimeType); err != nil {
		rsp.Message = fmt.Sprintf("Error: %v", err)
		return err
	}
	chunk, replaced := sanitizeText(chunk)
	rsp.Contents = chunk
	totalDesc := fmt.Sprintf("%d", total)
	if rangeSatisfied {
		totalDesc = fmt.Sprintf("at least %d", total)
	}
	rsp.Message = fmt.Sprintf(
		"Successfully read %s, start line: %d, "+
			"end line: %d, total lines: %s",
		req.FileName,
		start,
		endLine,
		totalDesc,
	)
	if replaced {
		rsp.Message += invalidUTF8Note
	}
	return nil
}

func (f *fileToolSet) sliceReadFile(
	req *readFileRequest,
	content string,
) (string, int, int, int, bool, error) {
	wholeFile := req.StartLine == nil && req.NumLines == nil
	if wholeFile && int64(len(content)) > f.maxFileSize {
		return "", 0, 0, 0, false, fmt.Errorf(
			"file size is beyond of max file size, "+
				"file size: %d, max file size: %d",
			len(content),
			f.maxFileSize,
		)
	}
	if content == "" {
		return "", 0, 0, 0, true, nil
	}
	chunk, start, end, total, err := sliceTextByLines(
		content,
		req.StartLine,
		req.NumLines,
	)
	if err != nil {
		return "", 0, 0, 0, false, err
	}
	if int64(len(chunk)) > f.maxFileSize {
		return "", 0, 0, 0, false, fmt.Errorf(
			"selected range is larger than %d bytes; "+
				"request fewer lines",
			f.maxFileSize,
		)
	}
	return chunk, start, end, total, false, nil
}

func sliceTextByLines(
	text string,
	startLine *int,
	numLines *int,
) (string, int, int, int, error) {
	lines := strings.Split(text, "\n")
	totalLines := len(lines)
	if totalLines == 0 {
		return "", 0, 0, 0, nil
	}

	start := 1
	limit := totalLines
	if startLine != nil {
		start = *startLine
	}
	if numLines != nil {
		limit = *numLines
	}
	if start > totalLines {
		return "", 0, 0, totalLines, fmt.Errorf(
			"start line is out of range, start line: %d, "+
				"total lines: %d",
			start,
			totalLines,
		)
	}
	end := start + limit - 1
	if end > totalLines {
		end = totalLines
	}
	return strings.Join(lines[start-1:end], "\n"),
		start,
		end,
		totalLines,
		nil
}

// readFileTool returns a callable tool for reading file.
func (f *fileToolSet) readFileTool() tool.CallableTool {
	return function.NewFunctionTool(
		f.readFile,
		function.WithName("read_file"),
		function.WithDescription(
			"Read a text file under base_directory. Supports "+
				"workspace:// refs, artifact:// refs, and "+
				"absolute paths under base_directory or configured "+
				"read-only roots. "+
				"Optional start_line and num_lines select line "+
				"ranges.",
		),
	)
}
