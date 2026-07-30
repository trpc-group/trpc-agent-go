package replayconsistency

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

func (h ReplayHarness) Run(ctx context.Context) (Report, error) {
	if len(h.Cases) == 0 {
		h.Cases = DefaultReplayCases()
	}
	if h.Options.MaxCases > 0 && h.Options.MaxCases < len(h.Cases) {
		h.Cases = h.Cases[:h.Options.MaxCases]
	}
	backends := h.Backends
	if len(backends) == 0 {
		created, err := newDefaultReplayBackends(h.Options)
		if err != nil {
			return BuildReport(nil), err
		}
		backends = created
	}
	defer closeBackends(backends)

	baselineName := strings.TrimSpace(h.Options.BaselineBackend)
	if baselineName == "" {
		baselineName = "inmemory"
	}
	baselineBackend := findBackend(backends, baselineName)
	if baselineBackend == nil {
		return BuildReport(nil), fmt.Errorf("baseline backend %q not found", baselineName)
	}

	var results []CaseResult
	for _, replayCase := range h.Cases {
		select {
		case <-ctx.Done():
			return BuildReport(results), ctx.Err()
		default:
		}

		baselineResult, err := runReplayCase(ctx, replayCase, baselineBackend)
		if err != nil {
			return BuildReport(results), fmt.Errorf("run baseline case %s on %s: %w", replayCase.Name, baselineBackend.Name(), err)
		}
		results = append(results, baselineResult)

		for _, backend := range backends {
			if backend.Name() == baselineBackend.Name() {
				continue
			}
			actualResult, err := runReplayCase(ctx, replayCase, backend)
			if err != nil {
				actualResult.Error = err.Error()
			}
			actualResult.Diffs = compareReplayCase(replayCase, backend, baselineResult.Snapshot, actualResult.Snapshot)
			if actualResult.Error != "" {
				actualResult.Diffs = append([]Diff{newDiff(replayCase.Name, backend.Name(), "error", "", actualResult.Error, false, "backend returned an error during replay")}, actualResult.Diffs...)
			}
			actualResult.Diffs = applyAllowedDiffPatterns(actualResult.Diffs, replayCase.AllowedDiffPatterns)
			actualResult.Diffs = collapseUnsupportedFeatureDiffs(replayCase, backend, baselineResult.Snapshot, actualResult.Snapshot, actualResult.Diffs)
			results = append(results, actualResult)
		}
	}
	return BuildReport(results), nil
}

func ValidateBackendConfig(name string, value string) error {
	if value == "" {
		return fmt.Errorf("%s is not configured", name)
	}
	return nil
}

func runReplayCase(ctx context.Context, replayCase ReplayCase, backend Backend) (CaseResult, error) {
	appName := replayAppName
	userID := replayUserForCase(replayCase.Name)
	sessionID := replaySessionForCase(replayCase.Name)
	sessKey := session.Key{AppName: appName, UserID: userID, SessionID: sessionID}
	sessionUserKey := session.UserKey{AppName: appName, UserID: userID}
	memoryUserKey := memory.UserKey{AppName: appName, UserID: userID}

	result := CaseResult{CaseName: replayCase.Name, Backend: backend.Name()}
	ctx, cancel := context.WithTimeout(ctx, 30_000_000_000)
	defer cancel()

	sess, err := createReplaySession(ctx, backend, sessKey)
	if err != nil {
		return result, err
	}

	var lastMemoryID string
	var seenTrack bool
	for _, op := range replayCase.Operations {
		switch op.Kind {
		case OperationKindAppendEvent:
			if op.Event == nil {
				continue
			}
			if err := backendSessionService(backend).AppendEvent(ctx, sess, op.Event); err != nil {
				return result, fmt.Errorf("append event %s: %w", op.Event.ID, err)
			}
		case OperationKindUpdateState:
			if err := applyStateUpdate(ctx, backend, sessKey, sessionUserKey, op, sess); err != nil {
				return result, err
			}
		case OperationKindDeleteState:
			if err := applyStateDelete(ctx, backend, sessKey, sessionUserKey, op, sess); err != nil {
				return result, err
			}
		case OperationKindClearState:
			if err := applyStateClear(ctx, backend, sessKey, sessionUserKey, op, sess); err != nil {
				return result, err
			}
		case OperationKindAddMemory:
			if op.MemoryAdd == nil {
				continue
			}
			if err := backendMemoryService(backend).AddMemory(ctx, op.MemoryAdd.UserKey, op.MemoryAdd.Content, op.MemoryAdd.Topics, memory.WithMetadata(op.MemoryAdd.Metadata)); err != nil {
				return result, fmt.Errorf("add memory %s: %w", op.MemoryAdd.MemoryID, err)
			}
			// Read back to resolve the backend-generated memory ID.
			if entries, err := backendMemoryService(backend).ReadMemories(ctx, op.MemoryAdd.UserKey, 100); err == nil && len(entries) > 0 {
				lastMemoryID = entries[len(entries)-1].ID
			}
		case OperationKindUpdateMemory:
			if op.MemoryUpdate == nil {
				continue
			}
			memoryKey := memory.Key{AppName: op.MemoryUpdate.UserKey.AppName, UserID: op.MemoryUpdate.UserKey.UserID, MemoryID: lastMemoryID}
			if memoryKey.MemoryID == "" {
				memoryKey.MemoryID = op.MemoryUpdate.MemoryID
			}
			var updateResult memory.UpdateResult
			if err := backendMemoryService(backend).UpdateMemory(ctx, memoryKey, op.MemoryUpdate.Content, op.MemoryUpdate.Topics, memory.WithUpdateMetadata(op.MemoryUpdate.Metadata), memory.WithUpdateResult(&updateResult)); err != nil {
				return result, fmt.Errorf("update memory %s: %w", memoryKey.MemoryID, err)
			}
			if updateResult.MemoryID != "" {
				lastMemoryID = updateResult.MemoryID
			} else {
				lastMemoryID = memoryKey.MemoryID
			}
		case OperationKindDeleteMemory:
			if op.MemoryDelete == nil {
				continue
			}
			memoryKey := *op.MemoryDelete
			if memoryKey.MemoryID == "" {
				memoryKey.MemoryID = lastMemoryID
			}
			if err := backendMemoryService(backend).DeleteMemory(ctx, memoryKey); err != nil {
				return result, fmt.Errorf("delete memory %s: %w", memoryKey.MemoryID, err)
			}
		case OperationKindCreateSummary:
			if err := backendSessionService(backend).CreateSessionSummary(ctx, sess, op.FilterKey, true); err != nil {
				return result, fmt.Errorf("create summary on filter %q: %w", op.FilterKey, err)
			}
		case OperationKindAppendTrackEvent:
			if op.Track == nil {
				continue
			}
			seenTrack = true
			if !backend.Supports("track") {
				continue
			}
			trackSvc, ok := backendSessionService(backend).(session.TrackService)
			if !ok {
				continue
			}
			if err := trackSvc.AppendTrackEvent(ctx, sess, op.Track); err != nil {
				return result, fmt.Errorf("append track %s: %w", op.Track.Track, err)
			}
		case OperationKindReadBack:
			// The readback pass happens after all operations complete.
		}
	}

	result.Snapshot, err = collectReplaySnapshot(ctx, backend, sessKey, memoryUserKey)
	if err != nil {
		return result, err
	}
	if seenTrack && !backend.Supports("track") {
		result.Diffs = append(result.Diffs, newDiff(replayCase.Name, backend.Name(), "tracks", "present", "unsupported", true, "track replay is unsupported on this backend"))
	}
	return result, nil
}

func compareReplayCase(replayCase ReplayCase, backend Backend, baseline, actual NormalizedSnapshot) []Diff {
	diffs := CompareSnapshots(replayCase.Name, backend.Name(), baseline, actual)
	return diffs
}

func applyStateUpdate(ctx context.Context, backend Backend, sessKey session.Key, userKey session.UserKey, op Operation, sess *session.Session) error {
	state := op.StatePatch
	if len(state) == 0 {
		return nil
	}
	scope := op.Scope
	if scope == "" {
		scope = StateScopeSession
	}
	switch scope {
	case StateScopeSession:
		return backendSessionService(backend).UpdateSessionState(ctx, sessKey, state)
	case StateScopeApp:
		return backendSessionService(backend).UpdateAppState(ctx, sessKey.AppName, state)
	case StateScopeUser:
		return backendSessionService(backend).UpdateUserState(ctx, userKey, state)
	default:
		return fmt.Errorf("unknown state scope %q", scope)
	}
}

func applyStateDelete(ctx context.Context, backend Backend, sessKey session.Key, userKey session.UserKey, op Operation, sess *session.Session) error {
	if len(op.StateDelete) == 0 {
		return nil
	}
	scope := op.Scope
	if scope == "" {
		scope = StateScopeSession
	}
	switch scope {
	case StateScopeSession:
		state := make(session.StateMap, len(op.StateDelete))
		for _, key := range op.StateDelete {
			state[key] = nil
		}
		return backendSessionService(backend).UpdateSessionState(ctx, sessKey, state)
	case StateScopeApp:
		for _, key := range op.StateDelete {
			if err := backendSessionService(backend).DeleteAppState(ctx, sessKey.AppName, key); err != nil {
				return err
			}
		}
		return nil
	case StateScopeUser:
		for _, key := range op.StateDelete {
			if err := backendSessionService(backend).DeleteUserState(ctx, userKey, key); err != nil {
				return err
			}
		}
		return nil
	default:
		return fmt.Errorf("unknown state scope %q", scope)
	}
}

func applyStateClear(ctx context.Context, backend Backend, sessKey session.Key, userKey session.UserKey, op Operation, sess *session.Session) error {
	scope := op.Scope
	if scope == "" {
		scope = StateScopeSession
	}
	switch scope {
	case StateScopeSession:
		if len(op.StateDelete) == 0 {
			current, err := backendSessionService(backend).GetSession(ctx, sessKey)
			if err != nil {
				return err
			}
			if current == nil {
				return nil
			}
			currentState := current.SnapshotState()
			for key := range currentState {
				if strings.HasPrefix(key, session.StateAppPrefix) || strings.HasPrefix(key, session.StateUserPrefix) {
					continue
				}
				op.StateDelete = append(op.StateDelete, key)
			}
		}
		state := make(session.StateMap, len(op.StateDelete))
		for _, key := range op.StateDelete {
			state[key] = nil
		}
		return backendSessionService(backend).UpdateSessionState(ctx, sessKey, state)
	case StateScopeApp:
		if len(op.StateDelete) == 0 {
			current, err := backendSessionService(backend).ListAppStates(ctx, sessKey.AppName)
			if err != nil {
				return err
			}
			for key := range current {
				op.StateDelete = append(op.StateDelete, key)
			}
		}
		for _, key := range op.StateDelete {
			if err := backendSessionService(backend).DeleteAppState(ctx, sessKey.AppName, key); err != nil {
				return err
			}
		}
		return nil
	case StateScopeUser:
		if len(op.StateDelete) == 0 {
			current, err := backendSessionService(backend).ListUserStates(ctx, userKey)
			if err != nil {
				return err
			}
			for key := range current {
				op.StateDelete = append(op.StateDelete, key)
			}
		}
		for _, key := range op.StateDelete {
			if err := backendSessionService(backend).DeleteUserState(ctx, userKey, key); err != nil {
				return err
			}
		}
		return nil
	default:
		return fmt.Errorf("unknown state scope %q", scope)
	}
}

func collectReplaySnapshot(ctx context.Context, backend Backend, sessKey session.Key, userKey memory.UserKey) (NormalizedSnapshot, error) {
	sess, err := backendSessionService(backend).GetSession(ctx, sessKey)
	if err != nil {
		return NormalizedSnapshot{}, err
	}
	if sess == nil {
		return NormalizedSnapshot{}, fmt.Errorf("session %s not found", sessKey.SessionID)
	}
	memories, err := backendMemoryService(backend).ReadMemories(ctx, userKey, 0)
	if err != nil {
		return NormalizedSnapshot{}, err
	}

	snapshot := NormalizedSnapshot{}
	for idx, evt := range sess.GetEvents() {
		normalized := NormalizeEvent(&evt)
		normalized.Index = idx
		snapshot.Events = append(snapshot.Events, normalized)
	}
	state := sess.SnapshotState()
	if len(state) > 0 {
		snapshot.State = NormalizeState(state)
	}
	if len(memories) > 0 {
		snapshot.Memories = normalizeMemoryEntries(memories)
	}
	if len(sess.Summaries) > 0 {
		for filterKey, sum := range sess.Summaries {
			snapshot.Summaries = append(snapshot.Summaries, NormalizeSummary(sess.ID, filterKey, sum))
		}
	}
	if len(sess.Tracks) > 0 {
		for trackName, history := range sess.Tracks {
			for _, trackEvent := range history.Events {
				snapshot.Tracks = append(snapshot.Tracks, NormalizedTrack{
					Track:     string(trackName),
					Timestamp: trackEvent.Timestamp.UTC().Format(timeLayout),
					Payload:   canonicalizeRawJSON(trackEvent.Payload),
				})
			}
		}
	}
	return NormalizeSnapshot(snapshot), nil
}

func normalizeMemoryEntries(entries []*memory.Entry) []NormalizedMemory {
	out := make([]NormalizedMemory, 0, len(entries))
	for _, entry := range entries {
		out = append(out, NormalizeMemoryEntry(entry))
	}
	return out
}

func applyAllowedDiffPatterns(diffs []Diff, patterns []string) []Diff {
	if len(patterns) == 0 || len(diffs) == 0 {
		return diffs
	}
	for i := range diffs {
		for _, pattern := range patterns {
			if diffMatchesPattern(diffs[i].Path, pattern) {
				diffs[i].AllowedDiff = true
				if diffs[i].Explanation == "" {
					diffs[i].Explanation = "allowed diff matched by case pattern"
				}
				break
			}
		}
	}
	return diffs
}

func collapseUnsupportedFeatureDiffs(replayCase ReplayCase, backend Backend, baseline, actual NormalizedSnapshot, diffs []Diff) []Diff {
	if backend.Supports("track") {
		return diffs
	}
	if len(actual.Tracks) > 0 || len(baseline.Tracks) > 0 {
		filtered := make([]Diff, 0, len(diffs)+1)
		for _, diff := range diffs {
			if strings.HasPrefix(diff.Path, "tracks[") || diff.Path == "tracks" {
				continue
			}
			filtered = append(filtered, diff)
		}
		filtered = append(filtered, newDiff(replayCase.Name, backend.Name(), "tracks", "supported", "unsupported", true, "track replay is unsupported on this backend"))
		return filtered
	}
	return diffs
}

func diffMatchesPattern(path, pattern string) bool {
	path = strings.TrimSpace(path)
	pattern = strings.TrimSpace(pattern)
	if path == "" || pattern == "" {
		return false
	}
	if path == pattern {
		return true
	}
	if strings.HasSuffix(pattern, "*") {
		return strings.HasPrefix(path, strings.TrimSuffix(pattern, "*"))
	}
	return strings.HasPrefix(path, pattern)
}

func backendSessionService(backend Backend) session.Service {
	if provider, ok := backend.(interface{ SessionService() session.Service }); ok {
		return provider.SessionService()
	}
	if replay, ok := backend.(*replayBackend); ok {
		return replay.sessionSvc
	}
	return nil
}

func backendMemoryService(backend Backend) memory.Service {
	if provider, ok := backend.(interface{ MemoryService() memory.Service }); ok {
		return provider.MemoryService()
	}
	if replay, ok := backend.(*replayBackend); ok {
		return replay.memorySvc
	}
	return nil
}

func createReplaySession(ctx context.Context, backend Backend, key session.Key) (*session.Session, error) {
	svc := backendSessionService(backend)
	if svc == nil {
		return nil, errors.New("session backend is nil")
	}
	return svc.CreateSession(ctx, key, session.StateMap{})
}

func findBackend(backends []Backend, name string) Backend {
	for _, backend := range backends {
		if backend.Name() == name {
			return backend
		}
	}
	return nil
}

func closeBackends(backends []Backend) {
	for i := len(backends) - 1; i >= 0; i-- {
		_ = backends[i].Close()
	}
}

func replayCaseKey(caseName string) string {
	return strings.ToLower(strings.NewReplacer(" ", "-", "/", "-", ":", "-", "_", "-").Replace(caseName))
}

func replaySessionForCase(caseName string) string {
	return replaySessionID + "-" + replayCaseKey(caseName)
}

func replayUserForCase(caseName string) string {
	return replayBaseUser + "-" + replayCaseKey(caseName)
}
