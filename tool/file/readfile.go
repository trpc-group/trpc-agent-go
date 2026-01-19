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
	"context"
	"fmt"
	"os"
	"strings"

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
		FileName:      req.FileName,
	}
	// Validate the start line and number of lines.
	if req.StartLine != nil && *req.StartLine <= 0 {
		rsp.Message = fmt.Sprintf(
			"Error: start line must be > 0: %v",
			*req.StartLine,
		)
		return rsp, fmt.Errorf("start line must be > 0: %v",
			*req.StartLine,
		)
	}
	if req.NumLines != nil && *req.NumLines <= 0 {
		rsp.Message = fmt.Sprintf(
			"Error: number of lines must be > 0: %v",
			*req.NumLines,
		)
		return rsp, fmt.Errorf("number of lines must be > 0: %v",
			*req.NumLines,
		)
	}

	content, _, handled, err := fileref.TryRead(ctx, req.FileName)
	if handled {
		if err != nil {
			rsp.Message = fmt.Sprintf("Error: %v", err)
			return rsp, err
		}
		ref, _ := fileref.Parse(req.FileName)
		source := "ref"
		switch ref.Scheme {
		case fileref.SchemeWorkspace:
			source = fileref.WorkspacePrefix
		case fileref.SchemeArtifact:
			source = fileref.ArtifactPrefix
		}
		if int64(len(content)) > f.maxFileSize {
			rsp.Message = fmt.Sprintf(
				"Error: file size is beyond of max file size, "+
					"file size: %d, max file size: %d",
				len(content),
				f.maxFileSize,
			)
			return rsp, fmt.Errorf(
				"file size is beyond of max file size, "+
					"file size: %d, max file size: %d",
				len(content),
				f.maxFileSize,
			)
		}
		if content == "" {
			rsp.Message = fmt.Sprintf(
				"Successfully read %s from %s, but file is empty",
				req.FileName,
				source,
			)
			rsp.Contents = ""
			return rsp, nil
		}
		chunk, start, end, total, err := sliceTextByLines(
			content,
			req.StartLine,
			req.NumLines,
		)
		if err != nil {
			rsp.Message = fmt.Sprintf("Error: %v", err)
			return rsp, err
		}
		rsp.Contents = chunk
		rsp.Message = fmt.Sprintf(
			"Successfully read %s from %s, start line: %d, "+
				"end line: %d, total lines: %d",
			req.FileName,
			source,
			start,
			end,
			total,
		)
		return rsp, nil
	}
	// Resolve and validate the file path.
	filePath, err := f.resolvePath(req.FileName)
	if err != nil {
		rsp.Message = fmt.Sprintf("Error: %v", err)
		return rsp, err
	}
	// Check if the target path exists.
	stat, err := os.Stat(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			content, mime, ok := toolcache.LookupSkillRunOutputFileFromContext(
				ctx,
				req.FileName,
			)
			if ok {
				if int64(len(content)) > f.maxFileSize {
					rsp.Message = fmt.Sprintf(
						"Error: file size is beyond of max "+
							"file size, file size: %d, "+
							"max file size: %d",
						len(content),
						f.maxFileSize,
					)
					return rsp, fmt.Errorf(
						"file size is beyond of max file "+
							"size, file size: %d, "+
							"max file size: %d",
						len(content),
						f.maxFileSize,
					)
				}
				if content == "" {
					rsp.Message = fmt.Sprintf(
						"Successfully read %s, but file "+
							"is empty",
						req.FileName,
					)
					rsp.Contents = ""
					return rsp, nil
				}
				chunk, start, end, total, err := sliceTextByLines(
					content,
					req.StartLine,
					req.NumLines,
				)
				if err != nil {
					rsp.Message = fmt.Sprintf("Error: %v", err)
					return rsp, err
				}
				rsp.Contents = chunk
				rsp.Message = fmt.Sprintf(
					"Loaded %s from a prior skill_run "+
						"output_files cache, start line: %d, "+
						"end line: %d, total lines: %d "+
						"(mime: %s)",
					req.FileName,
					start,
					end,
					total,
					mime,
				)
				return rsp, nil
			}
		}
		rsp.Message = fmt.Sprintf(
			"Error: cannot access file '%s': %v",
			req.FileName,
			err,
		)
		return rsp, fmt.Errorf("accessing file '%s': %w", req.FileName, err)
	}
	// Check if the target path is a file.
	if stat.IsDir() {
		rsp.Message = fmt.Sprintf(
			"Error: target path '%s' is a directory",
			req.FileName,
		)
		return rsp, fmt.Errorf(
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
		return rsp, fmt.Errorf(
			"file is too large: %d > %d",
			stat.Size(),
			f.maxFileSize,
		)
	}
	// Read the file.
	contents, err := os.ReadFile(filePath)
	if err != nil {
		rsp.Message = fmt.Sprintf("Error: cannot read file: %v", err)
		return rsp, fmt.Errorf("reading file: %w", err)
	}
	if len(contents) == 0 {
		rsp.Message = fmt.Sprintf(
			"Successfully read %s, but file is empty",
			req.FileName,
		)
		rsp.Contents = ""
		return rsp, nil
	}
	// Split the file contents into lines.
	chunk, startLine, endLine, totalLines, err := sliceTextByLines(
		string(contents),
		req.StartLine,
		req.NumLines,
	)
	if err != nil {
		rsp.Message = fmt.Sprintf("Error: %v", err)
		return rsp, err
	}
	rsp.Contents = chunk
	rsp.Message = fmt.Sprintf(
		"Successfully read %s, start line: %d, "+
			"end line: %d, total lines: %d",
		rsp.FileName,
		startLine,
		endLine,
		totalLines,
	)
	return rsp, nil
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
