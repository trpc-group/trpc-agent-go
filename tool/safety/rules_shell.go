//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

// ruleShell evaluates shell-bypass rules. It inspects shellsafe parse
// failures and the wrapper-name set so each bypass shape gets a stable
// finding id.
//
// Rule ids:
//
//   - shell.parse_failure            shellsafe.Parse rejected the command.
//   - shell.wrapper                  sh, bash, eval, exec, xargs, env, sudo,
//     busybox, or equivalent re-executor.
//   - shell.substitution             command/parameter/arithmetic/process
//     substitution (also surfaces as a parse
//     failure with HasSubstitution).
//   - shell.redirection_or_background redirection or & bypass shape.
func ruleShell(a *analysis, p Policy) []Finding {
	if !p.Rules.ShellBypass.Enabled {
		return nil
	}
	var out []Finding

	if a.ParseError != nil {
		risk := RiskHigh
		ruleID := "shell.parse_failure"
		evidence := redactedSnippet(a.ParseError.Error(), 80)
		switch {
		case a.HasSubstitution:
			ruleID = "shell.substitution"
			evidence = "command/parameter/arithmetic/process substitution"
		case a.HasRedirection:
			ruleID = "shell.redirection_or_background"
			evidence = "redirection operator"
		case a.HasBackground:
			ruleID = "shell.redirection_or_background"
			evidence = "background operator"
		}
		out = append(out, Finding{
			RuleID:         ruleID,
			RiskLevel:      risk,
			Decision:       ruleDecision(p.Rules.ShellBypass.Action, risk, p),
			Evidence:       evidence,
			Recommendation: "Refuse shell-bypass constructs; wrap the desired use in an auditable workspace script",
		})
	}

	if len(a.WrapperNames) > 0 {
		out = append(out, Finding{
			RuleID:         "shell.wrapper",
			RiskLevel:      RiskHigh,
			Decision:       ruleDecision(p.Rules.ShellBypass.Action, RiskHigh, p),
			Evidence:       "shell wrapper or re-executing builtin detected",
			Recommendation: "Refuse shell wrappers; allow the underlying command directly or wrap it in an auditable script",
		})
	}

	return out
}
