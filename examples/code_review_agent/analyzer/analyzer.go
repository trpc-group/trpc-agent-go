// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
// Package analyzer 提供基于 Go AST 的代码分析能力。
//
// 与正则匹配不同，AST 分析能理解代码结构：
//   - 跟踪变量赋值和数据流
//   - 检测未使用的返回值
//   - 理解函数调用链
//   - 分析控制流和错误处理
//
// 使用 Go 标准库 go/ast、go/parser、go/types。
package analyzer

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
)

// FileAnalysis 是一个文件的 AST 分析结果。
type FileAnalysis struct {
	FilePath    string          `json:"file_path"`
	PackageName string          `json:"package_name"`
	Imports     []ImportInfo    `json:"imports"`
	Functions   []FuncInfo      `json:"functions"`
	Globals     []VarInfo       `json:"globals"`
	Errors      []AnalysisError `json:"errors"`
}

// ImportInfo 导入信息。
type ImportInfo struct {
	Path  string `json:"path"`  // 导入路径
	Alias string `json:"alias"` // 别名（如有）
	Name  string `json:"name"`  // 包名（路径的最后一段）
}

// FuncInfo 函数信息。
type FuncInfo struct {
	Name       string         `json:"name"`        // 函数名
	Receiver   string         `json:"receiver"`    // 接收者类型（方法）
	IsExported bool           `json:"is_exported"` // 是否导出
	Params     []string       `json:"params"`      // 参数类型
	Returns    []string       `json:"returns"`     // 返回值类型
	Line       int            `json:"line"`        // 行号
	Body       *ast.BlockStmt `json:"-"`           // 函数体（不序列化）
}

// VarInfo 变量信息。
type VarInfo struct {
	Name       string `json:"name"`
	IsExported bool   `json:"is_exported"`
	Type       string `json:"type"`
	Line       int    `json:"line"`
}

// AnalysisError 分析过程中的错误。
type AnalysisError struct {
	Message string `json:"message"`
	Line    int    `json:"line"`
}

// Analyzer 是 Go AST 分析器。
type Analyzer struct {
	fset *token.FileSet
}

// NewAnalyzer 创建一个新的 AST 分析器。
func NewAnalyzer() *Analyzer {
	return &Analyzer{
		fset: token.NewFileSet(),
	}
}

// AnalyzeFile 分析单个 Go 文件。
func (a *Analyzer) AnalyzeFile(filePath string, src []byte) (*FileAnalysis, error) {
	// 解析源代码为 AST
	file, err := parser.ParseFile(a.fset, filePath, src, parser.ParseComments)
	if err != nil {
		return nil, fmt.Errorf("解析文件失败 %s: %w", filePath, err)
	}

	analysis := &FileAnalysis{
		FilePath:    filePath,
		PackageName: file.Name.Name,
	}

	// 提取导入信息
	analysis.Imports = a.extractImports(file)

	// 提取函数信息
	analysis.Functions = a.extractFunctions(file)

	// 提取全局变量
	analysis.Globals = a.extractGlobals(file)

	return analysis, nil
}

// AnalyzeSource 分析源代码字符串。
func (a *Analyzer) AnalyzeSource(filename, src string) (*FileAnalysis, error) {
	return a.AnalyzeFile(filename, []byte(src))
}

// FSet 返回文件位置集合（用于错误报告）。
func (a *Analyzer) FSet() *token.FileSet {
	return a.fset
}

// ========== 提取方法 ==========

// extractImports 提取导入信息。
func (a *Analyzer) extractImports(file *ast.File) []ImportInfo {
	var imports []ImportInfo
	for _, imp := range file.Imports {
		info := ImportInfo{
			Path: strings.Trim(imp.Path.Value, "\""),
		}
		// 提取包名（路径最后一段）
		parts := strings.Split(info.Path, "/")
		info.Name = parts[len(parts)-1]

		// 提取别名
		if imp.Name != nil {
			info.Alias = imp.Name.Name
			if info.Alias == "_" {
				info.Name = "_" // 空导入
			}
		}

		imports = append(imports, info)
	}
	return imports
}

// extractFunctions 提取函数信息。
func (a *Analyzer) extractFunctions(file *ast.File) []FuncInfo {
	var functions []FuncInfo

	for _, decl := range file.Decls {
		funcDecl, ok := decl.(*ast.FuncDecl)
		if !ok {
			continue
		}

		info := FuncInfo{
			Name:       funcDecl.Name.Name,
			IsExported: funcDecl.Name.IsExported(),
			Line:       a.fset.Position(funcDecl.Pos()).Line,
			Body:       funcDecl.Body,
		}

		// 提取接收者
		if funcDecl.Recv != nil && len(funcDecl.Recv.List) > 0 {
			recv := funcDecl.Recv.List[0]
			info.Receiver = a.typeToString(recv.Type)
		}

		// 提取参数类型
		if funcDecl.Type.Params != nil {
			for _, param := range funcDecl.Type.Params.List {
				typeStr := a.typeToString(param.Type)
				for range param.Names {
					info.Params = append(info.Params, typeStr)
				}
				if len(param.Names) == 0 {
					info.Params = append(info.Params, typeStr)
				}
			}
		}

		// 提取返回值类型
		if funcDecl.Type.Results != nil {
			for _, result := range funcDecl.Type.Results.List {
				typeStr := a.typeToString(result.Type)
				if len(result.Names) == 0 {
					info.Returns = append(info.Returns, typeStr)
				} else {
					for range result.Names {
						info.Returns = append(info.Returns, typeStr)
					}
				}
			}
		}

		functions = append(functions, info)
	}

	return functions
}

// extractGlobals 提取全局变量声明。
func (a *Analyzer) extractGlobals(file *ast.File) []VarInfo {
	var globals []VarInfo

	for _, decl := range file.Decls {
		genDecl, ok := decl.(*ast.GenDecl)
		if !ok {
			continue
		}

		if genDecl.Tok != token.VAR {
			continue
		}

		for _, spec := range genDecl.Specs {
			valueSpec, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}

			for _, name := range valueSpec.Names {
				info := VarInfo{
					Name:       name.Name,
					IsExported: name.IsExported(),
					Line:       a.fset.Position(name.Pos()).Line,
				}
				if valueSpec.Type != nil {
					info.Type = a.typeToString(valueSpec.Type)
				}
				globals = append(globals, info)
			}
		}
	}

	return globals
}

// typeToString 将 AST 类型节点转换为字符串。
func (a *Analyzer) typeToString(expr ast.Expr) string {
	switch t := expr.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.StarExpr:
		return "*" + a.typeToString(t.X)
	case *ast.SelectorExpr:
		return a.typeToString(t.X) + "." + t.Sel.Name
	case *ast.ArrayType:
		return "[]" + a.typeToString(t.Elt)
	case *ast.MapType:
		return "map[" + a.typeToString(t.Key) + "]" + a.typeToString(t.Value)
	case *ast.FuncType:
		return "func"
	case *ast.InterfaceType:
		return "any"
	case *ast.ChanType:
		return "chan"
	default:
		return fmt.Sprintf("%T", expr)
	}
}
