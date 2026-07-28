//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package rules contains static analysis rules for Go code review.
package rules

import (
	"regexp"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/examples/skills_code_review_agent/internal/parser"
)

const (
	SeverityHigh   = "high"
	SeverityMedium = "medium"
	SeverityLow    = "low"
)

const (
	CatGoroutineLeak = "goroutine_leak"
	CatResourceLeak  = "resource_leak"
	CatErrorHandling = "error_handling"
	CatSensitiveInfo = "sensitive_info"
	CatSQLInjection  = "sql_injection"
	CatCmdInjection  = "cmd_injection"
	CatContextLeak   = "context_leak"
	CatHTTPBody      = "http_body_leak"
	CatContextMisuse = "context_misuse"
	CatMutexMisuse   = "mutex_misuse"
	CatPerformance   = "performance"
	CatDeferInLoop   = "defer_in_loop"
	CatVet           = "vet"
)

// Finding is a single review finding on an added line.
type Finding struct {
	Severity       string `json:"severity"`
	Category       string `json:"category"`
	File           string `json:"file"`
	Line           int    `json:"line"`
	Title          string `json:"title"`
	Evidence       string `json:"evidence"`
	Recommendation string `json:"recommendation"`
	Confidence     string `json:"confidence"`
	Source         string `json:"source"`
	RuleID         string `json:"rule_id"`
}

// dedupeKey uniquely identifies a finding for deduplication.
type dedupeKey struct {
	file   string
	line   int
	ruleID string
}

// rule inspects diff hunks and appends any findings. It is unexported because
// its signature depends on the unexported dedupeKey, so it cannot be implemented
// or invoked from outside this package.
type rule func(file string, hunk parser.Hunk, startLine int, seen map[dedupeKey]struct{}, out *[]Finding)

// all returns the full set of static rules.
func all() []rule {
	return []rule{
		goroutineLeakRule,
		contextLeakRule,
		resourceLeakRule,
		httpBodyLeakRule,
		errorHandlingRule,
		sensitiveInfoRule,
		sqlInjectionRule,
		cmdInjectionRule,
		contextBackgroundMisuseRule,
		mutexNoDeferRule,
		dataRaceRule,
		stringConcatLoopRule,
		fmtSprintfConvRule,
		bareReturnErrRule,
		deferInLoopRule,
	}
}

// Run applies all rules to every hunk in every file diff, deduplicating findings.
func Run(diffs []parser.FileDiff) []Finding {
	seen := map[dedupeKey]struct{}{}
	var findings []Finding
	rules := all()
	for _, fd := range diffs {
		file := fd.NewPath
		if file == "" {
			file = fd.OldPath
		}
		if !strings.HasSuffix(file, ".go") {
			continue
		}
		for _, hunk := range fd.Hunks {
			for _, rule := range rules {
				rule(file, hunk, hunk.StartLine, seen, &findings)
			}
		}
	}
	return findings
}

var reGoRoutine = regexp.MustCompile(`\bgo\s+func\s*\(`)

// A bare channel send is not synchronisation: an unbuffered send with no
// receiver is itself a leak, so it must not suppress the goroutine-leak rule.
var reWg = regexp.MustCompile(`\bwg\.(Add|Wait|Done)\b|\b<-done\b|\bcancel\(\)`)

func goroutineLeakRule(file string, hunk parser.Hunk, startLine int, seen map[dedupeKey]struct{}, out *[]Finding) {
	added, lineNums := hunk.AddedLinesNumbered()
	joined := strings.Join(added, "\n")
	if !reGoRoutine.MatchString(joined) {
		return
	}
	if reWg.MatchString(joined) {
		return // has synchronisation primitive
	}
	lineNo := startLine
	for i, l := range added {
		if reGoRoutine.MatchString(l) {
			lineNo = lineNums[i]
			break
		}
	}
	emit(out, seen, Finding{
		Severity:       SeverityHigh,
		Category:       CatGoroutineLeak,
		File:           file,
		Line:           lineNo,
		Title:          "Goroutine started without synchronisation",
		Evidence:       strings.TrimSpace(firstMatch(added, reGoRoutine)),
		Recommendation: "Add WaitGroup, channel, or context cancellation to prevent goroutine leak.",
		Confidence:     "medium",
		Source:         "static",
		RuleID:         "GL-001",
	})
}

// GL-002: goroutine launched without context propagation.
var reGoCall = regexp.MustCompile(`\bgo\s+\w+\s*\(`)
var reCtxPass = regexp.MustCompile(`\bctx\b|\bcontext\.`)

func contextLeakRule(file string, hunk parser.Hunk, startLine int, seen map[dedupeKey]struct{}, out *[]Finding) {
	added, lineNums := hunk.AddedLinesNumbered()
	for i, l := range added {
		if !reGoCall.MatchString(l) {
			continue
		}
		if reCtxPass.MatchString(l) {
			continue
		}
		lo := i - 2
		if lo < 0 {
			lo = 0
		}
		hi := i + 3
		if hi > len(added) {
			hi = len(added)
		}
		if reCtxPass.MatchString(strings.Join(added[lo:hi], "\n")) {
			continue
		}
		emit(out, seen, Finding{
			Severity:       SeverityMedium,
			Category:       CatContextLeak,
			File:           file,
			Line:           lineNums[i],
			Title:          "Goroutine launched without context propagation",
			Evidence:       strings.TrimSpace(l),
			Recommendation: "Pass a context to the goroutine function to support cancellation and deadline propagation.",
			Confidence:     "low",
			Source:         "static",
			RuleID:         "GL-002",
		})
	}
}

// http.Get/Post/Do are intentionally absent: their resource is the response
// body, which RL-002 owns. Matching them here would flag a correctly
// `defer resp.Body.Close()`d response as a leak.
var reResourceOpen = regexp.MustCompile(`\bos\.Open\b|\bos\.Create\b|\bnet\.Dial\b|\bos\.OpenFile\b|\bsql\.Open\b`)

// reResourceVar captures the variable a resource is opened into, e.g. `f` in
// `f, _ := os.Open(...)`, so suppression can require that exact variable's Close.
var reResourceVar = regexp.MustCompile(`^\s*(\w+)\s*(?:,\s*\w+\s*)*:=`)

// Only a deferred Close mitigates the leak; an unrelated defer must not suppress it.
var reDeferClose = regexp.MustCompile(`\bdefer\b[^\n]*\.Close\(\)`)

func resourceLeakRule(file string, hunk parser.Hunk, startLine int, seen map[dedupeKey]struct{}, out *[]Finding) {
	added, lineNums := hunk.AddedLinesNumbered()
	// A valid close may sit on an unchanged context line, so inspect the whole
	// hunk (added + context, minus removed lines) rather than only added lines.
	scope := hunkScope(hunk)
	for i, l := range added {
		if !reResourceOpen.MatchString(l) {
			continue
		}
		if m := reResourceVar.FindStringSubmatch(l); m != nil {
			// Suppress only when this specific resource is deferred-closed; another
			// resource's Close must not hide this one's leak.
			closed := regexp.MustCompile(`\bdefer\b[^\n]*\b` + regexp.QuoteMeta(m[1]) + `\.Close\(\)`)
			if closed.MatchString(scope) {
				continue
			}
		} else if reDeferClose.MatchString(scope) {
			continue
		}
		emit(out, seen, Finding{
			Severity:       SeverityHigh,
			Category:       CatResourceLeak,
			File:           file,
			Line:           lineNums[i],
			Title:          "Resource opened without deferred close",
			Evidence:       strings.TrimSpace(l),
			Recommendation: "Add 'defer resource.Close()' immediately after the open call.",
			Confidence:     "medium",
			Source:         "static",
			RuleID:         "RL-001",
		})
	}
}

// hunkScope joins a hunk's added and unchanged context lines, dropping removed
// lines, which is where a still-valid deferred close would appear.
func hunkScope(hunk parser.Hunk) string {
	lines := make([]string, 0, len(hunk.Lines))
	for _, l := range hunk.Lines {
		if strings.HasPrefix(l, "-") {
			continue
		}
		lines = append(lines, l)
	}
	return strings.Join(lines, "\n")
}

// RL-002: HTTP response body not closed after http.Get/Post/Do.
var reHTTPCall = regexp.MustCompile(`\bhttp\.(Get|Post|Do)\b|\.Do\(`)
var reBodyClose = regexp.MustCompile(`\.Body\.Close\(\)|defer\s+\w+\.Body\.Close\(\)`)

func httpBodyLeakRule(file string, hunk parser.Hunk, startLine int, seen map[dedupeKey]struct{}, out *[]Finding) {
	added, lineNums := hunk.AddedLinesNumbered()
	for i, l := range added {
		if !reHTTPCall.MatchString(l) {
			continue
		}
		window := added[i:]
		if len(window) > 8 {
			window = window[:8]
		}
		if reBodyClose.MatchString(strings.Join(window, "\n")) {
			continue
		}
		emit(out, seen, Finding{
			Severity:       SeverityHigh,
			Category:       CatHTTPBody,
			File:           file,
			Line:           lineNums[i],
			Title:          "HTTP response body not closed",
			Evidence:       strings.TrimSpace(l),
			Recommendation: "Add 'defer resp.Body.Close()' after checking the error from the HTTP call.",
			Confidence:     "medium",
			Source:         "static",
			RuleID:         "RL-002",
		})
	}
}

var reErrAssign = regexp.MustCompile(`(?:,\s*err\s*:=|,\s*err\s*=|\berr\s*:=|\berr\s*=)`)
var reErrCheck = regexp.MustCompile(`\berr\s*!=\s*nil\b|\breturn\s+err\b`)

func errorHandlingRule(file string, hunk parser.Hunk, startLine int, seen map[dedupeKey]struct{}, out *[]Finding) {
	added, lineNums := hunk.AddedLinesNumbered()
	for i, l := range added {
		if !reErrAssign.MatchString(l) {
			continue
		}
		// Same-line forms like `if err := f(); err != nil { ... }` already handle
		// the error on the assignment line itself.
		if reErrCheck.MatchString(l) {
			continue
		}
		after := added[i+1:]
		if len(after) > 2 {
			after = after[:2]
		}
		afterText := strings.Join(after, "\n")
		if reErrCheck.MatchString(afterText) || strings.Contains(afterText, "log") {
			continue
		}
		emit(out, seen, Finding{
			Severity:       SeverityMedium,
			Category:       CatErrorHandling,
			File:           file,
			Line:           lineNums[i],
			Title:          "Error return value not checked",
			Evidence:       strings.TrimSpace(l),
			Recommendation: "Check the error return value with 'if err != nil'.",
			Confidence:     "low",
			Source:         "static",
			RuleID:         "EH-001",
		})
	}
}

var reSensitive = regexp.MustCompile(`(?i)(?:password|passwd|api[_-]?key|secret[_-]?key|access[_-]?token|private[_-]?key)\s*[:=]+\s*["'` + "`" + `][^"'` + "`" + `]{3,}["'` + "`" + `]`)

func sensitiveInfoRule(file string, hunk parser.Hunk, startLine int, seen map[dedupeKey]struct{}, out *[]Finding) {
	added, lineNums := hunk.AddedLinesNumbered()
	for i, l := range added {
		if !reSensitive.MatchString(l) {
			continue
		}
		emit(out, seen, Finding{
			Severity:       SeverityHigh,
			Category:       CatSensitiveInfo,
			File:           file,
			Line:           lineNums[i],
			Title:          "Hardcoded sensitive value detected",
			Evidence:       redact(strings.TrimSpace(l)),
			Recommendation: "Use environment variables or a secrets manager instead of hardcoded values.",
			Confidence:     "high",
			Source:         "static",
			RuleID:         "SI-001",
		})
	}
}

// SQL-001: string concatenation into SQL query arguments.
// Placeholders alone are not sufficient: `"SELECT * FROM "+table+" WHERE id=?"` still injects via table.
var reSQLMethod = regexp.MustCompile(`(?i)(?:db|tx|stmt)\s*\.\s*(?:Query|Exec|QueryRow|QueryContext|ExecContext|QueryRowContext)\s*\(`)
var reSQLConcat = regexp.MustCompile(`\+\s*\w|\bfmt\.Sprintf\b`)

func sqlInjectionRule(file string, hunk parser.Hunk, startLine int, seen map[dedupeKey]struct{}, out *[]Finding) {
	added, lineNums := hunk.AddedLinesNumbered()
	for i, l := range added {
		if !reSQLMethod.MatchString(l) {
			continue
		}
		if !reSQLConcat.MatchString(l) {
			continue
		}
		emit(out, seen, Finding{
			Severity:       SeverityHigh,
			Category:       CatSQLInjection,
			File:           file,
			Line:           lineNums[i],
			Title:          "Possible SQL injection via string concatenation",
			Evidence:       strings.TrimSpace(l),
			Recommendation: "Use parameterised queries (? or $N placeholders) instead of string concatenation.",
			Confidence:     "medium",
			Source:         "static",
			RuleID:         "SQL-001",
		})
	}
}

// CMD-001: exec.Command with non-literal arguments (potential command injection).
var reCmdExec = regexp.MustCompile(`\bexec\.Command(?:Context)?\s*\(`)
var reCmdLiteral = regexp.MustCompile(`^[^,)]*exec\.Command(?:Context)?\s*\(\s*"[^"]*"(?:\s*,\s*"[^"]*")*\s*\)`)

func cmdInjectionRule(file string, hunk parser.Hunk, startLine int, seen map[dedupeKey]struct{}, out *[]Finding) {
	added, lineNums := hunk.AddedLinesNumbered()
	for i, l := range added {
		if !reCmdExec.MatchString(l) {
			continue
		}
		if reCmdLiteral.MatchString(l) {
			continue // all-literal invocation
		}
		emit(out, seen, Finding{
			Severity:       SeverityHigh,
			Category:       CatCmdInjection,
			File:           file,
			Line:           lineNums[i],
			Title:          "Possible command injection via exec.Command",
			Evidence:       strings.TrimSpace(l),
			Recommendation: "Validate and sanitise all arguments passed to exec.Command; prefer fixed command names with variable arguments over shell string expansion.",
			Confidence:     "low",
			Source:         "static",
			RuleID:         "CMD-001",
		})
	}
}

// CC-001: context.Background() used inside a function that already receives a ctx parameter.
var reCtxBackground = regexp.MustCompile(`\bcontext\.Background\(\)`)
var reFuncCtxParam = regexp.MustCompile(`\bfunc\b[^{]*\bctx\s+context\.Context\b`)

func contextBackgroundMisuseRule(file string, hunk parser.Hunk, startLine int, seen map[dedupeKey]struct{}, out *[]Finding) {
	added, lineNums := hunk.AddedLinesNumbered()
	joined := strings.Join(added, "\n")
	// The enclosing signature is usually an unchanged context line, so match it
	// against the whole hunk while still flagging only added Background() calls.
	if !reCtxBackground.MatchString(joined) || !reFuncCtxParam.MatchString(strings.Join(hunk.Lines, "\n")) {
		return
	}
	for i, l := range added {
		if !reCtxBackground.MatchString(l) {
			continue
		}
		emit(out, seen, Finding{
			Severity:       SeverityMedium,
			Category:       CatContextMisuse,
			File:           file,
			Line:           lineNums[i],
			Title:          "context.Background() shadows incoming ctx",
			Evidence:       strings.TrimSpace(l),
			Recommendation: "Pass the incoming ctx parameter instead of creating a new context.Background().",
			Confidence:     "medium",
			Source:         "static",
			RuleID:         "CC-001",
		})
	}
}

// MT-001: sync.Mutex.Lock() without a paired defer Unlock() in the same hunk.
var reMutexLock = regexp.MustCompile(`\b\w+\.Lock\(\)`)
var reDeferUnlock = regexp.MustCompile(`\bdefer\s+\w+\.(?:Unlock|RUnlock)\(\)`)

func mutexNoDeferRule(file string, hunk parser.Hunk, startLine int, seen map[dedupeKey]struct{}, out *[]Finding) {
	added, lineNums := hunk.AddedLinesNumbered()
	for i, l := range added {
		if !reMutexLock.MatchString(l) {
			continue
		}
		window := added[i:]
		if len(window) > 10 {
			window = window[:10]
		}
		if reDeferUnlock.MatchString(strings.Join(window, "\n")) {
			continue
		}
		emit(out, seen, Finding{
			Severity:       SeverityHigh,
			Category:       CatMutexMisuse,
			File:           file,
			Line:           lineNums[i],
			Title:          "Mutex locked without deferred Unlock",
			Evidence:       strings.TrimSpace(l),
			Recommendation: "Add 'defer mu.Unlock()' immediately after Lock() to prevent deadlock on error paths.",
			Confidence:     "medium",
			Source:         "static",
			RuleID:         "MT-001",
		})
	}
}

// MT-002: goroutine started inside a loop may capture the loop variable without synchronisation.
var reGoFuncClose = regexp.MustCompile(`\bgo\s+func\s*\(\s*\)`)
var reSyncPrim = regexp.MustCompile(`\batomic\.\w+|\bsync\.\w+|\bmu\.`)

// reLoopVars pulls the variables a `for` header introduces, e.g. `i` and `v`
// from `for i, v := range xs`. A `for {` / `for cond {` header introduces none.
var reLoopVars = regexp.MustCompile(`^for\s+(\w+)(?:\s*,\s*(\w+))?\s*:=`)

// referencesWord reports whether s mentions any of words as a whole identifier.
func referencesWord(s string, words []string) bool {
	for _, w := range words {
		if regexp.MustCompile(`\b` + regexp.QuoteMeta(w) + `\b`).MatchString(s) {
			return true
		}
	}
	return false
}

func dataRaceRule(file string, hunk parser.Hunk, startLine int, seen map[dedupeKey]struct{}, out *[]Finding) {
	added, lineNums := hunk.AddedLinesNumbered()
	inLoop := false
	var loopVars []string
	for i, l := range added {
		trimmed := strings.TrimSpace(l)
		if strings.HasPrefix(trimmed, "for ") || trimmed == "for {" {
			inLoop = true
			loopVars = nil
			if m := reLoopVars.FindStringSubmatch(trimmed); m != nil {
				for _, v := range m[1:] {
					if v != "" && v != "_" {
						loopVars = append(loopVars, v)
					}
				}
			}
		}
		if inLoop && strings.Contains(trimmed, "}") {
			inLoop = false
			loopVars = nil
		}
		// Only a parameterless goroutine launched inside a loop is a likely
		// loop-variable-capture data race; a bare go func() elsewhere captures
		// nothing loop-scoped, so flagging every one produces false positives.
		if !inLoop || !reGoFuncClose.MatchString(l) {
			continue
		}
		window := added[i:]
		if len(window) > 12 {
			window = window[:12]
		}
		block := strings.Join(window, "\n")
		if reSyncPrim.MatchString(block) {
			continue
		}
		// Loop placement alone is not capture: require the goroutine body to
		// reference a loop variable. `for {}` and `go func(){ doWork() }()` with
		// no such reference are left alone.
		if len(loopVars) == 0 || !referencesWord(block, loopVars) {
			continue
		}
		emit(out, seen, Finding{
			Severity:       SeverityHigh,
			Category:       CatMutexMisuse,
			File:           file,
			Line:           lineNums[i],
			Title:          "Goroutine in loop may capture the loop variable without synchronisation",
			Evidence:       strings.TrimSpace(l),
			Recommendation: "Protect shared variables with sync/atomic, or pass them as arguments to avoid data races.",
			Confidence:     "low",
			Source:         "static",
			RuleID:         "MT-002",
		})
	}
}

// PF-001: string concatenation with += inside a for loop.
var reStrConcat = regexp.MustCompile(`\w+\s*\+=\s*\w+|\w+\s*=\s*\w+\s*\+\s*\w+`)

func stringConcatLoopRule(file string, hunk parser.Hunk, startLine int, seen map[dedupeKey]struct{}, out *[]Finding) {
	added, lineNums := hunk.AddedLinesNumbered()
	inLoop := false
	for i, l := range added {
		trimmed := strings.TrimSpace(l)
		if strings.HasPrefix(trimmed, "for ") || trimmed == "for {" {
			inLoop = true
		}
		if inLoop && strings.Contains(trimmed, "}") {
			inLoop = false
		}
		if !inLoop {
			continue
		}
		if !reStrConcat.MatchString(l) {
			continue
		}
		if strings.Contains(l, "strings.Builder") || strings.Contains(l, "bytes.Buffer") {
			continue
		}
		emit(out, seen, Finding{
			Severity:       SeverityLow,
			Category:       CatPerformance,
			File:           file,
			Line:           lineNums[i],
			Title:          "String concatenation in loop causes O(n²) allocations",
			Evidence:       strings.TrimSpace(l),
			Recommendation: "Use strings.Builder.WriteString() instead of += inside loops.",
			Confidence:     "low",
			Source:         "static",
			RuleID:         "PF-001",
		})
	}
}

// PF-002: fmt.Sprintf used for a single int/bool/float conversion (strconv is faster).
// %v is intentionally excluded: it accepts any type, so it is not a strconv-equivalent conversion.
var reFmtSprintfConv = regexp.MustCompile(`\bfmt\.Sprintf\s*\(\s*"%[dtf]"\s*,`)

func fmtSprintfConvRule(file string, hunk parser.Hunk, startLine int, seen map[dedupeKey]struct{}, out *[]Finding) {
	added, lineNums := hunk.AddedLinesNumbered()
	for i, l := range added {
		if !reFmtSprintfConv.MatchString(l) {
			continue
		}
		emit(out, seen, Finding{
			Severity:       SeverityLow,
			Category:       CatPerformance,
			File:           file,
			Line:           lineNums[i],
			Title:          "fmt.Sprintf used for simple type conversion",
			Evidence:       strings.TrimSpace(l),
			Recommendation: "Use strconv.Itoa / strconv.FormatBool / strconv.FormatFloat for single-value conversions.",
			Confidence:     "high",
			Source:         "static",
			RuleID:         "PF-002",
		})
	}
}

// ER-001: bare return of err (possibly multi-value like `return nil, err`) without %w wrapping.
var reBareReturnErr = regexp.MustCompile(`\breturn\b[^{(]*\berr\b\s*$`)
var reErrWrap = regexp.MustCompile(`fmt\.Errorf|errors\.Wrap|%w`)

func bareReturnErrRule(file string, hunk parser.Hunk, startLine int, seen map[dedupeKey]struct{}, out *[]Finding) {
	added, lineNums := hunk.AddedLinesNumbered()
	for i, l := range added {
		if !reBareReturnErr.MatchString(strings.TrimSpace(l)) {
			continue
		}
		lo := i - 5
		if lo < 0 {
			lo = 0
		}
		window := added[lo : i+1]
		if reErrWrap.MatchString(strings.Join(window, "\n")) {
			continue
		}
		emit(out, seen, Finding{
			Severity:       SeverityLow,
			Category:       CatErrorHandling,
			File:           file,
			Line:           lineNums[i],
			Title:          "Error returned without context wrapping",
			Evidence:       strings.TrimSpace(l),
			Recommendation: "Use fmt.Errorf(\"operation failed: %w\", err) to add context for debugging.",
			Confidence:     "low",
			Source:         "static",
			RuleID:         "ER-001",
		})
	}
}

// DP-001: defer inside a for loop — resources won't be released until function returns.
var reDeferStmt = regexp.MustCompile(`\bdefer\b`)

func deferInLoopRule(file string, hunk parser.Hunk, startLine int, seen map[dedupeKey]struct{}, out *[]Finding) {
	added, lineNums := hunk.AddedLinesNumbered()
	depth := 0
	inForLoop := false
	for i, l := range added {
		trimmed := strings.TrimSpace(l)
		if strings.HasPrefix(trimmed, "for ") || trimmed == "for {" {
			inForLoop = true
			depth = 0
		}
		if inForLoop {
			depth += strings.Count(l, "{") - strings.Count(l, "}")
			if depth <= 0 {
				inForLoop = false
				depth = 0
			}
		}
		if !inForLoop || !reDeferStmt.MatchString(l) {
			continue
		}
		emit(out, seen, Finding{
			Severity:       SeverityMedium,
			Category:       CatDeferInLoop,
			File:           file,
			Line:           lineNums[i],
			Title:          "defer inside for loop defers until function return, not loop iteration",
			Evidence:       strings.TrimSpace(l),
			Recommendation: "Extract the loop body into a helper function so defer runs each iteration, or close the resource explicitly.",
			Confidence:     "high",
			Source:         "static",
			RuleID:         "DP-001",
		})
	}
}

func emit(out *[]Finding, seen map[dedupeKey]struct{}, f Finding) {
	k := dedupeKey{file: f.File, line: f.Line, ruleID: f.RuleID}
	if _, ok := seen[k]; ok {
		return
	}
	seen[k] = struct{}{}
	*out = append(*out, f)
}

func firstMatch(lines []string, re *regexp.Regexp) string {
	for _, l := range lines {
		if re.MatchString(l) {
			return l
		}
	}
	return ""
}

// redact replaces the value part of a sensitive assignment with [REDACTED].
func redact(line string) string {
	return reSensitive.ReplaceAllStringFunc(line, func(s string) string {
		eq := strings.IndexAny(s, "=:")
		if eq < 0 {
			return s
		}
		return s[:eq+1] + " [REDACTED]"
	})
}
