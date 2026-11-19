//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package toolfilter

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestConstructorsTimeout(t *testing.T) {
	constructors := map[string]func(...Option) error{
		"tool callback": func(opts ...Option) error {
			_, err := NewToolCallback("https://decision.example", opts...)
			return err
		},
		"skill filter": func(opts ...Option) error {
			_, err := NewSkillFilter("https://decision.example", &fakeSkillRepo{}, opts...)
			return err
		},
		"model router": func(opts ...Option) error {
			opts = append(opts, WithModelCandidate("test", &stubModel{name: "test"}, "test"))
			_, err := NewModelRouter("https://decision.example", opts...)
			return err
		},
	}
	for name, construct := range constructors {
		t.Run(name, func(t *testing.T) {
			require.NoError(t, construct())
			require.NoError(t, construct(WithTimeout(time.Second)))
			for _, timeout := range []time.Duration{0, -time.Second} {
				err := construct(WithTimeout(timeout))
				require.ErrorContains(t, err, "timeout must be positive")
			}
		})
	}
	require.Equal(t, 30*time.Second, applyOptions(nil).timeout)
}
