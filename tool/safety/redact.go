//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

import "regexp"

// redactPlaceholder replaces every secret match.
const redactPlaceholder = "***REDACTED***"

// redact replaces secret-pattern matches in s with the placeholder and reports
// whether anything was redacted.
func (p *Policy) redact(s string) (string, bool) {
	return redactWith(p.compiled.secretRes, s)
}

func redactWith(res []*regexp.Regexp, s string) (string, bool) {
	if s == "" || len(res) == 0 {
		return s, false
	}
	redacted := false
	for _, re := range res {
		if re.MatchString(s) {
			redacted = true
			s = re.ReplaceAllString(s, redactPlaceholder)
		}
	}
	return s, redacted
}

// redactReport scrubs the command and every finding's evidence in place and
// sets Redacted when anything was removed. It must run before the report is
// written to the audit log or dumped, so secrets never reach a sink.
func (p *Policy) redactReport(r *Report) {
	if cmd, hit := p.redact(r.Command); hit {
		r.Command = cmd
		r.Redacted = true
	}
	for i := range r.Findings {
		if ev, hit := p.redact(r.Findings[i].Evidence); hit {
			r.Findings[i].Evidence = ev
			r.Redacted = true
		}
	}
}
