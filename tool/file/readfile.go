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
	"bytes"
	"context"
	"fmt"
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
	FileName  string `json:"file_name" jsonschema:"description=Relative path"`
	StartLine *int   `json:"start_line,omitempty" jsonschema:"description=Start"`
	NumLines  *int   `json:"num_lines,omitempty" jsonschema:"description=Max"`
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
		err := fmt.Errorf("request cannot be nil")
		rsp.Message = fmt.Sprintf("Error: %v", err)
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
		return fmt.Errorf("request cannot be nil")
	}
	if strings.TrimSpace(req.FileName) == "" {
		return fmt.Errorf("file name cannot be empty")
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
)

func validateTextString(content string, mimeType string) error {
	if content == "" {
		return nil
	}
	if strings.TrimSpace(mimeType) != "" &&
		!codeexecutor.IsTextMIME(mimeType) {
		return notTextFileErr(mimeType)
	}
	if strings.IndexByte(content, 0) >= 0 ||
		!utf8.ValidString(content) {
		return notTextFileErr(mimeType)
	}
	return nil
}

func validateTextBytes(data []byte, mimeType string) error {
	if len(data) == 0 {
		return nil
	}
	if strings.TrimSpace(mimeType) != "" &&
		!codeexecutor.IsTextMIME(mimeType) {
		return notTextFileErr(mimeType)
	}
	if bytes.IndexByte(data, 0) >= 0 || !utf8.Valid(data) {
		return notTextFileErr(mimeType)
	}
	return nil
}

func notTextFileErr(mimeType string) error {
	mt := strings.TrimSpace(mimeType)
	if mt == "" {
		return fmt.Errorf(errNotTextFile)
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

	if err := validateTextString(content, mimeType); err != nil {
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
	return true, nil
}

func (f *fileToolSet) readFileFromDiskOrCache(
	ctx context.Context,
	req *readFileRequest,
	rsp *readFileResponse,
) error {
	filePath, err := f.resolvePath(req.FileName)
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
			"Error: cannot access file '%s': %v",
			req.FileName,
			err,
		)
		return fmt.Errorf("accessing file '%s': %w", req.FileName, err)
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
		rsp.Message = fmt.Sprintf(
			"Error: file is too large: %d > %d",
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
	if err := validateTextBytes(contents, mimeType); err != nil {
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

	if err := validateTextString(content, mime); err != nil {
		rsp.Message = fmt.Sprintf("Error: %v", err)
		return true, err
	}

	chunk, start, end, total, empty, err := f.sliceReadFile(req, content)
	if err != nil {
		rsp.Message = fmt.Sprintf("Error: %v", err)
		return true, err
	}
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
	return true, nil
}

func (f *fileToolSet) sliceReadFile(
	req *readFileRequest,
	content string,
) (string, int, int, int, bool, error) {
	if int64(len(content)) > f.maxFileSize {
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
	return chunk, start, end, total, false, err
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
				"workspace:// and artifact:// refs. Optional "+
				"start_line and num_lines select line ranges.",
		),
	)
}
