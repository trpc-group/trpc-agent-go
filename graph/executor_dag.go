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
	"sort"

	"trpc.group/trpc-go/trpc-agent-go/agent"
)

type dagTaskResult struct {
	task *Task
	step int
	err  error
}

type dagLoop struct {
	executor   *Executor
	ctx        context.Context
	invocation *agent.Invocation
	execCtx    *ExecutionContext
	ckptCfg    *map[string]any
	extIntr    *externalInterruptWatcher
	report     *stepExecutionReport

	executed int
	nextStep int

	ready    []*Task
	inFlight map[string]*Task
	waiting  map[string][]*Task
	sem      chan struct{}
	done     chan dagTaskResult

	draining     bool
	drainErr     error
	drainMaxStep bool

	pendingIntr     *InterruptError
	pendingIntrStep int
	pendingExtra    map[string]any

	pauseRequested bool
	rerunNodes     []string
}

func newDagLoop(
	executor *Executor,
	ctx context.Context,
	invocation *agent.Invocation,
	execCtx *ExecutionContext,
	ckptCfg *map[string]any,
	startStep int,
	extIntr *externalInterruptWatcher,
) (*dagLoop, error) {
	if ctx == nil {
		return nil, errors.New("context is nil")
	}
	if execCtx == nil {
		return nil, errors.New("execution context is nil")
	}
	if execCtx.channels == nil {
		return nil, errors.New("execution channels are nil")
	}
	if executor == nil || executor.graph == nil {
		return nil, errors.New("graph is nil")
	}

	loop := &dagLoop{
		executor:   executor,
		ctx:        ctx,
		invocation: invocation,
		execCtx:    execCtx,
		ckptCfg:    ckptCfg,
		extIntr:    extIntr,
		report:     executor.newStepExecutionReportIfNeeded(extIntr),
		inFlight:   make(map[string]*Task),
		waiting:    make(map[string][]*Task),
		sem:        make(chan struct{}, executor.maxConcurrency),
		done:       make(chan dagTaskResult, executor.maxConcurrency),
		nextStep:   startStep,
	}
	if err := loop.seed(); err != nil {
		return nil, err
	}
	return loop, nil
}

func (e *Executor) runDagLoop(
	ctx context.Context,
	invocation *agent.Invocation,
	execCtx *ExecutionContext,
	checkpointConfig *map[string]any,
	startStep int,
	extInterrupt *externalInterruptWatcher,
) (int, error) {
	loop, err := newDagLoop(
		e,
		ctx,
		invocation,
		execCtx,
		checkpointConfig,
		startStep,
		extInterrupt,
	)
	if err != nil {
		return 0, err
	}
	return loop.run()
}

func (l *dagLoop) run() (int, error) {
	for {
		l.maybeRequestExternalInterrupt()
		l.startReadyTasks()
		l.planIfIdle()

		if done, err := l.tryFinalize(); done {
			return l.executed, err
		}

		l.waitForEvent()
	}
}

func (l *dagLoop) seed() error {
	if l.executor == nil || l.executor.graph == nil {
		return errors.New("graph is nil")
	}
	entryPoint := l.executor.graph.EntryPoint()
	if entryPoint == "" {
		return errors.New("no entry point defined")
	}

	nextNodes := l.consumeNextNodesFromState()
	if len(nextNodes) > 0 {
		for _, nodeID := range nextNodes {
			l.queue(l.executor.createDagTask(l.execCtx, nodeID))
		}
		return nil
	}
	if l.execCtx != nil && l.execCtx.resumed {
		return nil
	}
	l.queue(l.executor.createDagTask(l.execCtx, entryPoint))
	return nil
}

func (l *dagLoop) consumeNextNodesFromState() []string {
	if l.execCtx == nil || l.execCtx.State == nil {
		return nil
	}

	l.execCtx.stateMutex.Lock()
	defer l.execCtx.stateMutex.Unlock()

	nextNodesValue, ok := l.execCtx.State[StateKeyNextNodes]
	if !ok {
		return nil
	}
	nextNodes, ok := nextNodesValue.([]string)
	if !ok || len(nextNodes) == 0 {
		return nil
	}
	delete(l.execCtx.State, StateKeyNextNodes)
	return append([]string(nil), nextNodes...)
}

func (l *dagLoop) tryFinalize() (bool, error) {
	if l.pauseRequested && len(l.inFlight) == 0 {
		l.startRequestedExternalInterrupt()
	}

	if l.pendingIntr != nil && len(l.inFlight) == 0 {
		return true, l.finalizeInterrupt()
	}

	if l.draining && len(l.inFlight) == 0 {
		if l.drainMaxStep {
			return true, nil
		}
		return true, l.drainErr
	}

	if l.draining || l.pauseRequested {
		return false, nil
	}
	if len(l.inFlight) != 0 || len(l.ready) != 0 {
		return false, nil
	}

	l.maybeCreateFinalCheckpoint()
	return true, nil
}

func (l *dagLoop) queue(t *Task) {
	if t == nil {
		return
	}
	l.ready = append(l.ready, t)
}

func (l *dagLoop) startReadyTasks() {
	for !l.draining && !l.pauseRequested && len(l.ready) > 0 {
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
	if _, ok := l.inFlight[t.NodeID]; ok {
		l.waiting[t.NodeID] = append(l.waiting[t.NodeID], t)
		return true
	}
	if l.maybeStartStaticInterruptBefore(t) {
		return false
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

	l.inFlight[t.NodeID] = t
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
			l.report,
		)
		if err == nil && l.report != nil {
			l.report.markCompleted(t)
		}
		l.releaseWorker()
		l.done <- dagTaskResult{task: t, step: step, err: err}
	}(t, step)
}

func (l *dagLoop) planIfIdle() {
	if l.draining || l.pauseRequested || len(l.ready) > 0 {
		return
	}
	l.queuePlannedTasks()
}

func (l *dagLoop) queuePlannedTasks() {
	for _, t := range l.executor.planDagTasks(l.execCtx) {
		l.queue(t)
	}
}

func (l *dagLoop) waitForEvent() {
	select {
	case res := <-l.done:
		l.handleTaskResult(res)
		return
	default:
	}

	select {
	case res := <-l.done:
		l.handleTaskResult(res)
	case <-l.ctx.Done():
		l.handleContextDone()
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
		l.handleTaskError(res)
		return
	}
	l.executed++
	if l.maybeStartStaticInterruptAfter(res.task, res.step) {
		return
	}
	if !l.draining && !l.pauseRequested {
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

func (l *dagLoop) maybeRequestExternalInterrupt() {
	if l.draining || l.pauseRequested || l.extIntr == nil {
		return
	}
	if l.extIntr.requested() {
		l.pauseRequested = true
	}
}

func (l *dagLoop) startRequestedExternalInterrupt() {
	if !l.pauseRequested || l.draining || l.pendingIntr != nil {
		return
	}
	intr := newExternalInterruptError(false)
	l.startDrainingInterrupt(intr, l.lastStep(), nil)
}

func (l *dagLoop) lastStep() int {
	if l.nextStep <= 0 {
		return -1
	}
	return l.nextStep - 1
}

func (l *dagLoop) handleContextDone() {
	if l.draining {
		return
	}
	if l.extIntr != nil && l.extIntr.forced(l.ctx) {
		intr := newExternalInterruptError(true)
		l.startDrainingInterrupt(
			intr,
			l.lastStep(),
			forcedExternalInterruptExtra(),
		)
		return
	}
	l.startDraining(l.ctx.Err())
}

func (l *dagLoop) handleTaskError(res dagTaskResult) {
	if l.draining {
		l.recordRerunNode(res)
		return
	}

	if l.extIntr != nil && l.extIntr.forced(l.ctx) {
		intr := newExternalInterruptError(true)
		l.startDrainingInterrupt(
			intr,
			l.lastStep(),
			forcedExternalInterruptExtra(),
		)
		l.recordRerunNode(res)
		return
	}

	if interrupt, ok := GetInterruptError(res.err); ok {
		l.startDrainingInterrupt(interrupt, res.step, nil)
		return
	}

	l.startDraining(res.err)
}

func (l *dagLoop) recordRerunNode(res dagTaskResult) {
	if l.pendingIntr == nil || res.task == nil {
		return
	}
	nodeID := res.task.NodeID
	if nodeID == "" || nodeID == End {
		return
	}
	l.rerunNodes = append(l.rerunNodes, nodeID)
	l.recordGraphInterruptInput(res.task)
}

func (l *dagLoop) startDrainingInterrupt(
	interrupt *InterruptError,
	step int,
	metaExtra map[string]any,
) {
	if interrupt == nil {
		l.startDraining(errors.New("interrupt is nil"))
		return
	}
	if l.pendingIntr == nil {
		l.pendingIntr = interrupt
		l.pendingIntrStep = step
		l.pendingExtra = metaExtra
	}
	l.startDraining(interrupt)
}

func forcedExternalInterruptExtra() map[string]any {
	return map[string]any{
		CheckpointMetaKeyGraphInterruptInputs: make(map[string][]any),
	}
}

func (l *dagLoop) recordGraphInterruptInput(t *Task) {
	if l.pendingIntr == nil {
		return
	}
	if !isForcedExternalInterrupt(l.pendingIntr) {
		return
	}
	if t == nil || t.NodeID == "" {
		return
	}
	if l.pendingExtra == nil {
		return
	}
	inputsValue := l.pendingExtra[CheckpointMetaKeyGraphInterruptInputs]
	inputs, ok := inputsValue.(map[string][]any)
	if !ok || inputs == nil {
		return
	}

	state := l.stateInputForTask(t)
	if state == nil {
		return
	}
	inputs[t.NodeID] = append(inputs[t.NodeID], state)
}

func (l *dagLoop) stateInputForTask(t *Task) State {
	if t == nil {
		return nil
	}
	if l.report != nil {
		if input, ok := l.report.inputFor(t); ok && input != nil {
			return input
		}
	}
	fallback, ok := stateFromAny(t.Input)
	if !ok || fallback == nil {
		return nil
	}
	fields := map[string]StateField(nil)
	if l.executor != nil {
		fields = l.executor.stateFields()
	}
	return fallback.deepCopy(false, fields)
}

func isForcedExternalInterrupt(intr *InterruptError) bool {
	if intr == nil || intr.Key != ExternalInterruptKey {
		return false
	}
	payload, ok := intr.Value.(ExternalInterruptPayload)
	return ok && payload.Forced
}

func (l *dagLoop) finalizeInterrupt() error {
	if l.pendingIntr == nil {
		return errors.New("pending interrupt is nil")
	}
	l.pendingIntr.NextNodes = l.snapshotNextNodes()

	config := map[string]any(nil)
	if l.ckptCfg != nil {
		config = *l.ckptCfg
	}
	return l.executor.handleInterrupt(
		l.ctx,
		l.invocation,
		l.execCtx,
		l.pendingIntr,
		l.pendingIntrStep,
		config,
		l.pendingExtra,
	)
}

func (l *dagLoop) snapshotNextNodes() []string {
	var out []string
	seen := make(map[string]bool)

	appendNode := func(nodeID string) {
		if nodeID == "" || nodeID == End {
			return
		}
		out = append(out, nodeID)
		seen[nodeID] = true
	}

	for _, task := range l.ready {
		if task == nil {
			continue
		}
		appendNode(task.NodeID)
	}

	waitingKeys := make([]string, 0, len(l.waiting))
	for nodeID, tasks := range l.waiting {
		if nodeID == "" || len(tasks) == 0 {
			continue
		}
		waitingKeys = append(waitingKeys, nodeID)
	}
	sort.Strings(waitingKeys)
	for _, nodeID := range waitingKeys {
		for range l.waiting[nodeID] {
			appendNode(nodeID)
		}
	}

	for _, nodeID := range l.rerunNodes {
		appendNode(nodeID)
	}

	for _, nodeID := range l.executor.getNextNodes(l.execCtx) {
		if seen[nodeID] {
			continue
		}
		appendNode(nodeID)
	}

	return out
}

func (l *dagLoop) maybeStartStaticInterruptBefore(t *Task) bool {
	if l.draining || l.pendingIntr != nil || t == nil {
		return false
	}
	step := l.nextStep

	l.execCtx.stateMutex.Lock()
	interrupt := l.executor.maybeStaticInterruptBefore(
		l.execCtx,
		[]*Task{t},
		step,
	)
	l.execCtx.stateMutex.Unlock()

	if interrupt == nil {
		return false
	}
	l.startDrainingInterrupt(interrupt, step, nil)
	return true
}

func (l *dagLoop) maybeStartStaticInterruptAfter(t *Task, step int) bool {
	if l.draining || l.pendingIntr != nil || t == nil {
		return false
	}
	interrupt := l.executor.maybeStaticInterruptAfter([]*Task{t}, step)
	if interrupt == nil {
		return false
	}
	l.startDrainingInterrupt(interrupt, step, nil)
	return true
}

func (l *dagLoop) maybeCreateFinalCheckpoint() {
	if l.executor == nil || l.executor.checkpointSaver == nil {
		return
	}
	if l.ckptCfg == nil || *l.ckptCfg == nil {
		return
	}
	if l.executed == 0 {
		return
	}

	step := l.lastStep()
	_ = l.executor.createCheckpointAndSave(
		l.ctx,
		l.invocation,
		l.ckptCfg,
		CheckpointSourceLoop,
		step,
		l.execCtx,
	)
}

func (e *Executor) createDagTask(
	execCtx *ExecutionContext,
	nodeID string,
) *Task {
	if nodeID == "" || nodeID == End {
		return nil
	}
	node, exists := e.graph.Node(nodeID)
	if !exists || node == nil {
		return nil
	}
	var input any
	if execCtx != nil && execCtx.State != nil {
		execCtx.stateMutex.Lock()
		input = execCtx.State
		if override, ok := consumeGraphInterruptInput(execCtx.State, nodeID); ok {
			input = override
		}
		execCtx.stateMutex.Unlock()
	}
	return &Task{
		NodeID:   nodeID,
		Input:    input,
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
			task := e.createDagTask(execCtx, nodeID)
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
