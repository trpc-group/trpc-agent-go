//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package python

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/internal/codeast"
)

func testdataPath(name string) string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "testdata", name)
}

func TestParseContent_Nodes(t *testing.T) {
	parser := NewParser()
	result, err := parser.ParseFileAt(testdataPath("sample.py"), "sample")
	if err != nil {
		t.Fatalf("ParseFileAt failed: %v", err)
	}
	if result == nil {
		t.Fatal("result is nil")
	}

	nodesByName := make(map[string]*codeast.Node)
	for _, n := range result.Nodes {
		nodesByName[n.Name] = n
	}

	// Verify variables
	if n, ok := nodesByName["MAX_RETRIES"]; !ok {
		t.Error("missing Variable node: MAX_RETRIES")
	} else {
		if n.Type != codeast.EntityVariable {
			t.Errorf("MAX_RETRIES type = %s, want Variable", n.Type)
		}
		if n.Signature != "MAX_RETRIES: int" {
			t.Errorf("MAX_RETRIES signature = %q, want %q", n.Signature, "MAX_RETRIES: int")
		}
	}

	if _, ok := nodesByName["DEFAULT_TIMEOUT"]; !ok {
		t.Error("missing Variable node: DEFAULT_TIMEOUT")
	}

	// Verify classes
	if n, ok := nodesByName["BaseHandler"]; !ok {
		t.Error("missing Class node: BaseHandler")
	} else {
		if n.Type != codeast.EntityClass {
			t.Errorf("BaseHandler type = %s, want Class", n.Type)
		}
		if n.Language != codeast.LanguagePython {
			t.Errorf("BaseHandler language = %s, want python", n.Language)
		}
		// imports stay on the node (for metadata) even though no IMPORTS edge is emitted.
		if len(n.Imports) == 0 {
			t.Error("BaseHandler node.Imports should be populated")
		}
	}

	if n, ok := nodesByName["HTTPHandler"]; !ok {
		t.Error("missing Class node: HTTPHandler")
	} else if n.Type != codeast.EntityClass {
		t.Errorf("HTTPHandler type = %s, want Class", n.Type)
	}

	// Verify functions
	if n, ok := nodesByName["create_handler"]; !ok {
		t.Error("missing Function node: create_handler")
	} else if n.Type != codeast.EntityFunction {
		t.Errorf("create_handler type = %s, want Function", n.Type)
	}

	if n, ok := nodesByName["fetch_data"]; !ok {
		t.Error("missing Function node: fetch_data")
	} else {
		if n.Type != codeast.EntityFunction {
			t.Errorf("fetch_data type = %s, want Function", n.Type)
		}
		if n.Signature == "" {
			t.Error("fetch_data signature is empty")
		}
	}

	// Verify methods
	if n, ok := nodesByName["handle"]; !ok {
		t.Error("missing Method node: handle")
	} else if n.Type != codeast.EntityMethod {
		t.Errorf("handle type = %s, want Method", n.Type)
	}

	if n, ok := nodesByName["_send_request"]; !ok {
		t.Error("missing Method node: _send_request")
	} else if n.Type != codeast.EntityMethod {
		t.Errorf("_send_request type = %s, want Method", n.Type)
	}
}

func TestParseContent_Edges(t *testing.T) {
	parser := NewParser()
	result, err := parser.ParseFileAt(testdataPath("sample.py"), "sample")
	if err != nil {
		t.Fatalf("ParseFileAt failed: %v", err)
	}

	edgeTypes := make(map[string][]codeast.RelationType)
	for _, e := range result.Edges {
		key := e.FromID + " -> " + e.ToID
		edgeTypes[key] = append(edgeTypes[key], e.Type)
	}

	// Verify INHERITS edge: HTTPHandler -> BaseHandler
	// BaseHandler is in the same module so it won't be resolved with module prefix.
	inheritKey := "sample.HTTPHandler -> BaseHandler"
	if types, ok := edgeTypes[inheritKey]; !ok {
		t.Errorf("missing INHERITS edge: %s", inheritKey)
	} else {
		found := false
		for _, rt := range types {
			if rt == codeast.RelationInherits {
				found = true
			}
		}
		if !found {
			t.Errorf("edge %s has types %v, want INHERITS", inheritKey, types)
		}
	}

	// Verify METHOD edges
	methodKey := "sample.HTTPHandler -> sample.HTTPHandler.handle"
	if types, ok := edgeTypes[methodKey]; !ok {
		t.Errorf("missing METHOD edge: %s", methodKey)
	} else {
		found := false
		for _, rt := range types {
			if rt == codeast.RelationMethod {
				found = true
			}
		}
		if !found {
			t.Errorf("edge %s has types %v, want METHOD", methodKey, types)
		}
	}

	// Verify NO IMPORTS edges are emitted (aligns with Go reader; avoids
	// dangling external-module vertices). Imports stay on node.Imports only.
	for _, e := range result.Edges {
		if e.Type == codeast.RelationImports {
			t.Errorf("unexpected IMPORTS edge: %s -> %s", e.FromID, e.ToID)
		}
	}

	// Verify CALLS edges exist
	hasCalls := false
	for _, e := range result.Edges {
		if e.Type == codeast.RelationCalls {
			hasCalls = true
			break
		}
	}
	if !hasCalls {
		t.Error("no CALLS edges found")
	}
}

func TestParseContent_QualifiedProtocolAndOptionalEdges(t *testing.T) {
	parser := NewParser()
	result, err := parser.ParseContent("sample.py", strings.Join([]string{
		"import typing",
		"",
		"class Client:",
		"    def run(self):",
		"        pass",
		"",
		"class Service(typing.Protocol):",
		"    def call(self):",
		"        pass",
		"",
		"class User:",
		"    def __init__(self, client: typing.Optional[Client]):",
		"        self.client = client",
		"    def handle(self):",
		"        self.client.run()",
	}, "\n"))
	if err != nil {
		t.Fatalf("ParseContent failed: %v", err)
	}

	foundImplements := false
	foundOptionalCall := false
	for _, e := range result.Edges {
		if e.FromID == "sample.Service" && e.ToID == "typing.Protocol" && e.Type == codeast.RelationImplements {
			foundImplements = true
		}
		if e.FromID == "sample.User.handle" && e.ToID == "Client.run" && e.Type == codeast.RelationCalls {
			foundOptionalCall = true
		}
	}
	if !foundImplements {
		t.Fatal("missing IMPLEMENTS edge for typing.Protocol base")
	}
	if !foundOptionalCall {
		t.Fatal("missing CALLS edge resolved through typing.Optional annotation")
	}
}

func TestParserOptionsAndParseFileInfo(t *testing.T) {
	custom := NewParser(WithPythonPath("custom-python"), WithExtractImports(false))
	if custom.pythonPath != "custom-python" {
		t.Fatalf("pythonPath = %q, want custom-python", custom.pythonPath)
	}
	if custom.extractImports {
		t.Fatal("extractImports = true, want false")
	}

	parser := NewParser(WithExtractImports(false))
	result, err := parser.ParseContent("pkg/mod.py", "import os\nclass Service:\n    pass\n")
	if err != nil {
		t.Fatalf("ParseContent() error = %v", err)
	}
	if len(result.File.Imports) != 0 {
		t.Fatalf("File.Imports = %v, want empty when imports are disabled", result.File.Imports)
	}

	info, err := NewParser().ParseFileInfo("pkg/mod.py", "import os\nclass Service:\n    pass\n")
	if err != nil {
		t.Fatalf("ParseFileInfo() error = %v", err)
	}
	if info.Package != "pkg.mod" {
		t.Fatalf("FileInfo.Package = %q, want pkg.mod", info.Package)
	}
	if len(info.Imports) != 1 || info.Imports[0] != "os" {
		t.Fatalf("FileInfo.Imports = %v, want [os]", info.Imports)
	}
}

func TestParseContentRunError(t *testing.T) {
	parser := NewParser(WithPythonPath("python-not-found-for-trpc-agent-go-test"))
	_, err := parser.ParseContent("sample.py", "class Service:\n    pass\n")
	if err == nil || !strings.Contains(err.Error(), "run python parser") {
		t.Fatalf("ParseContent() error = %v, want run python parser error", err)
	}
}

func TestParseDirectory(t *testing.T) {
	parser := NewParser()
	dir := testdataPath("pkg")
	result, err := parser.ParseDirectory(dir)
	if err != nil {
		t.Fatalf("ParseDirectory failed: %v", err)
	}
	if result == nil {
		t.Fatal("result is nil")
	}

	if len(result.Nodes) == 0 {
		t.Fatal("expected nodes from directory parse")
	}
	if result.File == nil {
		t.Fatal("result.File is nil")
	}
	if result.File.Language != codeast.LanguagePython {
		t.Errorf("File.Language = %s, want python", result.File.Language)
	}

	nodeNames := make(map[string]bool)
	for _, n := range result.Nodes {
		nodeNames[n.Name] = true
	}

	for _, want := range []string{"User", "UserService", "add_user", "find_user", "display"} {
		if !nodeNames[want] {
			t.Errorf("missing node %q from directory parse", want)
		}
	}

	// Node.FilePath should be absolute so that graph_source can compute
	// repo-relative paths via toRelativeRepoPath.
	for _, n := range result.Nodes {
		if !filepath.IsAbs(n.FilePath) {
			t.Errorf("node %q FilePath = %q, want absolute path", n.Name, n.FilePath)
		}
	}

	hasMethod := false
	for _, e := range result.Edges {
		if e.Type == codeast.RelationMethod {
			hasMethod = true
			break
		}
	}
	if !hasMethod {
		t.Error("expected METHOD edges from directory parse")
	}
}

func TestFileToModuleInitFiles(t *testing.T) {
	tests := []struct {
		filePath   string
		baseModule string
		want       string
	}{
		{filePath: "__init__.py", want: ""},
		{filePath: filepath.Join("pkg", "__init__.py"), want: "pkg"},
		{filePath: filepath.Join("pkg", "mod.py"), want: "pkg.mod"},
		{filePath: "__init__.py", baseModule: "pkg", want: "pkg"},
	}
	for _, tt := range tests {
		if got := fileToModule(tt.filePath, tt.baseModule); got != tt.want {
			t.Errorf("fileToModule(%q, %q) = %q, want %q", tt.filePath, tt.baseModule, got, tt.want)
		}
	}
}

func TestParseDirectory_WithIncludeFiles(t *testing.T) {
	parser := NewParser()
	dir := testdataPath("pkg")
	modelsPath, _ := filepath.Abs(filepath.Join(dir, "models.py"))
	result, err := parser.ParseDirectory(dir, codeast.WithParseIncludeFiles([]string{modelsPath}))
	if err != nil {
		t.Fatalf("ParseDirectory with include failed: %v", err)
	}

	for _, n := range result.Nodes {
		if n.Name == "UserService" || n.Name == "add_user" {
			t.Errorf("node %q should be excluded by includeFiles filter", n.Name)
		}
	}

	found := false
	for _, n := range result.Nodes {
		if n.Name == "User" {
			found = true
		}
	}
	if !found {
		t.Error("expected User node from included models.py")
	}
}

func TestParseDirectory_EmptyDir(t *testing.T) {
	dir := t.TempDir()
	parser := NewParser()
	result, err := parser.ParseDirectory(dir)
	if err != nil {
		t.Fatalf("ParseDirectory on empty dir: %v", err)
	}
	if len(result.Nodes) != 0 {
		t.Errorf("expected 0 nodes from empty dir, got %d", len(result.Nodes))
	}
}

func TestParseDirectory_ContinuesWhenSomeFilesFail(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "good.py"), []byte("class Good:\n    pass\n"), 0644); err != nil {
		t.Fatalf("write good.py: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "bad.py"), []byte("def broken(:\n"), 0644); err != nil {
		t.Fatalf("write bad.py: %v", err)
	}

	parser := NewParser()
	result, err := parser.ParseDirectory(dir)
	if err != nil {
		t.Fatalf("ParseDirectory() error = %v, want nil for partial failure", err)
	}
	if result == nil {
		t.Fatal("ParseDirectory() result = nil")
	}
	foundGood := false
	for _, n := range result.Nodes {
		if n.Name == "Good" {
			foundGood = true
		}
	}
	if !foundGood {
		t.Fatal("ParseDirectory() missing node from successfully parsed file")
	}
}

func TestParseDirectory_ReturnsErrorWhenAllFilesFail(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "bad.py"), []byte("def broken(:\n"), 0644); err != nil {
		t.Fatalf("write bad.py: %v", err)
	}

	parser := NewParser()
	_, err := parser.ParseDirectory(dir)
	if err == nil {
		t.Fatal("ParseDirectory() error = nil, want parse error")
	}
	if !strings.Contains(err.Error(), "all 1 file(s) failed") {
		t.Fatalf("ParseDirectory() error = %v, want all-files-failed context", err)
	}
}

func TestParseDirectory_RegisteredAsDirectoryParser(t *testing.T) {
	p, ok := codeast.GetDirectoryParser(codeast.FileTypePython)
	if !ok {
		t.Fatal("Python parser not registered as DirectoryParser")
	}
	if p == nil {
		t.Fatal("registered Python DirectoryParser is nil")
	}
}

func TestBuildNodeEmbeddingText(t *testing.T) {
	node := &codeast.Node{
		ID:        "mymodule.MyClass",
		Type:      codeast.EntityClass,
		Name:      "MyClass",
		FullName:  "mymodule.MyClass",
		Package:   "mymodule",
		FilePath:  "mymodule.py",
		Signature: "class MyClass(BaseClass)",
		Comment:   "A test class.",
	}
	text := BuildNodeEmbeddingText(node)
	if text == "" {
		t.Fatal("embedding text is empty")
	}
	if len(text) < 10 {
		t.Errorf("embedding text too short: %s", text)
	}
}

func TestMapEntityType(t *testing.T) {
	tests := []struct {
		raw  string
		want codeast.EntityType
	}{
		{raw: "Function", want: codeast.EntityFunction},
		{raw: "Method", want: codeast.EntityMethod},
		{raw: "Class", want: codeast.EntityClass},
		{raw: "Interface", want: codeast.EntityInterface},
		{raw: "Variable", want: codeast.EntityVariable},
		{raw: "Module", want: codeast.EntityModule},
		{raw: "Custom", want: codeast.EntityType("Custom")},
	}
	for _, tt := range tests {
		if got := mapEntityType(tt.raw); got != tt.want {
			t.Errorf("mapEntityType(%q) = %q, want %q", tt.raw, got, tt.want)
		}
	}
}

func TestMapRelationType(t *testing.T) {
	tests := []struct {
		raw  string
		want codeast.RelationType
	}{
		{raw: "CALLS", want: codeast.RelationCalls},
		{raw: "METHOD", want: codeast.RelationMethod},
		{raw: "IMPLEMENTS", want: codeast.RelationImplements},
		{raw: "IMPORTS", want: codeast.RelationImports},
		{raw: "INHERITS", want: codeast.RelationInherits},
		{raw: "FIELD", want: codeast.RelationField},
		{raw: "PARAM", want: codeast.RelationParam},
		{raw: "RETURNS", want: codeast.RelationReturns},
		{raw: "CUSTOM", want: codeast.RelationType("CUSTOM")},
	}
	for _, tt := range tests {
		if got := mapRelationType(tt.raw); got != tt.want {
			t.Errorf("mapRelationType(%q) = %q, want %q", tt.raw, got, tt.want)
		}
	}
}

func TestExtractPythonError(t *testing.T) {
	tests := []struct {
		name   string
		stderr string
		want   string
	}{
		{
			name:   "known error",
			stderr: "Traceback\n  File x\nModuleNotFoundError: No module named 'missing'",
			want:   "ModuleNotFoundError: No module named 'missing'",
		},
		{
			name:   "last non-empty line",
			stderr: "line 1\n\nlast line",
			want:   "last line",
		},
		{
			name:   "empty",
			stderr: "",
			want:   "",
		},
	}
	for _, tt := range tests {
		if got := extractPythonError(tt.stderr); got != tt.want {
			t.Errorf("%s: extractPythonError() = %q, want %q", tt.name, got, tt.want)
		}
	}
}

func TestToInt(t *testing.T) {
	tests := []struct {
		value any
		want  int
	}{
		{value: 1, want: 1},
		{value: int64(2), want: 2},
		{value: 3.7, want: 3},
		{value: "4", want: 0},
	}
	for _, tt := range tests {
		if got := toInt(tt.value); got != tt.want {
			t.Errorf("toInt(%v) = %d, want %d", tt.value, got, tt.want)
		}
	}
}
