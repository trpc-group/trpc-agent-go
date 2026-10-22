//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package modelreplay checks native replay compatibility for routing models.
package modelreplay

import (
	"fmt"

	"trpc.group/trpc-go/trpc-agent-go/model"
)

// CandidateInfo checks that a routing wrapper can report one stable
// native producer identity. Heterogeneous legacy candidates remain supported.
func CandidateInfo(candidates []model.Model) (model.Info, error) {
	if len(candidates) == 0 {
		return model.Info{}, fmt.Errorf("native replay requires at least one candidate")
	}
	primary := candidates[0].Info()
	hasNative := false
	for _, candidate := range candidates {
		hasNative = hasNative || candidate.Info().ReplayProtocol != ""
	}
	if hasNative {
		for _, candidate := range candidates {
			info := candidate.Info()
			if info.Name != primary.Name || info.ReplayProtocol != primary.ReplayProtocol {
				return model.Info{}, fmt.Errorf("native replay requires candidates with the same protocol and configured model name")
			}
		}
	}
	return primary, nil
}
