//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package file

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	toolpkg "trpc.group/trpc-go/trpc-agent-go/tool"
)

func TestFileTool_InputSchemaDescriptions(t *testing.T) {
	set, err := NewToolSet(WithBaseDir(t.TempDir()))
	require.NoError(t, err)

	declarations := make(map[string]*toolpkg.Declaration)
	for _, candidate := range set.Tools(context.Background()) {
		decl := candidate.Declaration()
		declarations[decl.Name] = decl
	}

	expected := map[string]map[string]string{
		"save_file": {
			"file_name": "Relative file path under base_directory to write",
			"contents":  "Text content to write into the file",
			"overwrite": "Whether to replace the file if it already exists",
		},
		"read_file": {
			"file_name":  "Relative file path under base_directory or workspace:// or artifact:// file ref to read",
			"start_line": "Optional 1-based start line to begin reading from",
			"num_lines":  "Optional maximum number of lines to return",
		},
		"read_multiple_files": {
			"patterns":       "Glob patterns to read such as *.go or workspace://out/*.txt",
			"case_sensitive": "Whether glob matching should be case-sensitive",
		},
		"list_file": {
			"path":      "Relative directory path under base_directory or workspace:// directory ref; empty means the base directory",
			"with_size": "Whether to include file sizes in files_with_size",
		},
		"search_file": {
			"path":           "Relative directory path under base_directory or workspace:// directory ref; empty means the base directory",
			"pattern":        "Glob pattern to match files or folders such as *.go or **/*.md",
			"case_sensitive": "Whether glob matching should be case-sensitive",
		},
		"search_content": {
			"path":                   "Relative directory path under base_directory or workspace:// directory ref; can also be a single local file path",
			"file_pattern":           "Glob pattern for files to search or a direct workspace:// or artifact:// file ref",
			"file_case_sensitive":    "Whether file pattern matching should be case-sensitive",
			"content_pattern":        "Regular expression to search for within matched files",
			"content_case_sensitive": "Whether regular expression matching should be case-sensitive",
		},
		"replace_content": {
			"file_name":        "Relative file path under base_directory to modify",
			"old_string":       "Existing text to replace; supports multi-line content",
			"new_string":       "Replacement text; supports multi-line content",
			"num_replacements": "Optional replacement limit; 0 means 1 and negative means replace all matches",
		},
	}

	for toolName, properties := range expected {
		decl, ok := declarations[toolName]
		require.Truef(t, ok, "missing declaration for %s", toolName)
		require.NotNilf(t, decl.InputSchema, "missing input schema for %s", toolName)

		for propertyName, wantDescription := range properties {
			propertySchema, ok := decl.InputSchema.Properties[propertyName]
			require.Truef(
				t,
				ok,
				"missing %s input schema property for %s",
				propertyName,
				toolName,
			)
			assert.Equal(t, wantDescription, propertySchema.Description)
		}
	}
}
