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

var errInjectedPreWrite = errors.New("replaytest: injected pre-write failure")

// Runner executes cases and compares every backend with the reference.
type Runner struct {
	Reference string
	Now       func() time.Time
}

// Run executes the complete matrix and returns a report.
func (r Runner) Run(
	ctx context.Context,
	cases []Case,
	backends []Backend,
) (Report, error) {
	if len(cases) == 0 {
		return Report{}, errors.New("replaytest: no cases")
	}
	if err := validateBackends(backends); err != nil {
		return Report{}, err
	}
	reference := r.Reference
	if reference == "" {
		reference = backends[0].Name
	}
	if !hasBackend(backends, reference) {
		return Report{}, fmt.Errorf("replaytest: reference backend %q not found", reference)
	}
	caseNames := make(map[string]struct{}, len(cases))
	for _, replayCase := range cases {
		if err := validateCase(replayCase); err != nil {
			return Report{}, err
		}
		if _, exists := caseNames[replayCase.Name]; exists {
			return Report{}, fmt.Errorf("replaytest: duplicate case %q", replayCase.Name)
		}
		caseNames[replayCase.Name] = struct{}{}
		if err := validateAllowedDiffs(replayCase.AllowedDiffs); err != nil {
			return Report{}, fmt.Errorf("replaytest: case %q: %w", replayCase.Name, err)
		}
	}

	now := time.Now
	if r.Now != nil {
		now = r.Now
	}
	report := Report{
		GeneratedAt: now().UTC(),
		Reference:   reference,
		TotalCases:  len(cases),
		Cases:       make([]CaseResult, 0, len(cases)),
	}
	for _, backend := range backends {
		report.Backends = append(report.Backends, backend.Name)
	}

	for _, replayCase := range cases {
		started := time.Now()
		result := CaseResult{Name: replayCase.Name}
		snapshots := make(map[string]Snapshot, len(backends))
		unsupported := make(map[string][]Capability)
		for _, backend := range backends {
			missing := missingCapabilities(replayCase.Requires, backend.Capabilities)
			if len(missing) > 0 {
				unsupported[backend.Name] = missing
				continue
			}
			snapshot, err := Replay(ctx, replayCase, backend)
			if err != nil {
				result.Diffs = append(result.Diffs, Diff{
					Case:        replayCase.Name,
					BackendA:    reference,
					BackendB:    backend.Name,
					SessionID:   replayCase.Name,
					Path:        "/execution",
					Baseline:    "success",
					Actual:      err.Error(),
					Explanation: "backend replay failed",
				})
				continue
			}
			snapshots[backend.Name] = snapshot
		}

		baseline, baselineOK := snapshots[reference]
		if !baselineOK {
			result.Diffs = append(result.Diffs, Diff{
				Case:        replayCase.Name,
				BackendA:    reference,
				BackendB:    reference,
				SessionID:   replayCase.Name,
				Path:        "/execution",
				Baseline:    "reference snapshot",
				Actual:      "unavailable",
				Explanation: "reference backend did not produce a snapshot",
			})
		} else {
			for _, backend := range backends {
				if backend.Name == reference {
					continue
				}
				actual, ok := snapshots[backend.Name]
				if !ok {
					continue
				}
				diffs, err := Compare(replayCase.Name, baseline, actual, replayCase.AllowedDiffs)
				if err != nil {
					return Report{}, err
				}
				result.Diffs = append(result.Diffs, diffs...)
			}
		}
		for backendName, missing := range unsupported {
			for _, capability := range missing {
				result.Diffs = append(result.Diffs, Diff{
					Case:        replayCase.Name,
					BackendA:    reference,
					BackendB:    backendName,
					SessionID:   replayCase.Name,
					Path:        "/capabilities/" + string(capability),
					Baseline:    true,
					Actual:      false,
					Allowed:     true,
					Explanation: "backend reports this capability as unsupported",
				})
			}
		}

		blocking, allowed := countDiffs(result.Diffs)
		report.BlockingDiffs += blocking
		report.AllowedDiffs += allowed
		switch {
		case blocking > 0:
			result.Status = StatusFailed
			report.FailedCases++
		case len(unsupported) > 0:
			result.Status = StatusUnsupported
			report.UnsupportedCases++
		default:
			result.Status = StatusPassed
			report.PassedCases++
		}
		result.Duration = time.Since(started).Milliseconds()
		report.Cases = append(report.Cases, result)
	}
	if err := report.Validate(); err != nil {
		return Report{}, err
	}
	return report, nil
}

// Replay executes one case on one isolated backend and captures its snapshot.
func Replay(ctx context.Context, replayCase Case, backend Backend) (snapshot Snapshot, err error) {
	if err := validateCase(replayCase); err != nil {
		return Snapshot{}, err
	}
	if backend.Open == nil {
		return Snapshot{}, fmt.Errorf("replaytest: backend %q has no factory", backend.Name)
	}
	services, err := backend.Open(ctx, replayCase.Name)
	if err != nil {
		return Snapshot{}, fmt.Errorf("open backend %s: %w", backend.Name, err)
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
	if services.Session == nil || services.Memory == nil {
		return Snapshot{}, fmt.Errorf("open backend %s: incomplete services", backend.Name)
	}

	key := session.Key{AppName: "replaytest", UserID: "user-1", SessionID: replayCase.Name}
	sess, err := services.Session.CreateSession(ctx, key, cloneState(replayCase.InitialState))
	if err != nil {
		return Snapshot{}, fmt.Errorf("create session: %w", err)
	}
	exec := execution{services: services, key: key, session: sess}
	for _, step := range replayCase.Steps {
		if err := exec.runStep(ctx, step); err != nil {
			return Snapshot{}, fmt.Errorf("step %q (%s): %w", step.Name, step.Kind, err)
		}
	}
	return exec.snapshot(ctx, backend.Name, replayCase.Name, replayCase.EventOrder)
}

type execution struct {
	services *Services
	key      session.Key
	session  *session.Session
}

func (e *execution) runStep(ctx context.Context, step Step) error {
	switch step.Kind {
	case StepAppendEvent:
		return e.appendEvent(ctx, step.Event)
	case StepRetryEvent:
		return retryOnce(func(attempt int) error {
			if attempt == 0 {
				return errInjectedPreWrite
			}
			return e.appendEvent(ctx, step.Event)
		})
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
	if input == nil || input.Event == nil || input.LogicalID == "" {
		return errors.New("invalid event input")
	}
	evt := input.Event.Clone()
	evt.Timestamp = e.session.CreatedAt.Add(input.Offset)
	if evt.Response != nil {
		evt.Response.Timestamp = evt.Timestamp
	}
	if err := event.SetExtension(evt, logicalEventIDExtension, input.LogicalID); err != nil {
		return fmt.Errorf("set logical event id: %w", err)
	}
	return e.services.Session.AppendEvent(ctx, e.session, evt)
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
	e.session = sess
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
	return errors.Join(errs...)
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
	appState, err := e.services.Session.ListAppStates(ctx, e.key.AppName)
	if err != nil {
		return Snapshot{}, fmt.Errorf("list app state: %w", err)
	}
	userKey := session.UserKey{AppName: e.key.AppName, UserID: e.key.UserID}
	userState, err := e.services.Session.ListUserStates(ctx, userKey)
	if err != nil {
		return Snapshot{}, fmt.Errorf("list user state: %w", err)
	}
	memories, err := e.services.Memory.ReadMemories(ctx, memory.UserKey{
		AppName: e.key.AppName,
		UserID:  e.key.UserID,
	}, 0)
	if err != nil {
		return Snapshot{}, fmt.Errorf("read memories: %w", err)
	}
	return normalizeSnapshot(backendName, caseName, eventOrder, sess, appState, userState, memories)
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
	return nil
}

func validateStep(step Step) error {
	if step.Name == "" {
		return errors.New("unnamed step")
	}
	payloads := 0
	for _, populated := range []bool{
		step.Event != nil,
		step.State != nil,
		step.Memory != nil,
		step.Summary != nil,
		step.Track != nil,
		len(step.Concurrent) > 0,
	} {
		if populated {
			payloads++
		}
	}
	wantPayloads := 1
	if step.Kind == StepReloadSession {
		wantPayloads = 0
	}
	if payloads != wantPayloads {
		return fmt.Errorf("step %q has %d payloads, want %d", step.Name, payloads, wantPayloads)
	}
	switch step.Kind {
	case StepAppendEvent, StepRetryEvent:
		if step.Event == nil || step.Event.Event == nil || step.Event.LogicalID == "" {
			return fmt.Errorf("step %q has invalid event input", step.Name)
		}
	case StepUpdateState:
		if step.State == nil {
			return fmt.Errorf("step %q has no state input", step.Name)
		}
		switch step.State.Scope {
		case StateScopeApp, StateScopeUser:
		case StateScopeSession:
			if len(step.State.DeleteKeys) > 0 {
				return fmt.Errorf("step %q cannot delete session state", step.Name)
			}
		default:
			return fmt.Errorf("step %q has unknown state scope %q", step.Name, step.State.Scope)
		}
	case StepAddMemory:
		if step.Memory == nil || step.Memory.Memory == "" {
			return fmt.Errorf("step %q has invalid memory input", step.Name)
		}
	case StepCreateSummary:
		if step.Summary == nil {
			return fmt.Errorf("step %q has no summary input", step.Name)
		}
	case StepAppendTrack:
		if step.Track == nil || step.Track.Event == nil {
			return fmt.Errorf("step %q has invalid track input", step.Name)
		}
	case StepReloadSession:
	case StepConcurrent:
		if len(step.Concurrent) == 0 {
			return fmt.Errorf("step %q has no concurrent branches", step.Name)
		}
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
	default:
		return fmt.Errorf("step %q has unknown kind %q", step.Name, step.Kind)
	}
	return nil
}

func retryOnce(operation func(int) error) error {
	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		lastErr = operation(attempt)
		if lastErr == nil {
			return nil
		}
		if !errors.Is(lastErr, errInjectedPreWrite) {
			return lastErr
		}
	}
	return lastErr
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

func cloneJSONMap(input CanonicalMap) CanonicalMap {
	raw, _ := json.Marshal(input)
	var output CanonicalMap
	_ = json.Unmarshal(raw, &output)
	return output
}
