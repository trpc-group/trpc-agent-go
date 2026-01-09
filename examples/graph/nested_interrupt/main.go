//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"reflect"

	_ "github.com/mattn/go-sqlite3"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	graphagent "trpc.group/trpc-go/trpc-agent-go/agent/graphagent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/graph"
	checkpointsqlite "trpc.group/trpc-go/trpc-agent-go/graph/checkpoint/sqlite"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

const (
	modeRun    = "run"
	modeResume = "resume"

	sqliteDriverName = "sqlite3"

	defaultDBPath    = "nested-interrupt.db"
	defaultLineageID = "demo-nested-interrupt"

	parentAgentName = "parent"
	childAgentName  = "child"

	childNodeAsk   = "ask"
	stateKeyAnswer = "answer"

	startMessage  = "start"
	resumeMessage = "resume"

	interruptPrompt = "Please type an approval string."
)

type runSummary struct {
	interruptValue any
	answer         string
}

func main() {
	var (
		mode         string
		dbPath       string
		lineageID    string
		checkpointID string
		resumeValue  string
	)

	flag.StringVar(&mode, "mode", modeRun, "run or resume")
	flag.StringVar(&lineageID, "lineage-id", defaultLineageID, "lineage")
	flag.StringVar(&dbPath, "db", defaultDBPath, "sqlite path")
	flag.StringVar(&checkpointID, "checkpoint-id", "", "resume checkpoint")
	flag.StringVar(&resumeValue, "resume-value", "", "resume value")
	flag.Parse()

	if err := validateFlags(mode, lineageID, checkpointID); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}

	ctx := context.Background()
	saver, closeDB, err := openSQLiteSaver(dbPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer closeDB()

	parent, cm, err := buildAgents(saver)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	switch mode {
	case modeRun:
		err = runMode(ctx, parent, cm, lineageID)
	case modeResume:
		err = resumeMode(ctx, parent, lineageID, checkpointID, resumeValue)
	default:
		err = fmt.Errorf("unknown mode: %s", mode)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func validateFlags(mode string, lineageID string, checkpointID string) error {
	if mode != modeRun && mode != modeResume {
		return fmt.Errorf("invalid -mode: %q", mode)
	}
	if lineageID == "" {
		return errors.New("-lineage-id is required")
	}
	if mode == modeResume && checkpointID == "" {
		return errors.New("-checkpoint-id is required for -mode resume")
	}
	return nil
}

func openSQLiteSaver(
	dbPath string,
) (graph.CheckpointSaver, func(), error) {
	db, err := sql.Open(sqliteDriverName, dbPath)
	if err != nil {
		return nil, nil, fmt.Errorf("open sqlite: %w", err)
	}
	saver, err := checkpointsqlite.NewSaver(db)
	if err != nil {
		_ = db.Close()
		return nil, nil, fmt.Errorf("create sqlite saver: %w", err)
	}
	return saver, func() { _ = db.Close() }, nil
}

func buildAgents(
	saver graph.CheckpointSaver,
) (*graphagent.GraphAgent, *graph.CheckpointManager, error) {
	schema := graph.NewStateSchema()
	schema.AddField(
		stateKeyAnswer,
		graph.StateField{Type: reflect.TypeOf("")},
	)

	childGraph, err := graph.NewStateGraph(schema).
		AddNode(childNodeAsk, childAskNode()).
		SetEntryPoint(childNodeAsk).
		SetFinishPoint(childNodeAsk).
		Compile()
	if err != nil {
		return nil, nil, fmt.Errorf("build child graph: %w", err)
	}

	child, err := graphagent.New(
		childAgentName,
		childGraph,
		graphagent.WithCheckpointSaver(saver),
	)
	if err != nil {
		return nil, nil, fmt.Errorf("build child agent: %w", err)
	}

	parentGraph, err := graph.NewStateGraph(schema).
		AddAgentNode(
			childAgentName,
			graph.WithSubgraphOutputMapper(parentOutputMapper()),
		).
		SetEntryPoint(childAgentName).
		SetFinishPoint(childAgentName).
		Compile()
	if err != nil {
		return nil, nil, fmt.Errorf("build parent graph: %w", err)
	}

	parent, err := graphagent.New(
		parentAgentName,
		parentGraph,
		graphagent.WithCheckpointSaver(saver),
		graphagent.WithSubAgents([]agent.Agent{child}),
	)
	if err != nil {
		return nil, nil, fmt.Errorf("build parent agent: %w", err)
	}
	cm := parent.Executor().CheckpointManager()
	if cm == nil {
		return nil, nil, errors.New("checkpoint manager not configured")
	}
	return parent, cm, nil
}

func childAskNode() graph.NodeFunc {
	return func(ctx context.Context, state graph.State) (any, error) {
		v, err := graph.Interrupt(ctx, state, childNodeAsk, interruptPrompt)
		if err != nil {
			return nil, err
		}
		s, _ := v.(string)
		return graph.State{stateKeyAnswer: s}, nil
	}
}

func parentOutputMapper() graph.SubgraphOutputMapper {
	return func(_ graph.State, res graph.SubgraphResult) graph.State {
		answer, ok := graph.GetStateValue[string](res.FinalState, stateKeyAnswer)
		if !ok {
			return nil
		}
		return graph.State{stateKeyAnswer: answer}
	}
}

func runMode(
	ctx context.Context,
	parent *graphagent.GraphAgent,
	cm *graph.CheckpointManager,
	lineageID string,
) error {
	fmt.Println("Running parent graph (expected to interrupt)...")

	summary, err := runOnce(
		ctx,
		parent,
		agent.NewInvocation(
			agent.WithInvocationMessage(model.NewUserMessage(startMessage)),
			agent.WithInvocationRunOptions(agent.RunOptions{
				RuntimeState: map[string]any{
					graph.CfgKeyLineageID: lineageID,
				},
			}),
		),
	)
	if err != nil {
		return err
	}
	if summary.interruptValue == nil {
		fmt.Printf("Completed without interrupt. answer=%q\n", summary.answer)
		return nil
	}

	fmt.Printf("Interrupted with prompt: %v\n", summary.interruptValue)

	tuple, err := cm.Latest(ctx, lineageID, parent.Info().Name)
	if err != nil {
		return fmt.Errorf("latest checkpoint: %w", err)
	}
	if tuple == nil || tuple.Checkpoint == nil {
		return errors.New("no checkpoint found after interrupt")
	}
	if !tuple.Checkpoint.IsInterrupted() {
		return errors.New("latest checkpoint is not interrupted")
	}

	fmt.Println()
	fmt.Println("To resume, run:")
	fmt.Printf(
		"  go run . -mode %s -lineage-id %s -checkpoint-id %s "+
			"-resume-value <value>\n",
		modeResume,
		lineageID,
		tuple.Checkpoint.ID,
	)
	return nil
}

func resumeMode(
	ctx context.Context,
	parent *graphagent.GraphAgent,
	lineageID string,
	checkpointID string,
	resumeValue string,
) error {
	fmt.Println("Resuming from parent checkpoint...")

	summary, err := runOnce(
		ctx,
		parent,
		agent.NewInvocation(
			agent.WithInvocationMessage(
				model.NewUserMessage(resumeMessage),
			),
			agent.WithInvocationRunOptions(agent.RunOptions{
				RuntimeState: map[string]any{
					graph.CfgKeyLineageID:    lineageID,
					graph.CfgKeyCheckpointID: checkpointID,
					graph.StateKeyCommand: &graph.Command{
						Resume: resumeValue,
					},
				},
			}),
		),
	)
	if err != nil {
		return err
	}
	if summary.interruptValue != nil {
		return fmt.Errorf("unexpected interrupt: %v", summary.interruptValue)
	}
	fmt.Printf("Completed. answer=%q\n", summary.answer)
	return nil
}

func runOnce(
	ctx context.Context,
	a agent.Agent,
	inv *agent.Invocation,
) (runSummary, error) {
	ch, err := a.Run(ctx, inv)
	if err != nil {
		return runSummary{}, err
	}
	return readEvents(ch)
}

func readEvents(ch <-chan *event.Event) (runSummary, error) {
	var res runSummary
	for ev := range ch {
		if ev == nil || ev.Response == nil {
			continue
		}
		if ev.Error != nil {
			return runSummary{}, fmt.Errorf("agent error: %s", ev.Error.Message)
		}
		if res.interruptValue == nil {
			v, ok, err := interruptFromEvent(ev)
			if err != nil {
				return runSummary{}, err
			}
			if ok {
				res.interruptValue = v
			}
		}
		if ev.Done && ev.Object == graph.ObjectTypeGraphExecution {
			answer, err := answerFromStateDelta(ev.StateDelta)
			if err != nil {
				return runSummary{}, err
			}
			res.answer = answer
		}
	}
	return res, nil
}

func interruptFromEvent(ev *event.Event) (any, bool, error) {
	if ev.Object != graph.ObjectTypeGraphPregelStep {
		return nil, false, nil
	}
	if ev.StateDelta == nil {
		return nil, false, nil
	}
	raw, ok := ev.StateDelta[graph.MetadataKeyPregel]
	if !ok || len(raw) == 0 {
		return nil, false, nil
	}
	var meta graph.PregelStepMetadata
	if err := json.Unmarshal(raw, &meta); err != nil {
		return nil, false, fmt.Errorf("decode pregel metadata: %w", err)
	}
	if meta.InterruptValue == nil {
		return nil, false, nil
	}
	return meta.InterruptValue, true, nil
}

func answerFromStateDelta(delta map[string][]byte) (string, error) {
	if delta == nil {
		return "", nil
	}
	raw, ok := delta[stateKeyAnswer]
	if !ok || len(raw) == 0 {
		return "", nil
	}
	var answer string
	if err := json.Unmarshal(raw, &answer); err != nil {
		return "", fmt.Errorf("decode answer: %w", err)
	}
	return answer, nil
}
