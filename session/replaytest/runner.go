//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package replaytest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

// Runner executes cases using either a named reference or oracle-free
// pairwise consensus.
type Runner struct {
	// Reference names the reference backend. An empty value selects the first
	// backend in reference mode; consensus mode requires Reference to be empty.
	Reference string
	// Mode selects reference comparison by default or pairwise consensus.
	Mode ComparisonMode
	// Now supplies the report timestamp and defaults to time.Now.
	Now func() time.Time
}

// Run executes the complete matrix and returns a validated report. It stops
// without a partial report when ctx is canceled or comparison cannot continue;
// individual backend execution failures are recorded as blocking differences.
func (r Runner) Run(
	ctx context.Context,
	cases []Case,
	backends []Backend,
) (Report, error) {
	if ctx == nil {
		return Report{}, errors.New("replaytest: context is required")
	}
	if len(cases) == 0 {
		return Report{}, errors.New("replaytest: no cases")
	}
	if err := validateBackends(backends); err != nil {
		return Report{}, err
	}
	mode, reference, err := r.resolveComparison(backends)
	if err != nil {
		return Report{}, err
	}
	if err := validateCases(cases); err != nil {
		return Report{}, err
	}
	if err := ctx.Err(); err != nil {
		return Report{}, err
	}
	report := newReport(r, cases, backends, mode, reference)
	for _, replayCase := range cases {
		result, err := runCase(ctx, replayCase, backends, mode, reference)
		if err != nil {
			return Report{}, err
		}
		addCaseResult(&report, result)
	}
	if err := report.Validate(); err != nil {
		return Report{}, err
	}
	return report, nil
}

func (r Runner) resolveComparison(backends []Backend) (ComparisonMode, string, error) {
	mode := r.Mode
	if mode == "" {
		mode = ComparisonReference
	}
	if mode != ComparisonReference && mode != ComparisonConsensus {
		return "", "", fmt.Errorf("replaytest: unknown comparison mode %q", mode)
	}
	reference := r.Reference
	if mode == ComparisonConsensus {
		if reference != "" {
			return "", "", errors.New("replaytest: consensus mode does not use a reference backend")
		}
		return mode, "", nil
	}
	if reference == "" {
		reference = backends[0].Name
	}
	if !hasBackend(backends, reference) {
		return "", "", fmt.Errorf("replaytest: reference backend %q not found", reference)
	}
	return mode, reference, nil
}

func validateCases(cases []Case) error {
	caseNames := make(map[string]struct{}, len(cases))
	for _, replayCase := range cases {
		if err := validateCase(replayCase); err != nil {
			return err
		}
		if _, exists := caseNames[replayCase.Name]; exists {
			return fmt.Errorf("replaytest: duplicate case %q", replayCase.Name)
		}
		caseNames[replayCase.Name] = struct{}{}
		if err := validateAllowedDiffs(replayCase.AllowedDiffs); err != nil {
			return fmt.Errorf("replaytest: case %q: %w", replayCase.Name, err)
		}
	}
	return nil
}

func newReport(
	r Runner,
	cases []Case,
	backends []Backend,
	mode ComparisonMode,
	reference string,
) Report {
	now := time.Now
	if r.Now != nil {
		now = r.Now
	}
	report := Report{
		GeneratedAt:    now().UTC(),
		ComparisonMode: mode,
		Reference:      reference,
		Backends:       make([]string, 0, len(backends)),
		TotalCases:     len(cases),
		Cases:          make([]CaseResult, 0, len(cases)),
	}
	for _, backend := range backends {
		report.Backends = append(report.Backends, backend.Name)
	}
	return report
}

type replayOutcome struct {
	snapshots   map[string]Snapshot
	unsupported map[string][]Capability
	diffs       []Diff
}

func runCase(
	ctx context.Context,
	replayCase Case,
	backends []Backend,
	mode ComparisonMode,
	reference string,
) (CaseResult, error) {
	started := time.Now()
	outcome, err := replayOnBackends(ctx, replayCase, backends, mode, reference)
	if err != nil {
		return CaseResult{}, err
	}
	diffs, consensus, err := compareSnapshots(replayCase, backends, mode, reference, outcome)
	if err != nil {
		return CaseResult{}, err
	}
	diffs = append(outcome.diffs, diffs...)
	diffs = append(diffs, capabilityDiffs(replayCase.Name, backends, mode, reference, outcome.unsupported)...)
	result := CaseResult{
		Name:      replayCase.Name,
		Duration:  time.Since(started).Milliseconds(),
		Diffs:     diffs,
		Consensus: consensus,
	}
	blocking, _ := countDiffs(result.Diffs)
	result.Status = expectedCaseStatus(blocking, len(outcome.unsupported) > 0)
	return result, nil
}

func replayOnBackends(
	ctx context.Context,
	replayCase Case,
	backends []Backend,
	mode ComparisonMode,
	reference string,
) (replayOutcome, error) {
	outcome := replayOutcome{
		snapshots:   make(map[string]Snapshot, len(backends)),
		unsupported: make(map[string][]Capability),
	}
	for _, backend := range backends {
		if err := ctx.Err(); err != nil {
			return outcome, err
		}
		missing := missingCapabilities(replayCase.Requires, backend.Capabilities)
		if len(missing) > 0 {
			outcome.unsupported[backend.Name] = missing
			continue
		}
		snapshot, err := Replay(ctx, replayCase, backend)
		if err != nil {
			if contextErr := ctx.Err(); contextErr != nil {
				return outcome, contextErr
			}
			outcome.diffs = append(outcome.diffs, executionFailureDiff(replayCase.Name, backend.Name, mode, reference, err))
			continue
		}
		outcome.snapshots[backend.Name] = snapshot
	}
	return outcome, nil
}

func executionFailureDiff(
	caseName string,
	backendName string,
	mode ComparisonMode,
	reference string,
	err error,
) Diff {
	backendA := reference
	if mode == ComparisonConsensus {
		backendA = backendName
	}
	return Diff{
		Case:        caseName,
		BackendA:    backendA,
		BackendB:    backendName,
		SessionID:   caseName,
		Path:        "/execution",
		Baseline:    "success",
		Actual:      err.Error(),
		Explanation: "backend replay failed",
	}
}

func compareSnapshots(
	replayCase Case,
	backends []Backend,
	mode ComparisonMode,
	reference string,
	outcome replayOutcome,
) ([]Diff, *ConsensusResult, error) {
	if mode == ComparisonConsensus {
		diffs, consensus, err := compareByConsensus(replayCase.Name, outcome.snapshots, replayCase.AllowedDiffs)
		return diffs, &consensus, err
	}
	diffs, err := compareReferenceSnapshots(replayCase, backends, reference, outcome)
	return diffs, nil, err
}

func compareReferenceSnapshots(
	replayCase Case,
	backends []Backend,
	reference string,
	outcome replayOutcome,
) ([]Diff, error) {
	baseline, baselineOK := outcome.snapshots[reference]
	if !baselineOK {
		_, referenceUnsupported := outcome.unsupported[reference]
		if referenceUnsupported || hasSelfExecutionDiff(outcome.diffs, reference) {
			return nil, nil
		}
		return nil, fmt.Errorf(
			"replaytest: reference backend %q produced neither a snapshot nor exclusion evidence",
			reference,
		)
	}
	var diffs []Diff
	for _, backend := range backends {
		if backend.Name == reference {
			continue
		}
		actual, ok := outcome.snapshots[backend.Name]
		if !ok {
			continue
		}
		pairDiffs, err := Compare(replayCase.Name, baseline, actual, replayCase.AllowedDiffs)
		if err != nil {
			return nil, err
		}
		diffs = append(diffs, pairDiffs...)
	}
	return diffs, nil
}
func capabilityDiffs(
	caseName string,
	backends []Backend,
	mode ComparisonMode,
	reference string,
	unsupported map[string][]Capability,
) []Diff {
	var diffs []Diff
	for _, backend := range backends {
		missing, ok := unsupported[backend.Name]
		if !ok {
			continue
		}
		backendA := reference
		if mode == ComparisonConsensus {
			backendA = backend.Name
		}
		for _, capability := range missing {
			diffs = append(diffs, Diff{
				Case:        caseName,
				BackendA:    backendA,
				BackendB:    backend.Name,
				SessionID:   caseName,
				Path:        "/capabilities/" + string(capability),
				Baseline:    true,
				Actual:      false,
				Allowed:     true,
				Explanation: "backend reports this capability as unsupported",
			})
		}
	}
	return diffs
}

func addCaseResult(report *Report, result CaseResult) {
	blocking, allowed := countDiffs(result.Diffs)
	report.BlockingDiffs += blocking
	report.AllowedDiffs += allowed
	switch result.Status {
	case StatusPassed:
		report.PassedCases++
	case StatusFailed:
		report.FailedCases++
	case StatusUnsupported:
		report.UnsupportedCases++
	}
	report.Cases = append(report.Cases, result)
}

// Replay executes one case on one isolated backend and captures only the
// snapshot domains selected by Case.Requires. It always closes non-nil services,
// including partial services returned with an Open error, and propagates context
// cancellation instead of recording it as backend behavior.
func Replay(ctx context.Context, replayCase Case, backend Backend) (snapshot Snapshot, err error) {
	if ctx == nil {
		return Snapshot{}, errors.New("replaytest: context is required")
	}
	if err := validateCase(replayCase); err != nil {
		return Snapshot{}, err
	}
	if backend.Open == nil {
		return Snapshot{}, fmt.Errorf("replaytest: backend %q has no factory", backend.Name)
	}
	if err := ctx.Err(); err != nil {
		return Snapshot{}, err
	}
	services, openErr := backend.Open(ctx, replayCase.Name)
	if openErr != nil {
		wrapped := fmt.Errorf("open backend %s: %w", backend.Name, openErr)
		if services != nil {
			if closeErr := services.Close(); closeErr != nil {
				wrapped = errors.Join(wrapped, fmt.Errorf("close backend %s after open failure: %w", backend.Name, closeErr))
			}
		}
		return Snapshot{}, wrapped
	}
	if services == nil {
		return Snapshot{}, fmt.Errorf("open backend %s: incomplete services", backend.Name)
	}
	defer func() {
		closeErr := services.Close()
		if closeErr == nil {
			return
		}
		wrapped := fmt.Errorf("close backend %s: %w", backend.Name, closeErr)
		if err == nil {
			err = wrapped
		} else {
			err = errors.Join(err, wrapped)
		}
	}()
	if services.Session == nil {
		return Snapshot{}, fmt.Errorf("open backend %s: incomplete services", backend.Name)
	}
	required := capabilitySet(replayCase.Requires)
	if required[CapabilityMemory] && services.Memory == nil {
		return Snapshot{}, fmt.Errorf("open backend %s: memory capability has no service", backend.Name)
	}

	key := session.Key{AppName: "replaytest", UserID: "user-1", SessionID: replayCase.Name}
	sess, err := services.Session.CreateSession(ctx, key, cloneState(replayCase.InitialState))
	if err != nil {
		return Snapshot{}, fmt.Errorf("create session: %w", err)
	}
	if sess == nil {
		return Snapshot{}, errors.New("create session: backend returned nil session")
	}
	exec := execution{services: services, key: key, session: sess, required: required}
	for _, step := range replayCase.Steps {
		if err := exec.runStep(ctx, step); err != nil {
			return Snapshot{}, fmt.Errorf("step %q (%s): %w", step.Name, step.Kind, err)
		}
	}
	if required[CapabilitySummary] {
		if err := exec.verifySummaryIsolation(ctx); err != nil {
			return Snapshot{}, fmt.Errorf("verify summary isolation: %w", err)
		}
	}
	return exec.snapshot(ctx, backend.Name, replayCase.Name, replayCase.EventOrder)
}

type execution struct {
	services *Services
	key      session.Key
	session  *session.Session
	required Capabilities
}

func (e *execution) runStep(ctx context.Context, step Step) error {
	switch step.Kind {
	case StepAppendEvent:
		return e.appendEvent(ctx, step.Event)
	case StepUpdateState:
		return e.updateState(ctx, step.State)
	case StepAddMemory:
		return e.addMemory(ctx, step.Memory)
	case StepCreateSummary:
		return e.createSummary(ctx, step.Summary)
	case StepAppendTrack:
		return e.appendTrack(ctx, step.Track)
	case StepReloadSession:
		return e.reload(ctx)
	case StepConcurrent:
		return e.runConcurrent(ctx, step.Concurrent)
	default:
		return fmt.Errorf("unknown step kind %q", step.Kind)
	}
}

func (e *execution) appendEvent(ctx context.Context, input *EventInput) error {
	evt, err := e.prepareEvent(input)
	if err != nil {
		return err
	}
	return e.services.Session.AppendEvent(ctx, e.session, evt)
}

func (e *execution) prepareEvent(input *EventInput) (*event.Event, error) {
	if input == nil || input.Event == nil || input.LogicalID == "" {
		return nil, errors.New("invalid event input")
	}
	evt := input.Event.Clone()
	evt.Timestamp = e.session.CreatedAt.Add(input.Offset)
	if evt.Response != nil {
		evt.Response.Timestamp = evt.Timestamp
	}
	if err := event.SetExtension(evt, logicalEventIDExtension, input.LogicalID); err != nil {
		return nil, fmt.Errorf("set logical event id: %w", err)
	}
	return evt, nil
}

func (e *execution) updateState(ctx context.Context, input *StateInput) error {
	if input == nil {
		return errors.New("state input is nil")
	}
	switch input.Scope {
	case StateScopeApp:
		if len(input.Values) > 0 {
			if err := e.services.Session.UpdateAppState(ctx, e.key.AppName, cloneState(input.Values)); err != nil {
				return err
			}
		}
		for _, key := range input.DeleteKeys {
			if err := e.services.Session.DeleteAppState(ctx, e.key.AppName, key); err != nil {
				return err
			}
		}
	case StateScopeUser:
		userKey := session.UserKey{AppName: e.key.AppName, UserID: e.key.UserID}
		if len(input.Values) > 0 {
			if err := e.services.Session.UpdateUserState(ctx, userKey, cloneState(input.Values)); err != nil {
				return err
			}
		}
		for _, key := range input.DeleteKeys {
			if err := e.services.Session.DeleteUserState(ctx, userKey, key); err != nil {
				return err
			}
		}
	case StateScopeSession:
		if len(input.DeleteKeys) > 0 {
			return errors.New("session state deletion is not exposed by session.Service")
		}
		if len(input.Values) > 0 {
			return e.services.Session.UpdateSessionState(ctx, e.key, cloneState(input.Values))
		}
	default:
		return fmt.Errorf("unknown state scope %q", input.Scope)
	}
	return nil
}

func (e *execution) addMemory(ctx context.Context, input *MemoryInput) error {
	if input == nil || input.Memory == "" {
		return errors.New("invalid memory input")
	}
	userKey := memory.UserKey{AppName: e.key.AppName, UserID: e.key.UserID}
	var opts []memory.AddOption
	if input.Metadata != nil {
		metadata := *input.Metadata
		metadata.Participants = append([]string(nil), input.Metadata.Participants...)
		opts = append(opts, memory.WithMetadata(&metadata))
	}
	return e.services.Memory.AddMemory(
		ctx,
		userKey,
		input.Memory,
		append([]string(nil), input.Topics...),
		opts...,
	)
}

func (e *execution) createSummary(ctx context.Context, input *SummaryInput) error {
	if input == nil {
		return errors.New("summary input is nil")
	}
	if err := e.services.Session.CreateSessionSummary(ctx, e.session, input.FilterKey, input.Force); err != nil {
		return err
	}
	return e.reload(ctx)
}

func (e *execution) appendTrack(ctx context.Context, input *TrackInput) error {
	if input == nil || input.Event == nil {
		return errors.New("track input is nil")
	}
	trackService, ok := e.services.Session.(session.TrackService)
	if !ok {
		return errors.New("track capability advertised but service does not implement session.TrackService")
	}
	copyEvent := *input.Event
	copyEvent.Payload = append([]byte(nil), input.Event.Payload...)
	copyEvent.Timestamp = e.session.CreatedAt.Add(input.Offset)
	return trackService.AppendTrackEvent(ctx, e.session, &copyEvent)
}

func (e *execution) reload(ctx context.Context) error {
	sess, err := e.services.Session.GetSession(ctx, e.key)
	if err != nil {
		return err
	}
	if sess == nil {
		return errors.New("get session: backend returned nil session")
	}
	e.session = sess
	return nil
}

func (e *execution) verifySummaryIsolation(ctx context.Context) error {
	probeKey := e.key
	probeKey.SessionID += "-summary-isolation"
	probe, err := e.services.Session.CreateSession(ctx, probeKey, nil)
	if err != nil {
		return fmt.Errorf("create probe session: %w", err)
	}
	if probe == nil {
		return errors.New("create probe session: backend returned nil session")
	}
	probe, err = e.services.Session.GetSession(ctx, probeKey)
	if err != nil {
		return fmt.Errorf("get probe session: %w", err)
	}
	if probe == nil {
		return errors.New("get probe session: backend returned nil session")
	}
	probe.SummariesMu.RLock()
	summaryCount := len(probe.Summaries)
	probe.SummariesMu.RUnlock()
	if summaryCount != 0 {
		return fmt.Errorf("fresh probe session contains %d summaries", summaryCount)
	}
	if err := e.services.Session.DeleteSession(ctx, probeKey); err != nil {
		return fmt.Errorf("delete probe session: %w", err)
	}
	return nil
}

func (e *execution) runConcurrent(ctx context.Context, branches [][]Step) error {
	if len(branches) == 0 {
		return errors.New("concurrent step has no branches")
	}
	start := make(chan struct{})
	errs := make([]error, len(branches))
	var wg sync.WaitGroup
	for i, branch := range branches {
		i, branch := i, append([]Step(nil), branch...)
		branchExecution := &execution{
			services: e.services,
			key:      e.key,
			session:  e.session.Clone(),
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case <-ctx.Done():
				errs[i] = ctx.Err()
				return
			case <-start:
			}
			for _, nested := range branch {
				if err := branchExecution.runStep(ctx, nested); err != nil {
					errs[i] = fmt.Errorf("nested step %q: %w", nested.Name, err)
					return
				}
			}
		}()
	}
	close(start)
	wg.Wait()
	if err := errors.Join(errs...); err != nil {
		return err
	}
	return e.reload(ctx)
}

func (e *execution) snapshot(
	ctx context.Context,
	backendName string,
	caseName string,
	eventOrder EventOrderMode,
) (Snapshot, error) {
	sess, err := e.services.Session.GetSession(ctx, e.key)
	if err != nil {
		return Snapshot{}, fmt.Errorf("get session: %w", err)
	}
	if sess == nil {
		return Snapshot{}, errors.New("get session: backend returned nil session")
	}
	var appState session.StateMap
	if e.required[CapabilityAppState] {
		appState, err = e.services.Session.ListAppStates(ctx, e.key.AppName)
		if err != nil {
			return Snapshot{}, fmt.Errorf("list app state: %w", err)
		}
	}
	userKey := session.UserKey{AppName: e.key.AppName, UserID: e.key.UserID}
	var userState session.StateMap
	if e.required[CapabilityUserState] {
		userState, err = e.services.Session.ListUserStates(ctx, userKey)
		if err != nil {
			return Snapshot{}, fmt.Errorf("list user state: %w", err)
		}
	}
	var memories []*memory.Entry
	if e.required[CapabilityMemory] {
		memories, err = e.services.Memory.ReadMemories(ctx, memory.UserKey{
			AppName: e.key.AppName,
			UserID:  e.key.UserID,
		}, 0)
		if err != nil {
			return Snapshot{}, fmt.Errorf("read memories: %w", err)
		}
	}
	return normalizeSnapshot(
		backendName,
		caseName,
		eventOrder,
		e.required,
		sess,
		appState,
		userState,
		memories,
	)
}

func cloneState(input session.StateMap) session.StateMap {
	if input == nil {
		return nil
	}
	out := make(session.StateMap, len(input))
	for key, value := range input {
		out[key] = append([]byte(nil), value...)
	}
	return out
}

func validateBackends(backends []Backend) error {
	if len(backends) < 2 {
		return errors.New("replaytest: at least two backends are required")
	}
	seen := make(map[string]struct{}, len(backends))
	for _, backend := range backends {
		if backend.Name == "" || backend.Open == nil {
			return errors.New("replaytest: backend name and factory are required")
		}
		if _, ok := seen[backend.Name]; ok {
			return fmt.Errorf("replaytest: duplicate backend %q", backend.Name)
		}
		seen[backend.Name] = struct{}{}
		for capability := range backend.Capabilities {
			if !isKnownCapability(capability) {
				return fmt.Errorf(
					"replaytest: backend %q declares unknown capability %q",
					backend.Name,
					capability,
				)
			}
		}
	}
	return nil
}

func validateCase(replayCase Case) error {
	if replayCase.Name == "" {
		return errors.New("replaytest: case name is required")
	}
	if len(replayCase.Steps) == 0 {
		return fmt.Errorf("replaytest: case %q has no steps", replayCase.Name)
	}
	switch replayCase.EventOrder {
	case "", EventOrderGlobal, EventOrderCausal:
	default:
		return fmt.Errorf("replaytest: case %q has unknown event order %q", replayCase.Name, replayCase.EventOrder)
	}
	for _, step := range replayCase.Steps {
		if err := validateStep(step); err != nil {
			return fmt.Errorf("replaytest: case %q: %w", replayCase.Name, err)
		}
	}
	if err := validateLogicalEventIDs(replayCase.Steps, make(map[string]string)); err != nil {
		return fmt.Errorf("replaytest: case %q: %w", replayCase.Name, err)
	}
	return validateCaseCapabilities(replayCase)
}

func validateLogicalEventIDs(steps []Step, owners map[string]string) error {
	for _, step := range steps {
		if step.Kind == StepAppendEvent {
			logicalID := step.Event.LogicalID
			if owner, exists := owners[logicalID]; exists {
				return fmt.Errorf(
					"logical event id %q is reused by steps %q and %q",
					logicalID,
					owner,
					step.Name,
				)
			}
			owners[logicalID] = step.Name
		}
		if step.Kind != StepConcurrent {
			continue
		}
		for _, branch := range step.Concurrent {
			if err := validateLogicalEventIDs(branch, owners); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateCaseCapabilities(replayCase Case) error {
	declared := make(Capabilities, len(replayCase.Requires))
	for _, capability := range replayCase.Requires {
		if !isKnownCapability(capability) {
			return fmt.Errorf("replaytest: case %q requires unknown capability %q", replayCase.Name, capability)
		}
		if declared[capability] {
			return fmt.Errorf("replaytest: case %q repeats capability %q", replayCase.Name, capability)
		}
		declared[capability] = true
	}
	used := Capabilities{CapabilitySession: true}
	if len(replayCase.InitialState) > 0 {
		used[CapabilitySessionState] = true
	}
	for _, step := range replayCase.Steps {
		collectStepCapabilities(step, used)
	}
	usedCapabilities := make([]Capability, 0, len(used))
	for capability := range used {
		usedCapabilities = append(usedCapabilities, capability)
	}
	sort.Slice(usedCapabilities, func(i, j int) bool { return usedCapabilities[i] < usedCapabilities[j] })
	for _, capability := range usedCapabilities {
		if !declared[capability] {
			return fmt.Errorf("replaytest: case %q uses undeclared capability %q", replayCase.Name, capability)
		}
	}
	return nil
}

func collectStepCapabilities(step Step, capabilities Capabilities) {
	switch step.Kind {
	case StepUpdateState:
		switch step.State.Scope {
		case StateScopeApp:
			capabilities[CapabilityAppState] = true
		case StateScopeUser:
			capabilities[CapabilityUserState] = true
		case StateScopeSession:
			capabilities[CapabilitySessionState] = true
		}
	case StepAddMemory:
		capabilities[CapabilityMemory] = true
	case StepCreateSummary:
		capabilities[CapabilitySummary] = true
	case StepAppendTrack:
		capabilities[CapabilityTrack] = true
	case StepConcurrent:
		capabilities[CapabilityConcurrent] = true
		for _, branch := range step.Concurrent {
			for _, nested := range branch {
				collectStepCapabilities(nested, capabilities)
			}
		}
	}
}

func validateStep(step Step) error {
	if step.Name == "" {
		return errors.New("unnamed step")
	}
	payloads := stepPayloadCount(step)
	wantPayloads := 1
	if step.Kind == StepReloadSession {
		wantPayloads = 0
	}
	if payloads != wantPayloads {
		return fmt.Errorf("step %q has %d payloads, want %d", step.Name, payloads, wantPayloads)
	}
	return validateStepKind(step)
}

func stepPayloadCount(step Step) int {
	count := 0
	for _, populated := range []bool{
		step.Event != nil,
		step.State != nil,
		step.Memory != nil,
		step.Summary != nil,
		step.Track != nil,
		len(step.Concurrent) > 0,
	} {
		if populated {
			count++
		}
	}
	return count
}

func validateStepKind(step Step) error {
	switch step.Kind {
	case StepAppendEvent:
		return validateEventStep(step)
	case StepUpdateState:
		return validateStateStep(step)
	case StepAddMemory:
		if step.Memory.Memory == "" {
			return fmt.Errorf("step %q has invalid memory input", step.Name)
		}
	case StepCreateSummary:
	case StepAppendTrack:
		if step.Track.Event == nil || step.Track.Event.Track == "" {
			return fmt.Errorf("step %q has invalid track input", step.Name)
		}
	case StepReloadSession:
		return nil
	case StepConcurrent:
		return validateConcurrentStep(step)
	default:
		return fmt.Errorf("step %q has unknown kind %q", step.Name, step.Kind)
	}
	return nil
}

func validateEventStep(step Step) error {
	if step.Event.Event == nil || step.Event.LogicalID == "" {
		return fmt.Errorf("step %q has invalid event input", step.Name)
	}
	return nil
}

func validateStateStep(step Step) error {
	switch step.State.Scope {
	case StateScopeApp, StateScopeUser:
		return nil
	case StateScopeSession:
		if len(step.State.DeleteKeys) > 0 {
			return fmt.Errorf("step %q cannot delete session state", step.Name)
		}
		return nil
	default:
		return fmt.Errorf("step %q has unknown state scope %q", step.Name, step.State.Scope)
	}
}

func validateConcurrentStep(step Step) error {
	for _, branch := range step.Concurrent {
		if len(branch) == 0 {
			return fmt.Errorf("step %q has an empty concurrent branch", step.Name)
		}
		for _, nested := range branch {
			if err := validateStep(nested); err != nil {
				return fmt.Errorf("step %q: %w", step.Name, err)
			}
		}
	}
	return nil
}

func hasBackend(backends []Backend, name string) bool {
	for _, backend := range backends {
		if backend.Name == name {
			return true
		}
	}
	return false
}

func missingCapabilities(required []Capability, actual Capabilities) []Capability {
	var missing []Capability
	for _, capability := range required {
		if !actual[capability] {
			missing = append(missing, capability)
		}
	}
	sort.Slice(missing, func(i, j int) bool { return missing[i] < missing[j] })
	return missing
}

func capabilitySet(capabilities []Capability) Capabilities {
	set := make(Capabilities, len(capabilities))
	for _, capability := range capabilities {
		set[capability] = true
	}
	return set
}

func countDiffs(diffs []Diff) (blocking, allowed int) {
	for _, diff := range diffs {
		if diff.Allowed {
			allowed++
		} else {
			blocking++
		}
	}
	return blocking, allowed
}

func hasSelfExecutionDiff(diffs []Diff, backend string) bool {
	for _, diff := range diffs {
		if diff.BackendA == backend && diff.BackendB == backend && diff.Path == "/execution" {
			return true
		}
	}
	return false
}

func cloneJSONMap(input CanonicalMap) CanonicalMap {
	raw, _ := json.Marshal(input)
	var output CanonicalMap
	_ = decodeJSON(raw, &output)
	return output
}
