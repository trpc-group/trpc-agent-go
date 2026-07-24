//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

import (
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/internal/shellsafe"
)

// The whole shell layer trusts shellsafe.Parse. shellsafe is fail-closed: it
// rejects anything it cannot tokenize, which becomes a deny/ask via
// unparsable_action. The dangerous failure mode is therefore not "rejects a
// weird command" but "accepts a command yet incorrectly tokenizes argv[0]", hiding a
// dangerous executable behind a benign one. These tests pin both directions.

// TestAnchorRejectsBypassConstructs asserts shellsafe rejects the shell
// features that could smuggle arbitrary execution past an argv[0] check.
func TestAnchorRejectsBypassConstructs(t *testing.T) {
	rejected := []string{
		`echo $(whoami)`,          // command substitution
		"echo `whoami`",           // backtick substitution
		`echo $HOME`,              // parameter expansion
		`curl http://x > out`,     // redirection
		`echo hi & curl http://x`, // background operator
		`(curl http://x)`,         // subshell
		`echo ${X}url`,            // braced expansion
		`echo hi; rm -rf /tmp &`,  // trailing background
	}
	for _, cmd := range rejected {
		if _, err := shellsafe.Parse(cmd); err == nil {
			t.Errorf("shellsafe.Parse(%q) accepted; expected rejection (fail-closed)", cmd)
		}
	}
}

// TestAnchorTokenizesArgv0 asserts that for commands shellsafe *accepts*, the
// first token of each segment is the real executable, so wrapper detection and
// the allow/deny lists see the right name.
func TestAnchorTokenizesArgv0(t *testing.T) {
	cases := []struct {
		cmd      string
		wantArgv [][]string
	}{
		{`curl https://github.com/a`, [][]string{{"curl", "https://github.com/a"}}},
		{`cat a.txt | grep x`, [][]string{{"cat", "a.txt"}, {"grep", "x"}}},
		{`bash -c "curl http://x"`, [][]string{{"bash", "-c", "curl http://x"}}},
		{`rm -rf /`, [][]string{{"rm", "-rf", "/"}}},
	}
	for _, tc := range cases {
		pipe, err := shellsafe.Parse(tc.cmd)
		if err != nil {
			t.Errorf("shellsafe.Parse(%q) rejected: %v", tc.cmd, err)
			continue
		}
		if len(pipe.Commands) != len(tc.wantArgv) {
			t.Errorf("%q: %d segments, want %d", tc.cmd, len(pipe.Commands), len(tc.wantArgv))
			continue
		}
		for i, seg := range pipe.Commands {
			want := tc.wantArgv[i]
			if len(seg) != len(want) {
				t.Errorf("%q seg %d = %v, want %v", tc.cmd, i, seg, want)
				continue
			}
			for j := range seg {
				if seg[j] != want[j] {
					t.Errorf("%q seg %d token %d = %q, want %q", tc.cmd, i, j, seg[j], want[j])
				}
			}
		}
	}
}

// TestAnchorWrapperCaughtViaPolicy confirms a shell wrapper that parses (bash
// -c ...) is still blocked by shellsafe's implicit deny once a policy is
// active, which is how ruleCommandPolicy reports it as a shell bypass.
func TestAnchorWrapperCaughtViaPolicy(t *testing.T) {
	p := loadExamplePolicy(t)
	pipe, err := shellsafe.Parse(`bash -c "curl http://evil.io"`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if err := p.shellPolicy().Check(pipe); err == nil {
		t.Fatalf("expected shellsafe policy to reject bash wrapper")
	}
}
