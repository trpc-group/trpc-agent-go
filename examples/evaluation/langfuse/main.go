//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"syscall"
	"time"

	coreevaluation "trpc.group/trpc-go/trpc-agent-go/evaluation"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult"
	evalresultlocal "trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult/local"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	evalsetlocal "trpc.group/trpc-go/trpc-agent-go/evaluation/evalset/local"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
	metriclocal "trpc.group/trpc-go/trpc-agent-go/evaluation/metric/local"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	sevaluation "trpc.group/trpc-go/trpc-agent-go/server/evaluation"
	langfuseeval "trpc.group/trpc-go/trpc-agent-go/server/evaluation/langfuse"
	telemetrylangfuse "trpc.group/trpc-go/trpc-agent-go/telemetry/langfuse"
)

const (
	appNameValue           = "langfuse-remote-eval-app"
	defaultBasePath        = "/evaluation"
	defaultRoutePath       = "/langfuse/remote-experiment"
	defaultUserID          = "langfuse-remote-user"
	defaultEnvironment     = "development"
	defaultShutdownTimeout = 10 * time.Second
)

var (
	addr      = flag.String("addr", ":8088", "Address the evaluation server listens on.")
	modelName = flag.String("model", "gpt-4.1-mini", "Model identifier used by the example agent.")
	streaming = flag.Bool("streaming", false, "Enable streaming responses from the example agent.")
	dataDir   = flag.String("data-dir", "./data", "Directory containing evaluation set and metric files.")
	outputDir = flag.String("output-dir", "./output", "Directory where evaluation results will be stored.")
)

func main() {
	flag.Parse()
	cleanup, err := telemetrylangfuse.Start(context.Background())
	if err != nil {
		log.Fatalf("start Langfuse telemetry: %v", err)
	}
	defer func() {
		if err := cleanup(context.Background()); err != nil {
			log.Printf("shutdown Langfuse telemetry: %v", err)
		}
	}()
	agentRunner := runner.NewRunner(appNameValue, newRemoteEvalAgent(*modelName, *streaming))
	defer func() {
		if err := agentRunner.Close(); err != nil {
			log.Printf("close runner: %v", err)
		}
	}()
	judgeRunner := runner.NewRunner(appNameValue+"-judge", newJudgeAgent(*modelName))
	defer func() {
		if err := judgeRunner.Close(); err != nil {
			log.Printf("close judge runner: %v", err)
		}
	}()
	evalSetManager := evalsetlocal.New(evalset.WithBaseDir(*dataDir))
	defer func() {
		_ = evalSetManager.Close()
	}()
	evalResultManager := evalresultlocal.New(evalresult.WithBaseDir(*outputDir))
	defer func() {
		_ = evalResultManager.Close()
	}()
	metricManager := metriclocal.New(metric.WithBaseDir(*dataDir))
	defer func() {
		_ = metricManager.Close()
	}()
	agentEvaluator, err := coreevaluation.New(
		appNameValue,
		agentRunner,
		coreevaluation.WithEvalSetManager(evalSetManager),
		coreevaluation.WithEvalResultManager(evalResultManager),
		coreevaluation.WithMetricManager(metricManager),
		coreevaluation.WithJudgeRunner(judgeRunner),
	)
	if err != nil {
		log.Fatalf("create agent evaluator: %v", err)
	}
	defer func() {
		if err := agentEvaluator.Close(); err != nil {
			log.Printf("close agent evaluator: %v", err)
		}
	}()
	langfuseHandler, err := langfuseeval.New(
		appNameValue,
		agentEvaluator,
		evalSetManager,
		metricManager,
		evalResultManager,
		langfuseeval.WithCaseBuilder(buildCaseSpec),
		langfuseeval.WithPath(defaultRoutePath),
		langfuseeval.WithUserIDSupplier(func(_ context.Context) string {
			return defaultUserID
		}),
		langfuseeval.WithEnvironment(defaultEnvironment),
	)
	if err != nil {
		log.Fatalf("create Langfuse handler: %v", err)
	}
	server, err := sevaluation.New(
		sevaluation.WithAppName(appNameValue),
		sevaluation.WithBasePath(defaultBasePath),
		sevaluation.WithAgentEvaluator(agentEvaluator),
		sevaluation.WithEvalSetManager(evalSetManager),
		sevaluation.WithEvalResultManager(evalResultManager),
		sevaluation.WithRouteRegistrar(langfuseHandler),
	)
	if err != nil {
		log.Fatalf("create evaluation server: %v", err)
	}
	httpServer := &http.Server{
		Addr:              *addr,
		Handler:           server.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	routePath, err := url.JoinPath(server.BasePath(), langfuseHandler.Path())
	if err != nil {
		log.Fatalf("join route path: %v", err)
	}
	go func() {
		log.Printf("Evaluation server is listening on http://%s%s", *addr, server.BasePath())
		log.Printf("Langfuse remote experiment endpoint is available at http://%s%s", *addr, routePath)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("listen and serve: %v", err)
		}
	}()
	waitForShutdown(httpServer, defaultShutdownTimeout)
}

func waitForShutdown(httpServer *http.Server, timeout time.Duration) {
	signalCh := make(chan os.Signal, 1)
	signal.Notify(signalCh, syscall.SIGINT, syscall.SIGTERM)
	<-signalCh
	shutdownCtx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		log.Printf("shutdown HTTP server: %v", err)
	}
}
