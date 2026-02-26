//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package graph

import (
	"context"
	"errors"
	"fmt"

	"trpc.group/trpc-go/trpc-agent-go/agent"
)

type dagTaskResult struct {
	task *Task
	err  error
}

func (e *Executor) runDagLoop(
	ctx context.Context,
	invocation *agent.Invocation,
	execCtx *ExecutionContext,
) (int, error) {
	if execCtx == nil {
		return 0, errors.New("execution context is nil")
	}
	if execCtx.channels == nil {
		return 0, errors.New("execution channels are nil")
	}
	if e.graph == nil {
		return 0, errors.New("graph is nil")
	}

	entryPoint := e.graph.EntryPoint()
	if entryPoint == "" {
		return 0, errors.New("no entry point defined")
	}

	var (
		executed int
		nextStep int
		ready    []*Task
		inFlight = make(map[string]bool)
		waiting  = make(map[string][]*Task)
		sem      = make(chan struct{}, e.maxConcurrency)
		done     = make(chan dagTaskResult, e.maxConcurrency)

		draining     bool
		drainErr     error
		drainMaxStep bool
	)

	queue := func(t *Task) {
		if t == nil {
			return
		}
		ready = append(ready, t)
	}

	queue(e.createDagTask(entryPoint))

	start := func(t *Task) bool {
		if t == nil || t.NodeID == "" {
			return true
		}
		if inFlight[t.NodeID] {
			waiting[t.NodeID] = append(waiting[t.NodeID], t)
			return true
		}
		select {
		case sem <- struct{}{}:
		default:
			return false
		}
		if nextStep >= e.maxSteps {
			<-sem
			draining = true
			drainMaxStep = true
			return false
		}
		step := nextStep
		nextStep++
		t.TaskID = fmt.Sprintf("%s-%d", t.NodeID, step)
		inFlight[t.NodeID] = true

		go func(t *Task, step int) {
			taskInvocation, taskCtx := e.taskInvocationContext(
				ctx,
				invocation,
				t,
			)
			err := e.executeSingleTask(
				taskCtx,
				taskInvocation,
				execCtx,
				t,
				step,
				nil,
			)
			<-sem
			done <- dagTaskResult{task: t, err: err}
		}(t, step)

		return true
	}

	for {
		for !draining && len(ready) > 0 {
			t := ready[0]
			if !start(t) {
				break
			}
			ready = ready[1:]
		}

		if !draining && len(ready) == 0 {
			for _, t := range e.planDagTasks(execCtx) {
				queue(t)
			}
		}

		if len(inFlight) == 0 {
			if draining {
				if drainMaxStep {
					return executed, nil
				}
				return executed, drainErr
			}
			if len(ready) == 0 {
				return executed, nil
			}
		}

		select {
		case <-ctx.Done():
			if !draining {
				draining = true
				drainErr = ctx.Err()
			}
		case res := <-done:
			nodeID := ""
			if res.task != nil {
				nodeID = res.task.NodeID
			}
			if nodeID != "" {
				delete(inFlight, nodeID)
				if w := waiting[nodeID]; len(w) > 0 {
					ready = append(ready, w...)
					delete(waiting, nodeID)
				}
			}

			if res.err != nil && !draining {
				draining = true
				drainErr = res.err
				continue
			}

			if res.err == nil {
				executed++
			}

			if !draining {
				for _, t := range e.planDagTasks(execCtx) {
					queue(t)
				}
			}
		}
	}
}

func (e *Executor) createDagTask(nodeID string) *Task {
	if nodeID == "" || nodeID == End {
		return nil
	}
	node, exists := e.graph.Node(nodeID)
	if !exists || node == nil {
		return nil
	}
	return &Task{
		NodeID:   nodeID,
		Writes:   node.writers,
		Triggers: node.triggers,
		TaskPath: []string{nodeID},
	}
}

func (e *Executor) planDagTasks(execCtx *ExecutionContext) []*Task {
	if execCtx == nil || execCtx.channels == nil {
		return nil
	}

	var tasks []*Task

	execCtx.tasksMutex.Lock()
	if len(execCtx.pendingTasks) > 0 {
		tasks = append(tasks, execCtx.pendingTasks...)
		execCtx.pendingTasks = nil
	}
	execCtx.tasksMutex.Unlock()

	triggerToNodes := e.graph.getTriggerToNodes()
	for channelName, nodeIDs := range triggerToNodes {
		ch, ok := execCtx.channels.GetChannel(channelName)
		if !ok || ch == nil {
			continue
		}
		if !ch.ConsumeIfAvailable() {
			continue
		}
		for _, nodeID := range nodeIDs {
			task := e.createDagTask(nodeID)
			if task == nil {
				continue
			}
			tasks = append(tasks, task)
		}
	}

	return tasks
}

func hasStaticInterrupts(g *Graph) bool {
	if g == nil {
		return false
	}
	g.mu.RLock()
	defer g.mu.RUnlock()
	for _, n := range g.nodes {
		if n == nil {
			continue
		}
		if n.interruptBefore || n.interruptAfter {
			return true
		}
	}
	return false
}
