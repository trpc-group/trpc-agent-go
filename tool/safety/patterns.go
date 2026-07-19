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
// Recursive-delete patterns are handled separately by
// destructiveRmRe so a scoped "rm -rf /tmp/x" is not confused with a
// root wipe.
var destructivePatterns = []string{
	"rm -rf ~", "rm -rf --no-preserve-root", "rm --no-preserve-root",
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

// destructiveRmRe matches recursive force deletes that target the
// filesystem root, a wildcard root, a home directory or a top-level
// system directory. A scoped delete such as "rm -rf /tmp/build" or
// "rm -rf ./node_modules" is intentionally NOT matched here so the
// critical decision stays reserved for genuinely catastrophic cases.
var destructiveRmRe = regexp.MustCompile(
	`(?i)\brm\s+(?:-[a-z]*\s+)*-[a-z]*r[a-z]*f?[a-z]*\s+` +
		`(?:/\s|/\*|/$|~|--no-preserve-root|` +
		`/(?:etc|usr|bin|sbin|var|boot|lib|lib64|root|home|sys|proc|dev|opt)\b)`,
)

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

// firstHost extracts a hostname from egress-command arguments. It
// understands bare hosts, host:port and URLs.
func firstHost(args []string) string {
	for _, a := range args {
		a = strings.TrimSpace(a)
		if a == "" || strings.HasPrefix(a, "-") {
			continue
		}
		return hostFromToken(a)
	}
	return ""
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
