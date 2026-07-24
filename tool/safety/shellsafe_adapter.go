// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.

package safety

import (
	"errors"
	"fmt"

	"trpc.group/trpc-go/trpc-agent-go/internal/shellsafe"
)

// ShellParsePolicy controls the decision made when shellsafe cannot
// completely and safely parse a shell command. FailureDecision may be either
// DecisionDeny or DecisionAsk. The zero value, DecisionAllow, and every other
// value fail closed as DecisionDeny.
type ShellParsePolicy struct {
	FailureDecision Decision
}

func (p ShellParsePolicy) failureDecision() Decision {
	if p.FailureDecision == DecisionAsk {
		return DecisionAsk
	}
	return DecisionDeny
}

// ShellCommandSegment is the stable command view supplied to shell rules.
// Executable is argv[0]; Args contains the remaining argv elements. Args is
// copied from shellsafe's Pipeline and may safely be retained or modified by
// a caller without changing this adapter's parser state.
type ShellCommandSegment struct {
	Executable string
	Args       []string
}

// ShellCommandView is the shellsafe-derived view supplied to shell rules.
//
// RawCommand is always the command received by the adapter, including any
// leading or trailing whitespace. When Trusted is true, Segments is a copy of
// shellsafe.Pipeline.Commands, represented as executable plus arguments.
// shellsafe intentionally discards sequencing operators, so this view makes
// no claim about whether adjacent segments were joined by |, &&, ||, or ;.
// Rules that only need to inspect every parsed segment should use Segments;
// a rule needing an operator kind cannot infer it from this view.
//
// When Trusted is false, Segments is nil, ParseError records shellsafe's
// rejection (or an adapter validation/panic error), and ParseDecision is
// always deny or ask. This prevents a caller from treating a partial parse as
// permission to execute the original command.
type ShellCommandView struct {
	RawCommand    string
	Segments      []ShellCommandSegment
	ParseError    error
	ParseDecision Decision
	Trusted       bool
}

// AdaptShellCommand parses command with shellsafe and converts its public
// Pipeline.Commands structure into a stable view for safety rules. It does
// not parse shell syntax itself. shellsafe rejections are retained verbatim
// in ParseError, including its conclusions about substitution, redirection,
// backgrounding, and expansion.
//
// The adapter recovers a parser panic and treats malformed parser output as
// untrusted. Both cases use policy's fail-closed failure decision. The
// adapter has no mutable package state.
func AdaptShellCommand(
	command string,
	policy ShellParsePolicy,
) ShellCommandView {
	return adaptShellCommand(command, policy, shellsafe.Parse)
}

type shellsafeParseFunc func(string) (*shellsafe.Pipeline, error)

func adaptShellCommand(
	command string,
	policy ShellParsePolicy,
	parse shellsafeParseFunc,
) (view ShellCommandView) {
	fallback := ShellCommandView{
		RawCommand:    command,
		ParseDecision: policy.failureDecision(),
	}
	view = fallback
	completed := false
	defer func() {
		if !completed {
			// A nil panic value must also fail closed. recover clears a
			// non-nil panic; completed distinguishes panic(nil) from a
			// normal return.
			_ = recover()
			view = fallback
			view.ParseError = errors.New("shellsafe parser panicked")
		}
	}()

	view = adaptShellsafeResult(command, policy, parse)
	completed = true
	return view
}

func adaptShellsafeResult(
	command string,
	policy ShellParsePolicy,
	parse shellsafeParseFunc,
) (view ShellCommandView) {
	view.RawCommand = command
	view.ParseDecision = policy.failureDecision()

	pipe, err := parse(command)
	if err != nil {
		view.ParseError = err
		return view
	}
	if pipe == nil {
		view.ParseError = errors.New("shellsafe returned a nil pipeline")
		return view
	}
	if len(pipe.Commands) == 0 {
		view.ParseError = errors.New("shellsafe returned no command segments")
		return view
	}

	segments := make([]ShellCommandSegment, 0, len(pipe.Commands))
	for i, argv := range pipe.Commands {
		if len(argv) == 0 {
			view.ParseError = fmt.Errorf(
				"shellsafe returned an empty command segment at index %d", i,
			)
			return view
		}
		segments = append(segments, ShellCommandSegment{
			Executable: argv[0],
			Args:       append([]string(nil), argv[1:]...),
		})
	}

	view.Segments = segments
	view.ParseDecision = DecisionAllow
	view.Trusted = true
	return view
}
