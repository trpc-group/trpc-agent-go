//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package rules

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	findingpkg "trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/findings"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/input"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/review"
)

func TestEngineUsesLayerAwareSnapshotsForSamePath(t *testing.T) {
	path := "config/config.go"
	stagedSource := "package config\nconst endpoint = \"https://example.com\"\n"
	worktreeSource := "package config\nconst apiKey = \"sk-worktree-secret-value\"\n"
	diff := input.Diff{Files: []input.File{
		parsedFile(t, review.ChangeLayerStaged, path, "const endpoint = \"https://example.com\""),
		parsedFile(t, review.ChangeLayerWorktree, path, "const apiKey = \"sk-worktree-secret-value\""),
	}}
	snapshots := Snapshots{
		{Layer: review.ChangeLayerStaged, Path: path}:   []byte(stagedSource),
		{Layer: review.ChangeLayerWorktree, Path: path}: []byte(worktreeSource),
	}

	candidates, err := (Engine{}).Review(diff, snapshots)
	if err != nil {
		t.Fatalf("Review() error = %v", err)
	}
	secret := candidatesForRule(candidates, RuleHardcodedSecret)
	if len(secret) != 1 || secret[0].Layer != review.ChangeLayerWorktree {
		t.Fatalf("hard-coded secret candidates = %#v, want only worktree", secret)
	}
}

func TestEngineReturnsCanonicalizableCandidates(t *testing.T) {
	path := "config/config.go"
	source := "package config\nconst apiKey = \"sk-live-secret-value\"\n"
	diff := input.Diff{Files: []input.File{
		parsedFile(t, review.ChangeLayerUnified, path, "const apiKey = \"sk-live-secret-value\""),
	}}
	candidates, err := (Engine{}).Review(diff, snapshotsFor(review.ChangeLayerUnified, path, source))
	if err != nil {
		t.Fatalf("Review() error = %v", err)
	}
	if len(candidates) == 0 {
		t.Fatal("Review() returned no candidates")
	}
	for _, candidate := range candidates {
		if strings.Contains(candidate.RuleID, "@v1") || !strings.HasSuffix(candidate.RuleID, "/v1") {
			t.Errorf("candidate rule id = %q", candidate.RuleID)
		}
		if candidate.Layer != review.ChangeLayerUnified || candidate.SemanticAnchor == "" {
			t.Errorf("candidate identity = %#v", candidate)
		}
		if strings.Contains(candidate.Evidence, "sk-live-secret") {
			t.Errorf("candidate leaked secret: %q", candidate.Evidence)
		}
		if _, err := findingpkg.Canonicalize(diff, candidate); err != nil {
			t.Errorf("Canonicalize(%#v) error = %v", candidate, err)
		}
	}
}

func TestEngineAllowsTextRulesWithoutCompleteSnapshotWhenOptedIn(t *testing.T) {
	diff := input.Diff{Files: []input.File{
		parsedFile(t, review.ChangeLayerUnified, "config/config.go",
			`const apiKey = "sk-partial-secret-value-123456"`),
	}}
	candidates, err := (Engine{AllowPartialSnapshots: true}).Review(diff, nil)
	if err != nil {
		t.Fatalf("Review() error = %v", err)
	}
	if got := candidatesForRule(candidates, RuleHardcodedSecret); len(got) != 1 {
		t.Fatalf("hard-coded secret candidates = %#v, want one", got)
	}
}

func TestEngineRequiresDirectLexicalCleanup(t *testing.T) {
	tests := []struct {
		name   string
		source string
		ruleID string
		want   bool
	}{
		{
			name: "resource clean",
			source: `package sample
import "os"
func run() error {
	f, err := os.Open("input.txt")
	if err != nil { return err }
	defer f.Close()
	return nil
}
`,
			ruleID: RuleResourceClose,
		},
		{
			name: "resource conditional defer",
			source: `package sample
import "os"
func run(ok bool) error {
	f, err := os.Open("input.txt")
	if err != nil { return err }
	if ok { defer f.Close() }
	return nil
}
`,
			ruleID: RuleResourceClose,
			want:   true,
		},
		{
			name: "resource cleanup closure",
			source: `package sample
import "os"
func run() error {
	f, err := os.Open("input.txt")
	if err != nil { return err }
	defer func() { f.Close() }()
	return nil
}
`,
			ruleID: RuleResourceClose,
			want:   true,
		},
		{
			name: "resource cleanup before acquire",
			source: `package sample
import "os"
func run(f *os.File) error {
	defer f.Close()
	f, err := os.Open("input.txt")
	if err != nil { return err }
	return nil
}
`,
			ruleID: RuleResourceClose,
			want:   true,
		},
		{
			name: "resource shadow bypass",
			source: `package sample
import "os"
func run() error {
	f, err := os.Open("input.txt")
	if err != nil { return err }
	{
		f := f
		defer f.Close()
	}
	return nil
}
`,
			ruleID: RuleResourceClose,
			want:   true,
		},
		{
			name: "context clean",
			source: `package sample
import "context"
func run(parent context.Context) {
	_, cancel := context.WithCancel(parent)
	defer cancel()
}
`,
			ruleID: RuleGoroutineLifetime,
		},
		{
			name: "context nested cleanup",
			source: `package sample
import "context"
func run(parent context.Context) {
	_, cancel := context.WithCancel(parent)
	if true { defer cancel() }
}
`,
			ruleID: RuleGoroutineLifetime,
			want:   true,
		},
		{
			name: "context cleanup closure",
			source: `package sample
import "context"
func run(parent context.Context) {
	_, cancel := context.WithCancel(parent)
	defer func() { cancel() }()
}
`,
			ruleID: RuleGoroutineLifetime,
			want:   true,
		},
		{
			name: "transaction clean",
			source: `package sample
import (
	"context"
	"database/sql"
)
func run(ctx context.Context, db *sql.DB) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil { return err }
	defer tx.Rollback()
	return tx.Commit()
}
`,
			ruleID: RuleTransactionLifecycle,
		},
		{
			name: "transaction checked commit clean",
			source: `package sample
import (
	"context"
	"database/sql"
)
func run(ctx context.Context, db *sql.DB) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil { return err }
	defer tx.Rollback()
	if err := tx.Commit(); err != nil { return err }
	return nil
}
`,
			ruleID: RuleTransactionLifecycle,
		},
		{
			name: "transaction conditional rollback",
			source: `package sample
import (
	"context"
	"database/sql"
)
func run(ctx context.Context, db *sql.DB, ok bool) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil { return err }
	if ok { defer tx.Rollback() }
	return tx.Commit()
}
`,
			ruleID: RuleTransactionLifecycle,
			want:   true,
		},
		{
			name: "transaction missing commit",
			source: `package sample
import (
	"context"
	"database/sql"
)
func run(ctx context.Context, db *sql.DB) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil { return err }
	defer tx.Rollback()
	return nil
}
`,
			ruleID: RuleTransactionLifecycle,
			want:   true,
		},
		{
			name: "transaction rollback closure",
			source: `package sample
import (
	"context"
	"database/sql"
)
func run(ctx context.Context, db *sql.DB) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil { return err }
	defer func() { tx.Rollback() }()
	return tx.Commit()
}
`,
			ruleID: RuleTransactionLifecycle,
			want:   true,
		},
		{
			name: "transaction bare commit is not credible",
			source: `package sample
import (
	"context"
	"database/sql"
)
func run(ctx context.Context, db *sql.DB) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil { return err }
	defer tx.Rollback()
	tx.Commit()
	return nil
}
`,
			ruleID: RuleTransactionLifecycle,
			want:   true,
		},
		{
			name: "transaction unchecked commit assignment is not credible",
			source: `package sample
import (
	"context"
	"database/sql"
)
func run(ctx context.Context, db *sql.DB) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil { return err }
	defer tx.Rollback()
	err = tx.Commit()
	return nil
}
`,
			ruleID: RuleTransactionLifecycle,
			want:   true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := "sample/sample.go"
			diff := diffForSource(review.ChangeLayerUnified, path, test.source, semanticLines(test.source))
			candidates, err := (Engine{}).Review(diff, snapshotsFor(review.ChangeLayerUnified, path, test.source))
			if err != nil {
				t.Fatalf("Review() error = %v", err)
			}
			got := len(candidatesForRule(candidates, test.ruleID)) != 0
			if got != test.want {
				t.Fatalf("Review() %s finding = %v, want %v; candidates=%#v", test.ruleID, got, test.want, candidates)
			}
		})
	}
}

func TestEngineGoroutineCancellationAndNamedFunctions(t *testing.T) {
	tests := []struct {
		name   string
		source string
		want   bool
	}{
		{
			name: "default is not cancellation",
			source: `package worker
func start(ch chan int) {
	go func() {
		for {
			select {
			case ch <- 1:
			default:
			}
		}
	}()
}
`,
			want: true,
		},
		{
			name: "done without return",
			source: `package worker
import "context"
func start(ctx context.Context, ch chan int) {
	go func() {
		for {
			select {
			case ch <- 1:
			case <-ctx.Done():
				continue
			}
		}
	}()
}
`,
			want: true,
		},
		{
			name: "done with return",
			source: `package worker
import "context"
func start(ctx context.Context, ch chan int) {
	go func() {
		for {
			select {
			case ch <- 1:
			case <-ctx.Done():
				return
			}
		}
	}()
}
`,
		},
		{
			name: "named goroutine",
			source: `package worker
func loop(ch chan int) {
	for { ch <- 1 }
}
func start(ch chan int) {
	go loop(ch)
}
`,
			want: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := "worker/worker.go"
			diff := diffForSource(review.ChangeLayerUnified, path, test.source, semanticLines(test.source))
			candidates, err := (Engine{}).Review(diff, snapshotsFor(review.ChangeLayerUnified, path, test.source))
			if err != nil {
				t.Fatalf("Review() error = %v", err)
			}
			got := len(candidatesForRule(candidates, RuleGoroutineLifetime)) != 0
			if got != test.want {
				t.Fatalf("goroutine finding = %v, want %v; candidates=%#v", got, test.want, candidates)
			}
		})
	}
}

func TestEngineDetectsBareAndBlankIgnoredErrorsWithAliases(t *testing.T) {
	source := `package codec
import j "encoding/json"
func run(v any, data []byte) {
	_, _ = j.Marshal(v)
	j.Unmarshal(data, &v)
}
`
	path := "codec/codec.go"
	diff := diffForSource(review.ChangeLayerUnified, path, source, []int{4, 5})
	candidates, err := (Engine{}).Review(diff, snapshotsFor(review.ChangeLayerUnified, path, source))
	if err != nil {
		t.Fatalf("Review() error = %v", err)
	}
	ignored := candidatesForRule(candidates, RuleIgnoredError)
	if len(ignored) != 2 {
		t.Fatalf("ignored-error candidates = %#v, want 2", ignored)
	}
}

func TestEngineAvoidsLocalPackageNameFalsePositives(t *testing.T) {
	source := `package sample
import json "encoding/json"
type codec struct{}
func (codec) Marshal(any) ([]byte, error) { return nil, nil }
func run(json codec, v any) {
	_, _ = json.Marshal(v)
}
`
	path := "sample/sample.go"
	diff := diffForSource(review.ChangeLayerUnified, path, source, []int{6})
	candidates, err := (Engine{}).Review(diff, snapshotsFor(review.ChangeLayerUnified, path, source))
	if err != nil {
		t.Fatalf("Review() error = %v", err)
	}
	if got := candidatesForRule(candidates, RuleIgnoredError); len(got) != 0 {
		t.Fatalf("local json produced ignored-error candidates: %#v", got)
	}
}

func TestEngineDetectsAllASTSecretForms(t *testing.T) {
	source := `package config
const apiKey = "value-one-secret"
var password = []byte("value-two-secret")
func load() map[string]string {
	token, clientSecret := "value-three-secret", "value-four-secret"
	values := map[string]string{"private_key": "value-five-secret", "token_count": "10"}
	_, _ = token, clientSecret
	return values
}
`
	path := "config/config.go"
	diff := diffForSource(review.ChangeLayerUnified, path, source, []int{2, 3, 5, 6})
	candidates, err := (Engine{}).Review(diff, snapshotsFor(review.ChangeLayerUnified, path, source))
	if err != nil {
		t.Fatalf("Review() error = %v", err)
	}
	secrets := candidatesForRule(candidates, RuleHardcodedSecret)
	if len(secrets) != 5 {
		t.Fatalf("hard-coded secret candidates = %#v, want 5", secrets)
	}
	anchors := make(map[string]bool)
	for _, candidate := range secrets {
		anchors[candidate.SemanticAnchor] = true
	}
	if len(anchors) != 5 {
		t.Fatalf("secret anchors = %#v, want one stable anchor per candidate", anchors)
	}
}

func TestEngineResolvesAliasesForResourcesContextAndCommands(t *testing.T) {
	source := `package sample
import (
	c "context"
	o "os"
	x "os/exec"
)
func run(parent c.Context, input string) error {
	_, cancel := c.WithCancel(parent)
	f, err := o.Open("input.txt")
	if err != nil { return err }
	_ = cancel
	_ = f
	return x.Command("sh", "-c", input).Run()
}
`
	path := "sample/sample.go"
	diff := diffForSource(review.ChangeLayerUnified, path, source, []int{8, 9, 10, 11, 12, 13})
	candidates, err := (Engine{}).Review(diff, snapshotsFor(review.ChangeLayerUnified, path, source))
	if err != nil {
		t.Fatalf("Review() error = %v", err)
	}
	for _, ruleID := range []string{RuleGoroutineLifetime, RuleResourceClose, RuleDangerousCommand} {
		if len(candidatesForRule(candidates, ruleID)) == 0 {
			t.Errorf("missing aliased rule %q in %#v", ruleID, candidates)
		}
	}
}

func TestEngineBindsOnlySemanticTokenLines(t *testing.T) {
	source := `package runner
import "os/exec"
func run(input string) error {
	return exec.Command(
		"sh", "-c", input,
	).Run()
}
`
	path := "runner/run.go"
	diff := diffForSource(review.ChangeLayerUnified, path, source, []int{6})
	candidates, err := (Engine{}).Review(diff, snapshotsFor(review.ChangeLayerUnified, path, source))
	if err != nil {
		t.Fatalf("Review() error = %v", err)
	}
	if got := candidatesForRule(candidates, RuleDangerousCommand); len(got) != 0 {
		t.Fatalf("formatting-only line produced command candidate: %#v", got)
	}
}

func TestEngineIgnoresFormattingInsideResourceCall(t *testing.T) {
	source := `package files
import "os"
func read() error {
	f, err := os.Open(
		"input.txt",
	)
	if err != nil { return err }
	_ = f
	return nil
}
`
	path := "files/read.go"
	diff := diffForSource(review.ChangeLayerUnified, path, source, []int{6})
	candidates, err := (Engine{}).Review(diff, snapshotsFor(review.ChangeLayerUnified, path, source))
	if err != nil {
		t.Fatalf("Review() error = %v", err)
	}
	if got := candidatesForRule(candidates, RuleResourceClose); len(got) != 0 {
		t.Fatalf("formatting-only line produced resource candidate: %#v", got)
	}
}

func TestEngineRequiresRealCompatibleChangedTestFunction(t *testing.T) {
	production := "package service\nfunc Value() int { return 2 }\n"
	tests := []struct {
		name      string
		testLayer review.ChangeLayer
		testSrc   string
		want      bool
	}{
		{name: "arbitrary text", testLayer: review.ChangeLayerStaged, testSrc: "package service\n// TestValue should be added later.\n", want: true},
		{name: "wrong signature", testLayer: review.ChangeLayerStaged, testSrc: "package service\nfunc TestValue() {}\n", want: true},
		{name: "wrong package", testLayer: review.ChangeLayerStaged, testSrc: "package other\nimport \"testing\"\nfunc TestValue(t *testing.T) {}\n", want: true},
		{name: "other layer", testLayer: review.ChangeLayerWorktree, testSrc: "package service\nimport \"testing\"\nfunc TestValue(t *testing.T) {}\n", want: true},
		{name: "compatible test", testLayer: review.ChangeLayerStaged, testSrc: "package service_test\nimport \"testing\"\nfunc TestValue(t *testing.T) {}\n"},
		{name: "benchmark", testLayer: review.ChangeLayerStaged, testSrc: "package service\nimport t \"testing\"\nfunc BenchmarkValue(b *t.B) {}\n"},
		{name: "fuzz", testLayer: review.ChangeLayerStaged, testSrc: "package service\nimport \"testing\"\nfunc FuzzValue(f *testing.F) {}\n"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			prodPath := "service/value.go"
			testPath := "service/value_test.go"
			diff := input.Diff{Files: []input.File{
				fileForSource(review.ChangeLayerStaged, prodPath, production, []int{2}),
				fileForSource(test.testLayer, testPath, test.testSrc, semanticLines(test.testSrc)),
			}}
			snapshots := Snapshots{
				{Layer: review.ChangeLayerStaged, Path: prodPath}: []byte(production),
				{Layer: test.testLayer, Path: testPath}:           []byte(test.testSrc),
			}
			candidates, err := (Engine{}).Review(diff, snapshots)
			if err != nil {
				t.Fatalf("Review() error = %v", err)
			}
			got := len(candidatesForRule(candidates, RuleMissingTests)) != 0
			if got != test.want {
				t.Fatalf("missing-test candidate = %v, want %v; candidates=%#v", got, test.want, candidates)
			}
		})
	}
}

func TestEngineCleanFixtureHasNoRuleFindings(t *testing.T) {
	production := readFixture(t, "clean.go.txt")
	testSource := readFixture(t, "clean_test.go.txt")
	diff := input.Diff{Files: []input.File{
		fileForSource(review.ChangeLayerUnified, "clean/clean.go", production, semanticLines(production)),
		fileForSource(review.ChangeLayerUnified, "clean/clean_test.go", testSource, semanticLines(testSource)),
	}}
	candidates, err := (Engine{}).Review(diff, Snapshots{
		{Layer: review.ChangeLayerUnified, Path: "clean/clean.go"}:      []byte(production),
		{Layer: review.ChangeLayerUnified, Path: "clean/clean_test.go"}: []byte(testSource),
	})
	if err != nil {
		t.Fatalf("Review() error = %v", err)
	}
	if len(candidates) != 0 {
		t.Fatalf("clean fixture candidates = %#v", candidates)
	}
}

func TestEngineIsDeterministicBoundedAndNonMutating(t *testing.T) {
	path := "config/config.go"
	source := "package config\nconst password = \"secret-value\"\n"
	diff := diffForSource(review.ChangeLayerUnified, path, source, []int{2})
	snapshots := snapshotsFor(review.ChangeLayerUnified, path, source)
	before := append([]byte(nil), snapshots[SnapshotKey{Layer: review.ChangeLayerUnified, Path: path}]...)
	one, err := (Engine{}).Review(diff, snapshots)
	if err != nil {
		t.Fatalf("first Review() error = %v", err)
	}
	two, err := (Engine{}).Review(diff, snapshots)
	if err != nil {
		t.Fatalf("second Review() error = %v", err)
	}
	if !reflect.DeepEqual(one, two) {
		t.Fatalf("Review() is nondeterministic:\nfirst=%#v\nsecond=%#v", one, two)
	}
	if !reflect.DeepEqual(before, snapshots[SnapshotKey{Layer: review.ChangeLayerUnified, Path: path}]) {
		t.Fatal("Review() mutated snapshots")
	}
	_, err = (Engine{MaxFileBytes: 8, MaxTotalBytes: 8}).Review(diff, snapshots)
	if err == nil || !strings.Contains(err.Error(), "snapshot byte limit") {
		t.Fatalf("oversized snapshot error = %v", err)
	}
	_, err = (Engine{}).Review(input.Diff{}, Snapshots{{Layer: review.ChangeLayerUnified, Path: "a\\b.go"}: []byte("package a\n")})
	if err == nil || !strings.Contains(err.Error(), "invalid snapshot path") {
		t.Fatalf("invalid snapshot path error = %v", err)
	}
}

func TestEngineRejectsMissingLayerSnapshotAndMalformedSource(t *testing.T) {
	path := "broken/broken.go"
	source := "package broken\nfunc Value() {}\n"
	diff := diffForSource(review.ChangeLayerStaged, path, source, []int{2})
	if _, err := (Engine{}).Review(diff, nil); err == nil || !strings.Contains(err.Error(), "missing source snapshot") {
		t.Fatalf("missing snapshot error = %v", err)
	}
	wrongLayer := snapshotsFor(review.ChangeLayerWorktree, path, source)
	if _, err := (Engine{}).Review(diff, wrongLayer); err == nil || !strings.Contains(err.Error(), "missing source snapshot") {
		t.Fatalf("wrong-layer snapshot error = %v", err)
	}
	malformed := snapshotsFor(review.ChangeLayerStaged, path, "package")
	if _, err := (Engine{}).Review(diff, malformed); err == nil || !strings.Contains(err.Error(), "parse source snapshot") {
		t.Fatalf("malformed snapshot error = %v", err)
	}
}

func parsedFile(t *testing.T, layer review.ChangeLayer, filePath, addedLine string) input.File {
	t.Helper()
	patch := "diff --git a/" + filePath + " b/" + filePath + "\n" +
		"--- a/" + filePath + "\n+++ b/" + filePath + "\n" +
		"@@ -1 +1,2 @@\n package config\n+" + addedLine + "\n"
	parsed, err := input.Parse(strings.NewReader(patch))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	parsed.Files[0].Layer = layer
	return parsed.Files[0]
}

func snapshotsFor(layer review.ChangeLayer, path, source string) Snapshots {
	return Snapshots{{Layer: layer, Path: path}: []byte(source)}
}

func diffForSource(layer review.ChangeLayer, path, source string, added []int) input.Diff {
	return input.Diff{Files: []input.File{fileForSource(layer, path, source, added)}}
}

func fileForSource(layer review.ChangeLayer, path, source string, added []int) input.File {
	lines := strings.Split(strings.TrimSuffix(source, "\n"), "\n")
	hunkLines := make([]input.Line, 0, len(added))
	for _, number := range added {
		line := number
		text := ""
		if number > 0 && number <= len(lines) {
			text = lines[number-1]
		}
		hunkLines = append(hunkLines, input.Line{Kind: input.LineAdded, Text: text, NewNumber: &line})
	}
	start := 1
	if len(added) != 0 {
		start = added[0]
	}
	return input.File{
		Layer:   layer,
		OldPath: path,
		NewPath: path,
		Change:  input.ChangeModified,
		Hunks:   []input.Hunk{{NewStart: start, NewLines: len(added), Lines: hunkLines}},
	}
}

func semanticLines(source string) []int {
	lines := strings.Split(strings.TrimSuffix(source, "\n"), "\n")
	numbers := make([]int, 0, len(lines))
	for index, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "//") {
			continue
		}
		numbers = append(numbers, index+1)
	}
	return numbers
}

func candidatesForRule(candidates []findingpkg.Candidate, ruleID string) []findingpkg.Candidate {
	matched := make([]findingpkg.Candidate, 0)
	for _, candidate := range candidates {
		if candidate.RuleID == ruleID {
			matched = append(matched, candidate)
		}
	}
	sort.Slice(matched, func(i, j int) bool {
		if matched[i].Line != matched[j].Line {
			return matched[i].Line < matched[j].Line
		}
		return matched[i].SemanticAnchor < matched[j].SemanticAnchor
	})
	return matched
}

func readFixture(t *testing.T, name string) string {
	t.Helper()
	content, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	return string(content)
}
