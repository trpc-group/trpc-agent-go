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

var recordIncludes = []string{"documents", "metadatas"}

// ReadMemories reads memories for a user in reverse update order.
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
		return lessRecentRecord(records[i], records[j])
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

func (svc *Service) fetchRecordByID(
	ctx context.Context,
	id string,
	where map[string]any,
) (*storedRecord, error) {
	response, err := svc.client.getRecords(ctx, svc.collection, getRecordsRequest{
		IDs:     []string{id},
		Where:   where,
		Include: stringSlicePointer(recordIncludes),
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
			Include: stringSlicePointer(recordIncludes),
		})
		if err != nil {
			return nil, err
		}
		if len(response.IDs) == 0 {
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
		offset += len(response.IDs)
	}
}

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
		if len(response.IDs) == 0 {
			return count, nil
		}
		count += len(response.IDs)
		offset += len(response.IDs)
	}
	return count, nil
}

func decodeGetResponse(response *getRecordsResponse) ([]*storedRecord, error) {
	if response == nil {
		return nil, fmt.Errorf("get records returned a nil response")
	}
	if response.Documents == nil || response.Metadatas == nil {
		return nil, fmt.Errorf("get records did not include documents and metadatas")
	}
	documents := *response.Documents
	metadatas := *response.Metadatas
	if len(documents) != len(response.IDs) || len(metadatas) != len(response.IDs) {
		return nil, fmt.Errorf(
			"get records column length mismatch: ids=%d documents=%d metadatas=%d",
			len(response.IDs),
			len(documents),
			len(metadatas),
		)
	}
	records := make([]*storedRecord, len(response.IDs))
	for i, id := range response.IDs {
		record, err := decodeStoredRecord(id, documents[i], metadatas[i])
		if err != nil {
			return nil, err
		}
		records[i] = record
	}
	return records, nil
}

func lessRecentRecord(left, right *storedRecord) bool {
	if !left.entry.UpdatedAt.Equal(right.entry.UpdatedAt) {
		return left.entry.UpdatedAt.After(right.entry.UpdatedAt)
	}
	if !left.entry.CreatedAt.Equal(right.entry.CreatedAt) {
		return left.entry.CreatedAt.After(right.entry.CreatedAt)
	}
	return left.entry.ID < right.entry.ID
}

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

func intPointer(value int) *int {
	copy := value
	return &copy
}

func stringSlicePointer(value []string) *[]string {
	copy := append([]string(nil), value...)
	return &copy
}

const (
	fnvOffset64 = uint64(14695981039346656037)
	fnvPrime64  = uint64(1099511628211)
)

// AddMemory adds or refreshes a canonical memory record.
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

func addRequest(record *storedRecord) addRecordsRequest {
	document := record.entry.Memory.Memory
	return addRecordsRequest{
		IDs:        []string{record.entry.ID},
		Embeddings: [][]float32{record.embedding},
		Documents:  []*string{&document},
		Metadatas:  []map[string]any{addMetadata(record)},
	}
}

func updateRequest(record *storedRecord) updateRecordsRequest {
	document := record.entry.Memory.Memory
	return updateRecordsRequest{
		IDs:        []string{record.entry.ID},
		Embeddings: [][]float32{record.embedding},
		Documents:  []*string{&document},
		Metadatas:  []map[string]any{updateMetadata(record)},
	}
}

// DeleteMemory deletes a memory for a user.
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

// ClearMemories clears all active memories for a user.
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
	if svc.opts.softDelete {
		return svc.clearSoftDeleted(ctx, scope)
	}
	return svc.clearHardDeleted(ctx, scope)
}

func (svc *Service) clearHardDeleted(ctx context.Context, scope recordScope) error {
	for {
		response, err := svc.client.deleteRecords(ctx, svc.collection, deleteRecordsRequest{
			Where: ownedScopeWhere(scope),
			Limit: intPointer(svc.maxBatchSize),
		})
		if err != nil {
			return fmt.Errorf("clear memories: %w", err)
		}
		if response.Deleted == 0 {
			return nil
		}
	}
}

func (svc *Service) clearSoftDeleted(ctx context.Context, scope recordScope) error {
	deletedAtNS := time.Now().UTC().UnixNano()
	for {
		ids, err := svc.loadActiveIDs(ctx, scope, svc.maxBatchSize)
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
	}
}

func (svc *Service) loadActiveIDs(
	ctx context.Context,
	scope recordScope,
	limit int,
) ([]string, error) {
	include := []string{}
	response, err := svc.client.getRecords(ctx, svc.collection, getRecordsRequest{
		Where:   activeScopeWhere(scope),
		Limit:   intPointer(limit),
		Offset:  intPointer(0),
		Include: &include,
	})
	if err != nil {
		return nil, err
	}
	return append([]string(nil), response.IDs...), nil
}

func validateRecordOwner(record *storedRecord, scope recordScope) error {
	if record.entry.AppName != scope.appName || record.entry.UserID != scope.userID {
		return fmt.Errorf(
			"memory id %s belongs to a different app or user",
			record.entry.ID,
		)
	}
	return nil
}

func memoryNotFoundError(id string) error {
	return fmt.Errorf("memory with id %s not found", id)
}

func setUpdateResult(opts []memory.UpdateOption, id string) {
	if result := memory.ResolveUpdateResult(opts); result != nil {
		result.MemoryID = id
	}
}

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
