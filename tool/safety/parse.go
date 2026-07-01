//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

import (
	"net/url"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/internal/shellsafe"
)

// windowsExecExts are stripped from command names so "curl.exe" matches "curl".
var windowsExecExts = []string{".exe", ".cmd", ".bat", ".com", ".ps1"}

// commandBase returns the lower-cased basename of an executable reference with
// any Windows executable suffix removed, e.g. "/usr/bin/Curl" -> "curl",
// "cmd.exe" -> "cmd". It mirrors the normalisation internal/shellsafe applies
// so the guard and the underlying policy agree on command identity.
func commandBase(s string) string {
	s = strings.Trim(strings.TrimSpace(s), `"'`)
	s = strings.ReplaceAll(s, "\\", "/")
	if idx := strings.LastIndex(s, "/"); idx >= 0 {
		s = s[idx+1:]
	}
	s = strings.ToLower(s)
	for _, ext := range windowsExecExts {
		if strings.HasSuffix(s, ext) {
			return s[:len(s)-len(ext)]
		}
	}
	return s
}

// normalizePathArg canonicalises a path argument for denied-path matching:
// forward slashes, unquoted, with $HOME / ${HOME} folded to "~".
func normalizePathArg(s string) string {
	s = strings.Trim(strings.TrimSpace(s), `"'`)
	s = strings.ReplaceAll(s, "\\", "/")
	s = strings.ReplaceAll(s, "${HOME}", "~")
	s = strings.ReplaceAll(s, "$HOME", "~")
	return s
}

// parsePipeline runs the conservative shellsafe parser and returns the pipeline
// segments (each is an argv slice). A non-nil error means the command used a
// construct shellsafe rejects (command substitution, redirection, subshell,
// leading assignment, ...) and must not be treated as safe.
func parsePipeline(command string) ([][]string, error) {
	pipe, err := shellsafe.Parse(command)
	if err != nil {
		return nil, err
	}
	return pipe.Commands, nil
}

// splitScriptLines splits a possibly multi-line script into logical command
// lines, joining backslash continuations and dropping blank / comment lines.
func splitScriptLines(script string) []string {
	raw := strings.Split(script, "\n")
	var lines []string
	var pending strings.Builder
	for _, ln := range raw {
		ln = strings.TrimRight(ln, "\r")
		trimmed := strings.TrimSpace(ln)
		if pending.Len() == 0 && (trimmed == "" || strings.HasPrefix(trimmed, "#")) {
			continue
		}
		if strings.HasSuffix(ln, "\\") {
			pending.WriteString(strings.TrimSuffix(ln, "\\"))
			pending.WriteString(" ")
			continue
		}
		pending.WriteString(ln)
		line := strings.TrimSpace(pending.String())
		pending.Reset()
		if line != "" && !strings.HasPrefix(line, "#") {
			lines = append(lines, line)
		}
	}
	if rest := strings.TrimSpace(pending.String()); rest != "" && !strings.HasPrefix(rest, "#") {
		lines = append(lines, rest)
	}
	return lines
}

// isFlag reports whether an argv token is an option flag.
func isFlag(s string) bool {
	return strings.HasPrefix(s, "-") && s != "-"
}

// extractHost returns the target host of a network command's arguments, if one
// can be determined. It understands scheme URLs, user@host forms and bare
// host[:port][/path] tokens.
func extractHost(argv []string) (string, bool) {
	for _, a := range argv[1:] {
		if a == "" || isFlag(a) {
			continue
		}
		if h := hostFromToken(a); h != "" {
			return h, true
		}
	}
	return "", false
}

// hostFromToken extracts a hostname from a single token, or "" if the token is
// not host-like. A token that explicitly marks a host position — a scheme URL
// (curl http://h) or a user@host form (scp f user@h:/p) — yields its host even
// when single-label, so short intranet exfil hosts are not missed. A bare
// token must contain a dot (or be localhost) to avoid mistaking flag values
// like "POST"/"GET" for hosts.
func hostFromToken(a string) string {
	a = strings.Trim(a, `"'`)
	explicit := false
	if i := strings.Index(a, "://"); i >= 0 {
		if u, err := url.Parse(a); err == nil && u.Hostname() != "" {
			return strings.ToLower(u.Hostname())
		}
		a = a[i+3:]
		explicit = true
	}
	if i := strings.LastIndex(a, "@"); i >= 0 {
		a = a[i+1:]
		explicit = true
	}
	if i := strings.IndexByte(a, '/'); i >= 0 {
		a = a[:i]
	}
	if i := strings.LastIndex(a, ":"); i >= 0 {
		a = a[:i]
	}
	a = strings.ToLower(strings.TrimSpace(a))
	if a == "" {
		return ""
	}
	if explicit || a == "localhost" || strings.Contains(a, ".") {
		return a
	}
	return ""
}
