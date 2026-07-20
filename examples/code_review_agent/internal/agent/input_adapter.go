//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package agent

import (
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/input"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/review"
)

func readInput(cfg Config, req Request) ([]byte, string, error) {
	return input.Read(inputConfig(cfg), inputRequest(req))
}

func inputMetadata(diff []byte, repoPath string) review.InputMetadata {
	return input.Metadata(diff, repoPath)
}

func inputMetadataForRequest(diff []byte, req Request) review.InputMetadata {
	return input.MetadataForRequest(diff, inputRequest(req))
}

func inputConfig(cfg Config) input.Config {
	return input.Config{
		FixturesRoot:  cfg.FixturesRoot,
		MaxInputBytes: cfg.MaxInputBytes,
	}
}

func inputRequest(req Request) input.Request {
	return input.Request{
		DiffFile: req.DiffFile,
		FileList: req.FileList,
		RepoPath: req.RepoPath,
		Fixture:  req.Fixture,
		BaseRef:  req.BaseRef,
		HeadRef:  req.HeadRef,
	}
}
