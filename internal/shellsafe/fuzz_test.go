//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package shellsafe

import "testing"

func FuzzParseNeverPanics(f *testing.F) {
	seeds := []string{
		"",
		`""`,
		`''`,
		`"" argument`,
		`echo ""`,
		"echo hello",
		"echo hello | wc -c",
		"echo $(whoami)",
		"echo $HOME",
		"echo hello > output.txt",
		"sh -c 'echo hello'",
		"echo 'unterminated",
		"echo \"unterminated",
		"echo first\necho second",
		"\x00",
	}

	for _, seed := range seeds {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, input string) {
		pipeline, err := Parse(input)
		if err != nil {
			if pipeline != nil {
				t.Fatalf(
					"Parse() returned both pipeline and error: "+
						"pipeline=%+v error=%v",
					pipeline,
					err,
				)
			}
			return
		}

		if pipeline == nil {
			t.Fatal(
				"Parse() succeeded with nil pipeline",
			)
		}

		if len(pipeline.Commands) == 0 {
			t.Fatal(
				"Parse() succeeded with no commands",
			)
		}

		for i, argv := range pipeline.Commands {
			if len(argv) == 0 {
				t.Fatalf(
					"Parse() command %d has empty argv",
					i,
				)
			}

			if argv[0] == "" {
				t.Fatalf(
					"Parse() command %d has empty argv[0]",
					i,
				)
			}
		}
	})
}
