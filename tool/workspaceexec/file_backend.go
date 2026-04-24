//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package workspaceexec

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	ds "github.com/bmatcuk/doublestar/v4"

	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
)

// File-tool constants.
const (
	// defaultFileToolMaxBytes caps the number of bytes any single
	// read/replace operation may pull back from the workspace. The
	// local backend's Collect imposes its own 4 MiB ceiling; this
	// further trims the model-visible payload to something that fits
	// comfortably in a single tool response.
	defaultFileToolMaxBytes int64 = 1 << 20 // 1 MiB

	// defaultFileToolMode is the POSIX mode applied when writing text
	// files. Matches codeexecutor.DefaultScriptFileMode so that
	// bootstrap-written and tool-written files are indistinguishable.
	defaultFileToolMode uint32 = codeexecutor.DefaultScriptFileMode

	// fileBackendShellTimeout bounds shell-driven metadata operations
	// (list / search / exists). Metadata shells must always finish
	// quickly; a timeout prevents a broken executor from stalling the
	// tool call indefinitely.
	fileBackendShellTimeout = 20 * time.Second

	// maxListingEntriesDefault caps the number of entries returned
	// by a recursive listing. Trees larger than this are truncated;
	// the caller surfaces the truncation through the tool output.
	maxListingEntriesDefault = 5000
)

// maxListingEntriesOverride is a test-only hook. When non-zero it
// replaces maxListingEntriesDefault so tests can exercise the
// truncation path without seeding 5000+ entries. Production code
// leaves this at zero.
var maxListingEntriesOverride = 0

// Shell output markers. Using four-letter all-caps sentinels keeps
// them distinguishable from ordinary file names while being short
// enough to emit comfortably from portable sh.
const (
	wsOutEntryPrefix = "ENTRY\t"
	wsOutNotFound    = "__NOT_FOUND__"
	wsOutNotDir      = "__NOT_DIR__"
	wsOutExistsYes   = "YES"
	wsOutExistsNo    = "NO"
)

// Shell exit codes used by metadata scripts to signal structured
// errors. Any exit code from Runner().RunProgram that is not 0 and
// not listed here is treated as an opaque backend error.
const (
	exitNotFound = 10
	exitNotDir   = 11
)

// dirEntry describes a single listing entry produced by the
// workspace-side shell script.
type dirEntry struct {
	Name string
	Type string // "file" | "dir" | "symlink" | "other"
	Size int64
}

// notFoundError signals that a workspace path does not exist.
type notFoundError struct{ Path string }

func (e notFoundError) Error() string {
	return fmt.Sprintf("workspace path not found: %s", e.Path)
}

func isNotFound(err error) bool {
	var n notFoundError
	return errors.As(err, &n)
}

// notDirError signals that a workspace path exists but is not a
// directory (relevant only to list operations).
type notDirError struct{ Path string }

func (e notDirError) Error() string {
	return fmt.Sprintf("workspace path is not a directory: %s", e.Path)
}

func isNotDir(err error) bool {
	var n notDirError
	return errors.As(err, &n)
}

// notTextError signals that a file's bytes are not safe to surface
// as UTF-8 text (e.g. binary content or invalid encoding).
type notTextError struct{ Path string }

func (e notTextError) Error() string {
	return fmt.Sprintf("file is not valid UTF-8 text: %s", e.Path)
}

func isNotText(err error) bool {
	var n notTextError
	return errors.As(err, &n)
}

// readFileLimited reads a workspace-relative file using
// Engine.FS().Collect and caps the result at maxBytes. The returned
// truncated flag is true when either the backend or this helper
// truncated the file.
func readFileLimited(
	ctx context.Context,
	eng codeexecutor.Engine,
	ws codeexecutor.Workspace,
	rel string,
	maxBytes int64,
) (data []byte, sizeBytes int64, truncated bool, err error) {
	if eng == nil || eng.FS() == nil {
		return nil, 0, false, errors.New(
			"executor does not provide a live filesystem",
		)
	}
	if maxBytes <= 0 {
		maxBytes = defaultFileToolMaxBytes
	}
	files, err := eng.FS().Collect(ctx, ws, []string{rel})
	if err != nil {
		return nil, 0, false, err
	}
	if len(files) == 0 {
		return nil, 0, false, notFoundError{Path: rel}
	}
	f := files[0]
	content := []byte(f.Content)
	if int64(len(content)) > maxBytes {
		content = content[:maxBytes]
		return content, f.SizeBytes, true, nil
	}
	return content, f.SizeBytes, f.Truncated, nil
}

// writeFileBytes writes data into the workspace at rel using
// Engine.FS().PutFiles. When mode is zero the default script file
// mode is used.
func writeFileBytes(
	ctx context.Context,
	eng codeexecutor.Engine,
	ws codeexecutor.Workspace,
	rel string,
	data []byte,
	mode uint32,
) error {
	if eng == nil || eng.FS() == nil {
		return errors.New("executor does not provide a live filesystem")
	}
	if mode == 0 {
		mode = defaultFileToolMode
	}
	return eng.FS().PutFiles(ctx, ws, []codeexecutor.PutFile{{
		Path:    rel,
		Content: data,
		Mode:    mode,
	}})
}

// workspacePathExists reports whether rel exists inside the live
// workspace. It drives a minimal shell test so that both local and
// container backends answer consistently, regardless of which
// filesystem view the model has.
func workspacePathExists(
	ctx context.Context,
	eng codeexecutor.Engine,
	ws codeexecutor.Workspace,
	rel string,
) (bool, error) {
	if eng == nil || eng.Runner() == nil {
		return false, errors.New(
			"executor does not provide a live runner",
		)
	}
	script := `if [ -e "$1" ]; then echo ` + wsOutExistsYes +
		`; else echo ` + wsOutExistsNo + `; fi`
	rr, err := eng.Runner().RunProgram(ctx, ws, codeexecutor.RunProgramSpec{
		Cmd:     "sh",
		Args:    []string{"-c", script, "sh", rel},
		Timeout: fileBackendShellTimeout,
	})
	if err != nil {
		return false, err
	}
	if rr.ExitCode != 0 {
		return false, fmt.Errorf(
			"workspace exists check failed (exit %d): %s",
			rr.ExitCode, strings.TrimSpace(rr.Stderr),
		)
	}
	out := strings.TrimSpace(rr.Stdout)
	switch out {
	case wsOutExistsYes:
		return true, nil
	case wsOutExistsNo:
		return false, nil
	default:
		return false, fmt.Errorf(
			"unexpected workspace exists output: %q", out,
		)
	}
}

// listDirEntries returns the immediate children of the workspace
// directory at rel. The rel="." form lists the workspace root. The
// returned truncated flag is true when the underlying shell
// produced more than maxListingEntries rows and the slice had to be
// clipped; callers should plumb this flag into their tool output so
// the model knows the listing is partial.
func listDirEntries(
	ctx context.Context,
	eng codeexecutor.Engine,
	ws codeexecutor.Workspace,
	rel string,
) ([]dirEntry, bool, error) {
	return runListingScript(ctx, eng, ws, rel, buildListScript(false))
}

// listTreeAll returns a depth-first listing of every file and
// directory under rel. It returns (entries, truncated, error) where
// truncated is true when the walk had to be clipped at
// maxListingEntries.
func listTreeAll(
	ctx context.Context,
	eng codeexecutor.Engine,
	ws codeexecutor.Workspace,
	rel string,
) ([]dirEntry, bool, error) {
	return runListingScript(ctx, eng, ws, rel, buildListScript(true))
}

// buildListScript constructs the portable /bin/sh listing helper.
// The script accepts the workspace-relative target as $1 and emits
//
//	ENTRY\t<type>\t<size>\t<path>\n
//
// lines on stdout, or one of the __NOT_FOUND__/__NOT_DIR__ markers
// on stderr. Separate markers let the Go side translate backend
// errors into typed Go errors without parsing localized OS text.
func buildListScript(recursive bool) string {
	depth := "-maxdepth 1"
	if recursive {
		depth = ""
	}
	return fmt.Sprintf(`
set -u
TARGET="$1"
if [ ! -e "$TARGET" ]; then
    printf '%%s\n' "%s" 1>&2
    exit %d
fi
if [ ! -d "$TARGET" ]; then
    printf '%%s\n' "%s" 1>&2
    exit %d
fi
cd "$TARGET" || exit 1
find . -mindepth 1 %s -print 2>/dev/null | LC_ALL=C sort | while IFS= read -r p; do
    p="${p#./}"
    if [ -L "$p" ]; then
        t=symlink; s=0
    elif [ -d "$p" ]; then
        t=dir; s=0
    elif [ -f "$p" ]; then
        t=file
        s=$(wc -c < "$p" 2>/dev/null | tr -d ' \t\n')
        [ -z "$s" ] && s=0
    else
        t=other; s=0
    fi
    printf 'ENTRY\t%%s\t%%s\t%%s\n' "$t" "$s" "$p"
done
`, wsOutNotFound, exitNotFound, wsOutNotDir, exitNotDir, depth)
}

func runListingScript(
	ctx context.Context,
	eng codeexecutor.Engine,
	ws codeexecutor.Workspace,
	rel string,
	script string,
) ([]dirEntry, bool, error) {
	if eng == nil || eng.Runner() == nil {
		return nil, false, errors.New(
			"executor does not provide a live runner",
		)
	}
	rr, err := eng.Runner().RunProgram(ctx, ws, codeexecutor.RunProgramSpec{
		Cmd:     "sh",
		Args:    []string{"-c", script, "sh", rel},
		Timeout: fileBackendShellTimeout,
	})
	if err != nil {
		return nil, false, err
	}
	if rr.ExitCode != 0 {
		return nil, false, translateListingError(rel, rr)
	}
	entries, err := parseListingStdout(rr.Stdout)
	if err != nil {
		return nil, false, err
	}
	truncated := false
	limit := maxListingEntriesDefault
	if maxListingEntriesOverride > 0 {
		limit = maxListingEntriesOverride
	}
	if len(entries) > limit {
		entries = entries[:limit]
		truncated = true
	}
	return entries, truncated, nil
}

func translateListingError(rel string, rr codeexecutor.RunResult) error {
	stderr := rr.Stderr
	switch rr.ExitCode {
	case exitNotFound:
		return notFoundError{Path: rel}
	case exitNotDir:
		return notDirError{Path: rel}
	}
	if strings.Contains(stderr, wsOutNotFound) {
		return notFoundError{Path: rel}
	}
	if strings.Contains(stderr, wsOutNotDir) {
		return notDirError{Path: rel}
	}
	trimmed := strings.TrimSpace(stderr)
	if trimmed == "" {
		trimmed = strings.TrimSpace(rr.Stdout)
	}
	return fmt.Errorf(
		"workspace listing failed (exit %d): %s", rr.ExitCode, trimmed,
	)
}

// parseListingStdout converts the ENTRY\t<type>\t<size>\t<path>
// lines emitted by buildListScript into dirEntry values. Malformed
// lines are skipped rather than aborting the whole call; the shell
// script only emits well-formed lines, so malformed lines indicate a
// genuinely hostile environment that we would rather tolerate than
// amplify.
func parseListingStdout(stdout string) ([]dirEntry, error) {
	scanner := bufio.NewScanner(strings.NewReader(stdout))
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	var entries []dirEntry
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, wsOutEntryPrefix) {
			continue
		}
		body := strings.TrimPrefix(line, wsOutEntryPrefix)
		parts := strings.SplitN(body, "\t", 3)
		if len(parts) != 3 {
			continue
		}
		size, err := strconv.ParseInt(parts[1], 10, 64)
		if err != nil {
			size = 0
		}
		name := parts[2]
		if name == "" {
			continue
		}
		entries = append(entries, dirEntry{
			Name: name,
			Type: parts[0],
			Size: size,
		})
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("parse workspace listing: %w", err)
	}
	return entries, nil
}

// validateTextBytes checks that data is plausibly human-readable
// UTF-8 text. It rejects invalid UTF-8 and any occurrence of a NUL
// byte, which is the cheapest reliable signal that a file is
// binary.
func validateTextBytes(data []byte, rel string) error {
	if bytes.IndexByte(data, 0) >= 0 {
		return notTextError{Path: rel}
	}
	if !utf8.Valid(data) {
		return notTextError{Path: rel}
	}
	return nil
}

// validateTextString is a convenience wrapper for callers that
// already hold the content as a string.
func validateTextString(s, rel string) error {
	return validateTextBytes([]byte(s), rel)
}

// detectTextMIME returns a best-effort MIME type for textual data
// that has already passed validateTextBytes. The caller owns the
// filename because that is the strongest hint for hand-rolled
// extensions such as .md / .go.
func detectTextMIME(data []byte, rel string) string {
	if mime := mimeFromExt(rel); mime != "" {
		return mime
	}
	if mime := http.DetectContentType(data); mime != "" {
		return mime
	}
	return "text/plain; charset=utf-8"
}

func mimeFromExt(rel string) string {
	lower := strings.ToLower(rel)
	switch {
	case strings.HasSuffix(lower, ".md"):
		return "text/markdown; charset=utf-8"
	case strings.HasSuffix(lower, ".json"):
		return "application/json"
	case strings.HasSuffix(lower, ".yaml"), strings.HasSuffix(lower, ".yml"):
		return "application/yaml"
	case strings.HasSuffix(lower, ".go"):
		return "text/x-go; charset=utf-8"
	case strings.HasSuffix(lower, ".py"):
		return "text/x-python; charset=utf-8"
	case strings.HasSuffix(lower, ".sh"):
		return "application/x-sh"
	case strings.HasSuffix(lower, ".txt"), strings.HasSuffix(lower, ".log"):
		return "text/plain; charset=utf-8"
	}
	return ""
}

// sliceTextByLines extracts a 1-indexed, inclusive [startLine,
// endLine] window from s. When startLine is zero or negative the
// window starts at line 1; when endLine is zero or exceeds the line
// count it runs to the end of the file. The returned window is
// normalized so that startLine/endLine reflect the true slice
// boundaries.
func sliceTextByLines(
	s string, startLine, endLine int,
) (body string, start, end, total int) {
	total = countLines(s)
	if total == 0 {
		return "", 1, 0, 0
	}
	start = startLine
	if start <= 0 {
		start = 1
	}
	if start > total {
		return "", total + 1, total, total
	}
	end = endLine
	if end <= 0 || end > total {
		end = total
	}
	if end < start {
		end = start
	}
	lines := splitLinesPreserving(s)
	body = strings.Join(lines[start-1:end], "\n")
	return body, start, end, total
}

func countLines(s string) int {
	if s == "" {
		return 0
	}
	n := strings.Count(s, "\n")
	if !strings.HasSuffix(s, "\n") {
		n++
	}
	return n
}

func splitLinesPreserving(s string) []string {
	if s == "" {
		return nil
	}
	trimmed := strings.TrimSuffix(s, "\n")
	return strings.Split(trimmed, "\n")
}

// compileContentRegex compiles the regex supplied by the model. The
// caller toggles case insensitivity explicitly (workspace_search_
// content exposes it as a separate boolean) so compileContentRegex
// handles it via the (?i) inline flag to keep the API simple.
func compileContentRegex(pattern string, caseInsensitive bool) (*regexp.Regexp, error) {
	if strings.TrimSpace(pattern) == "" {
		return nil, errors.New("pattern must not be empty")
	}
	src := pattern
	if caseInsensitive && !strings.HasPrefix(pattern, "(?i)") {
		src = "(?i)" + pattern
	}
	rx, err := regexp.Compile(src)
	if err != nil {
		return nil, fmt.Errorf("invalid regex: %w", err)
	}
	return rx, nil
}

// contentHit captures a single matching line inside a file.
type contentHit struct {
	Line    int    `json:"line"`
	Preview string `json:"preview"`
}

// scanContentLines walks s line-by-line and returns at most
// maxMatches hits. previewMax trims long lines so that the tool
// response remains bounded even on pathological inputs.
func scanContentLines(
	rx *regexp.Regexp, s string, maxMatches, previewMax int,
) []contentHit {
	if maxMatches <= 0 {
		maxMatches = 50
	}
	if previewMax <= 0 {
		previewMax = 240
	}
	var hits []contentHit
	lineNo := 0
	for _, line := range splitLinesPreserving(s) {
		lineNo++
		if !rx.MatchString(line) {
			continue
		}
		preview := line
		if len(preview) > previewMax {
			preview = preview[:previewMax] + "..."
		}
		hits = append(hits, contentHit{Line: lineNo, Preview: preview})
		if len(hits) >= maxMatches {
			break
		}
	}
	return hits
}

// matchGlob reports whether name matches pattern using doublestar
// semantics (supports ** and ? in addition to *). A pattern of ""
// matches any name, mirroring the convention used by tool/file.
func matchGlob(pattern, name string) bool {
	if strings.TrimSpace(pattern) == "" {
		return true
	}
	ok, err := ds.Match(pattern, name)
	if err != nil {
		return false
	}
	return ok
}

// -----------------------------------------------------------------------------
// Workspace-relative path helpers.
// -----------------------------------------------------------------------------

// cleanWorkspaceRelPath normalizes a workspace-relative file path
// supplied by the model. It enforces the same "stay inside the
// workspace" invariants used elsewhere in this package:
//
//   - backslashes are converted to forward slashes;
//   - $WORKSPACE_DIR / $WORK_DIR / $SKILLS_DIR / $OUTPUT_DIR /
//     $RUN_DIR prefixes are rewritten via NormalizeGlobs;
//   - absolute paths (with or without env expansion) must resolve to
//     one of the allowed workspace roots;
//   - the path must not contain glob metacharacters;
//   - the path must not walk outside the workspace (..);
//   - when allowEmpty is false, "" and "." are rejected.
//
// On success the returned value is either "." or a clean, forward-
// slash, workspace-relative path without a leading ".".
func cleanWorkspaceRelPath(raw string, allowEmpty bool) (string, error) {
	s := strings.TrimSpace(raw)
	s = strings.ReplaceAll(s, "\\", "/")
	if s == "" {
		if allowEmpty {
			return ".", nil
		}
		return "", errors.New("path must not be empty")
	}
	if hasGlobMeta(s) {
		return "", errors.New("path must not contain glob patterns")
	}
	if isWorkspaceEnvPath(s) {
		out := codeexecutor.NormalizeGlobs([]string{s})
		if len(out) == 0 {
			return "", fmt.Errorf("invalid path: %q", raw)
		}
		s = out[0]
	}
	if strings.HasPrefix(s, "/") {
		rel := strings.TrimPrefix(path.Clean(s), "/")
		if rel == "" || rel == "." {
			if allowEmpty {
				return ".", nil
			}
			return "", errors.New("path must not resolve to workspace root")
		}
		if !isAllowedWorkspacePath(rel) {
			return "", fmt.Errorf(
				"path must stay under workspace roots such as work/, out/, runs/, or skills/: %q",
				raw,
			)
		}
		return rel, nil
	}
	rel := path.Clean(s)
	if rel == "." {
		if allowEmpty {
			return ".", nil
		}
		return "", errors.New("path must not resolve to workspace root")
	}
	if rel == ".." || strings.HasPrefix(rel, "../") {
		return "", errors.New("path must stay within the workspace")
	}
	if !isAllowedWorkspacePath(rel) {
		return "", fmt.Errorf(
			"path must stay under workspace roots such as work/, out/, runs/, or skills/: %q",
			raw,
		)
	}
	return rel, nil
}

// isBuiltInProtectedWorkspacePath reports whether rel is owned by a
// framework-managed subtree that file tools must not overwrite:
//
//   - work/inputs/**: conversation-file staging area (reconciler-
//     authoritative);
//   - skills/**: skill working copies staged by skill_load +
//     LoadedSkillsProvider.
//
// These checks are purely syntactic; callers that need to protect
// WorkspaceBootstrapSpec.Files targets should call
// (*ExecTool).isBootstrapProtectedPath in addition to this helper.
func isBuiltInProtectedWorkspacePath(rel string) bool {
	clean := path.Clean(rel)
	if clean == "" || clean == "." {
		return false
	}
	if clean == "work/inputs" || strings.HasPrefix(clean, "work/inputs/") {
		return true
	}
	if clean == codeexecutor.DirSkills ||
		strings.HasPrefix(clean, codeexecutor.DirSkills+"/") {
		return true
	}
	return false
}

// -----------------------------------------------------------------------------
// ExecTool support hooks used by file tools.
//
// These methods let every file tool share exactly one "prepare the
// workspace" code path and one bootstrap-target registry, so file
// tools and workspace_exec never see different views of the shared
// workspace state.
// -----------------------------------------------------------------------------

// prepareForFileTool brings the shared executor workspace to the
// desired state that workspace_exec would have observed at the same
// moment, and returns the live engine plus workspace handle. File
// tools call this helper instead of reimplementing the "liveEngine
// -> CreateWorkspace -> reconcile" sequence, ensuring that file
// tools and workspace_exec always see the same conversation inputs,
// bootstrap files, and loaded skills.
func (t *ExecTool) prepareForFileTool(
	ctx context.Context,
) (codeexecutor.Engine, codeexecutor.Workspace, error) {
	if t == nil {
		return nil, codeexecutor.Workspace{}, errors.New(
			"workspace file tools are not configured",
		)
	}
	eng, err := t.liveEngine()
	if err != nil {
		return nil, codeexecutor.Workspace{}, err
	}
	if t.resolver == nil {
		return nil, codeexecutor.Workspace{}, errors.New(
			"workspace file tools require a workspace resolver",
		)
	}
	ws, err := t.resolver.CreateWorkspace(ctx, eng, "workspace")
	if err != nil {
		return nil, codeexecutor.Workspace{}, err
	}
	if err := t.reconcileWorkspace(ctx, eng, ws); err != nil {
		return nil, codeexecutor.Workspace{}, err
	}
	return eng, ws, nil
}

// registerBootstrapFileTargets records the workspace-relative Target
// of every WorkspaceFile declared via WithWorkspaceBootstrap. File
// tools consult this set when validating writes so that declarative
// bootstrap outputs cannot be silently mutated or replaced by the
// model.
//
// The method tolerates arbitrary user input: targets that fail
// cleanWorkspaceRelPath are dropped (they will be rejected at
// reconcile-time anyway and would never match a live write path).
func (t *ExecTool) registerBootstrapFileTargets(
	spec codeexecutor.WorkspaceBootstrapSpec,
) {
	if t == nil || len(spec.Files) == 0 {
		return
	}
	seen := make(map[string]struct{}, len(t.bootstrapFileTargets))
	for _, existing := range t.bootstrapFileTargets {
		seen[existing] = struct{}{}
	}
	for _, f := range spec.Files {
		rel, err := cleanWorkspaceRelPath(f.Target, false)
		if err != nil {
			continue
		}
		if _, ok := seen[rel]; ok {
			continue
		}
		seen[rel] = struct{}{}
		t.bootstrapFileTargets = append(t.bootstrapFileTargets, rel)
	}
	sort.Strings(t.bootstrapFileTargets)
}

// isBootstrapProtectedPath reports whether rel collides with a
// bootstrap-managed file target. A path is protected when it matches
// a target exactly or when it lives beneath a target that is itself
// a directory prefix of another protected path.
//
// The lookup is O(len(targets)); bootstrap specs are expected to
// hold tens of entries at most, so linear scanning is adequate.
func (t *ExecTool) isBootstrapProtectedPath(rel string) bool {
	if t == nil || len(t.bootstrapFileTargets) == 0 {
		return false
	}
	clean := path.Clean(rel)
	if clean == "" || clean == "." {
		return false
	}
	for _, target := range t.bootstrapFileTargets {
		if clean == target {
			return true
		}
		if strings.HasPrefix(clean, target+"/") {
			return true
		}
	}
	return false
}
