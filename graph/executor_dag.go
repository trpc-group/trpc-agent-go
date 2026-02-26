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

type dagLoop struct {
	executor   *Executor
	ctx        context.Context
	invocation *agent.Invocation
	execCtx    *ExecutionContext

	executed int
	nextStep int

	ready    []*Task
	inFlight map[string]bool
	waiting  map[string][]*Task
	sem      chan struct{}
	done     chan dagTaskResult

	draining     bool
	drainErr     error
	drainMaxStep bool
}

func newDagLoop(
	executor *Executor,
	ctx context.Context,
	invocation *agent.Invocation,
	execCtx *ExecutionContext,
) (*dagLoop, error) {
	if execCtx == nil {
		return nil, errors.New("execution context is nil")
	}
	if execCtx.channels == nil {
		return nil, errors.New("execution channels are nil")
	}
	if executor == nil || executor.graph == nil {
		return nil, errors.New("graph is nil")
	}

	entryPoint := executor.graph.EntryPoint()
	if entryPoint == "" {
		return nil, errors.New("no entry point defined")
	}

	loop := &dagLoop{
		executor:   executor,
		ctx:        ctx,
		invocation: invocation,
		execCtx:    execCtx,
		inFlight:   make(map[string]bool),
		waiting:    make(map[string][]*Task),
		sem:        make(chan struct{}, executor.maxConcurrency),
		done:       make(chan dagTaskResult, executor.maxConcurrency),
	}
	loop.queue(executor.createDagTask(entryPoint))
	return loop, nil
}

func (e *Executor) runDagLoop(
	ctx context.Context,
	invocation *agent.Invocation,
	execCtx *ExecutionContext,
) (int, error) {
	loop, err := newDagLoop(e, ctx, invocation, execCtx)
	if err != nil {
		return 0, err
	}
	return loop.run()
}

func (l *dagLoop) run() (int, error) {
	for {
		l.startReadyTasks()
		l.planIfIdle()

		if done, err := l.checkExit(); done {
			return l.executed, err
		}

		l.waitForEvent()
	}
}

func (l *dagLoop) queue(t *Task) {
	if t == nil {
		return
	}
	l.ready = append(l.ready, t)
}

func (l *dagLoop) startReadyTasks() {
	for !l.draining && len(l.ready) > 0 {
		if !l.consumeReadyHead() {
			return
		}
	}
}

func (l *dagLoop) consumeReadyHead() bool {
	if len(l.ready) == 0 {
		return false
	}
	t := l.ready[0]
	if !l.startTask(t) {
		return false
	}
	l.ready = l.ready[1:]
	return true
}

func (l *dagLoop) startTask(t *Task) bool {
	if t == nil || t.NodeID == "" {
		return true
	}
	if l.inFlight[t.NodeID] {
		l.waiting[t.NodeID] = append(l.waiting[t.NodeID], t)
		return true
	}
	if !l.tryAcquireWorker() {
		return false
	}
	step, ok := l.takeNextStep()
	if !ok {
		l.releaseWorker()
		l.startDrainingMaxStep()
		return false
	}

	l.inFlight[t.NodeID] = true
	t.TaskID = fmt.Sprintf("%s-%d", t.NodeID, step)
	l.launchTask(t, step)
	return true
}

func (l *dagLoop) tryAcquireWorker() bool {
	select {
	case l.sem <- struct{}{}:
		return true
	default:
		return false
	}
}

func (l *dagLoop) releaseWorker() {
	<-l.sem
}

func (l *dagLoop) takeNextStep() (int, bool) {
	if l.nextStep >= l.executor.maxSteps {
		return 0, false
	}
	step := l.nextStep
	l.nextStep++
	return step, true
}

func (l *dagLoop) launchTask(t *Task, step int) {
	go func(t *Task, step int) {
		taskInvocation, taskCtx := l.executor.taskInvocationContext(
			l.ctx,
			l.invocation,
			t,
		)
		err := l.executor.executeSingleTask(
			taskCtx,
			taskInvocation,
			l.execCtx,
			t,
			step,
			nil,
		)
		l.releaseWorker()
		l.done <- dagTaskResult{task: t, err: err}
	}(t, step)
}

func (l *dagLoop) planIfIdle() {
	if l.draining || len(l.ready) > 0 {
		return
	}
	l.queuePlannedTasks()
}

func (l *dagLoop) queuePlannedTasks() {
	for _, t := range l.executor.planDagTasks(l.execCtx) {
		l.queue(t)
	}
}

func (l *dagLoop) checkExit() (bool, error) {
	if len(l.inFlight) != 0 {
		return false, nil
	}
	if l.draining {
		if l.drainMaxStep {
			return true, nil
		}
		return true, l.drainErr
	}
	if len(l.ready) == 0 {
		return true, nil
	}
	return false, nil
}

func (l *dagLoop) waitForEvent() {
	select {
	case <-l.ctx.Done():
		l.startDraining(l.ctx.Err())
	case res := <-l.done:
		l.handleTaskResult(res)
	}
}

func (l *dagLoop) handleTaskResult(res dagTaskResult) {
	nodeID := ""
	if res.task != nil {
		nodeID = res.task.NodeID
	}
	if nodeID != "" {
		delete(l.inFlight, nodeID)
		if w := l.waiting[nodeID]; len(w) > 0 {
			l.ready = append(l.ready, w...)
			delete(l.waiting, nodeID)
		}
	}

	if res.err != nil {
		l.startDraining(res.err)
		return
	}
	l.executed++
	if !l.draining {
		l.queuePlannedTasks()
	}
}

func (l *dagLoop) startDraining(err error) {
	if l.draining {
		return
	}
	l.draining = true
	l.drainErr = err
}

func (l *dagLoop) startDrainingMaxStep() {
	if l.draining {
		return
	}
	l.draining = true
	l.drainMaxStep = true
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
