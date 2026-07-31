package input

import (
	"io"
	"testing"
)

func TestDiffParser_ParseString(t *testing.T) {
	parser := NewDiffParser("")

	tests := []struct {
		name      string
		input     string
		wantFiles int
		wantAdded int
		wantDel   int
	}{
		{
			name: "single file with additions",
			input: `diff --git a/main.go b/main.go
--- a/main.go
+++ b/main.go
@@ -1,3 +1,6 @@
 package main

+import "fmt"
+
 func main() {
+	fmt.Println("Hello")
+}
 `,
			wantFiles: 1,
			wantAdded: 4,
			wantDel:   0,
		},
		{
			name: "single file with deletions",
			input: `diff --git a/main.go b/main.go
--- a/main.go
+++ b/main.go
@@ -1,5 +1,3 @@
 package main

-import "fmt"
-
 func main() {
 }
`,
			wantFiles: 1,
			wantAdded: 0,
			wantDel:   2,
		},
		{
			name: "multiple files",
			input: `diff --git a/main.go b/main.go
--- a/main.go
+++ b/main.go
@@ -1,3 +1,4 @@
 package main

+import "fmt"
 func main() {
 }
diff --git a/util.go b/util.go
--- /dev/null
+++ b/util.go
@@ -0,0 +1,3 @@
+package main
+
+func helper() {}
`,
			wantFiles: 2,
			wantAdded: 4,
			wantDel:   0,
		},
		{
			name:      "empty diff",
			input:     "",
			wantFiles: 0,
			wantAdded: 0,
			wantDel:   0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reader := &stringReader{content: tt.input}
			result, err := parser.Parse(reader)
			if err != nil {
				t.Fatalf("Parse() error = %v", err)
			}

			if len(result.Files) != tt.wantFiles {
				t.Errorf("Parse() got %d files, want %d", len(result.Files), tt.wantFiles)
			}

			if result.TotalAdded != tt.wantAdded {
				t.Errorf("Parse() got %d additions, want %d", result.TotalAdded, tt.wantAdded)
			}

			if result.TotalDeleted != tt.wantDel {
				t.Errorf("Parse() got %d deletions, want %d", result.TotalDeleted, tt.wantDel)
			}
		})
	}
}

func TestDiffParser_ParsePlainUnifiedDiff(t *testing.T) {
	parser := NewDiffParser("")
	result, err := parser.Parse(&stringReader{content: `--- a/main.go
+++ b/main.go
@@ -1,1 +1,2 @@
 package main
+func main() {}
`})
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if len(result.Files) != 1 || result.Files[0].Path != "main.go" {
		t.Fatalf("Parse() got files = %#v", result.Files)
	}
	if result.TotalAdded != 1 {
		t.Fatalf("Parse() additions = %d, want 1", result.TotalAdded)
	}
}

func TestDiffParser_ParseFileRejectsEmptyRepositoryPath(t *testing.T) {
	parser := NewDiffParser("")
	if _, err := parser.ParseFile("/etc/passwd"); err == nil {
		t.Fatal("ParseFile() should reject an empty repository path")
	}
}

func TestDiffParser_ParseFileListRejectsMissingFile(t *testing.T) {
	parser := NewDiffParser(t.TempDir())
	if _, err := parser.ParseFileList([]string{"missing.go"}); err == nil {
		t.Fatal("ParseFileList() should reject missing files")
	}
}

func TestDiffParser_ParseFileListRejectsTraversal(t *testing.T) {
	parser := NewDiffParser(t.TempDir())
	if _, err := parser.ParseFileList([]string{"../outside.go"}); err == nil {
		t.Fatal("ParseFileList() should reject paths outside repository")
	}
}

func TestDiffParser_GetChangedLines(t *testing.T) {
	parser := NewDiffParser("")

	diff := `diff --git a/main.go b/main.go
--- a/main.go
+++ b/main.go
@@ -1,5 +1,8 @@
 package main

+import "fmt"
+
 func main() {
+	fmt.Println("Hello")
 	fmt.Println("World")
 }
`

	reader := &stringReader{content: diff}
	result, err := parser.Parse(reader)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}

	if len(result.Files) != 1 {
		t.Fatalf("expected 1 file, got %d", len(result.Files))
	}

	// 检查变更行数
	changes := 0
	for _, hunk := range result.Files[0].Hunks {
		for _, change := range hunk.Changes {
			if change.Type == "add" {
				changes++
			}
		}
	}

	if changes != 3 {
		t.Errorf("expected 3 additions, got %d", changes)
	}
}

type stringReader struct {
	content string
	pos     int
	eof     bool
}

func (r *stringReader) Read(p []byte) (n int, err error) {
	if r.pos >= len(r.content) {
		if !r.eof {
			r.eof = true
			return 0, io.EOF
		}
		return 0, nil
	}
	n = copy(p, r.content[r.pos:])
	r.pos += n
	return n, nil
}
