//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

//go:build cgo

package main

import (
	"context"
	"strings"

	"github.com/yanyiwu/gojieba"
	"trpc.group/trpc-go/trpc-agent-go/evaluation"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult"
	evalresultlocal "trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult/local"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	evalsetlocal "trpc.group/trpc-go/trpc-agent-go/evaluation/evalset/local"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator/registry"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
	metriclocal "trpc.group/trpc-go/trpc-agent-go/evaluation/metric/local"
	metricregistry "trpc.group/trpc-go/trpc-agent-go/evaluation/metric/registry"
	"trpc.group/trpc-go/trpc-agent-go/runner"
)

type jiebaTokenizer struct {
	segmenter *gojieba.Jieba
}

// Tokenize implements the ROUGE tokenizer interface with Jieba segmentation.
func (t jiebaTokenizer) Tokenize(text string) []string {
	segments := t.segmenter.Cut(text, true)
	tokens := make([]string, 0, len(segments))
	for _, segment := range segments {
		segment = strings.TrimSpace(segment)
		if segment != "" {
			tokens = append(tokens, segment)
		}
	}
	return tokens
}

func run(ctx context.Context) error {
	segmenter := gojieba.NewJieba()
	defer segmenter.Free()
	run := runner.NewRunner(appName, newJiebaAgent(*modelName, *streaming))
	defer run.Close()
	evalSetManager := evalsetlocal.New(evalset.WithBaseDir(*dataDir))
	metricManager := metriclocal.New(metric.WithBaseDir(*dataDir))
	evalResultManager := evalresultlocal.New(evalresult.WithBaseDir(*outputDir))
	evaluatorRegistry := registry.New()
	metricRegistry := metricregistry.New()
	if err := metricRegistry.RegisterRougeTokenizer("jieba", jiebaTokenizer{segmenter: segmenter}); err != nil {
		return err
	}
	agentEvaluator, err := evaluation.New(
		appName,
		run,
		evaluation.WithEvalSetManager(evalSetManager),
		evaluation.WithMetricManager(metricManager),
		evaluation.WithEvalResultManager(evalResultManager),
		evaluation.WithRegistry(evaluatorRegistry),
		evaluation.WithMetricRegistry(metricRegistry),
		evaluation.WithNumRuns(*numRuns),
	)
	if err != nil {
		return err
	}
	defer agentEvaluator.Close()
	result, err := agentEvaluator.Evaluate(ctx, *evalSetID)
	if err != nil {
		return err
	}
	printSummary(result, *outputDir)
	return nil
}
