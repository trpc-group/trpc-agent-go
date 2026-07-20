//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

import (
	"regexp"
	"strconv"
	"strings"
)

// destructivePatterns are literal fragments that indicate an
// irreversible or system-damaging operation. Matched case-insensitively
// as substrings, so they stay conservative and language-agnostic.
// Recursive "rm" deletes are handled separately by analyzeRm, which
// parses flags and targets instead of matching one literal flag shape,
// so a scoped "rm -rf /tmp/x" is not confused with a root wipe while
// split-flag forms such as "rm -r -f /" are still caught.
var destructivePatterns = []string{
	"rm -rf --no-preserve-root", "rm --no-preserve-root",
	":(){ :|:& };:", // fork bomb
	"mkfs", "dd if=/dev/zero", "dd of=/dev/",
	"> /dev/sda", "of=/dev/sda", "of=/dev/vda", "of=/dev/nvme",
	"chmod -r 777 /", "chmod 777 /", "chown -r root /",
	"shred ", "wipefs", "> /etc/passwd", "> /etc/shadow",
	"truncate -s 0 /", "find / -delete",
	"git push --force origin main", "git push -f origin main",
	"drop database", "drop table", "truncate table",
	"format c:", "del /f /s /q c:\\",
}

// catastrophicRoots are absolute targets whose recursive deletion is
// treated as critical: the filesystem root and the top-level system
// directories. Home ("~") and a bare wildcard ("*", "/*") are handled
// separately in isCatastrophicTarget.
var catastrophicRoots = map[string]struct{}{
	"/etc": {}, "/usr": {}, "/bin": {}, "/sbin": {}, "/var": {},
	"/boot": {}, "/lib": {}, "/lib64": {}, "/root": {}, "/home": {},
	"/sys": {}, "/proc": {}, "/dev": {}, "/opt": {}, "/srv": {},
}

// analyzeRm inspects one command's argv (argv[0] must be an "rm"
// invocation) and reports whether it is a catastrophic recursive
// delete. It understands combined flags ("-rf"), split flags
// ("-r -f"), long options ("--recursive --force"), the "--" end-of-
// options marker and normalised targets ("/etc//"), closing the gap
// where only a single "-[a-z]*r...f" flag shape was matched before.
func analyzeRm(argv []string) (string, bool) {
	if len(argv) == 0 {
		return "", false
	}
	if lastPathSegment(strings.ToLower(argv[0])) != "rm" {
		return "", false
	}
	recursive := false
	noPreserveRoot := false
	endOfFlags := false
	var targets []string
	for _, tok := range argv[1:] {
		switch {
		case !endOfFlags && tok == "--":
			endOfFlags = true
		case !endOfFlags && strings.HasPrefix(tok, "--"):
			switch tok {
			case "--recursive", "--dir":
				recursive = true
			case "--no-preserve-root":
				noPreserveRoot = true
			}
		case !endOfFlags && strings.HasPrefix(tok, "-") && len(tok) > 1:
			for _, r := range tok[1:] {
				if r == 'r' || r == 'R' {
					recursive = true
				}
			}
		default:
			targets = append(targets, tok)
		}
	}
	if !recursive {
		return "", false
	}
	for _, t := range targets {
		if isCatastrophicTarget(t) {
			return "recursive delete of a system path: rm " + strings.Join(argv[1:], " "), true
		}
	}
	if noPreserveRoot {
		return "recursive delete with --no-preserve-root: rm " + strings.Join(argv[1:], " "), true
	}
	return "", false
}

// isCatastrophicTarget reports whether a recursive-delete target is a
// whole-filesystem, wildcard-root, home or top-level system path.
// Redundant separators and a trailing slash are normalised first, so
// "/etc//" and "/etc/" match "/etc".
func isCatastrophicTarget(t string) bool {
	t = strings.Trim(strings.TrimSpace(t), `"'`)
	if t == "" {
		return false
	}
	switch t {
	case "/", "/*", "*", "~", "~/", "$HOME", "${HOME}", "$HOME/", "${HOME}/":
		return true
	}
	clean := collapseSlashes(t)
	// A wildcard directly under a catastrophic root ("/etc/*", "/*").
	if strings.HasSuffix(clean, "/*") {
		base := strings.TrimRight(strings.TrimSuffix(clean, "/*"), "/")
		if base == "" {
			return true
		}
		if _, ok := catastrophicRoots[base]; ok {
			return true
		}
	}
	trimmed := strings.TrimRight(clean, "/")
	if _, ok := catastrophicRoots[trimmed]; ok {
		return true
	}
	return false
}

// rmSegments finds "rm ..." runs in free text and returns each as a
// whitespace-split argv starting at the "rm" token. It is a best-effort
// tokeniser for text that never reaches shellsafe (non-shell code
// blocks, raw arguments); shell command lines and shell code blocks are
// parsed structurally instead.
//
// Command boundaries (`;`, `|`, `&`, backtick, quotes and parentheses)
// are emitted as standalone separator tokens so an rm segment ends at
// the next command: "rm -rf ./build; ls /usr" no longer folds "/usr"
// into the rm operands, while "os.system('rm -r -f /')" still yields
// ["rm","-r","-f","/"].
func rmSegments(text string) [][]string {
	toks := tokenizeWithBoundaries(text)
	var segs [][]string
	for i := 0; i < len(toks); i++ {
		if toks[i] == rmBoundaryToken || lastPathSegment(strings.ToLower(toks[i])) != "rm" {
			continue
		}
		seg := []string{"rm"}
		for j := i + 1; j < len(toks); j++ {
			// Stop at a command boundary or the start of another rm.
			if toks[j] == rmBoundaryToken ||
				lastPathSegment(strings.ToLower(toks[j])) == "rm" {
				break
			}
			seg = append(seg, toks[j])
		}
		segs = append(segs, seg)
	}
	return segs
}

// rmBoundaryToken is a sentinel emitted by tokenizeWithBoundaries where
// a shell command separator was found. It cannot collide with a real
// argv token because it contains characters a shell word never yields.
const rmBoundaryToken = "\x00boundary\x00"

// tokenizeWithBoundaries splits text on whitespace and quotes/parens
// like the previous tokeniser, but turns command separators (`;`, `|`,
// `&`, backtick) into an explicit boundary sentinel instead of a plain
// separator, so callers can tell "rm; ls" from "rm ls".
func tokenizeWithBoundaries(text string) []string {
	var toks []string
	var cur strings.Builder
	flush := func() {
		if cur.Len() > 0 {
			toks = append(toks, cur.String())
			cur.Reset()
		}
	}
	for _, r := range text {
		switch r {
		case ';', '|', '&', '`':
			flush()
			toks = append(toks, rmBoundaryToken)
		case ' ', '\t', '\n', '\r', '\'', '"', '(', ')', ',':
			flush()
		default:
			cur.WriteRune(r)
		}
	}
	flush()
	return toks
}

// collapseSlashes replaces runs of "/" with a single "/", so redundant
// separators cannot be used to dodge a substring or exact path match
// ("/etc//shadow" -> "/etc/shadow"). Windows separators are folded to
// "/" first so the same denied-path list covers both.
func collapseSlashes(s string) string {
	s = strings.ReplaceAll(s, `\`, "/")
	for strings.Contains(s, "//") {
		s = strings.ReplaceAll(s, "//", "/")
	}
	return s
}

// dependencyInstallPatterns flag package-manager and toolchain
// mutations that pull remote code into the environment.
var dependencyInstallPatterns = []string{
	"go install", "go get",
	"pip install", "pip3 install", "pipx install",
	"npm install", "npm i", "npm ci", "pnpm add", "yarn add",
	"apt install", "apt-get install", "apk add", "yum install",
	"dnf install", "brew install", "gem install", "cargo install",
	"curl | sh", "curl | bash", "wget | sh", "wget | bash",
}

// hardBypass are shell interpreters and re-executing builtins whose
// presence is a strong bypass signal (sh -c, bash -c, eval, ...).
// They are denied even without an explicit allow/deny list because
// their whole purpose is to run an arbitrary child command that the
// per-segment policy can no longer see.
var hardBypass = map[string]struct{}{
	"sh": {}, "bash": {}, "zsh": {}, "ash": {}, "dash": {},
	"ksh": {}, "mksh": {}, "fish": {}, "pwsh": {}, "powershell": {}, "cmd": {},
	"busybox": {}, "toybox": {}, "eval": {}, "exec": {}, "command": {},
	"source": {},
}

// softWrapper are process runners and privilege tools that are risky
// but legitimately used (timeout, env, sudo, ...). They escalate to an
// ask decision so a human can confirm rather than a hard deny.
var softWrapper = map[string]struct{}{
	"xargs": {}, "env": {}, "sudo": {}, "su": {}, "doas": {},
	"nohup": {}, "setsid": {}, "chroot": {}, "timeout": {}, "runuser": {},
}

// infiniteSources are device files that produce unbounded output when
// read, a classic resource-abuse vector.
var infiniteSources = []string{"/dev/urandom", "/dev/random", "/dev/zero"}

// defaultEgress is the built-in set of network-client executables.
var defaultEgress = map[string]struct{}{
	"curl": {}, "wget": {}, "nc": {}, "ncat": {}, "netcat": {},
	"ssh": {}, "scp": {}, "sftp": {}, "rsync": {}, "ftp": {},
	"telnet": {}, "socat": {}, "aria2c": {}, "http": {}, "https": {},
}

// hostBridgePatterns are code-level constructs that shell out to the
// host from inside a code executor.
var hostBridgePatterns = []string{
	"os.system(", "subprocess.", "os.popen(", "commands.getoutput(",
	"exec.command(", "exec.commandcontext(", "syscall.exec",
	"runtime.exec(", "child_process", "shell_exec(", "system(",
	"popen(", "process.start", "`" /* backtick exec */, "pty.spawn(",
}

// secretPatterns detect credential-shaped substrings. Each returns
// the matched text (already truncated) so it can be redacted.
var secretPatterns = []*regexp.Regexp{
	// Private key headers.
	regexp.MustCompile(`-----BEGIN (?:RSA |EC |OPENSSH |DSA |PGP )?PRIVATE KEY-----`),
	// AWS access key id.
	regexp.MustCompile(`AKIA[0-9A-Z]{16}`),
	// GitHub tokens.
	regexp.MustCompile(`gh[pousr]_[0-9A-Za-z]{20,}`),
	// Slack tokens.
	regexp.MustCompile(`xox[baprs]-[0-9A-Za-z-]{10,}`),
	// Google API key.
	regexp.MustCompile(`AIza[0-9A-Za-z_\-]{35}`),
	// Bearer tokens.
	regexp.MustCompile(`(?i)bearer\s+[0-9A-Za-z._\-]{20,}`),
	// Generic key=value assignments for common secret names.
	regexp.MustCompile(`(?i)(?:api[_-]?key|secret|token|password|passwd|access[_-]?key)\s*[=:]\s*['"]?[0-9A-Za-z._\-/+]{8,}`),
}

// detectSecrets returns the distinct secret matches in text.
func detectSecrets(text string) []string {
	if text == "" {
		return nil
	}
	seen := map[string]struct{}{}
	var out []string
	for _, re := range secretPatterns {
		for _, m := range re.FindAllString(text, -1) {
			key := m
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			out = append(out, truncate(m, 24))
		}
	}
	return out
}

// redactSecrets masks every secret match in text with a fixed token.
func redactSecrets(text string) string {
	if text == "" {
		return text
	}
	for _, re := range secretPatterns {
		text = re.ReplaceAllStringFunc(text, func(m string) string {
			// Preserve any leading name= prefix so the report stays
			// legible while hiding the value.
			if idx := strings.IndexAny(m, "=:"); idx >= 0 {
				return m[:idx+1] + "***REDACTED***"
			}
			return "***REDACTED***"
		})
	}
	return text
}

var sleepRe = regexp.MustCompile(`(?i)\bsleep\s+(\d+)`)

// yesRe matches the coreutils "yes" generator invoked as its own
// segment (start of string or after a pipe/operator), which emits
// output forever. It deliberately does not match "yes" appearing as
// an argument to another command.
var yesRe = regexp.MustCompile(`(?i)(?:^|[|&;]\s*)yes\b`)

// hasYesCommand reports whether text invokes the unbounded "yes"
// generator as a command.
func hasYesCommand(lc string) bool {
	return yesRe.MatchString(lc)
}

// longestSleep returns the largest sleep duration (seconds) found in
// text, if any.
func longestSleep(text string) (int, bool) {
	matches := sleepRe.FindAllStringSubmatch(text, -1)
	best := 0
	found := false
	for _, m := range matches {
		if len(m) < 2 {
			continue
		}
		if v, err := strconv.Atoi(m[1]); err == nil {
			found = true
			if v > best {
				best = v
			}
		}
	}
	return best, found
}

// allHosts extracts every destination host from egress-command
// arguments, so a multi-target invocation (curl URL1 URL2, wget of
// several URLs, rsync src dst) is fully checked rather than only its
// first operand. Flags and their attached values ("-o file", "--url=")
// are handled so an option operand is not mistaken for a destination.
func allHosts(args []string) []string {
	var hosts []string
	seen := map[string]struct{}{}
	skipNext := false
	for _, a := range args {
		a = strings.TrimSpace(a)
		if a == "" {
			continue
		}
		if skipNext {
			skipNext = false
			// A flag value may itself be a URL (e.g. --url=... is
			// handled below; "-u https://..." lands here).
			if h := hostFromToken(a); h != "" && looksLikeURL(a) {
				addHost(&hosts, seen, h)
			}
			continue
		}
		if strings.HasPrefix(a, "-") {
			// --flag=value: inspect the value for a URL.
			if i := strings.IndexByte(a, '='); i >= 0 {
				if v := a[i+1:]; looksLikeURL(v) {
					if h := hostFromToken(v); h != "" {
						addHost(&hosts, seen, h)
					}
				}
				continue
			}
			// A bare option consumes the following token as its value.
			if optionTakesValue(a) {
				skipNext = true
			}
			continue
		}
		if h := hostFromToken(a); h != "" {
			addHost(&hosts, seen, h)
		}
	}
	return hosts
}

func addHost(hosts *[]string, seen map[string]struct{}, h string) {
	if _, ok := seen[h]; ok {
		return
	}
	seen[h] = struct{}{}
	*hosts = append(*hosts, h)
}

// optionTakesValue reports whether a short egress-client flag consumes
// the next argument. The conservative default is false so that a plain
// destination is never skipped; only well-known value-taking flags are
// listed, and being wrong here only risks an extra (safe) check.
func optionTakesValue(flag string) bool {
	switch strings.ToLower(flag) {
	case "-o", "-out", "--output", "-t", "--upload-file", "-u", "--user",
		"-h", "--header", "-d", "--data", "-p", "-i", "-l", "-e":
		return true
	}
	return false
}

// looksLikeURL reports whether a token names a network destination: an
// explicit scheme, a host:port, or a dotted host with a path. It is
// deliberately loose so egress checks err towards inspecting a value.
func looksLikeURL(tok string) bool {
	tok = strings.Trim(strings.TrimSpace(tok), `"'`)
	if tok == "" {
		return false
	}
	if strings.Contains(tok, "://") {
		return true
	}
	if strings.HasPrefix(tok, "//") {
		return true
	}
	// user@host (scp/ssh style) or host:path (rsync).
	if strings.Contains(tok, "@") || strings.Contains(tok, ":") {
		return true
	}
	// dotted hostname, optionally with a path.
	head := tok
	if i := strings.IndexAny(head, "/?#"); i >= 0 {
		head = head[:i]
	}
	return strings.Contains(head, ".")
}

// shellLanguages are code-block language tags whose content is executed
// by a shell and therefore gets the full per-command rule treatment.
var shellLanguages = map[string]struct{}{
	"sh": {}, "bash": {}, "shell": {}, "zsh": {}, "ksh": {},
	"dash": {}, "ash": {}, "console": {}, "shellscript": {},
}

// isShellLanguage reports whether a code-block language tag denotes a
// shell script.
func isShellLanguage(lang string) bool {
	_, ok := shellLanguages[strings.ToLower(strings.TrimSpace(lang))]
	return ok
}

func hostFromToken(tok string) string {
	// Strip scheme.
	if i := strings.Index(tok, "://"); i >= 0 {
		tok = tok[i+3:]
	}
	// Strip user@.
	if i := strings.LastIndex(tok, "@"); i >= 0 {
		tok = tok[i+1:]
	}
	// Cut path / query.
	if i := strings.IndexAny(tok, "/?#"); i >= 0 {
		tok = tok[:i]
	}
	// Cut port.
	if i := strings.LastIndex(tok, ":"); i >= 0 {
		tok = tok[:i]
	}
	return strings.ToLower(strings.Trim(tok, "\"'"))
}

// lastPathSegment returns the basename of an executable reference,
// handling both / and \ separators.
func lastPathSegment(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.LastIndexAny(s, `/\`); i >= 0 {
		return s[i+1:]
	}
	return s
}

func toLowerSet(in []string) map[string]struct{} {
	out := make(map[string]struct{}, len(in))
	for _, s := range in {
		s = strings.TrimSpace(strings.ToLower(s))
		if s == "" {
			continue
		}
		out[s] = struct{}{}
	}
	return out
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// commandPreview renders a short echo of the request command for the
// report (before redaction is applied by the caller).
func commandPreview(req Request) string {
	if strings.TrimSpace(req.Command) != "" {
		return truncate(req.Command, 240)
	}
	if len(req.Args) > 0 {
		return truncate(strings.Join(req.Args, " "), 240)
	}
	var lines []string
	for _, b := range req.CodeBlocks {
		first := b.Code
		if i := strings.IndexByte(first, '\n'); i >= 0 {
			first = first[:i]
		}
		lines = append(lines, "["+b.Language+"] "+first)
	}
	return truncate(strings.Join(lines, " ; "), 240)
}

// oversized reports whether the request payload exceeds the envelope
// cap the scanner is willing to inspect in full.
func oversized(req Request) bool {
	total := len(req.Command)
	for _, a := range req.Args {
		total += len(a)
	}
	for _, b := range req.CodeBlocks {
		total += len(b.Code) + len(b.Language)
	}
	for k, v := range req.Env {
		total += len(k) + len(v)
	}
	return total > maxEnvelopeBytes
}
