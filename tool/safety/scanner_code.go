//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

func (s *DefaultScanner) scanCode(req ScanRequest) []Finding {
	lang := strings.ToLower(strings.TrimSpace(req.Language))
	if lang == "" {
		return []Finding{{
			RuleID:         "codeexec.unsupported_language",
			RiskLevel:      RiskMedium,
			Decision:       DecisionAsk,
			Evidence:       "missing language",
			Recommendation: "review code blocks with missing language metadata",
		}}
	}
	var findings []Finding
	switch lang {
	case "bash", "sh", "shell":
		for _, line := range strings.Split(req.Code, "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			lineReq := req
			lineReq.Command = line
			lineReq.Code = ""
			findings = append(findings, s.scanCommand(lineReq)...)
		}
	case "python":
		findings = append(findings, s.scanTextForUnknownRisk(req, req.Code)...)
		findings = append(findings, s.scanCodeResourceAbuse(lang, req.Code)...)
		if strings.Contains(req.Code, "subprocess") ||
			strings.Contains(req.Code, "os.system") {
			findings = append(findings, Finding{
				RuleID:         "codeexec.subprocess",
				RiskLevel:      RiskHigh,
				Decision:       DecisionAsk,
				Evidence:       "python subprocess or os.system usage",
				Recommendation: "review subprocess execution inside code blocks",
			})
		}
	case "go", "javascript", "typescript", "node":
		findings = append(findings, s.scanTextForUnknownRisk(req, req.Code)...)
		findings = append(findings, s.scanCodeResourceAbuse(lang, req.Code)...)
	default:
		findings = append(findings, Finding{
			RuleID:         "codeexec.unsupported_language",
			RiskLevel:      RiskMedium,
			Decision:       DecisionAsk,
			Evidence:       lang,
			Recommendation: "review unsupported code execution languages manually",
		})
	}
	return findings
}

var (
	pythonInfiniteLoopPattern = regexp.MustCompile(`(?mi)\bwhile\s+(?:true|1)\s*:`)
	goInfiniteLoopPattern     = regexp.MustCompile(`(?mi)\bfor\s*\{`)
	jsInfiniteLoopPattern     = regexp.MustCompile(`(?mi)\bwhile\s*\(\s*(?:true|1)\s*\)|\bfor\s*\(\s*;\s*;\s*\)`)
	codeSleepPattern          = regexp.MustCompile(`(?i)(?:time\.)?sleep\s*\(\s*([0-9]+)\s*(seconds?|secs?|minutes?|mins?|hours?|hrs?|days?|d)?\s*\)`)
	goSleepPattern            = regexp.MustCompile(`(?i)time\.sleep\s*\(\s*([0-9]+)\s*\*\s*time\.(second|minute|hour|day)s?\s*\)`)
	jsSleepPattern            = regexp.MustCompile(`(?i)settimeout\s*\([^,]+,\s*([0-9]+)\s*\)`)
)

func (s *DefaultScanner) scanCodeResourceAbuse(lang, code string) []Finding {
	if hasObviousInfiniteLoop(lang, code) {
		return []Finding{{
			RuleID:         "resource.long_running",
			RiskLevel:      RiskCritical,
			Decision:       DecisionDeny,
			Evidence:       fmt.Sprintf("%s code contains an obvious infinite loop", lang),
			Recommendation: "bound code execution with a terminating condition or timeout",
		}}
	}
	seconds, ok := codeSleepSeconds(lang, code)
	if !ok || s.policy.MaxTimeoutSec <= 0 || seconds <= s.policy.MaxTimeoutSec {
		return nil
	}
	decision := DecisionAsk
	risk := RiskHigh
	if seconds > s.policy.MaxTimeoutSec*10 {
		decision = DecisionDeny
		risk = RiskCritical
	}
	return []Finding{{
		RuleID:         "resource.long_running",
		RiskLevel:      risk,
		Decision:       decision,
		Evidence:       fmt.Sprintf("%s code sleeps for %d seconds", lang, seconds),
		Recommendation: "use bounded execution time or require approval",
	}}
}

func hasObviousInfiniteLoop(lang, code string) bool {
	code = maskNonExecutableSource(lang, code)
	switch lang {
	case "python":
		return pythonInfiniteLoopPattern.MatchString(code)
	case "go":
		return goInfiniteLoopPattern.MatchString(code)
	case "javascript", "typescript", "node":
		return jsInfiniteLoopPattern.MatchString(code)
	default:
		return false
	}
}

func maskNonExecutableSource(lang, code string) string {
	var out strings.Builder
	out.Grow(len(code))
	for i := 0; i < len(code); {
		switch {
		case lang == "python" && code[i] == '#':
			i = maskLineComment(code, &out, i)
		case sourceCommentStart(lang, code, i):
			i = maskSourceComment(code, &out, i)
		case isJavaScriptLanguage(lang) && code[i] == '`':
			i = maskJavaScriptTemplateLiteral(code, &out, i)
		case sourceQuoteStart(lang, code[i]):
			i = maskSourceString(lang, code, &out, i)
		default:
			out.WriteByte(code[i])
			i++
		}
	}
	return out.String()
}

func isJavaScriptLanguage(lang string) bool {
	switch lang {
	case "javascript", "typescript", "node":
		return true
	default:
		return false
	}
}

func maskJavaScriptTemplateLiteral(code string, out *strings.Builder, index int) int {
	out.WriteByte(' ')
	return maskJavaScriptTemplateLiteralBody(code, out, index+1)
}

func maskJavaScriptTemplateLiteralBody(
	code string,
	out *strings.Builder,
	index int,
) int {
	for index < len(code) {
		switch {
		case code[index] == '\n':
			out.WriteByte('\n')
			index++
		case code[index] == '\\':
			index = maskEscapedSourceCharacter(code, out, index)
		case code[index] == '`':
			out.WriteByte(' ')
			return index + 1
		case index+1 < len(code) && code[index] == '$' && code[index+1] == '{':
			out.WriteString("${")
			index = maskJavaScriptTemplateExpression(code, out, index+2)
		default:
			out.WriteByte(' ')
			index++
		}
	}
	return index
}

func maskJavaScriptTemplateExpression(
	code string,
	out *strings.Builder,
	index int,
) int {
	depth := 1
	for index < len(code) {
		switch {
		case sourceCommentStart("javascript", code, index):
			index = maskSourceComment(code, out, index)
		case code[index] == '`':
			index = maskJavaScriptTemplateLiteral(code, out, index)
		case code[index] == '\'' || code[index] == '"':
			index = maskSourceString("javascript", code, out, index)
		case code[index] == '{':
			out.WriteByte(code[index])
			depth++
			index++
		case code[index] == '}':
			out.WriteByte(code[index])
			depth--
			index++
			if depth == 0 {
				return index
			}
		default:
			out.WriteByte(code[index])
			index++
		}
	}
	return index
}

func maskEscapedSourceCharacter(
	code string,
	out *strings.Builder,
	index int,
) int {
	out.WriteByte(' ')
	index++
	if index >= len(code) {
		return index
	}
	if code[index] == '\n' {
		out.WriteByte('\n')
	} else {
		out.WriteByte(' ')
	}
	return index + 1
}

func sourceCommentStart(lang, code string, index int) bool {
	return lang != "python" && index+1 < len(code) &&
		code[index] == '/' && (code[index+1] == '/' || code[index+1] == '*')
}

func sourceQuoteStart(lang string, quote byte) bool {
	return quote == '\'' || quote == '"' || (lang != "python" && quote == '`')
}

func maskLineComment(code string, out *strings.Builder, index int) int {
	for index < len(code) && code[index] != '\n' {
		out.WriteByte(' ')
		index++
	}
	return index
}

func maskSourceComment(code string, out *strings.Builder, index int) int {
	out.WriteString("  ")
	index += 2
	if code[index-1] == '/' {
		return maskLineComment(code, out, index)
	}
	for index+1 < len(code) && !(code[index] == '*' && code[index+1] == '/') {
		if code[index] == '\n' {
			out.WriteByte('\n')
		} else {
			out.WriteByte(' ')
		}
		index++
	}
	if index+1 < len(code) {
		out.WriteString("  ")
		index += 2
	}
	return index
}

func maskSourceString(lang string, code string, out *strings.Builder, index int) int {
	quote := code[index]
	triple := lang == "python" && index+2 < len(code) &&
		code[index+1] == quote && code[index+2] == quote
	if triple {
		out.WriteString("   ")
		index += 3
	} else {
		out.WriteByte(' ')
		index++
	}
	for index < len(code) {
		if code[index] == '\n' {
			out.WriteByte('\n')
			index++
			continue
		}
		out.WriteByte(' ')
		if code[index] == '\\' && index+1 < len(code) {
			index++
			out.WriteByte(' ')
			index++
			continue
		}
		if sourceStringEnd(code, index, quote, triple) {
			if triple {
				out.WriteString("  ")
				return index + 3
			}
			return index + 1
		}
		index++
	}
	return index
}

func sourceStringEnd(code string, index int, quote byte, triple bool) bool {
	if triple {
		return index+2 < len(code) && code[index] == quote &&
			code[index+1] == quote && code[index+2] == quote
	}
	return code[index] == quote
}

func codeSleepSeconds(lang, code string) (int, bool) {
	code = maskNonExecutableSource(lang, code)
	maxSeconds := 0
	found := false
	if lang == "go" {
		if seconds, ok := maxGoSleepSeconds(code); ok {
			maxSeconds = seconds
			found = true
		}
	}
	if isJavaScriptLanguage(lang) {
		if seconds, ok := maxJavaScriptSleepSeconds(code); ok {
			if !found || seconds > maxSeconds {
				maxSeconds = seconds
			}
			found = true
		}
	}
	if seconds, ok := maxGenericSleepSeconds(code); ok {
		if !found || seconds > maxSeconds {
			maxSeconds = seconds
		}
		found = true
	}
	return maxSeconds, found
}

func maxGoSleepSeconds(code string) (int, bool) {
	maxSeconds := 0
	found := false
	for _, match := range goSleepPattern.FindAllStringSubmatch(code, -1) {
		if len(match) != 3 {
			continue
		}
		value, err := strconv.Atoi(match[1])
		if err != nil {
			continue
		}
		multiplier := 1
		switch strings.ToLower(match[2]) {
		case "minute":
			multiplier = 60
		case "hour":
			multiplier = 60 * 60
		case "day":
			multiplier = 24 * 60 * 60
		}
		seconds := value * multiplier
		if !found || seconds > maxSeconds {
			maxSeconds = seconds
			found = true
		}
	}
	return maxSeconds, found
}

func maxJavaScriptSleepSeconds(code string) (int, bool) {
	maxSeconds := 0
	found := false
	for _, match := range jsSleepPattern.FindAllStringSubmatch(code, -1) {
		if len(match) != 2 {
			continue
		}
		value, err := strconv.Atoi(match[1])
		if err != nil {
			continue
		}
		seconds := value / 1000
		if !found || seconds > maxSeconds {
			maxSeconds = seconds
			found = true
		}
	}
	return maxSeconds, found
}

func maxGenericSleepSeconds(code string) (int, bool) {
	maxSeconds := 0
	found := false
	for _, match := range codeSleepPattern.FindAllStringSubmatch(code, -1) {
		if len(match) != 3 {
			continue
		}
		value, err := strconv.Atoi(match[1])
		if err != nil {
			continue
		}
		seconds := value * sleepUnitMultiplier(strings.ToLower(match[2]))
		if !found || seconds > maxSeconds {
			maxSeconds = seconds
			found = true
		}
	}
	return maxSeconds, found
}

func sleepUnitMultiplier(unit string) int {
	switch unit {
	case "minute", "minutes", "min", "mins":
		return 60
	case "hour", "hours", "hr", "hrs":
		return 60 * 60
	case "day", "days", "d":
		return 24 * 60 * 60
	default:
		return 1
	}
}
