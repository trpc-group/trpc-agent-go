//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package rules implements deterministic, side-effect-free Go review rules.
package rules

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/scanner"
	"go/token"
	"path"
	"sort"
	"strconv"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/findings"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/input"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/review"
)

const (
	defaultMaxFileBytes  = 1 << 20
	defaultMaxTotalBytes = 8 << 20

	// RuleGoroutineLifetime identifies potentially unbounded goroutine or
	// derived-context lifetimes.
	RuleGoroutineLifetime = "go/goroutine-lifetime/v1"
	// RuleResourceClose identifies acquired resources without direct cleanup.
	RuleResourceClose = "go/resource-close/v1"
	// RuleIgnoredError identifies discarded errors from known error-returning APIs.
	RuleIgnoredError = "go/ignored-error/v1"
	// RuleTransactionLifecycle identifies incomplete SQL transaction lifecycles.
	RuleTransactionLifecycle = "go/transaction-lifecycle/v1"
	// RuleHardcodedSecret identifies literal credentials added to source files.
	RuleHardcodedSecret = "go/hardcoded-secret/v1"
	// RuleDangerousCommand identifies dynamic commands passed through a shell.
	RuleDangerousCommand = "go/dangerous-command/v1"
	// RuleMissingTests identifies changed production Go files without related tests.
	RuleMissingTests = "go/missing-related-tests/v1"
)

// SnapshotKey identifies one complete source snapshot by repository change
// layer and normalized path.
type SnapshotKey struct {
	// Layer identifies the repository state transition represented by the snapshot.
	Layer review.ChangeLayer
	// Path is the normalized repository-relative source path.
	Path string
}

// Snapshots contains complete bounded source files keyed by change layer and
// normalized path. Callers retain ownership of all byte slices.
type Snapshots map[SnapshotKey][]byte

// Engine runs bounded deterministic rules. Zero limits use conservative
// defaults. Engine does not retain or mutate caller data.
type Engine struct {
	// MaxFileBytes limits one source snapshot.
	MaxFileBytes int
	// MaxTotalBytes limits all source snapshots combined.
	MaxTotalBytes int
	// AllowPartialSnapshots keeps added-line text rules active when a diff
	// source cannot provide a complete parseable file. AST rules remain limited
	// to complete snapshots. The default false preserves strict repository-mode
	// validation.
	AllowPartialSnapshots bool
}

// Review analyzes diff using complete source snapshots for each changed Go
// file. It returns untrusted candidates for the findings package to
// canonicalize, redact, validate, and fingerprint.
func (e Engine) Review(diff input.Diff, snapshots Snapshots) ([]findings.Candidate, error) {
	maxFileBytes := e.MaxFileBytes
	if maxFileBytes == 0 {
		maxFileBytes = defaultMaxFileBytes
	}
	maxTotalBytes := e.MaxTotalBytes
	if maxTotalBytes == 0 {
		maxTotalBytes = defaultMaxTotalBytes
	}
	if maxFileBytes < 1 || maxTotalBytes < 1 || maxFileBytes > maxTotalBytes {
		return nil, errors.New("review rules: invalid snapshot byte limits")
	}
	if err := validateSnapshots(snapshots, maxFileBytes, maxTotalBytes); err != nil {
		return nil, err
	}

	changed := collectChangedFiles(diff)
	fileSet := token.NewFileSet()
	units := make([]sourceUnit, 0, len(changed))
	partialGoFiles := make([]changedFile, 0)
	for _, file := range changed {
		if !file.isGo || file.deleted || len(file.added) == 0 {
			continue
		}
		key := SnapshotKey{Layer: file.layer, Path: file.path}
		source, ok := snapshots[key]
		if !ok {
			if e.AllowPartialSnapshots {
				partialGoFiles = append(partialGoFiles, file)
				continue
			}
			return nil, fmt.Errorf(
				"review rules: missing source snapshot for %s:%q",
				file.layer,
				file.path,
			)
		}
		parsed, err := parser.ParseFile(fileSet, file.path, source, parser.SkipObjectResolution)
		if err != nil {
			if e.AllowPartialSnapshots {
				partialGoFiles = append(partialGoFiles, file)
				continue
			}
			return nil, fmt.Errorf(
				"review rules: parse source snapshot %s:%q: %w",
				file.layer,
				file.path,
				err,
			)
		}
		units = append(units, sourceUnit{
			changedFile:   file,
			source:        source,
			parsed:        parsed,
			imports:       resolveImports(parsed),
			semanticAdded: addedSemanticLines(fileSet, parsed, source, file.added),
		})
	}

	candidates := make([]findings.Candidate, 0)
	candidates = append(candidates, runTextRules(changed)...)
	candidates = append(candidates, runLiteralSecretRules(partialGoFiles)...)
	for index := range units {
		candidates = append(candidates, runASTRules(fileSet, &units[index])...)
	}
	candidates = append(candidates, missingTestCandidates(fileSet, units)...)
	sortCandidates(candidates)
	return candidates, nil
}

type changedFile struct {
	layer   review.ChangeLayer
	path    string
	added   map[int]string
	first   int
	isGo    bool
	isTest  bool
	deleted bool
}

type sourceUnit struct {
	changedFile
	source        []byte
	parsed        *ast.File
	imports       importResolver
	semanticAdded map[int]bool
}

func collectChangedFiles(diff input.Diff) []changedFile {
	files := make([]changedFile, 0, len(diff.Files))
	for _, candidate := range diff.Files {
		filePath := candidate.NewPath
		if filePath == "" {
			filePath = candidate.OldPath
		}
		layer := candidate.Layer
		if layer == "" {
			layer = review.ChangeLayerUnified
		}
		file := changedFile{
			layer:   layer,
			path:    filePath,
			added:   make(map[int]string),
			isGo:    strings.HasSuffix(filePath, ".go"),
			isTest:  strings.HasSuffix(filePath, "_test.go"),
			deleted: candidate.Change == input.ChangeDeleted || candidate.NewPath == "",
		}
		for _, hunk := range candidate.Hunks {
			for _, line := range hunk.Lines {
				if line.Kind != input.LineAdded || line.NewNumber == nil || *line.NewNumber < 1 {
					continue
				}
				number := *line.NewNumber
				file.added[number] = line.Text
				if file.first == 0 || number < file.first {
					file.first = number
				}
			}
		}
		files = append(files, file)
	}
	return files
}

func validateSnapshots(snapshots Snapshots, maxFileBytes, maxTotalBytes int) error {
	keys := make([]SnapshotKey, 0, len(snapshots))
	for key := range snapshots {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].Layer != keys[j].Layer {
			return keys[i].Layer < keys[j].Layer
		}
		return keys[i].Path < keys[j].Path
	})
	total := 0
	for _, key := range keys {
		if !validLayer(key.Layer) {
			return fmt.Errorf("review rules: invalid snapshot layer %q", key.Layer)
		}
		if key.Path == "" || key.Path == "." || path.IsAbs(key.Path) ||
			path.Clean(key.Path) != key.Path || strings.HasPrefix(key.Path, "../") ||
			strings.ContainsAny(key.Path, "\\\x00") {
			return fmt.Errorf("review rules: invalid snapshot path %q", key.Path)
		}
		size := len(snapshots[key])
		if size > maxFileBytes || total > maxTotalBytes-size {
			return fmt.Errorf(
				"review rules: snapshot byte limit exceeded for %s:%q",
				key.Layer,
				key.Path,
			)
		}
		total += size
	}
	return nil
}

func validLayer(layer review.ChangeLayer) bool {
	switch layer {
	case review.ChangeLayerUnified, review.ChangeLayerStaged, review.ChangeLayerWorktree:
		return true
	default:
		return false
	}
}

func addedSemanticLines(
	fileSet *token.FileSet,
	parsed *ast.File,
	source []byte,
	added map[int]string,
) map[int]bool {
	semantic := make(map[int]bool)
	file := fileSet.File(parsed.Pos())
	if file == nil {
		return semantic
	}
	var lexer scanner.Scanner
	lexer.Init(file, source, nil, scanner.ScanComments)
	for {
		position, tokenType, _ := lexer.Scan()
		if tokenType == token.EOF {
			break
		}
		line := fileSet.Position(position).Line
		if _, ok := added[line]; !ok || !semanticToken(tokenType) {
			continue
		}
		semantic[line] = true
	}
	return semantic
}

func semanticToken(tokenType token.Token) bool {
	switch tokenType {
	case token.COMMENT, token.SEMICOLON, token.LPAREN, token.RPAREN,
		token.LBRACK, token.RBRACK, token.LBRACE, token.RBRACE,
		token.COMMA, token.PERIOD, token.COLON:
		return false
	default:
		return true
	}
}

func sortCandidates(candidates []findings.Candidate) {
	sort.SliceStable(candidates, func(i, j int) bool {
		left, right := candidates[i], candidates[j]
		if left.Layer != right.Layer {
			return left.Layer < right.Layer
		}
		if left.File != right.File {
			return left.File < right.File
		}
		if left.Line != right.Line {
			return left.Line < right.Line
		}
		if left.RuleID != right.RuleID {
			return left.RuleID < right.RuleID
		}
		return left.SemanticAnchor < right.SemanticAnchor
	})
}

func newCandidate(
	ruleID string,
	severity review.Severity,
	confidence review.Confidence,
	category string,
	file changedFile,
	line int,
	semanticAnchor string,
	title string,
	evidence string,
	recommendation string,
) findings.Candidate {
	return findings.Candidate{
		SchemaVersion:  review.SchemaVersion,
		Severity:       severity,
		Category:       category,
		Layer:          file.layer,
		File:           file.path,
		Line:           line,
		SemanticAnchor: normalizeAnchor(semanticAnchor),
		Title:          title,
		Evidence:       evidence,
		Recommendation: recommendation,
		Confidence:     confidence,
		Source:         review.SourceRule,
		RuleID:         ruleID,
		Disposition:    review.DispositionFinding,
	}
}

func normalizeAnchor(value string) string {
	var builder strings.Builder
	previousDash := false
	for _, character := range strings.ToLower(value) {
		valid := character >= 'a' && character <= 'z' || character >= '0' && character <= '9' ||
			strings.ContainsRune("._:/-", character)
		if !valid {
			if builder.Len() > 0 && !previousDash {
				builder.WriteByte('-')
				previousDash = true
			}
			continue
		}
		builder.WriteRune(character)
		previousDash = character == '-'
		if builder.Len() >= 128 {
			break
		}
	}
	anchor := strings.Trim(builder.String(), "-._:/")
	if anchor == "" {
		return "finding"
	}
	return anchor
}

func stableAnchor(kind string, parts ...string) string {
	digest := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return normalizeAnchor(fmt.Sprintf("%s:%x", kind, digest[:8]))
}

type importResolver map[string]string

func resolveImports(file *ast.File) importResolver {
	resolved := make(importResolver)
	for _, declaration := range file.Imports {
		importPath, err := strconv.Unquote(declaration.Path.Value)
		if err != nil {
			continue
		}
		name := path.Base(importPath)
		if declaration.Name != nil {
			name = declaration.Name.Name
		}
		if name == "_" || name == "." {
			continue
		}
		resolved[name] = importPath
	}
	return resolved
}
