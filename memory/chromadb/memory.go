//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package chromadb

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/memory"
	imemory "trpc.group/trpc-go/trpc-agent-go/memory/internal/memory"
)

// ReadMemories reads memories for a user in reverse update order.
//
// ChromaDB limit/offset pagination does not provide a snapshot token, so a scan is
// best-effort when another Service instance writes the same user concurrently.
func (svc *Service) ReadMemories(
	ctx context.Context,
	userKey memory.UserKey,
	limit int,
) ([]*memory.Entry, error) {
	if err := svc.beginOperation(); err != nil {
		return nil, err
	}
	defer svc.endOperation()
	if err := userKey.CheckUserKey(); err != nil {
		return nil, err
	}
	scope := recordScope{appName: userKey.AppName, userID: userKey.UserID}
	records, err := svc.listRecords(ctx, activeScopeWhere(scope), 0)
	if err != nil {
		return nil, fmt.Errorf("read memories: %w", err)
	}
	sort.Slice(records, func(i, j int) bool {
		return moreRecentRecord(records[i], records[j])
	})
	if limit > 0 && len(records) > limit {
		records = records[:limit]
	}
	entries := make([]*memory.Entry, len(records))
	for i, record := range records {
		entries[i] = record.entry
	}
	return entries, nil
}

// fetchRecordByID reads an ID without scope filtering so ownership collisions remain visible.
func (svc *Service) fetchRecordByID(
	ctx context.Context,
	id string,
	where map[string]any,
) (*storedRecord, error) {
	response, err := svc.client.getRecords(ctx, svc.collection, getRecordsRequest{
		IDs:     []string{id},
		Where:   where,
		Include: stringSlicePointer([]string{"documents", "metadatas"}),
	})
	if err != nil {
		return nil, err
	}
	records, err := decodeGetResponse(response)
	if err != nil {
		return nil, err
	}
	if len(records) == 0 {
		return nil, nil
	}
	if len(records) != 1 {
		return nil, fmt.Errorf("expected one record for id %s, got %d", id, len(records))
	}
	return records[0], nil
}

// listRecords pages through a filter, deduplicates IDs, and applies global ordering.
func (svc *Service) listRecords(
	ctx context.Context,
	where map[string]any,
	limit int,
) ([]*storedRecord, error) {
	records := make([]*storedRecord, 0)
	seen := make(map[string]struct{})
	offset := 0
	for {
		pageSize := nextPageSize(len(records), limit)
		response, err := svc.client.getRecords(ctx, svc.collection, getRecordsRequest{
			Where:   where,
			Limit:   intPointer(pageSize),
			Offset:  intPointer(offset),
			Include: stringSlicePointer([]string{"documents", "metadatas"}),
		})
		if err != nil {
			return nil, err
		}
		if len(response.IDs.value) == 0 {
			return records, nil
		}
		page, err := decodeGetResponse(response)
		if err != nil {
			return nil, err
		}
		for _, record := range page {
			if _, ok := seen[record.entry.ID]; ok {
				continue
			}
			seen[record.entry.ID] = struct{}{}
			records = append(records, record)
			if limit > 0 && len(records) >= limit {
				return records[:limit], nil
			}
		}
		offset += len(response.IDs.value)
	}
}

// countActiveAtLeast stops counting once the capacity decision can be made.
func (svc *Service) countActiveAtLeast(
	ctx context.Context,
	scope recordScope,
	threshold int,
) (int, error) {
	if threshold <= 0 {
		return 0, nil
	}
	include := []string{}
	count := 0
	offset := 0
	for count < threshold {
		response, err := svc.client.getRecords(ctx, svc.collection, getRecordsRequest{
			Where:   activeScopeWhere(scope),
			Limit:   intPointer(defaultReadPageSize),
			Offset:  intPointer(offset),
			Include: &include,
		})
		if err != nil {
			return 0, err
		}
		if len(response.IDs.value) == 0 {
			return count, nil
		}
		count += len(response.IDs.value)
		offset += len(response.IDs.value)
	}
	return count, nil
}

// decodeGetResponse converts validated columnar Get output into owned records.
func decodeGetResponse(response *getRecordsResponse) ([]*storedRecord, error) {
	if response == nil {
		return nil, fmt.Errorf("get records returned a nil response")
	}
	if response.Documents == nil || response.Metadatas == nil {
		return nil, fmt.Errorf("get records did not include documents and metadatas")
	}
	documents := *response.Documents
	metadatas := *response.Metadatas
	ids := response.IDs.value
	if len(documents) != len(ids) || len(metadatas) != len(ids) {
		return nil, fmt.Errorf(
			"get records column length mismatch: ids=%d documents=%d metadatas=%d",
			len(ids),
			len(documents),
			len(metadatas),
		)
	}
	records := make([]*storedRecord, len(ids))
	for i, id := range ids {
		record, err := decodeStoredRecord(id, documents[i], metadatas[i])
		if err != nil {
			return nil, err
		}
		records[i] = record
	}
	return records, nil
}

// moreRecentRecord orders records by update time, creation time, and stable ID.
func moreRecentRecord(left, right *storedRecord) bool {
	if !left.entry.UpdatedAt.Equal(right.entry.UpdatedAt) {
		return left.entry.UpdatedAt.After(right.entry.UpdatedAt)
	}
	if !left.entry.CreatedAt.Equal(right.entry.CreatedAt) {
		return left.entry.CreatedAt.After(right.entry.CreatedAt)
	}
	return left.entry.ID < right.entry.ID
}

// nextPageSize caps a requested page at the adapter's read-page limit.
func nextPageSize(current, limit int) int {
	if limit <= 0 {
		return defaultReadPageSize
	}
	remaining := limit - current
	if remaining < defaultReadPageSize {
		return remaining
	}
	return defaultReadPageSize
}

// intPointer returns a pointer used to distinguish an explicit REST integer field.
func intPointer(value int) *int {
	copy := value
	return &copy
}

// stringSlicePointer copies an include list before attaching it to a request.
func stringSlicePointer(value []string) *[]string {
	copy := append([]string(nil), value...)
	return &copy
}

const (
	fnvOffset64 = uint64(14695981039346656037)
	fnvPrime64  = uint64(1099511628211)
)

// AddMemory adds a memory or refreshes an existing record with the same canonical ID.
//
// Capacity checking and persistence are serialized for the app and user within this
// Service instance.
func (svc *Service) AddMemory(
	ctx context.Context,
	userKey memory.UserKey,
	content string,
	topics []string,
	opts ...memory.AddOption,
) error {
	if err := svc.beginOperation(); err != nil {
		return err
	}
	defer svc.endOperation()
	if err := userKey.CheckUserKey(); err != nil {
		return err
	}
	scope := recordScope{appName: userKey.AppName, userID: userKey.UserID}
	record := newAddRecord(
		scope,
		content,
		topics,
		memory.ResolveAddOptions(opts),
		time.Now().UTC(),
	)
	embedding, err := svc.embed(ctx, content)
	if err != nil {
		return err
	}
	record.embedding = embedding

	lock := svc.writeLock(scope)
	lock.Lock()
	defer lock.Unlock()
	return svc.storeAddRecord(ctx, scope, record)
}

// storeAddRecord handles create, replace, and tombstone revival under the scope lock.
func (svc *Service) storeAddRecord(
	ctx context.Context,
	scope recordScope,
	record *storedRecord,
) error {
	existing, err := svc.fetchRecordByID(ctx, record.entry.ID, nil)
	if err != nil {
		return fmt.Errorf("check existing memory %s: %w", record.entry.ID, err)
	}
	if existing != nil {
		return svc.refreshExistingRecord(ctx, scope, existing, record)
	}
	if err := svc.ensureCapacity(ctx, scope); err != nil {
		return err
	}
	if err := svc.client.addRecords(ctx, svc.collection, addRequest(record)); err != nil {
		return fmt.Errorf("add memory %s: %w", record.entry.ID, err)
	}
	return svc.verifyAddedRecord(ctx, scope, record)
}

// refreshExistingRecord replaces an active deterministic-ID record after ownership checks.
func (svc *Service) refreshExistingRecord(
	ctx context.Context,
	scope recordScope,
	existing *storedRecord,
	record *storedRecord,
) error {
	if err := validateRecordOwner(existing, scope); err != nil {
		return err
	}
	if !sameRecordIdentity(existing, record) {
		return fmt.Errorf("memory id %s is occupied by different content", record.entry.ID)
	}
	if existing.deletedAtNS != notDeletedAtNS {
		if err := svc.ensureCapacity(ctx, scope); err != nil {
			return err
		}
	}
	record.entry.CreatedAt = existing.entry.CreatedAt
	record.updateToken = existing.updateToken
	record.replacesID = existing.replacesID
	return svc.updateAndVerify(ctx, scope, record)
}

// verifyAddedRecord confirms that Chroma persisted the exact intended Add state.
func (svc *Service) verifyAddedRecord(
	ctx context.Context,
	scope recordScope,
	expected *storedRecord,
) error {
	actual, err := svc.fetchRecordByID(ctx, expected.entry.ID, nil)
	if err != nil {
		return fmt.Errorf("verify added memory %s: %w", expected.entry.ID, err)
	}
	if actual == nil {
		return fmt.Errorf("verify added memory %s: record is missing", expected.entry.ID)
	}
	if err := validateRecordOwner(actual, scope); err != nil {
		return err
	}
	if samePersistedRecord(actual, expected) {
		return nil
	}
	if actual.updateToken != "" || !sameRecordIdentity(actual, expected) {
		return fmt.Errorf("memory id %s was concurrently occupied", expected.entry.ID)
	}
	expected.entry.CreatedAt = actual.entry.CreatedAt
	return svc.updateAndVerify(ctx, scope, expected)
}

// ensureCapacity enforces the per-scope active-memory limit on a best-effort basis.
func (svc *Service) ensureCapacity(ctx context.Context, scope recordScope) error {
	if svc.opts.memoryLimit <= 0 {
		return nil
	}
	count, err := svc.countActiveAtLeast(ctx, scope, svc.opts.memoryLimit)
	if err != nil {
		return fmt.Errorf("count active memories: %w", err)
	}
	if count < svc.opts.memoryLimit {
		return nil
	}
	return fmt.Errorf(
		"memory limit exceeded for user %s, limit: %d, current: %d",
		scope.userID,
		svc.opts.memoryLimit,
		count,
	)
}

// UpdateMemory updates an existing memory and reports its effective canonical ID.
//
// A content change may rotate the deterministic memory ID. The rotation is recoverable
// after a retry, but it is not a transaction across multiple Service instances.
func (svc *Service) UpdateMemory(
	ctx context.Context,
	memoryKey memory.Key,
	content string,
	topics []string,
	opts ...memory.UpdateOption,
) error {
	if err := svc.beginOperation(); err != nil {
		return err
	}
	defer svc.endOperation()
	if err := memoryKey.CheckMemoryKey(); err != nil {
		return err
	}
	command := updateCommand{
		key:      memoryKey,
		content:  content,
		topics:   topics,
		metadata: memory.ResolveUpdateOptions(opts),
	}
	token, err := updateToken(command)
	if err != nil {
		return err
	}
	embedding, err := svc.embed(ctx, content)
	if err != nil {
		return err
	}
	scope := recordScope{appName: memoryKey.AppName, userID: memoryKey.UserID}
	lock := svc.writeLock(scope)
	lock.Lock()
	defer lock.Unlock()
	return svc.applyUpdate(ctx, command, embedding, token, opts)
}

// applyUpdate resumes a prior rotation or updates the currently owned source record.
func (svc *Service) applyUpdate(
	ctx context.Context,
	command updateCommand,
	embedding []float32,
	token string,
	opts []memory.UpdateOption,
) error {
	scope := recordScope{appName: command.key.AppName, userID: command.key.UserID}
	old, err := svc.fetchRecordByID(ctx, command.key.MemoryID, activeScopeWhere(scope))
	if err != nil {
		return fmt.Errorf("load memory %s: %w", command.key.MemoryID, err)
	}
	if old == nil {
		return svc.resolveCompletedUpdate(ctx, scope, command.key.MemoryID, token, opts)
	}

	now := time.Now().UTC()
	newID := imemory.ApplyMemoryUpdate(
		old.entry,
		command.key.AppName,
		command.key.UserID,
		command.content,
		command.topics,
		command.metadata,
		now,
	)
	old.embedding = embedding
	old.deletedAtNS = notDeletedAtNS
	if newID == command.key.MemoryID {
		if err := svc.updateAndVerify(ctx, scope, old); err != nil {
			return err
		}
		setUpdateResult(opts, newID)
		return nil
	}
	old.updateToken = token
	old.replacesID = command.key.MemoryID
	return svc.rotateRecord(ctx, scope, command.key.MemoryID, old, opts)
}

// resolveCompletedUpdate locates an idempotent rotation target when the old ID is absent.
func (svc *Service) resolveCompletedUpdate(
	ctx context.Context,
	scope recordScope,
	oldID string,
	token string,
	opts []memory.UpdateOption,
) error {
	records, err := svc.listRecords(ctx, tokenWhere(scope, token), 2)
	if err != nil {
		return fmt.Errorf("find completed memory update: %w", err)
	}
	if len(records) == 0 {
		return memoryNotFoundError(oldID)
	}
	if len(records) > 1 {
		return fmt.Errorf("memory update token %s matches multiple records", token)
	}
	setUpdateResult(opts, records[0].entry.ID)
	return nil
}

// rotateRecord commits the new ID before retiring the old ID for roll-forward safety.
func (svc *Service) rotateRecord(
	ctx context.Context,
	scope recordScope,
	oldID string,
	record *storedRecord,
	opts []memory.UpdateOption,
) error {
	target, err := svc.fetchRecordByID(ctx, record.entry.ID, nil)
	if err != nil {
		return fmt.Errorf("check update target %s: %w", record.entry.ID, err)
	}
	if target == nil {
		if err := svc.client.addRecords(ctx, svc.collection, addRequest(record)); err != nil {
			return fmt.Errorf("add update target %s: %w", record.entry.ID, err)
		}
	} else if err := validateUpdateTarget(target, record, scope); err != nil {
		return err
	}
	if err := svc.verifyRotationTarget(ctx, scope, record); err != nil {
		return err
	}
	if err := svc.retireRecord(ctx, scope, oldID); err != nil {
		return fmt.Errorf(
			"memory update partially completed: old_id=%s new_id=%s: %w",
			oldID,
			record.entry.ID,
			err,
		)
	}
	setUpdateResult(opts, record.entry.ID)
	return nil
}

// validateUpdateTarget prevents an ID rotation from overwriting unrelated content.
func validateUpdateTarget(
	actual *storedRecord,
	expected *storedRecord,
	scope recordScope,
) error {
	if err := validateRecordOwner(actual, scope); err != nil {
		return err
	}
	if actual.updateToken != expected.updateToken || !sameSemanticRecord(actual, expected) {
		return fmt.Errorf("memory update target %s is occupied", expected.entry.ID)
	}
	return nil
}

// verifyRotationTarget confirms the new record and its update token after Add.
func (svc *Service) verifyRotationTarget(
	ctx context.Context,
	scope recordScope,
	expected *storedRecord,
) error {
	actual, err := svc.fetchRecordByID(ctx, expected.entry.ID, activeScopeWhere(scope))
	if err != nil {
		return fmt.Errorf("verify update target %s: %w", expected.entry.ID, err)
	}
	if actual == nil || !sameSemanticRecord(actual, expected) {
		return fmt.Errorf("verify update target %s: content mismatch", expected.entry.ID)
	}
	return nil
}

// updateAndVerify applies an in-place update and rejects silent updates of missing IDs.
func (svc *Service) updateAndVerify(
	ctx context.Context,
	scope recordScope,
	record *storedRecord,
) error {
	if err := svc.client.updateRecords(ctx, svc.collection, updateRequest(record)); err != nil {
		return fmt.Errorf("update memory %s: %w", record.entry.ID, err)
	}
	actual, err := svc.fetchRecordByID(ctx, record.entry.ID, activeScopeWhere(scope))
	if err != nil {
		return fmt.Errorf("verify updated memory %s: %w", record.entry.ID, err)
	}
	if actual == nil || !samePersistedRecord(actual, record) {
		return fmt.Errorf("verify updated memory %s: content mismatch", record.entry.ID)
	}
	return nil
}

// addRequest encodes one stored record as a create-only REST batch.
func addRequest(record *storedRecord) addRecordsRequest {
	document := record.entry.Memory.Memory
	return addRecordsRequest{
		IDs:        []string{record.entry.ID},
		Embeddings: [][]float32{record.embedding},
		Documents:  []*string{&document},
		Metadatas:  []map[string]any{addMetadata(record)},
	}
}

// updateRequest encodes one stored record with explicit optional-field clearing.
func updateRequest(record *storedRecord) updateRecordsRequest {
	document := record.entry.Memory.Memory
	return updateRecordsRequest{
		IDs:        []string{record.entry.ID},
		Embeddings: [][]float32{record.embedding},
		Documents:  []*string{&document},
		Metadatas:  []map[string]any{updateMetadata(record)},
	}
}

// DeleteMemory idempotently deletes a memory for a user.
//
// With soft delete enabled, the record is retained as an inactive tombstone and
// read back to verify it is no longer active.
func (svc *Service) DeleteMemory(ctx context.Context, memoryKey memory.Key) error {
	if err := svc.beginOperation(); err != nil {
		return err
	}
	defer svc.endOperation()
	if err := memoryKey.CheckMemoryKey(); err != nil {
		return err
	}
	scope := recordScope{appName: memoryKey.AppName, userID: memoryKey.UserID}
	lock := svc.writeLock(scope)
	lock.Lock()
	defer lock.Unlock()
	return svc.retireRecord(ctx, scope, memoryKey.MemoryID)
}

// retireRecord tombstones or hard-deletes one owned record and verifies inactivity.
func (svc *Service) retireRecord(ctx context.Context, scope recordScope, id string) error {
	record, err := svc.fetchRecordByID(ctx, id, activeScopeWhere(scope))
	if err != nil {
		return err
	}
	if record == nil {
		return nil
	}
	if svc.opts.softDelete {
		metadata := map[string]any{metadataDeletedAtKey: time.Now().UTC().UnixNano()}
		request := updateRecordsRequest{
			IDs:       []string{id},
			Metadatas: []map[string]any{metadata},
		}
		if err := svc.client.updateRecords(ctx, svc.collection, request); err != nil {
			return err
		}
	} else {
		request := deleteRecordsRequest{
			IDs:   []string{id},
			Where: ownedScopeWhere(scope),
		}
		if _, err := svc.client.deleteRecords(ctx, svc.collection, request); err != nil {
			return err
		}
	}
	active, err := svc.fetchRecordByID(ctx, id, activeScopeWhere(scope))
	if err != nil {
		return err
	}
	if active != nil {
		return fmt.Errorf("memory %s remained active after delete", id)
	}
	return nil
}

// ClearMemories clears memories that were active when the operation began.
//
// Memories created after the operation's cutoff are not removed. ChromaDB does not
// provide a snapshot transaction, so cross-instance clock skew remains best-effort.
func (svc *Service) ClearMemories(ctx context.Context, userKey memory.UserKey) error {
	if err := svc.beginOperation(); err != nil {
		return err
	}
	defer svc.endOperation()
	if err := userKey.CheckUserKey(); err != nil {
		return err
	}
	scope := recordScope{appName: userKey.AppName, userID: userKey.UserID}
	lock := svc.writeLock(scope)
	lock.Lock()
	defer lock.Unlock()
	cutoff := time.Now().UTC().UnixNano()
	if svc.opts.softDelete {
		return svc.softDeleteAll(ctx, scope, cutoff)
	}
	return svc.hardDeleteAll(ctx, scope, cutoff)
}

// hardDeleteAll repeatedly removes the cutoff-bounded page zero until it is empty.
func (svc *Service) hardDeleteAll(ctx context.Context, scope recordScope, cutoff int64) error {
	where := clearScopeWhere(scope, cutoff)
	batchSize := svc.clearBatchSize()
	for {
		ids, err := svc.loadActiveIDs(ctx, where, batchSize)
		if err != nil {
			return fmt.Errorf("load memories to clear: %w", err)
		}
		if len(ids) == 0 {
			return nil
		}
		response, err := svc.client.deleteRecords(ctx, svc.collection, deleteRecordsRequest{
			IDs:   ids,
			Where: where,
		})
		if err != nil {
			return fmt.Errorf("hard delete memories: %w", err)
		}
		if response.Deleted.value != len(ids) {
			return fmt.Errorf(
				"hard delete memories made no progress: targeted %d, deleted %d",
				len(ids),
				response.Deleted.value,
			)
		}
		if err := svc.verifyInactiveIDs(ctx, scope, ids); err != nil {
			return fmt.Errorf("verify hard deleted memories: %w", err)
		}
	}
}

// softDeleteAll repeatedly tombstones the cutoff-bounded page zero until it is empty.
func (svc *Service) softDeleteAll(ctx context.Context, scope recordScope, cutoff int64) error {
	where := clearScopeWhere(scope, cutoff)
	batchSize := svc.clearBatchSize()
	deletedAtNS := time.Now().UTC().UnixNano()
	for {
		ids, err := svc.loadActiveIDs(ctx, where, batchSize)
		if err != nil {
			return fmt.Errorf("load memories to clear: %w", err)
		}
		if len(ids) == 0 {
			return nil
		}
		metadatas := make([]map[string]any, len(ids))
		for i := range ids {
			metadatas[i] = map[string]any{metadataDeletedAtKey: deletedAtNS}
		}
		request := updateRecordsRequest{IDs: ids, Metadatas: metadatas}
		if err := svc.client.updateRecords(ctx, svc.collection, request); err != nil {
			return fmt.Errorf("soft delete memories: %w", err)
		}
		if err := svc.verifyInactiveIDs(ctx, scope, ids); err != nil {
			return fmt.Errorf("verify soft deleted memories: %w", err)
		}
	}
}

// loadActiveIDs reads one explicit ID batch from the cutoff-bounded active scope.
func (svc *Service) loadActiveIDs(
	ctx context.Context,
	where map[string]any,
	limit int,
) ([]string, error) {
	include := []string{}
	response, err := svc.client.getRecords(ctx, svc.collection, getRecordsRequest{
		Where:   where,
		Limit:   intPointer(limit),
		Offset:  intPointer(0),
		Include: &include,
	})
	if err != nil {
		return nil, err
	}
	return append([]string(nil), response.IDs.value...), nil
}

// verifyInactiveIDs confirms that no requested record remains active in the scope.
func (svc *Service) verifyInactiveIDs(
	ctx context.Context,
	scope recordScope,
	ids []string,
) error {
	include := []string{}
	response, err := svc.client.getRecords(ctx, svc.collection, getRecordsRequest{
		IDs:     ids,
		Where:   activeScopeWhere(scope),
		Include: &include,
	})
	if err != nil {
		return err
	}
	if len(response.IDs.value) > 0 {
		return fmt.Errorf("%d memories remained active", len(response.IDs.value))
	}
	return nil
}

// clearBatchSize respects both Chroma's advertised write limit and local page size.
func (svc *Service) clearBatchSize() int {
	if svc.maxBatchSize < defaultReadPageSize {
		return svc.maxBatchSize
	}
	return defaultReadPageSize
}

// validateRecordOwner prevents operations on foreign or incompatible records.
func validateRecordOwner(record *storedRecord, scope recordScope) error {
	if record.entry.AppName != scope.appName || record.entry.UserID != scope.userID {
		return fmt.Errorf(
			"memory id %s belongs to a different app or user",
			record.entry.ID,
		)
	}
	return nil
}

// memoryNotFoundError preserves the shared memory service not-found error text.
func memoryNotFoundError(id string) error {
	return fmt.Errorf("memory with id %s not found", id)
}

// setUpdateResult reports a rotated memory ID through the framework update options.
func setUpdateResult(opts []memory.UpdateOption, id string) {
	if result := memory.ResolveUpdateResult(opts); result != nil {
		result.MemoryID = id
	}
}

// writeLock maps one app/user scope to a stable in-process lock stripe.
func (svc *Service) writeLock(scope recordScope) *sync.Mutex {
	hash := fnvOffset64
	for _, value := range []string{scope.appName, "\x00", scope.userID} {
		for i := 0; i < len(value); i++ {
			hash ^= uint64(value[i])
			hash *= fnvPrime64
		}
	}
	return &svc.writeLocks[hash%uint64(len(svc.writeLocks))]
}
