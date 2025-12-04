//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package main

import (
	"flag"
	"net/http"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult"
	evalresultlocal "trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult/local"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	evalsetlocal "trpc.group/trpc-go/trpc-agent-go/evaluation/evalset/local"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
	metriclocal "trpc.group/trpc-go/trpc-agent-go/evaluation/metric/local"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/server/debug"
)

const (
	defaultListenAddr = ":8080"
	defaultAppName    = "evaluation-assistant"
)

func main() {
	modelName := flag.String("model", "deepseek-chat", "Name of the model to use.")
	addr := flag.String("addr", defaultListenAddr, "Listen address.")
	appName := flag.String("app", defaultAppName, "App name registered in the debug server.")
	dataDir := flag.String("data-dir", "./data", "Directory where eval sets and metric configs are stored.")
	outputDir := flag.String("output-dir", "./output", "Directory where eval results are stored.")
	flag.Parse()

	agents := map[string]agent.Agent{
		*appName: newDemoAgent(*appName, *modelName, true),
	}
	evalSetManager := evalsetlocal.New(evalset.WithBaseDir(*dataDir))
	evalResultManager := evalresultlocal.New(evalresult.WithBaseDir(*outputDir))
	metricManager := metriclocal.New(metric.WithBaseDir(*dataDir))
	server := debug.New(
		agents,
		debug.WithEvalSetManager(evalSetManager),
		debug.WithEvalResultManager(evalResultManager),
		debug.WithMetricManager(metricManager),
	)

	log.Infof("debug+evaluation server listening on %s (app=%s, model=%s)", *addr, *appName, *modelName)
	if err := http.ListenAndServe(*addr, server.Handler()); err != nil {
		log.Fatalf("server error: %v", err)
	}
}
