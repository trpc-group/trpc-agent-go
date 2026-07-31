//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

import (
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

func (s *DefaultScanner) scanCwd(req ScanRequest) []Finding {
	if req.cwdResolutionRequired && !req.cwdResolved {
		return []Finding{{
			RuleID:         "path.workdir_unresolved",
			RiskLevel:      RiskHigh,
			Decision:       DecisionNeedsHumanReview,
			Evidence:       "host execution workdir could not be resolved before scanning",
			Recommendation: "resolve the host tool base directory before execution",
		}}
	}
	cwd := req.Cwd
	if strings.TrimSpace(cwd) == "" {
		return nil
	}
	for _, denied := range s.policy.DeniedPaths {
		if !sensitivePathMatch(cwd, denied) {
			continue
		}
		rule := "path.sensitive_credentials"
		if strings.Contains(strings.ToLower(denied), ".env") {
			rule = "path.secret_file"
		}
		return []Finding{{
			RuleID:         rule,
			RiskLevel:      RiskCritical,
			Decision:       DecisionDeny,
			Evidence:       "<redacted>",
			Recommendation: "do not run tool calls from credential or secret directories",
			Redacted:       true,
		}}
	}
	return nil
}

func (s *DefaultScanner) scanCollectionPaths(req ScanRequest) []Finding {
	if len(req.CollectionPaths) == 0 {
		return nil
	}
	var findings []Finding
	for _, collectionPath := range req.CollectionPaths {
		for _, denied := range s.policy.DeniedPaths {
			if !sensitivePathOrGlobMatch(collectionPath, denied) &&
				!sensitivePathOrGlobMatch(joinCwdPath(req.Cwd, collectionPath), denied) {
				continue
			}
			findings = append(findings, Finding{
				RuleID:         "path.output_collection",
				RiskLevel:      RiskCritical,
				Decision:       DecisionDeny,
				Evidence:       "<redacted>",
				Recommendation: "do not collect credential or secret paths from tool workspaces",
				Redacted:       true,
			})
		}
	}
	return findings
}

func (s *DefaultScanner) scanInputPaths(req ScanRequest) []Finding {
	if len(req.InputPaths) == 0 {
		return nil
	}
	var findings []Finding
	for _, inputPath := range req.InputPaths {
		for _, denied := range s.policy.DeniedPaths {
			if !sensitivePathMatch(inputPath, denied) &&
				!sensitivePathMatch(joinCwdPath(req.Cwd, inputPath), denied) &&
				!hostInputMayContainDenied(inputPath, denied) {
				continue
			}
			findings = append(findings, Finding{
				RuleID:         "path.input_staging",
				RiskLevel:      RiskCritical,
				Decision:       DecisionDeny,
				Evidence:       "<redacted>",
				Recommendation: "do not stage credential or secret paths into tool workspaces",
				Redacted:       true,
			})
		}
	}
	return findings
}

func (s *DefaultScanner) scanEditorText(req ScanRequest) []Finding {
	if strings.TrimSpace(req.EditorText) == "" {
		return nil
	}
	findings := s.scanSecretText(req.EditorText)
	if s.textMentionsDeniedPath(req.EditorText) {
		findings = append(findings, Finding{
			RuleID:         "path.sensitive_credentials",
			RiskLevel:      RiskCritical,
			Decision:       DecisionDeny,
			Evidence:       "<redacted>",
			Recommendation: "do not stage credential or secret paths through editor input",
			Redacted:       true,
		})
	}
	return findings
}

func (s *DefaultScanner) scanSecretText(text string) []Finding {
	var findings []Finding
	if containsSecret(text) {
		findings = append(findings, Finding{
			RuleID:         "secret.inline_value",
			RiskLevel:      RiskCritical,
			Decision:       s.policy.SecretAction,
			Evidence:       "<redacted>",
			Recommendation: "remove secrets before executing or auditing tool calls",
			Redacted:       true,
		})
	}
	lower := strings.ToLower(text)
	if strings.Contains(lower, "-----begin") &&
		strings.Contains(lower, "private key-----") {
		findings = append(findings, Finding{
			RuleID:         "secret.private_key",
			RiskLevel:      RiskCritical,
			Decision:       s.policy.SecretAction,
			Evidence:       "<redacted>",
			Recommendation: "never pass private keys through tool execution",
			Redacted:       true,
		})
	}
	return findings
}

func hostInputMayContainDenied(inputPath, denied string) bool {
	const hostScheme = "host://"
	raw := strings.TrimSpace(strings.TrimPrefix(inputPath, hostScheme))
	if raw == strings.TrimSpace(inputPath) || raw == "" {
		return false
	}
	raw = normalizePathForMatch(raw)
	if raw == "/" || raw == "." {
		return strings.TrimSpace(denied) != ""
	}
	deniedPath := normalizePathForMatch(denied)
	return strings.HasPrefix(deniedPath, strings.TrimRight(raw, "/")+"/")
}

func sensitivePathOrGlobMatch(value, denied string) bool {
	return sensitivePathMatch(value, denied) || globMayMatchDenied(value, denied)
}

func globMayMatchDenied(pattern, denied string) bool {
	pattern = slashNormalizedPathText(pattern)
	if !strings.ContainsAny(pattern, "*?[") {
		return false
	}
	matcher := globRegexp(pattern)
	if matcher == nil {
		return true
	}
	candidates := sensitivePathCandidates(denied)
	meta := strings.IndexAny(pattern, "*?[")
	prefix := strings.TrimSuffix(pattern[:meta], "/")
	for _, candidate := range candidates {
		if matcher.MatchString(slashNormalizedPathText(candidate)) {
			return true
		}
		if prefix != "" && matcher.MatchString(prefix+"/"+strings.TrimLeft(slashNormalizedPathText(candidate), "/")) {
			return true
		}
	}
	return false
}

func globRegexp(pattern string) *regexp.Regexp {
	var b strings.Builder
	b.WriteString("^")
	for i := 0; i < len(pattern); {
		switch {
		case strings.HasPrefix(pattern[i:], "**/"):
			b.WriteString("(?:.*/)?")
			i += 3
		case pattern[i] == '*':
			b.WriteString("[^/]*")
			i++
		case pattern[i] == '?':
			b.WriteString("[^/]")
			i++
		case pattern[i] == '[':
			end := strings.IndexByte(pattern[i+1:], ']')
			if end < 0 {
				return nil
			}
			b.WriteString("[^/]")
			i += end + 2
		default:
			b.WriteString(regexp.QuoteMeta(pattern[i : i+1]))
			i++
		}
	}
	b.WriteString("$")
	matcher, err := regexp.Compile(b.String())
	if err != nil {
		return nil
	}
	return matcher
}

func (s *DefaultScanner) scanSensitivePaths(req ScanRequest, argv []string) []Finding {
	var findings []Finding
	for _, arg := range argv[1:] {
		for _, denied := range s.policy.DeniedPaths {
			if sensitivePathMatch(arg, denied) || sensitivePathMatch(joinCwdPath(req.Cwd, arg), denied) {
				rule := "path.sensitive_credentials"
				if strings.Contains(strings.ToLower(denied), ".env") {
					rule = "path.secret_file"
				}
				findings = append(findings, Finding{
					RuleID:         rule,
					RiskLevel:      RiskCritical,
					Decision:       DecisionDeny,
					Evidence:       "<redacted>",
					Recommendation: "do not read credential or secret files through tools",
					Redacted:       true,
				})
			}
		}
	}
	return findings
}

func sensitivePathMatch(arg, denied string) bool {
	if strings.TrimSpace(arg) == "" || strings.TrimSpace(denied) == "" {
		return false
	}
	bareRule := isBareSensitivePathRule(denied)
	deniedNeedles := sensitivePathNeedles(denied)
	for _, candidate := range sensitivePathCandidates(arg) {
		candidateLower := strings.ToLower(candidate)
		for _, deniedNeedle := range deniedNeedles {
			if sensitiveNeedleMatch(candidateLower, deniedNeedle, bareRule) {
				return true
			}
		}
	}
	return false
}

func sensitiveNeedleMatch(candidate, needle string, bareRule bool) bool {
	if needle == "" {
		return false
	}
	if !bareRule {
		return strings.Contains(candidate, needle)
	}
	for offset := 0; offset <= len(candidate)-len(needle); {
		index := strings.Index(candidate[offset:], needle)
		if index < 0 {
			return false
		}
		start := offset + index
		end := start + len(needle)
		beforeOK := start == 0 || !isSensitiveWordRune(lastRune(candidate[:start]))
		afterOK := end == len(candidate) || !isSensitiveWordRune(firstRune(candidate[end:]))
		if beforeOK && afterOK {
			return true
		}
		offset = start + len(needle)
	}
	return false
}

func isBareSensitivePathRule(denied string) bool {
	denied = slashNormalizedPathText(denied)
	return denied != "" && !strings.Contains(denied, "/")
}

func firstRune(value string) rune {
	r, _ := utf8.DecodeRuneInString(value)
	return r
}

func lastRune(value string) rune {
	r, _ := utf8.DecodeLastRuneInString(value)
	return r
}

func isSensitiveWordRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsNumber(r)
}

func joinCwdPath(cwd, arg string) string {
	cwd = strings.Trim(strings.TrimSpace(cwd), `"'`)
	arg = strings.Trim(strings.TrimSpace(arg), `"'`)
	if cwd == "" || arg == "" || filepath.IsAbs(arg) ||
		strings.HasPrefix(arg, "~/") || strings.HasPrefix(arg, "~\\") {
		return arg
	}
	if strings.HasPrefix(arg, "-") {
		return arg
	}
	return filepath.ToSlash(filepath.Join(cwd, arg))
}

func (s *DefaultScanner) commandMentionsDeniedPath(command string) bool {
	for _, denied := range s.policy.DeniedPaths {
		if sensitivePathMatch(command, denied) {
			return true
		}
	}
	return false
}

func (s *DefaultScanner) textMentionsDeniedPath(text string) bool {
	for _, denied := range s.policy.DeniedPaths {
		if sensitivePathMatch(text, denied) {
			return true
		}
	}
	return false
}

func (s *DefaultScanner) redactReportText(text string) (string, bool) {
	return redactReportTextWithDeniedPaths(text, append([]string(nil), s.policy.DeniedPaths...))
}

func redactSensitivePath(text, denied string) string {
	needle := strings.Trim(strings.TrimSpace(denied), `"'`)
	if needle == "" {
		return text
	}
	if isBareSensitivePathRule(needle) {
		return redactNormalizedSensitiveTokens(text, denied)
	}
	out := replaceFold(text, needle, "<redacted>")
	slashed := strings.ReplaceAll(needle, "\\", "/")
	if slashed != needle {
		out = replaceFold(out, slashed, "<redacted>")
	}
	backslashed := strings.ReplaceAll(needle, "/", "\\")
	if backslashed != needle {
		out = replaceFold(out, backslashed, "<redacted>")
	}
	if strings.HasPrefix(slashed, "~/") {
		trimmed := strings.TrimPrefix(slashed, "~/")
		out = replaceFold(out, trimmed, "<redacted>")
	}
	out = redactNormalizedSensitiveTokens(out, denied)
	return out
}

func redactNormalizedSensitiveTokens(text, denied string) string {
	needles := sensitivePathNeedles(denied)
	if len(needles) == 0 {
		return text
	}
	var b strings.Builder
	start := 0
	for _, span := range sensitiveTokenSpans(text) {
		b.WriteString(text[start:span[0]])
		token := text[span[0]:span[1]]
		redacted, ok := redactSensitiveToken(token, needles)
		if ok {
			b.WriteString(redacted)
		} else {
			b.WriteString(token)
		}
		start = span[1]
	}
	b.WriteString(text[start:])
	return b.String()
}

func redactSensitiveToken(token string, deniedNeedles []string) (string, bool) {
	candidates := []struct {
		text  string
		start int
		end   int
	}{
		{text: token, start: 0, end: len(token)},
	}
	if idx := strings.Index(token, "="); idx >= 0 && idx+1 < len(token) {
		candidates = append(candidates, struct {
			text  string
			start int
			end   int
		}{
			text:  token[idx+1:],
			start: idx + 1,
			end:   len(token),
		})
	}
	for _, candidate := range candidates {
		for _, normalized := range sensitivePathCandidates(candidate.text) {
			lower := strings.ToLower(normalized)
			for _, deniedNeedle := range deniedNeedles {
				if sensitiveNeedleMatch(
					lower,
					deniedNeedle,
					isBareSensitivePathRule(deniedNeedle),
				) {
					return token[:candidate.start] + "<redacted>" + token[candidate.end:], true
				}
			}
		}
	}
	return token, false
}

func sensitivePathNeedles(denied string) []string {
	var needles []string
	add := func(value string) {
		value = strings.ToLower(strings.TrimSpace(value))
		if value == "" {
			return
		}
		for _, existing := range needles {
			if existing == value {
				return
			}
		}
		needles = append(needles, value)
	}
	raw := slashNormalizedPathText(denied)
	add(raw)
	add(normalizePathForMatch(raw))
	if strings.HasPrefix(raw, "~/") {
		add(strings.TrimPrefix(raw, "~/"))
	}
	normalized := normalizePathForMatch(raw)
	if strings.HasPrefix(normalized, "~/") {
		add(strings.TrimPrefix(normalized, "~/"))
	}
	return needles
}

func sensitivePathCandidates(text string) []string {
	base := slashNormalizedPathText(text)
	if base == "" {
		return nil
	}
	var candidates []string
	add := func(value string) {
		value = strings.TrimSpace(value)
		if value == "" {
			return
		}
		for _, existing := range candidates {
			if existing == value {
				return
			}
		}
		candidates = append(candidates, value)
	}
	add(base)
	if isPathLikeCandidate(base) {
		add(normalizePathForMatch(base))
	}
	if looksFreeFormText(base) {
		for _, span := range sensitiveTokenSpans(base) {
			token := base[span[0]:span[1]]
			add(token)
			if idx := strings.Index(token, "="); idx >= 0 && idx+1 < len(token) {
				token = token[idx+1:]
				add(token)
			}
			if isPathLikeCandidate(token) {
				add(normalizePathForMatch(token))
			}
		}
	}
	return candidates
}

func slashNormalizedPathText(text string) string {
	return strings.ReplaceAll(strings.Trim(strings.TrimSpace(text), `"'`), "\\", "/")
}

func normalizePathForMatch(value string) string {
	value = slashNormalizedPathText(value)
	if value == "" {
		return ""
	}
	switch {
	case strings.HasPrefix(value, "~/"):
		return "~/" + strings.TrimPrefix(path.Clean("/"+strings.TrimPrefix(value, "~/")), "/")
	case strings.HasPrefix(value, "/"):
		return path.Clean(value)
	case len(value) >= 3 && value[1] == ':' && value[2] == '/':
		return value[:2] + path.Clean(value[2:])
	default:
		return path.Clean(value)
	}
}

func isPathLikeCandidate(value string) bool {
	if value == "" || strings.ContainsAny(value, "\r\n\t ") {
		return false
	}
	return strings.ContainsAny(value, `/\`) ||
		strings.HasPrefix(value, "~") ||
		strings.HasPrefix(value, ".")
}

func looksFreeFormText(value string) bool {
	return strings.ContainsAny(value, "\r\n\t ") ||
		strings.ContainsAny(value, "|&;()[]{}<>,")
}

func sensitiveTokenSpans(text string) [][2]int {
	var spans [][2]int
	start := -1
	for i, r := range text {
		if isSensitiveTokenSeparator(r) {
			if start >= 0 {
				spans = append(spans, [2]int{start, i})
				start = -1
			}
			continue
		}
		if start < 0 {
			start = i
		}
	}
	if start >= 0 {
		spans = append(spans, [2]int{start, len(text)})
	}
	return spans
}

func isSensitiveTokenSeparator(r rune) bool {
	return unicode.IsSpace(r) || strings.ContainsRune(`|&;()[]{}<>,`, r)
}

func replaceFold(s, old, new string) string {
	if old == "" {
		return s
	}
	lower := strings.ToLower(s)
	oldLower := strings.ToLower(old)
	var b strings.Builder
	for {
		idx := strings.Index(lower, oldLower)
		if idx < 0 {
			b.WriteString(s)
			return b.String()
		}
		b.WriteString(s[:idx])
		b.WriteString(new)
		cut := idx + len(old)
		s = s[cut:]
		lower = lower[cut:]
	}
}
