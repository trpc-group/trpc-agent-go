package agent

import "testing"

func TestParseUnifiedDiff(t *testing.T) {
	raw := "diff --git a/main.go b/main.go\n--- a/main.go\n+++ b/main.go\n@@ -1,3 +1,4 @@\n package main\n+func Added() {}\n func main() {}\n"
	input, err := ParseUnifiedDiff(raw)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(input.Files); got != 1 {
		t.Fatalf("files=%d", got)
	}
	if input.Files[0].NewPath != "main.go" {
		t.Fatalf("new path=%q", input.Files[0].NewPath)
	}
	if input.Summary.AddedLineCount != 1 || input.Summary.GoFileCount != 1 {
		t.Fatalf("bad summary: %+v", input.Summary)
	}
	if input.Files[0].Hunks[0].Lines[1].NewLine != 2 {
		t.Fatalf("bad new line mapping: %+v", input.Files[0].Hunks[0].Lines[1])
	}
}
