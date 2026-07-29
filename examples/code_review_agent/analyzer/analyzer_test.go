// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
package analyzer

import (
	"testing"
)

func TestAnalyzeSource_BasicPackage(t *testing.T) {
	a := NewAnalyzer()
	src := `package main

import "fmt"

func main() {
	fmt.Println("hello")
}
`
	result, err := a.AnalyzeSource("main.go", src)
	if err != nil {
		t.Fatalf("AnalyzeSource 失败: %v", err)
	}

	if result.PackageName != "main" {
		t.Errorf("PackageName = %q, 期望 %q", result.PackageName, "main")
	}
	if result.FilePath != "main.go" {
		t.Errorf("FilePath = %q, 期望 %q", result.FilePath, "main.go")
	}
}

func TestAnalyzeSource_Imports(t *testing.T) {
	a := NewAnalyzer()
	src := `package main

import (
	"fmt"
	"os"
	mylib "github.com/user/mylib"
	_ "database/sql"
)

func main() {}
`
	result, err := a.AnalyzeSource("main.go", src)
	if err != nil {
		t.Fatalf("AnalyzeSource 失败: %v", err)
	}

	if len(result.Imports) != 4 {
		t.Fatalf("Imports 数量 = %d, 期望 4", len(result.Imports))
	}

	tests := []struct {
		path  string
		alias string
		name  string
	}{
		{"fmt", "", "fmt"},
		{"os", "", "os"},
		{"github.com/user/mylib", "mylib", "mylib"},
		{"database/sql", "_", "sql"},
	}

	for i, tt := range tests {
		imp := result.Imports[i]
		if imp.Path != tt.path {
			t.Errorf("Import[%d].Path = %q, 期望 %q", i, imp.Path, tt.path)
		}
		if imp.Alias != tt.alias {
			t.Errorf("Import[%d].Alias = %q, 期望 %q", i, imp.Alias, tt.alias)
		}
	}
}

func TestAnalyzeSource_Functions(t *testing.T) {
	a := NewAnalyzer()
	src := `package main

import "fmt"

// ExportedFunc 是一个导出函数
func ExportedFunc(a int, b string) (string, error) {
	return "", nil
}

func privateFunc() {
	fmt.Println("private")
}

// Method 是一个方法
func (s *Service) Method(name string) error {
	return nil
}

func main() {}
`
	result, err := a.AnalyzeSource("main.go", src)
	if err != nil {
		t.Fatalf("AnalyzeSource 失败: %v", err)
	}

	if len(result.Functions) != 4 {
		t.Fatalf("Functions 数量 = %d, 期望 4", len(result.Functions))
	}

	// ExportedFunc
	f0 := result.Functions[0]
	if f0.Name != "ExportedFunc" {
		t.Errorf("Functions[0].Name = %q, 期望 %q", f0.Name, "ExportedFunc")
	}
	if !f0.IsExported {
		t.Error("ExportedFunc 应该是导出的")
	}
	if len(f0.Params) != 2 {
		t.Errorf("ExportedFunc 参数数量 = %d, 期望 2", len(f0.Params))
	}
	if f0.Params[0] != "int" || f0.Params[1] != "string" {
		t.Errorf("ExportedFunc 参数 = %v, 期望 [int string]", f0.Params)
	}
	if len(f0.Returns) != 2 {
		t.Errorf("ExportedFunc 返回值数量 = %d, 期望 2", len(f0.Returns))
	}

	// privateFunc
	f1 := result.Functions[1]
	if f1.Name != "privateFunc" {
		t.Errorf("Functions[1].Name = %q, 期望 %q", f1.Name, "privateFunc")
	}
	if f1.IsExported {
		t.Error("privateFunc 不应该是导出的")
	}

	// Method
	f2 := result.Functions[2]
	if f2.Name != "Method" {
		t.Errorf("Functions[2].Name = %q, 期望 %q", f2.Name, "Method")
	}
	if f2.Receiver != "*Service" {
		t.Errorf("Method Receiver = %q, 期望 %q", f2.Receiver, "*Service")
	}
}

func TestAnalyzeSource_Globals(t *testing.T) {
	a := NewAnalyzer()
	src := `package config

import "os"

var (
	Host    string = "localhost"
	Port    int    = 8080
	private bool
)

var GlobalError error

func init() {}
`
	result, err := a.AnalyzeSource("config.go", src)
	if err != nil {
		t.Fatalf("AnalyzeSource 失败: %v", err)
	}

	if len(result.Globals) != 4 {
		t.Fatalf("Globals 数量 = %d, 期望 4", len(result.Globals))
	}

	// Host
	if result.Globals[0].Name != "Host" {
		t.Errorf("Globals[0].Name = %q, 期望 %q", result.Globals[0].Name, "Host")
	}
	if !result.Globals[0].IsExported {
		t.Error("Host 应该是导出的")
	}
	if result.Globals[0].Type != "string" {
		t.Errorf("Host Type = %q, 期望 %q", result.Globals[0].Type, "string")
	}

	// private
	if result.Globals[2].Name != "private" {
		t.Errorf("Globals[2].Name = %q, 期望 %q", result.Globals[2].Name, "private")
	}
	if result.Globals[2].IsExported {
		t.Error("private 不应该是导出的")
	}
}

func TestAnalyzeSource_SyntaxError(t *testing.T) {
	a := NewAnalyzer()
	src := `package main

func broken( {
	// 缺少右括号
}
`
	_, err := a.AnalyzeSource("broken.go", src)
	if err == nil {
		t.Error("语法错误应返回 error")
	}
}

func TestAnalyzeSource_EmptyFile(t *testing.T) {
	a := NewAnalyzer()
	src := `package empty
`
	result, err := a.AnalyzeSource("empty.go", src)
	if err != nil {
		t.Fatalf("AnalyzeSource 失败: %v", err)
	}

	if result.PackageName != "empty" {
		t.Errorf("PackageName = %q, 期望 %q", result.PackageName, "empty")
	}
	if len(result.Functions) != 0 {
		t.Errorf("Functions 数量 = %d, 期望 0", len(result.Functions))
	}
}

func TestAnalyzeSource_ComplexTypes(t *testing.T) {
	a := NewAnalyzer()
	src := `package example

func Process(data map[string][]int, ch chan string, fn func(int) error) (*Result, error) {
	return nil, nil
}
`
	result, err := a.AnalyzeSource("example.go", src)
	if err != nil {
		t.Fatalf("AnalyzeSource 失败: %v", err)
	}

	if len(result.Functions) != 1 {
		t.Fatalf("Functions 数量 = %d, 期望 1", len(result.Functions))
	}

	f := result.Functions[0]
	if f.Name != "Process" {
		t.Errorf("Name = %q, 期望 %q", f.Name, "Process")
	}
	// 参数数量：data, ch, fn = 3
	if len(f.Params) != 3 {
		t.Errorf("Params 数量 = %d, 期望 3, 实际 %v", len(f.Params), f.Params)
	}
	// 返回值数量：*Result, error = 2
	if len(f.Returns) != 2 {
		t.Errorf("Returns 数量 = %d, 期望 2, 实际 %v", len(f.Returns), f.Returns)
	}
}
