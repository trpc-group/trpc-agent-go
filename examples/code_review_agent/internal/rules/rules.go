//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights
// reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package rules implements deterministic code-review rules that scan
// the added lines of a unified diff and emit structured findings.
package rules

import (
	"regexp"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/diffparse"
)

// Finding is a single issue reported by a rule.
type Finding struct {
	RuleID         string
	Severity       string // "critical"|"high"|"medium"|"low"
	Category       string
	File           string
	Line           int
	Title          string
	Evidence       string
	Recommendation string
	Confidence     float64
	Source         string // "rule:SI-001" etc.
}

// Rule is a deterministic code-review rule that scans a single diff file.
type Rule interface {
	ID() string
	Severity() string
	Category() string
	Confidence() float64
	Scan(file diffparse.DiffFile) []Finding
}

// CrossFileScanner is an optional interface implemented by rules that need
// to see all files in the diff at once (e.g. TM-001 needs to know whether a
// source file has a corresponding _test.go in the same changeset). When a
// Rule implements CrossFileScanner, the Engine calls ScanAll instead of
// Scan per file.
type CrossFileScanner interface {
	ScanAll(files []diffparse.DiffFile) []Finding
}

// Engine runs the registered rules against parsed diff files.
type Engine struct {
	rules []Rule
}

// NewEngine constructs an Engine populated with the default rule set.
func NewEngine() *Engine {
	return &Engine{rules: defaultRules()}
}

// Run executes every rule against the provided files. Rules that implement
// CrossFileScanner receive the full file list at once; per-file rules are
// run once per file. Findings are returned in rule-then-file order; the
// review layer sorts them by severity before rendering.
func (e *Engine) Run(files []diffparse.DiffFile) []Finding {
	var out []Finding
	for _, r := range e.rules {
		if cf, ok := r.(CrossFileScanner); ok {
			out = append(out, cf.ScanAll(files)...)
			continue
		}
		for _, f := range files {
			out = append(out, r.Scan(f)...)
		}
	}
	return out
}

// Rules returns the rules registered with the engine.
func (e *Engine) Rules() []Rule { return e.rules }

// defaultRules returns the built-in rule set in evaluation order.
func defaultRules() []Rule {
	return []Rule{
		newSecretRule(),
		newGoroutineRule(),
		newContextLeakRule(),
		newResourceNotClosedRule(),
		newErrorUncheckedRule(),
		newMissingTestsRule(),
		newDBLifecycleRule(),
		newSensitiveInfoRule(),
		// Phase-1 additions (borrowed from competitor PRs #2190/#2243):
		// broaden coverage to transaction rollback, panic-in-goroutine,
		// command injection, and sensitive-info-in-log patterns.
		newMissingTxRollbackRule(),
		newPanicInGoroutineRule(),
		newCmdInjectionRule(),
		newSensitiveInfoInLogRule(),
	}
}

// addedContent returns the content of all added lines of the file joined by
// newlines. It is used by rules that need a file-wide view to detect the
// presence (or absence) of a compensating construct such as a defer or a
// WaitGroup declaration.
func addedContent(file diffparse.DiffFile) string {
	var sb strings.Builder
	for _, l := range file.AddedLinesNumbered() {
		sb.WriteString(l.Content)
		sb.WriteByte('\n')
	}
	return sb.String()
}

// makeFinding constructs a Finding for the given rule and line.
func makeFinding(r Rule, file diffparse.DiffFile, l diffparse.HunkLine, title, rec string) Finding {
	return Finding{
		RuleID:         r.ID(),
		Severity:       r.Severity(),
		Category:       r.Category(),
		File:           file.NewPath,
		Line:           l.NewFileLine,
		Title:          title,
		Evidence:       l.Content,
		Recommendation: rec,
		Confidence:     r.Confidence(),
		Source:         "rule:" + r.ID(),
	}
}

// --- SI-001: Hardcoded secret ---

type secretRule struct {
	re *regexp.Regexp
}

func newSecretRule() *secretRule {
	return &secretRule{
		re: regexp.MustCompile(`(?i)(api[_-]?key|apikey|password|passwd|pwd|secret|token)\s*[=:]\s*["'][^"']{6,}["']`),
	}
}

func (r *secretRule) ID() string          { return "SI-001" }
func (r *secretRule) Severity() string    { return "critical" }
func (r *secretRule) Category() string    { return "security" }
func (r *secretRule) Confidence() float64 { return 0.85 }

func (r *secretRule) Scan(file diffparse.DiffFile) []Finding {
	var out []Finding
	for _, l := range file.AddedLinesNumbered() {
		if !r.re.MatchString(l.Content) {
			continue
		}
		out = append(out, makeFinding(r, file, l,
			"Hardcoded secret",
			"Load secrets from env or secret manager"))
	}
	return out
}

// --- GL-001: Goroutine without sync ---

type goroutineRule struct {
	goFuncRe *regexp.Regexp
}

func newGoroutineRule() *goroutineRule {
	return &goroutineRule{goFuncRe: regexp.MustCompile(`\bgo\s+(func\b|\w+\()`)}
}

func (r *goroutineRule) ID() string          { return "GL-001" }
func (r *goroutineRule) Severity() string    { return "high" }
func (r *goroutineRule) Category() string    { return "correctness" }
func (r *goroutineRule) Confidence() float64 { return 0.80 }

func (r *goroutineRule) Scan(file diffparse.DiffFile) []Finding {
	if strings.Contains(addedContent(file), "sync.WaitGroup") {
		return nil
	}
	var out []Finding
	for _, l := range file.AddedLinesNumbered() {
		if !r.goFuncRe.MatchString(l.Content) {
			continue
		}
		out = append(out, makeFinding(r, file, l,
			"Goroutine launched without synchronization",
			"Use sync.WaitGroup or a channel to track goroutine completion"))
	}
	return out
}

// --- GL-002: context leak ---

type contextLeakRule struct {
	re *regexp.Regexp
}

func newContextLeakRule() *contextLeakRule {
	return &contextLeakRule{
		re: regexp.MustCompile(`context\.(WithCancel|WithTimeout|WithDeadline)\(`),
	}
}

func (r *contextLeakRule) ID() string          { return "GL-002" }
func (r *contextLeakRule) Severity() string    { return "medium" }
func (r *contextLeakRule) Category() string    { return "correctness" }
func (r *contextLeakRule) Confidence() float64 { return 0.90 }

func (r *contextLeakRule) Scan(file diffparse.DiffFile) []Finding {
	if strings.Contains(addedContent(file), "defer cancel()") {
		return nil
	}
	var out []Finding
	for _, l := range file.AddedLinesNumbered() {
		if !r.re.MatchString(l.Content) {
			continue
		}
		out = append(out, makeFinding(r, file, l,
			"Context cancel function not deferred",
			"Add 'defer cancel()' immediately after creating the context"))
	}
	return out
}

// --- RL-001: Resource not closed ---

type resourceNotClosedRule struct {
	openRe  *regexp.Regexp
	closeRe *regexp.Regexp
}

func newResourceNotClosedRule() *resourceNotClosedRule {
	return &resourceNotClosedRule{
		openRe:  regexp.MustCompile(`os\.(Open|Create|OpenFile)\(`),
		closeRe: regexp.MustCompile(`defer\s+.*\.Close\(\)`),
	}
}

func (r *resourceNotClosedRule) ID() string          { return "RL-001" }
func (r *resourceNotClosedRule) Severity() string    { return "high" }
func (r *resourceNotClosedRule) Category() string    { return "reliability" }
func (r *resourceNotClosedRule) Confidence() float64 { return 0.90 }

func (r *resourceNotClosedRule) Scan(file diffparse.DiffFile) []Finding {
	if r.closeRe.MatchString(addedContent(file)) {
		return nil
	}
	var out []Finding
	for _, l := range file.AddedLinesNumbered() {
		if !r.openRe.MatchString(l.Content) {
			continue
		}
		out = append(out, makeFinding(r, file, l,
			"Opened resource may not be closed",
			"Add 'defer <handle>.Close()' right after opening"))
	}
	return out
}

// --- EH-001: Error unchecked ---

type errorUncheckedRule struct {
	callRe *regexp.Regexp
}

func newErrorUncheckedRule() *errorUncheckedRule {
	return &errorUncheckedRule{
		callRe: regexp.MustCompile(`=\s*\w+\.\w+\(`),
	}
}

func (r *errorUncheckedRule) ID() string          { return "EH-001" }
func (r *errorUncheckedRule) Severity() string    { return "medium" }
func (r *errorUncheckedRule) Category() string    { return "correctness" }
func (r *errorUncheckedRule) Confidence() float64 { return 0.75 }

// isKnownErrorReturner reports whether the line calls a function known to
// return an error that should be checked.
func isKnownErrorReturner(line string) bool {
	return strings.Contains(line, "os.Open") ||
		strings.Contains(line, "os.Create") ||
		strings.Contains(line, "strconv.Atoi") ||
		strings.Contains(line, "json.Marshal") ||
		strings.Contains(line, "json.Unmarshal")
}

// hasNextIfErr reports whether the added line following index i starts with
// "if err", indicating an immediate error check.
func hasNextIfErr(lines []diffparse.HunkLine, i int) bool {
	if i+1 >= len(lines) {
		return false
	}
	return strings.HasPrefix(strings.TrimSpace(lines[i+1].Content), "if err")
}

func (r *errorUncheckedRule) Scan(file diffparse.DiffFile) []Finding {
	lines := file.AddedLinesNumbered()
	var out []Finding
	for i, l := range lines {
		if !r.callRe.MatchString(l.Content) || !isKnownErrorReturner(l.Content) {
			continue
		}
		if hasNextIfErr(lines, i) {
			continue
		}
		out = append(out, makeFinding(r, file, l,
			"Error return value not checked",
			"Check the returned error immediately with 'if err != nil { ... }'"))
	}
	return out
}

// --- TM-001: Missing tests ---

type missingTestsRule struct{}

func newMissingTestsRule() *missingTestsRule { return &missingTestsRule{} }

func (r *missingTestsRule) ID() string          { return "TM-001" }
func (r *missingTestsRule) Severity() string    { return "low" }
func (r *missingTestsRule) Category() string    { return "quality" }
func (r *missingTestsRule) Confidence() float64 { return 0.70 }

// Scan is retained for the Rule interface but is a no-op: TM-001 needs
// cross-file context to avoid false positives when the diff adds both a
// source file and its _test.go. The Engine dispatches to ScanAll via the
// CrossFileScanner interface.
func (r *missingTestsRule) Scan(file diffparse.DiffFile) []Finding {
	return nil
}

// ScanAll implements CrossFileScanner. It flags each new non-test .go file
// whose corresponding _test.go is NOT also present in the diff. This
// avoids the false positive where TM-001 fired on foo.go even when
// foo_test.go was added in the same changeset.
func (r *missingTestsRule) ScanAll(files []diffparse.DiffFile) []Finding {
	// Collect the set of source paths that have a corresponding _test.go
	// in the same diff. The key is the source path (e.g. "foo.go") derived
	// from each test file (e.g. "foo_test.go").
	hasTest := make(map[string]bool, len(files))
	for _, f := range files {
		if !strings.HasSuffix(f.NewPath, "_test.go") {
			continue
		}
		src := strings.TrimSuffix(f.NewPath, "_test.go") + ".go"
		hasTest[src] = true
	}

	var out []Finding
	for _, f := range files {
		if !strings.HasSuffix(f.NewPath, ".go") || strings.HasSuffix(f.NewPath, "_test.go") {
			continue
		}
		if hasTest[f.NewPath] {
			continue
		}
		out = append(out, Finding{
			RuleID:         r.ID(),
			Severity:       r.Severity(),
			Category:       r.Category(),
			File:           f.NewPath,
			Line:           1,
			Title:          "No test file added for this source file",
			Evidence:       "no test file added for this file",
			Recommendation: "Add a *_test.go file with table-driven tests covering the new behavior",
			Confidence:     r.Confidence(),
			Source:         "rule:" + r.ID(),
		})
	}
	return out
}

// --- DB-001: DB lifecycle ---

type dbLifecycleRule struct {
	openRe    *regexp.Regexp
	releaseRe *regexp.Regexp
}

func newDBLifecycleRule() *dbLifecycleRule {
	return &dbLifecycleRule{
		openRe:    regexp.MustCompile(`sql\.Open\(|\.Begin\(`),
		releaseRe: regexp.MustCompile(`defer\s+.*\.(Close|Rollback)\(\)`),
	}
}

func (r *dbLifecycleRule) ID() string          { return "DB-001" }
func (r *dbLifecycleRule) Severity() string    { return "high" }
func (r *dbLifecycleRule) Category() string    { return "reliability" }
func (r *dbLifecycleRule) Confidence() float64 { return 0.85 }

func (r *dbLifecycleRule) Scan(file diffparse.DiffFile) []Finding {
	if r.releaseRe.MatchString(addedContent(file)) {
		return nil
	}
	var out []Finding
	for _, l := range file.AddedLinesNumbered() {
		if !r.openRe.MatchString(l.Content) {
			continue
		}
		out = append(out, makeFinding(r, file, l,
			"Database resource not released",
			"Defer Close (for *sql.DB) or Rollback/Commit (for *Tx)"))
	}
	return out
}

// --- SC-001: Sensitive info (added lines only) ---

type sensitiveInfoRule struct {
	patterns []*regexp.Regexp
}

func newSensitiveInfoRule() *sensitiveInfoRule {
	return &sensitiveInfoRule{patterns: []*regexp.Regexp{
		regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----`),
		regexp.MustCompile(`AKIA[0-9A-Z]{16}`),
		regexp.MustCompile(`sk-[a-zA-Z0-9]{20,}`),
		regexp.MustCompile(`(?i)bearer\s+[A-Za-z0-9]{20,}`),
	}}
}

func (r *sensitiveInfoRule) ID() string          { return "SC-001" }
func (r *sensitiveInfoRule) Severity() string    { return "high" }
func (r *sensitiveInfoRule) Category() string    { return "security" }
func (r *sensitiveInfoRule) Confidence() float64 { return 0.80 }

// anyMatch reports whether s matches any of the rule's patterns.
func (r *sensitiveInfoRule) anyMatch(s string) bool {
	for _, p := range r.patterns {
		if p.MatchString(s) {
			return true
		}
	}
	return false
}

func (r *sensitiveInfoRule) Scan(file diffparse.DiffFile) []Finding {
	var out []Finding
	for _, l := range file.AddedLinesNumbered() {
		if !r.anyMatch(l.Content) {
			continue
		}
		out = append(out, makeFinding(r, file, l,
			"Sensitive information in added line",
			"Remove the secret and rotate it; load from a secret manager"))
	}
	return out
}

// --- DB-002: Missing transaction rollback ---
//
// Borrowed from competitor PR #2190. sql.Tx.Begin must be paired with a
// defer Rollback() (or Commit) so a mid-transaction error does not leave
// the transaction open. This rule flags Begin() calls whose surrounding
// added content lacks any Rollback/Commit defer.

type missingTxRollbackRule struct {
	beginRe   *regexp.Regexp
	releaseRe *regexp.Regexp
}

func newMissingTxRollbackRule() *missingTxRollbackRule {
	return &missingTxRollbackRule{
		// Match .Begin( without requiring a word char before the dot —
		// the receiver may be a type-assertion result like db.(...).Begin().
		beginRe:   regexp.MustCompile(`\.Begin\(`),
		releaseRe: regexp.MustCompile(`defer\s+.*\.(Rollback|Commit)\(\)`),
	}
}

func (r *missingTxRollbackRule) ID() string          { return "DB-002" }
func (r *missingTxRollbackRule) Severity() string    { return "high" }
func (r *missingTxRollbackRule) Category() string    { return "reliability" }
func (r *missingTxRollbackRule) Confidence() float64 { return 0.80 }

func (r *missingTxRollbackRule) Scan(file diffparse.DiffFile) []Finding {
	// If the same added content defers Rollback/Commit, the transaction
	// is properly released — skip the file.
	if r.releaseRe.MatchString(addedContent(file)) {
		return nil
	}
	var out []Finding
	for _, l := range file.AddedLinesNumbered() {
		if !r.beginRe.MatchString(l.Content) {
			continue
		}
		out = append(out, makeFinding(r, file, l,
			"Transaction started without rollback",
			"Add 'defer tx.Rollback()' immediately after Begin to release the transaction on error"))
	}
	return out
}

// --- GL-003: Panic in goroutine ---
//
// Borrowed from competitor PR #2190. A panic inside a goroutine crashes
// the whole process because there is no recovering caller. This rule
// flags `go func(...) { ... }` literals whose body contains panic() and
// lacks a deferred recover().

type panicInGoroutineRule struct {
	goFuncRe  *regexp.Regexp
	panicRe   *regexp.Regexp
	recoverRe *regexp.Regexp
}

func newPanicInGoroutineRule() *panicInGoroutineRule {
	return &panicInGoroutineRule{
		goFuncRe:  regexp.MustCompile(`^\s*go\s+func\s*\(`),
		panicRe:   regexp.MustCompile(`\bpanic\(`),
		recoverRe: regexp.MustCompile(`defer\s+.*recover\(\)`),
	}
}

func (r *panicInGoroutineRule) ID() string          { return "GL-003" }
func (r *panicInGoroutineRule) Severity() string    { return "high" }
func (r *panicInGoroutineRule) Category() string    { return "correctness" }
func (r *panicInGoroutineRule) Confidence() float64 { return 0.75 }

func (r *panicInGoroutineRule) Scan(file diffparse.DiffFile) []Finding {
	content := addedContent(file)
	if r.recoverRe.MatchString(content) {
		// A deferred recover is present somewhere in the added content;
		// assume the goroutine is protected.
		return nil
	}
	var out []Finding
	for _, l := range file.AddedLinesNumbered() {
		if !r.goFuncRe.MatchString(l.Content) {
			continue
		}
		// Flag the go-func line only if a panic appears anywhere in the
		// added content — we cannot reliably scope to the goroutine body
		// with regex, so the line anchors the finding on the go statement.
		if !r.panicRe.MatchString(content) {
			continue
		}
		out = append(out, makeFinding(r, file, l,
			"Panic inside goroutine without recover",
			"Add 'defer func() { if r := recover(); r != nil { ... } }()' at the top of the goroutine"))
	}
	return out
}

// --- SC-002: Command injection ---
//
// Borrowed from competitor PR #2190. Executing a shell with user-
// controlled input is a command-injection vector. This rule flags
// exec.Command("sh", "-c", ...) / exec.Command("bash", "-c", ...) /
// exec.Command("cmd", "/C", ...) calls where the third argument is not
// a string literal (heuristic: contains a variable interpolation or
// string concatenation).

type cmdInjectionRule struct {
	shellRe   *regexp.Regexp
	literalRe *regexp.Regexp
}

func newCmdInjectionRule() *cmdInjectionRule {
	return &cmdInjectionRule{
		shellRe: regexp.MustCompile(`exec\.Command\(\s*"(?:sh|bash|cmd)"\s*,\s*"(?:-c|/C)"\s*,`),
		// A literal command has no variable interpolation or `+` concat
		// after the third argument. We approximate "safe" by checking the
		// remainder of the line for `+`, `fmt.Sprintf`, or a backtick.
		literalRe: regexp.MustCompile(`exec\.Command\(\s*"(?:sh|bash|cmd)"\s*,\s*"(?:-c|/C)"\s*,\s*"`),
	}
}

func (r *cmdInjectionRule) ID() string          { return "SC-002" }
func (r *cmdInjectionRule) Severity() string    { return "critical" }
func (r *cmdInjectionRule) Category() string    { return "security" }
func (r *cmdInjectionRule) Confidence() float64 { return 0.85 }

func (r *cmdInjectionRule) Scan(file diffparse.DiffFile) []Finding {
	var out []Finding
	for _, l := range file.AddedLinesNumbered() {
		if !r.shellRe.MatchString(l.Content) {
			continue
		}
		// If the third argument is a string literal (starts with `"` right
		// after the comma), assume it is safe. Anything else (variable,
		// Sprintf, concatenation) is flagged.
		if r.literalRe.MatchString(l.Content) {
			continue
		}
		out = append(out, makeFinding(r, file, l,
			"Potential command injection via shell exec",
			"Avoid 'sh -c <user input>'; pass arguments directly to exec.Command without a shell"))
	}
	return out
}

// --- SC-003: Sensitive info in log ---
//
// Borrowed from competitor PR #2190. Logging secrets (passwords,
// tokens, credit cards) exposes them in log aggregation systems. This
// rule flags log.Print*/fmt.Print*/logf calls whose arguments contain
// common secret-bearing identifiers (password, token, secret, apiKey).

type sensitiveInfoInLogRule struct {
	logRe    *regexp.Regexp
	secretRe *regexp.Regexp
}

func newSensitiveInfoInLogRule() *sensitiveInfoInLogRule {
	return &sensitiveInfoInLogRule{
		logRe:    regexp.MustCompile(`\b(log\.(Print|Printf|Println|Fatal|Fatalf|Fatalln|Panic|Panicf|Panicln)|fmt\.(Print|Printf|Println|Fprint|Fprintf|Fprintln))\(`),
		secretRe: regexp.MustCompile(`(?i)\b(password|passwd|secret|token|apiKey|api_key|accessKey|access_key|privateKey|private_key|bearer|credential)\b`),
	}
}

func (r *sensitiveInfoInLogRule) ID() string          { return "SC-003" }
func (r *sensitiveInfoInLogRule) Severity() string    { return "high" }
func (r *sensitiveInfoInLogRule) Category() string    { return "security" }
func (r *sensitiveInfoInLogRule) Confidence() float64 { return 0.75 }

func (r *sensitiveInfoInLogRule) Scan(file diffparse.DiffFile) []Finding {
	var out []Finding
	for _, l := range file.AddedLinesNumbered() {
		if !r.logRe.MatchString(l.Content) {
			continue
		}
		if !r.secretRe.MatchString(l.Content) {
			continue
		}
		out = append(out, makeFinding(r, file, l,
			"Sensitive information in log statement",
			"Redact or omit the secret field before logging; log a placeholder like '***'"))
	}
	return out
}
