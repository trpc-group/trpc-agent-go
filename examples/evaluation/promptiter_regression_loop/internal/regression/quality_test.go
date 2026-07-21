//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package regression

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	maximumFileLines          = 1000
	maximumFunctionLines      = 80
	maximumFunctionStatements = 60
	maximumCyclomatic         = 15
	maximumParameters         = 4
)

func TestCodeQualityLimits(t *testing.T) {
	root := filepath.Clean(filepath.Join("..", ".."))
	paths, err := goFiles(root)
	require.NoError(t, err)
	require.NotEmpty(t, paths)
	files := token.NewFileSet()
	for _, path := range paths {
		data, readErr := os.ReadFile(path)
		require.NoError(t, readErr)
		assert.LessOrEqual(t, lineCount(data), maximumFileLines, path)
		parsed, parseErr := parser.ParseFile(files, path, data, parser.SkipObjectResolution)
		require.NoError(t, parseErr)
		checkFunctions(t, files, path, parsed)
	}
}

func goFiles(root string) ([]string, error) {
	paths := make([]string, 0)
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") {
			return nil
		}
		paths = append(paths, path)
		return nil
	})
	return paths, err
}

func checkFunctions(t *testing.T, files *token.FileSet, path string, file *ast.File) {
	t.Helper()
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || function.Body == nil {
			continue
		}
		label := path + ":" + function.Name.Name
		lines := files.Position(function.End()).Line - files.Position(function.Pos()).Line + 1
		assert.LessOrEqual(t, lines, maximumFunctionLines, label)
		assert.LessOrEqual(t, statementCount(function.Body), maximumFunctionStatements, label)
		assert.LessOrEqual(t, cyclomaticComplexity(function.Body), maximumCyclomatic, label)
		assert.LessOrEqual(t, parameterCount(function.Type.Params), maximumParameters, label)
	}
}

func statementCount(body *ast.BlockStmt) int {
	count := 0
	ast.Inspect(body, func(node ast.Node) bool {
		if _, ok := node.(ast.Stmt); ok {
			if _, block := node.(*ast.BlockStmt); !block {
				count++
			}
		}
		return true
	})
	return count
}

func cyclomaticComplexity(body *ast.BlockStmt) int {
	complexity := 1
	ast.Inspect(body, func(node ast.Node) bool {
		switch typed := node.(type) {
		case *ast.IfStmt, *ast.ForStmt, *ast.RangeStmt:
			complexity++
		case *ast.CaseClause:
			complexity++
		case *ast.BinaryExpr:
			if typed.Op == token.LAND || typed.Op == token.LOR {
				complexity++
			}
		}
		return true
	})
	return complexity
}

func parameterCount(fields *ast.FieldList) int {
	if fields == nil {
		return 0
	}
	count := 0
	for _, field := range fields.List {
		if len(field.Names) == 0 {
			count++
			continue
		}
		count += len(field.Names)
	}
	return count
}

func lineCount(data []byte) int {
	return strings.Count(string(data), "\n") + 1
}
