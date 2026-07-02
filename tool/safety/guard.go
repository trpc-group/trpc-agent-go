//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

import (
	"context"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// Guard wraps a Scanner as a tool.PermissionPolicy so it can be
// plugged into a Runner before every tool call.
//
// Usage:
//
//	guard := safety.NewGuard(safety.WithRules(
//	    safety.NewDangerousCommandRule(),
//	    safety.NewNetworkAccessRule(),
//	    ...
//	))
//	// Then pass to Runner as a per-run option:
//	//   runner.Run(ctx, userID, sessionID, msg,
//	//       agent.WithToolPermissionPolicy(guard))
type Guard struct {
	scanner *Scanner
	extract func(args []byte) ScanInput
}

// GuardOption configures a Guard.
type GuardOption func(*Guard)

// WithRules sets the rules used by the guard's Scanner.
func WithRules(rules ...Rule) GuardOption {
	return func(g *Guard) { g.scanner = NewScanner(rules...) }
}

// WithScanner uses an existing Scanner.
func WithScanner(s *Scanner) GuardOption {
	return func(g *Guard) { g.scanner = s }
}

// WithExtractor sets a custom function to extract ScanInput from tool arguments.
// The default extractor looks for a "command" field in the JSON arguments.
func WithExtractor(fn func(args []byte) ScanInput) GuardOption {
	return func(g *Guard) { g.extract = fn }
}

// NewGuard creates a Guard that implements tool.PermissionPolicy.
func NewGuard(opts ...GuardOption) *Guard {
	g := &Guard{extract: defaultExtractor}
	for _, o := range opts {
		o(g)
	}
	if g.scanner == nil {
		g.scanner = NewScanner(
			NewDangerousCommandRule(),
			NewNetworkAccessRule(),
			NewShellBypassRule(),
			NewInstallAndMutateRule(),
			NewHostExecRiskRule(),
			NewResourceAbuseRule(),
			NewSensitiveInfoLeakRule(),
			NewAskForReviewRule(),
		)
	}
	return g
}

// CheckToolPermission implements tool.PermissionPolicy.
func (g *Guard) CheckToolPermission(ctx context.Context, req *tool.PermissionRequest) (tool.PermissionDecision, error) {
	input := g.extract(req.Arguments)
	res := g.scanner.Scan(input)

	switch res.Decision {
	case DecisionDeny:
		return tool.DenyPermission(res.Reason), nil
	case DecisionAsk:
		return tool.AskPermission(res.Reason), nil
	default:
		return tool.AllowPermission(), nil
	}
}

// defaultExtractor reads a "command" field from JSON arguments.
func defaultExtractor(args []byte) ScanInput {
	in := ScanInput{ExecutorType: "local"}
	if len(args) == 0 {
		return in
	}
	// Simple JSON extraction: look for "command":"..." pattern.
	start := -1
	for i := 0; i < len(args)-9; i++ {
		if args[i] == '"' && args[i+1] == 'c' && args[i+2] == 'o' &&
			args[i+3] == 'm' && args[i+4] == 'm' && args[i+5] == 'a' &&
			args[i+6] == 'n' && args[i+7] == 'd' && args[i+8] == '"' {
			start = i + 10 // skip "command":"
			break
		}
	}
	if start < 0 {
		return in
	}
	// Read until the closing unescaped quote.
	var cmd []byte
	for i := start; i < len(args); i++ {
		if args[i] == '\\' && i+1 < len(args) {
			cmd = append(cmd, args[i+1])
			i++
			continue
		}
		if args[i] == '"' {
			break
		}
		cmd = append(cmd, args[i])
	}
	in.Command = string(cmd)
	return in
}
