//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package mysql

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"strings"
	"sync"
	"time"

	drivermysql "github.com/go-sql-driver/mysql"
	"github.com/google/uuid"
	"trpc.group/trpc-go/trpc-agent-go/internal/session/sqldb"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

const (
	tableNameStateInitializationLeases = "state_initialization_leases"
	stateInitializationLeaseIndexUniq  = "uniq"
	stateInitializationLeaseIndexExp   = "exp"

	defaultStateInitializationLeaseTTL      = 30 * time.Second
	defaultStateInitializationRenewInterval = 10 * time.Second
	defaultStateInitializationPollMin       = 50 * time.Millisecond
	defaultStateInitializationPollMax       = 500 * time.Millisecond
	stateInitializationAbortTimeout         = time.Second
)

var (
	errStateInitializationOwnershipLost = errors.New(
		"initialize session state: lease ownership lost",
	)
	errStateInitializationSessionNotFound = errors.New(
		"initialize session state: session not found",
	)
	errStateInitializationGenerationChanged = errors.New(
		"initialize session state: session generation changed",
	)
)

type stateInitializationGeneration struct {
	rowID     int64
	createdAt time.Time
}

func (g stateInitializationGeneration) equal(other stateInitializationGeneration) bool {
	return g.rowID == other.rowID && g.createdAt.Equal(other.createdAt)
}

// LoadOrInitializeSessionState implements session.StateInitializationService.
func (s *Service) LoadOrInitializeSessionState(
	ctx context.Context,
	key session.Key,
	stateKey string,
	validate func([]byte) bool,
	initialize func(context.Context) ([]byte, error),
	projections ...session.StateInitializationProjection,
) ([]byte, bool, error) {
	if err := validateStateInitializationArguments(
		key,
		stateKey,
		validate,
		initialize,
		projections,
	); err != nil {
		return nil, false, err
	}
	if ctx == nil {
		ctx = context.Background()
	}

	value, present, expectedGeneration, err := s.loadStateInitializationValue(
		ctx,
		key,
		stateKey,
	)
	if err != nil {
		return nil, false, err
	}

	coordinationKey := stateInitializationCoordinationKey(key, stateKey)
	pollDelay := s.stateInitializationPollMin
	for {
		if err := ctx.Err(); err != nil {
			return nil, false, err
		}
		if present && validate(cloneStateInitializationValue(value)) {
			return cloneStateInitializationValue(value), false, nil
		}

		ownerToken := uuid.NewString()
		acquired, leaseDeadline, err := s.tryAcquireStateInitializationLease(
			ctx,
			key,
			coordinationKey,
			ownerToken,
			expectedGeneration,
		)
		if err != nil {
			return nil, false, err
		}
		if acquired {
			return s.initializeSessionState(
				ctx,
				key,
				stateKey,
				coordinationKey,
				ownerToken,
				leaseDeadline,
				expectedGeneration,
				validate,
				initialize,
				projections,
			)
		}

		if err := waitForStateInitializationPoll(ctx, pollDelay); err != nil {
			return nil, false, err
		}
		var generation stateInitializationGeneration
		value, present, generation, err = s.loadStateInitializationValue(
			ctx,
			key,
			stateKey,
		)
		if err != nil {
			return nil, false, err
		}
		if !generation.equal(expectedGeneration) {
			return nil, false, fmt.Errorf(
				"%w while waiting for ownership",
				errStateInitializationGenerationChanged,
			)
		}
		pollDelay *= 2
		if pollDelay <= 0 {
			pollDelay = time.Millisecond
		}
		if maxDelay := s.stateInitializationPollMax; maxDelay > 0 && pollDelay > maxDelay {
			pollDelay = maxDelay
		}
	}
}

func validateStateInitializationArguments(
	key session.Key,
	stateKey string,
	validate func([]byte) bool,
	initialize func(context.Context) ([]byte, error),
	projections []session.StateInitializationProjection,
) error {
	if err := key.CheckSessionKey(); err != nil {
		return err
	}
	if err := validateStateInitializationStateKey(stateKey); err != nil {
		return err
	}
	if validate == nil {
		return errors.New("state validation function is required")
	}
	if initialize == nil {
		return errors.New("state initialization function is required")
	}
	seen := map[string]struct{}{stateKey: {}}
	for _, projection := range projections {
		if err := validateStateInitializationStateKey(projection.StateKey); err != nil {
			return fmt.Errorf("state initialization projection: %w", err)
		}
		if _, exists := seen[projection.StateKey]; exists {
			return fmt.Errorf(
				"state initialization projection: duplicate state key %q",
				projection.StateKey,
			)
		}
		seen[projection.StateKey] = struct{}{}
		if projection.Project == nil {
			return errors.New("state initialization projection is required")
		}
	}
	return nil
}

func validateStateInitializationStateKey(stateKey string) error {
	if strings.TrimSpace(stateKey) == "" {
		return errors.New("state key is required")
	}
	if strings.HasPrefix(stateKey, session.StateAppPrefix) {
		return fmt.Errorf(
			"mysql session service initialize session state failed: %s is not allowed, use UpdateAppState instead",
			stateKey,
		)
	}
	if strings.HasPrefix(stateKey, session.StateUserPrefix) {
		return fmt.Errorf(
			"mysql session service initialize session state failed: %s is not allowed, use UpdateUserState instead",
			stateKey,
		)
	}
	return nil
}

func (s *Service) loadStateInitializationValue(
	ctx context.Context,
	key session.Key,
	stateKey string,
) ([]byte, bool, stateInitializationGeneration, error) {
	var (
		generation stateInitializationGeneration
		stateBytes []byte
	)
	err := s.mysqlClient.QueryRow(
		ctx,
		[]any{&generation.rowID, &stateBytes, &generation.createdAt},
		fmt.Sprintf(`SELECT id, state, created_at FROM %s
			WHERE app_name = ? AND user_id = ? AND session_id = ?
			AND deleted_at IS NULL
			AND (expires_at IS NULL OR expires_at > CURRENT_TIMESTAMP(6))`,
			s.tableSessionStates,
		),
		key.AppName,
		key.UserID,
		key.SessionID,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, stateInitializationGeneration{}, errStateInitializationSessionNotFound
	}
	if err != nil {
		return nil, false, stateInitializationGeneration{}, fmt.Errorf(
			"initialize session state: load session: %w",
			err,
		)
	}

	var sessState SessionState
	if err := json.Unmarshal(stateBytes, &sessState); err != nil {
		return nil, false, stateInitializationGeneration{}, fmt.Errorf(
			"initialize session state: unmarshal session state: %w",
			err,
		)
	}
	value, present := sessState.State[stateKey]
	return cloneStateInitializationValue(value), present, generation, nil
}

func stateInitializationCoordinationKey(
	key session.Key,
	stateKey string,
) [sha256.Size]byte {
	values := [...]string{key.AppName, key.UserID, key.SessionID, stateKey}
	encoded := make([]byte, 0, len(key.AppName)+len(key.UserID)+len(key.SessionID)+len(stateKey)+8*len(values))
	var length [8]byte
	for _, value := range values {
		binary.BigEndian.PutUint64(length[:], uint64(len(value)))
		encoded = append(encoded, length[:]...)
		encoded = append(encoded, value...)
	}
	return sha256.Sum256(encoded)
}

func (s *Service) effectiveStateInitializationLeaseTTL() time.Duration {
	if s.stateInitializationLeaseTTL <= 0 {
		return time.Millisecond
	}
	return s.stateInitializationLeaseTTL
}

func stateInitializationLeaseMicros(ttl time.Duration) int64 {
	micros := ttl.Microseconds()
	if micros <= 0 {
		return 1
	}
	return micros
}

func (s *Service) tryAcquireStateInitializationLease(
	ctx context.Context,
	key session.Key,
	coordinationKey [sha256.Size]byte,
	ownerToken string,
	expectedGeneration stateInitializationGeneration,
) (bool, time.Time, error) {
	ttl := s.effectiveStateInitializationLeaseTTL()
	ttlMicros := stateInitializationLeaseMicros(ttl)
	started := time.Now()
	_, err := s.mysqlClient.Exec(
		ctx,
		fmt.Sprintf(`INSERT INTO %s
			(coordination_key, user_id, owner_token, session_row_id,
			 session_created_at, expires_at, updated_at)
			VALUES (?, ?, ?, ?, ?,
			 TIMESTAMPADD(MICROSECOND, ?, CURRENT_TIMESTAMP(6)),
			 CURRENT_TIMESTAMP(6))`, s.tableStateInitializationLeases),
		coordinationKey[:],
		key.UserID,
		ownerToken,
		expectedGeneration.rowID,
		expectedGeneration.createdAt,
		ttlMicros,
	)
	if err == nil {
		return true, started.Add(ttl), nil
	}
	if !isDuplicateEntryError(err) {
		return false, time.Time{}, fmt.Errorf(
			"acquire session state initialization lease: %w",
			err,
		)
	}

	var (
		acquired bool
		deadline time.Time
	)
	err = s.mysqlClient.Transaction(ctx, func(tx *sql.Tx) error {
		var (
			leaseGeneration stateInitializationGeneration
			expired         int
		)
		err := tx.QueryRowContext(
			ctx,
			fmt.Sprintf(`SELECT session_row_id, session_created_at,
				(expires_at <= CURRENT_TIMESTAMP(6))
				FROM %s
				WHERE coordination_key = ? AND user_id = ?
				FOR UPDATE`, s.tableStateInitializationLeases),
			coordinationKey[:],
			key.UserID,
		).Scan(
			&leaseGeneration.rowID,
			&leaseGeneration.createdAt,
			&expired,
		)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("lock initialization lease: %w", err)
		}
		if expired == 0 && leaseGeneration.equal(expectedGeneration) {
			return nil
		}

		currentGeneration, err := loadStateInitializationGenerationForUpdate(
			ctx,
			tx,
			s.tableSessionStates,
			key,
		)
		if errors.Is(err, errSessionNotFound) ||
			(err == nil && !currentGeneration.equal(expectedGeneration)) {
			return fmt.Errorf(
				"%w while acquiring ownership",
				errStateInitializationGenerationChanged,
			)
		}
		if err != nil {
			return err
		}

		renewStarted := time.Now()
		result, err := tx.ExecContext(
			ctx,
			fmt.Sprintf(`UPDATE %s
				SET owner_token = ?, session_row_id = ?, session_created_at = ?,
					expires_at = TIMESTAMPADD(MICROSECOND, ?, CURRENT_TIMESTAMP(6)),
					updated_at = CURRENT_TIMESTAMP(6)
				WHERE coordination_key = ? AND user_id = ?`,
				s.tableStateInitializationLeases,
			),
			ownerToken,
			expectedGeneration.rowID,
			expectedGeneration.createdAt,
			ttlMicros,
			coordinationKey[:],
			key.UserID,
		)
		if err != nil {
			return fmt.Errorf("take over initialization lease: %w", err)
		}
		rowsAffected, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("inspect initialization lease takeover: %w", err)
		}
		if rowsAffected != 1 {
			return fmt.Errorf(
				"take over initialization lease: updated %d rows",
				rowsAffected,
			)
		}
		acquired = true
		deadline = renewStarted.Add(ttl)
		return nil
	})
	if err != nil {
		return false, time.Time{}, fmt.Errorf(
			"acquire session state initialization lease: %w",
			err,
		)
	}
	return acquired, deadline, nil
}

func loadStateInitializationGenerationForUpdate(
	ctx context.Context,
	tx *sql.Tx,
	tableSessionStates string,
	key session.Key,
) (stateInitializationGeneration, error) {
	var generation stateInitializationGeneration
	err := tx.QueryRowContext(
		ctx,
		fmt.Sprintf(`SELECT id, created_at FROM %s
			WHERE app_name = ? AND user_id = ? AND session_id = ?
			AND deleted_at IS NULL
			AND (expires_at IS NULL OR expires_at > CURRENT_TIMESTAMP(6))
			FOR UPDATE`, tableSessionStates),
		key.AppName,
		key.UserID,
		key.SessionID,
	).Scan(&generation.rowID, &generation.createdAt)
	if errors.Is(err, sql.ErrNoRows) {
		return stateInitializationGeneration{}, errSessionNotFound
	}
	if err != nil {
		return stateInitializationGeneration{}, fmt.Errorf(
			"lock session generation: %w",
			err,
		)
	}
	return generation, nil
}

func (s *Service) initializeSessionState(
	ctx context.Context,
	key session.Key,
	stateKey string,
	coordinationKey [sha256.Size]byte,
	ownerToken string,
	leaseDeadline time.Time,
	expectedGeneration stateInitializationGeneration,
	validate func([]byte) bool,
	initialize func(context.Context) ([]byte, error),
	projections []session.StateInitializationProjection,
) ([]byte, bool, error) {
	owned := true
	initializeCtx, cancelInitialize := context.WithCancel(ctx)
	defer cancelInitialize()
	renewal := s.startStateInitializationRenewal(
		ctx,
		key,
		coordinationKey,
		ownerToken,
		expectedGeneration,
		cancelInitialize,
		leaseDeadline,
	)
	defer func() {
		_ = renewal.stop()
		if owned {
			s.abortStateInitializationLeaseForCleanup(
				ctx,
				key,
				coordinationKey,
				ownerToken,
				expectedGeneration,
			)
		}
	}()

	value, present, generation, err := s.loadStateInitializationValue(
		initializeCtx,
		key,
		stateKey,
	)
	if err != nil {
		return nil, false, err
	}
	if !generation.equal(expectedGeneration) {
		return nil, false, fmt.Errorf(
			"%w before ownership was established",
			errStateInitializationGenerationChanged,
		)
	}
	if present && validate(cloneStateInitializationValue(value)) {
		if renewalErr := renewal.stop(); renewalErr != nil {
			return nil, false, renewalErr
		}
		released, releaseErr := s.abortStateInitializationLease(
			initializeCtx,
			key,
			coordinationKey,
			ownerToken,
			expectedGeneration,
		)
		if releaseErr != nil {
			return nil, false, releaseErr
		}
		if !released {
			return nil, false, errors.New(
				"initialize session state: lease ownership lost before recheck completed",
			)
		}
		owned = false
		return cloneStateInitializationValue(value), false, nil
	}

	renewStarted := time.Now()
	ownedNow, renewErr := s.renewStateInitializationLease(
		initializeCtx,
		key,
		coordinationKey,
		ownerToken,
		expectedGeneration,
	)
	if renewErr != nil {
		if renewalErr := renewal.stop(); renewalErr != nil {
			return nil, false, errors.Join(renewalErr, renewErr)
		}
		return nil, false, renewErr
	}
	if !ownedNow {
		if renewalErr := renewal.stop(); renewalErr != nil {
			return nil, false, renewalErr
		}
		return nil, false, errors.New(
			"initialize session state: lease ownership lost before callback",
		)
	}
	renewal.extendDeadline(renewStarted.Add(s.effectiveStateInitializationLeaseTTL()))

	value, callbackErr := initialize(initializeCtx)
	renewalErr := renewal.stop()
	if callbackErr != nil {
		callbackErr = fmt.Errorf("initialize session state: %w", callbackErr)
		if renewalErr != nil {
			return nil, false, errors.Join(renewalErr, callbackErr)
		}
		return nil, false, callbackErr
	}
	if err := initializeCtx.Err(); err != nil {
		if renewalErr != nil {
			return nil, false, errors.Join(renewalErr, err)
		}
		return nil, false, err
	}
	if renewalErr != nil {
		return nil, false, renewalErr
	}

	value = cloneStateInitializationValue(value)
	if !validate(cloneStateInitializationValue(value)) {
		return nil, false, errors.New(
			"initialize session state: callback returned an invalid value",
		)
	}
	projectedState, err := projectInitializedSessionState(
		value,
		projections,
	)
	if err != nil {
		return nil, false, err
	}
	if err := s.commitStateInitialization(
		initializeCtx,
		key,
		stateKey,
		value,
		projectedState,
		coordinationKey,
		ownerToken,
		expectedGeneration,
	); err != nil {
		return nil, false, err
	}
	owned = false
	return cloneStateInitializationValue(value), true, nil
}

func projectInitializedSessionState(
	value []byte,
	projections []session.StateInitializationProjection,
) (session.StateMap, error) {
	projectedState := make(session.StateMap, len(projections))
	for _, projection := range projections {
		projected, err := projection.Project(cloneStateInitializationValue(value))
		if err != nil {
			return nil, fmt.Errorf("project initialized session state: %w", err)
		}
		projectedState[projection.StateKey] = cloneStateInitializationValue(projected)
	}
	return projectedState, nil
}

func (s *Service) commitStateInitialization(
	ctx context.Context,
	key session.Key,
	stateKey string,
	value []byte,
	projectedState session.StateMap,
	coordinationKey [sha256.Size]byte,
	ownerToken string,
	expectedGeneration stateInitializationGeneration,
) error {
	err := s.mysqlClient.Transaction(ctx, func(tx *sql.Tx) error {
		var (
			leaseGeneration stateInitializationGeneration
			leaseActive     int
		)
		err := tx.QueryRowContext(
			ctx,
			fmt.Sprintf(`SELECT session_row_id, session_created_at,
				(expires_at > CURRENT_TIMESTAMP(6))
				FROM %s
				WHERE coordination_key = ? AND user_id = ? AND owner_token = ?
				FOR UPDATE`, s.tableStateInitializationLeases),
			coordinationKey[:],
			key.UserID,
			ownerToken,
		).Scan(
			&leaseGeneration.rowID,
			&leaseGeneration.createdAt,
			&leaseActive,
		)
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("%w before commit", errStateInitializationOwnershipLost)
		}
		if err != nil {
			return fmt.Errorf("lock initialization lease for commit: %w", err)
		}
		if leaseActive == 0 || !leaseGeneration.equal(expectedGeneration) {
			return fmt.Errorf("%w before commit", errStateInitializationOwnershipLost)
		}

		var (
			currentGeneration stateInitializationGeneration
			stateBytes        []byte
			sessionActive     int
		)
		err = tx.QueryRowContext(
			ctx,
			fmt.Sprintf(`SELECT id, state, created_at,
				(expires_at IS NULL OR expires_at > CURRENT_TIMESTAMP(6))
				FROM %s
				WHERE app_name = ? AND user_id = ? AND session_id = ?
				AND deleted_at IS NULL
				FOR UPDATE`, s.tableSessionStates),
			key.AppName,
			key.UserID,
			key.SessionID,
		).Scan(
			&currentGeneration.rowID,
			&stateBytes,
			&currentGeneration.createdAt,
			&sessionActive,
		)
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf(
				"%w during commit",
				errStateInitializationSessionNotFound,
			)
		}
		if err != nil {
			return fmt.Errorf("lock session for initialization commit: %w", err)
		}
		if sessionActive == 0 || !currentGeneration.equal(expectedGeneration) {
			return fmt.Errorf(
				"%w during commit",
				errStateInitializationGenerationChanged,
			)
		}

		var sessState SessionState
		if err := json.Unmarshal(stateBytes, &sessState); err != nil {
			return fmt.Errorf("unmarshal session state for initialization commit: %w", err)
		}
		if sessState.State == nil {
			sessState.State = make(session.StateMap)
		}
		sessState.State[stateKey] = cloneStateInitializationValue(value)
		for projectedKey, projectedValue := range projectedState {
			sessState.State[projectedKey] = cloneStateInitializationValue(projectedValue)
		}
		now := time.Now()
		sessState.UpdatedAt = now
		updatedStateBytes, err := json.Marshal(&sessState)
		if err != nil {
			return fmt.Errorf("marshal session state for initialization commit: %w", err)
		}

		result, err := tx.ExecContext(
			ctx,
			fmt.Sprintf(`UPDATE %s
				SET state = ?, updated_at = ?, expires_at = ?
				WHERE id = ? AND user_id = ? AND created_at = ?
				AND app_name = ? AND session_id = ? AND deleted_at IS NULL
				AND (expires_at IS NULL OR expires_at > CURRENT_TIMESTAMP(6))`,
				s.tableSessionStates,
			),
			string(updatedStateBytes),
			now,
			calculateExpiresAt(s.opts.sessionTTL),
			expectedGeneration.rowID,
			key.UserID,
			expectedGeneration.createdAt,
			key.AppName,
			key.SessionID,
		)
		if err != nil {
			return fmt.Errorf("persist initialized session state: %w", err)
		}
		rowsAffected, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("inspect initialized session state update: %w", err)
		}
		if rowsAffected != 1 {
			return fmt.Errorf(
				"%w during commit",
				errStateInitializationGenerationChanged,
			)
		}

		result, err = tx.ExecContext(
			ctx,
			fmt.Sprintf(`DELETE FROM %s
				WHERE coordination_key = ? AND user_id = ? AND owner_token = ?
				AND session_row_id = ? AND session_created_at = ?`,
				s.tableStateInitializationLeases,
			),
			coordinationKey[:],
			key.UserID,
			ownerToken,
			expectedGeneration.rowID,
			expectedGeneration.createdAt,
		)
		if err != nil {
			return fmt.Errorf("release initialization lease after commit: %w", err)
		}
		rowsAffected, err = result.RowsAffected()
		if err != nil {
			return fmt.Errorf("inspect initialization lease release: %w", err)
		}
		if rowsAffected != 1 {
			return fmt.Errorf("%w during commit", errStateInitializationOwnershipLost)
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("commit session state initialization: %w", err)
	}
	return nil
}

func (s *Service) renewStateInitializationLease(
	ctx context.Context,
	key session.Key,
	coordinationKey [sha256.Size]byte,
	ownerToken string,
	expectedGeneration stateInitializationGeneration,
) (bool, error) {
	result, err := s.mysqlClient.Exec(
		ctx,
		fmt.Sprintf(`UPDATE %s
			SET expires_at = TIMESTAMPADD(MICROSECOND, ?, CURRENT_TIMESTAMP(6)),
				updated_at = CURRENT_TIMESTAMP(6)
			WHERE coordination_key = ? AND user_id = ? AND owner_token = ?
			AND session_row_id = ? AND session_created_at = ?
			AND expires_at > CURRENT_TIMESTAMP(6)`,
			s.tableStateInitializationLeases,
		),
		stateInitializationLeaseMicros(s.effectiveStateInitializationLeaseTTL()),
		coordinationKey[:],
		key.UserID,
		ownerToken,
		expectedGeneration.rowID,
		expectedGeneration.createdAt,
	)
	if err != nil {
		return false, fmt.Errorf("renew session state initialization lease: %w", err)
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("inspect session state initialization lease renewal: %w", err)
	}
	return rowsAffected == 1, nil
}

func (s *Service) abortStateInitializationLease(
	ctx context.Context,
	key session.Key,
	coordinationKey [sha256.Size]byte,
	ownerToken string,
	expectedGeneration stateInitializationGeneration,
) (bool, error) {
	result, err := s.mysqlClient.Exec(
		ctx,
		fmt.Sprintf(`DELETE FROM %s
			WHERE coordination_key = ? AND user_id = ? AND owner_token = ?
			AND session_row_id = ? AND session_created_at = ?`,
			s.tableStateInitializationLeases,
		),
		coordinationKey[:],
		key.UserID,
		ownerToken,
		expectedGeneration.rowID,
		expectedGeneration.createdAt,
	)
	if err != nil {
		return false, fmt.Errorf("abort session state initialization lease: %w", err)
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("inspect session state initialization lease abort: %w", err)
	}
	return rowsAffected == 1, nil
}

func (s *Service) abortStateInitializationLeaseForCleanup(
	ctx context.Context,
	key session.Key,
	coordinationKey [sha256.Size]byte,
	ownerToken string,
	expectedGeneration stateInitializationGeneration,
) {
	if ctx == nil {
		ctx = context.Background()
	} else {
		ctx = context.WithoutCancel(ctx)
	}
	cleanupCtx, cancel := context.WithTimeout(ctx, stateInitializationAbortTimeout)
	defer cancel()
	_, _ = s.abortStateInitializationLease(
		cleanupCtx,
		key,
		coordinationKey,
		ownerToken,
		expectedGeneration,
	)
}

type stateInitializationRenewal struct {
	cancel     context.CancelFunc
	done       chan struct{}
	lost       chan error
	once       sync.Once
	deadlineMu sync.Mutex
	deadline   time.Time
}

func (s *Service) startStateInitializationRenewal(
	ctx context.Context,
	key session.Key,
	coordinationKey [sha256.Size]byte,
	ownerToken string,
	expectedGeneration stateInitializationGeneration,
	cancelInitialize context.CancelFunc,
	leaseDeadline time.Time,
) *stateInitializationRenewal {
	renewCtx, cancel := context.WithCancel(ctx)
	renewal := &stateInitializationRenewal{
		cancel:   cancel,
		done:     make(chan struct{}),
		lost:     make(chan error, 1),
		deadline: leaseDeadline,
	}
	interval := s.stateInitializationRenewInterval
	if interval <= 0 {
		interval = s.effectiveStateInitializationLeaseTTL() / 3
	}
	if interval <= 0 {
		interval = time.Millisecond
	}
	go func() {
		defer close(renewal.done)
		for {
			delay := interval
			if deadline := renewal.currentDeadline(); !deadline.IsZero() {
				untilDeadline := time.Until(deadline)
				if untilDeadline <= 0 {
					renewal.reportLoss(
						errors.New("renew session state initialization lease: lease deadline exceeded"),
						cancelInitialize,
					)
					return
				}
				if untilDeadline < delay {
					delay = untilDeadline
				}
			}
			timer := time.NewTimer(delay)
			select {
			case <-renewCtx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}

			deadline := renewal.currentDeadline()
			if !deadline.IsZero() && !time.Now().Before(deadline) {
				renewal.reportLoss(
					errors.New("renew session state initialization lease: lease deadline exceeded"),
					cancelInitialize,
				)
				return
			}
			renewStarted := time.Now()
			renewCtxForCall := renewCtx
			cancelRenewCtx := func() {}
			if !deadline.IsZero() {
				renewCtxForCall, cancelRenewCtx = context.WithDeadline(renewCtx, deadline)
			}
			renewed, err := s.renewStateInitializationLease(
				renewCtxForCall,
				key,
				coordinationKey,
				ownerToken,
				expectedGeneration,
			)
			cancelRenewCtx()
			if renewCtx.Err() != nil {
				return
			}
			if err == nil && !renewed {
				err = errors.New(
					"renew session state initialization lease: ownership lost",
				)
			}
			if err != nil {
				renewal.reportLoss(err, cancelInitialize)
				return
			}
			renewal.extendDeadline(
				renewStarted.Add(s.effectiveStateInitializationLeaseTTL()),
			)
		}
	}()
	return renewal
}

func (r *stateInitializationRenewal) reportLoss(
	err error,
	cancelInitialize context.CancelFunc,
) {
	if err == nil {
		return
	}
	select {
	case r.lost <- err:
	default:
	}
	cancelInitialize()
}

func (r *stateInitializationRenewal) currentDeadline() time.Time {
	r.deadlineMu.Lock()
	defer r.deadlineMu.Unlock()
	return r.deadline
}

func (r *stateInitializationRenewal) extendDeadline(deadline time.Time) {
	r.deadlineMu.Lock()
	if deadline.After(r.deadline) {
		r.deadline = deadline
	}
	r.deadlineMu.Unlock()
}

func (r *stateInitializationRenewal) stop() error {
	if r == nil {
		return nil
	}
	r.once.Do(r.cancel)
	<-r.done
	select {
	case err := <-r.lost:
		return err
	default:
		return nil
	}
}

func waitForStateInitializationPoll(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		delay = time.Millisecond
	}
	half := delay / 2
	if half > 0 {
		delay = half + time.Duration(rand.Int63n(int64(half)+1))
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func cloneStateInitializationValue(value []byte) []byte {
	if value == nil {
		return nil
	}
	cloned := make([]byte, len(value))
	copy(cloned, value)
	return cloned
}

func isDuplicateEntryError(err error) bool {
	if err == nil {
		return false
	}
	var mysqlErr *drivermysql.MySQLError
	return errors.As(err, &mysqlErr) && mysqlErr.Number == sqldb.MySQLErrDuplicateEntry
}
