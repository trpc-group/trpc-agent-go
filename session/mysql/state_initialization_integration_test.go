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
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

type stateInitializationIntegrationResult struct {
	value         []byte
	didInitialize bool
	err           error
}

func newStateInitializationIntegrationServices(
	t *testing.T,
) (*Service, *Service, *sql.DB, func()) {
	t.Helper()
	dsn := os.Getenv("TRPC_AGENT_GO_MYSQL_TEST_DSN")
	if dsn == "" {
		t.Skip("set TRPC_AGENT_GO_MYSQL_TEST_DSN to run MySQL integration test")
	}
	prefix := fmt.Sprintf("si_%x", time.Now().UnixNano())
	serviceOptions := []ServiceOpt{
		WithMySQLClientDSN(dsn),
		WithTablePrefix(prefix),
		WithSessionTTL(3 * time.Second),
		WithCleanupInterval(time.Hour),
	}
	ownerService, err := NewService(serviceOptions...)
	require.NoError(t, err)
	waiterService, err := NewService(serviceOptions...)
	if err != nil {
		_ = ownerService.Close()
		require.NoError(t, err)
	}
	for _, service := range []*Service{ownerService, waiterService} {
		service.stateInitializationLeaseTTL = 600 * time.Millisecond
		service.stateInitializationRenewInterval = 100 * time.Millisecond
		service.stateInitializationPollMin = 10 * time.Millisecond
		service.stateInitializationPollMax = 40 * time.Millisecond
	}

	rawDB, err := sql.Open("mysql", dsn)
	if err != nil {
		_ = waiterService.Close()
		_ = ownerService.Close()
		require.NoError(t, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := rawDB.PingContext(ctx); err != nil {
		_ = rawDB.Close()
		_ = waiterService.Close()
		_ = ownerService.Close()
		require.NoError(t, err)
	}

	cleanup := func() {
		_ = waiterService.Close()
		_ = ownerService.Close()
		for _, table := range []string{
			ownerService.tableStateInitializationLeases,
			ownerService.tableSessionTracks,
			ownerService.tableSessionEvents,
			ownerService.tableSessionSummaries,
			ownerService.tableSessionStates,
			ownerService.tableAppStates,
			ownerService.tableUserStates,
		} {
			_, _ = rawDB.ExecContext(
				context.Background(),
				fmt.Sprintf("DROP TABLE IF EXISTS %s", table),
			)
		}
		_ = rawDB.Close()
	}
	return ownerService, waiterService, rawDB, cleanup
}

func newStateInitializationIntegrationKey() session.Key {
	return session.Key{
		AppName:   "state-initialization-test",
		UserID:    "user-" + uuid.NewString(),
		SessionID: "session-" + uuid.NewString(),
	}
}

func mysqlStateInitializationLeaseCount(
	t *testing.T,
	db *sql.DB,
	service *Service,
	key session.Key,
	stateKey string,
) int {
	t.Helper()
	coordinationKey := stateInitializationCoordinationKey(key, stateKey)
	var count int
	err := db.QueryRowContext(
		context.Background(),
		fmt.Sprintf(`SELECT COUNT(*) FROM %s
			WHERE coordination_key = ? AND user_id = ?`,
			service.tableStateInitializationLeases,
		),
		coordinationKey[:],
		key.UserID,
	).Scan(&count)
	require.NoError(t, err)
	return count
}

func TestLoadOrInitializeSessionStateMySQLIntegration(t *testing.T) {
	ownerService, waiterService, rawDB, cleanup := newStateInitializationIntegrationServices(t)
	defer cleanup()

	t.Run("concurrent creation preserves one generation", func(t *testing.T) {
		key := newStateInitializationIntegrationKey()
		const creators = 8
		startCreate := make(chan struct{})
		createResults := make(chan error, creators)
		for i := 0; i < creators; i++ {
			service := ownerService
			if i%2 != 0 {
				service = waiterService
			}
			go func() {
				<-startCreate
				_, err := service.CreateSession(context.Background(), key, nil)
				createResults <- err
			}()
		}
		close(startCreate)
		var created int
		for i := 0; i < creators; i++ {
			if err := <-createResults; err == nil {
				created++
			}
		}
		require.Equal(t, 1, created)

		var activeRows int
		err := rawDB.QueryRowContext(
			context.Background(),
			fmt.Sprintf(`SELECT COUNT(*) FROM %s
				WHERE app_name = ? AND user_id = ? AND session_id = ?
				AND deleted_at IS NULL AND state_initialization_active = 1`,
				ownerService.tableSessionStates,
			),
			key.AppName,
			key.UserID,
			key.SessionID,
		).Scan(&activeRows)
		require.NoError(t, err)
		require.Equal(t, 1, activeRows)

		startInitialize := make(chan struct{})
		initializeResults := make(chan stateInitializationIntegrationResult, 2)
		var callbackCalls atomic.Int32
		for _, service := range []*Service{ownerService, waiterService} {
			go func() {
				<-startInitialize
				value, initialized, err := service.LoadOrInitializeSessionState(
					context.Background(),
					key,
					"principal",
					func(value []byte) bool { return strings.HasPrefix(string(value), "principal-") },
					func(context.Context) ([]byte, error) {
						call := callbackCalls.Add(1)
						return []byte(fmt.Sprintf("principal-%d", call)), nil
					},
				)
				initializeResults <- stateInitializationIntegrationResult{value, initialized, err}
			}()
		}
		close(startInitialize)
		first := <-initializeResults
		second := <-initializeResults
		require.NoError(t, first.err)
		require.NoError(t, second.err)
		require.NotEqual(t, first.didInitialize, second.didInitialize)
		require.Equal(t, string(first.value), string(second.value))
		require.Equal(t, int32(1), callbackCalls.Load())
	})

	t.Run("cross instance convergence and renewal", func(t *testing.T) {
		key := newStateInitializationIntegrationKey()
		_, err := ownerService.CreateSession(context.Background(), key, session.StateMap{
			"cleared": []byte("stale"),
		})
		require.NoError(t, err)

		ownerStarted := make(chan struct{})
		releaseOwner := make(chan struct{})
		var callbackCalls atomic.Int32
		ownerDone := make(chan stateInitializationIntegrationResult, 1)
		go func() {
			value, initialized, ownerErr := ownerService.LoadOrInitializeSessionState(
				context.Background(),
				key,
				"canonical",
				func(value []byte) bool { return string(value) == "shared" },
				func(ctx context.Context) ([]byte, error) {
					callbackCalls.Add(1)
					close(ownerStarted)
					select {
					case <-releaseOwner:
						return []byte("shared"), nil
					case <-ctx.Done():
						return nil, ctx.Err()
					}
				},
				session.StateInitializationProjection{
					StateKey: "legacy",
					Project: func([]byte) ([]byte, error) {
						return []byte("legacy"), nil
					},
				},
				session.StateInitializationProjection{
					StateKey: "cleared",
					Project:  func([]byte) ([]byte, error) { return nil, nil },
				},
			)
			ownerDone <- stateInitializationIntegrationResult{value, initialized, ownerErr}
		}()
		select {
		case <-ownerStarted:
		case <-time.After(5 * time.Second):
			t.Fatal("owner callback did not start")
		}

		waiterDone := make(chan stateInitializationIntegrationResult, 1)
		go func() {
			value, initialized, waiterErr := waiterService.LoadOrInitializeSessionState(
				context.Background(),
				key,
				"canonical",
				func(value []byte) bool { return string(value) == "shared" },
				func(context.Context) ([]byte, error) {
					callbackCalls.Add(1)
					return []byte("waiter"), nil
				},
			)
			waiterDone <- stateInitializationIntegrationResult{value, initialized, waiterErr}
		}()

		// Keep the callback alive beyond the original lease TTL. The waiter must
		// still observe the same owner because renewal extends the logical lease.
		time.Sleep(900 * time.Millisecond)
		close(releaseOwner)
		ownerResult := <-ownerDone
		waiterResult := <-waiterDone
		require.NoError(t, ownerResult.err)
		require.NoError(t, waiterResult.err)
		require.True(t, ownerResult.didInitialize)
		require.False(t, waiterResult.didInitialize)
		require.Equal(t, "shared", string(ownerResult.value))
		require.Equal(t, "shared", string(waiterResult.value))
		require.Equal(t, int32(1), callbackCalls.Load())

		stored, err := waiterService.GetSession(context.Background(), key)
		require.NoError(t, err)
		require.NotNil(t, stored)
		canonical, present := stored.GetState("canonical")
		require.True(t, present)
		require.Equal(t, "shared", string(canonical))
		legacy, present := stored.GetState("legacy")
		require.True(t, present)
		require.Equal(t, "legacy", string(legacy))
		cleared, present := stored.GetState("cleared")
		require.True(t, present)
		require.Nil(t, cleared)
		require.Zero(t, mysqlStateInitializationLeaseCount(
			t,
			rawDB,
			ownerService,
			key,
			"canonical",
		))
	})

	t.Run("waiter cancellation and owner cleanup", func(t *testing.T) {
		key := newStateInitializationIntegrationKey()
		_, err := ownerService.CreateSession(context.Background(), key, nil)
		require.NoError(t, err)

		ownerStarted := make(chan struct{})
		releaseOwner := make(chan struct{})
		ownerDone := make(chan error, 1)
		go func() {
			_, _, ownerErr := ownerService.LoadOrInitializeSessionState(
				context.Background(),
				key,
				"state",
				func(value []byte) bool { return string(value) == "owner" },
				func(ctx context.Context) ([]byte, error) {
					close(ownerStarted)
					select {
					case <-releaseOwner:
						return []byte("owner"), nil
					case <-ctx.Done():
						return nil, ctx.Err()
					}
				},
			)
			ownerDone <- ownerErr
		}()
		<-ownerStarted

		waiterCtx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
		defer cancel()
		_, initialized, err := waiterService.LoadOrInitializeSessionState(
			waiterCtx,
			key,
			"state",
			func(value []byte) bool { return string(value) == "owner" },
			func(context.Context) ([]byte, error) { return []byte("waiter"), nil },
		)
		require.ErrorIs(t, err, context.DeadlineExceeded)
		require.False(t, initialized)
		close(releaseOwner)
		require.NoError(t, <-ownerDone)
		require.Zero(t, mysqlStateInitializationLeaseCount(
			t,
			rawDB,
			ownerService,
			key,
			"state",
		))
	})

	t.Run("callback failure panic and projection failure release ownership", func(t *testing.T) {
		key := newStateInitializationIntegrationKey()
		_, err := ownerService.CreateSession(context.Background(), key, nil)
		require.NoError(t, err)
		wantCallbackErr := errors.New("callback failed")
		_, initialized, err := ownerService.LoadOrInitializeSessionState(
			context.Background(),
			key,
			"state",
			func(value []byte) bool { return string(value) == "valid" },
			func(context.Context) ([]byte, error) { return nil, wantCallbackErr },
		)
		require.ErrorIs(t, err, wantCallbackErr)
		require.False(t, initialized)
		require.Zero(t, mysqlStateInitializationLeaseCount(
			t,
			rawDB,
			ownerService,
			key,
			"state",
		))

		require.Panics(t, func() {
			_, _, _ = ownerService.LoadOrInitializeSessionState(
				context.Background(),
				key,
				"state",
				func(value []byte) bool { return string(value) == "valid" },
				func(context.Context) ([]byte, error) { panic("boom") },
			)
		})
		require.Zero(t, mysqlStateInitializationLeaseCount(
			t,
			rawDB,
			ownerService,
			key,
			"state",
		))

		wantProjectionErr := errors.New("projection failed")
		_, initialized, err = ownerService.LoadOrInitializeSessionState(
			context.Background(),
			key,
			"state",
			func(value []byte) bool { return string(value) == "valid" },
			func(context.Context) ([]byte, error) { return []byte("valid"), nil },
			session.StateInitializationProjection{
				StateKey: "projection",
				Project: func([]byte) ([]byte, error) {
					return nil, wantProjectionErr
				},
			},
		)
		require.ErrorIs(t, err, wantProjectionErr)
		require.False(t, initialized)
		stored, err := ownerService.GetSession(context.Background(), key)
		require.NoError(t, err)
		_, present := stored.GetState("state")
		require.False(t, present)
		_, present = stored.GetState("projection")
		require.False(t, present)
	})

	t.Run("ownership loss cancels callback and fences stale commit", func(t *testing.T) {
		key := newStateInitializationIntegrationKey()
		_, err := ownerService.CreateSession(context.Background(), key, nil)
		require.NoError(t, err)
		ownerStarted := make(chan struct{})
		ownerDone := make(chan error, 1)
		go func() {
			_, _, ownerErr := ownerService.LoadOrInitializeSessionState(
				context.Background(),
				key,
				"state",
				func(value []byte) bool { return string(value) == "stale" },
				func(ctx context.Context) ([]byte, error) {
					close(ownerStarted)
					<-ctx.Done()
					return []byte("stale"), ctx.Err()
				},
			)
			ownerDone <- ownerErr
		}()
		<-ownerStarted
		coordinationKey := stateInitializationCoordinationKey(key, "state")
		result, err := rawDB.ExecContext(
			context.Background(),
			fmt.Sprintf(`UPDATE %s
				SET owner_token = ?, expires_at = TIMESTAMPADD(SECOND, 5, CURRENT_TIMESTAMP(6))
				WHERE coordination_key = ? AND user_id = ?`,
				ownerService.tableStateInitializationLeases,
			),
			"replacement-owner",
			coordinationKey[:],
			key.UserID,
		)
		require.NoError(t, err)
		rowsAffected, err := result.RowsAffected()
		require.NoError(t, err)
		require.Equal(t, int64(1), rowsAffected)
		err = <-ownerDone
		require.Error(t, err)
		require.True(t,
			errors.Is(err, context.Canceled) ||
				strings.Contains(err.Error(), "ownership lost"),
		)
		stored, err := ownerService.GetSession(context.Background(), key)
		require.NoError(t, err)
		_, present := stored.GetState("state")
		require.False(t, present)
		_, err = rawDB.ExecContext(
			context.Background(),
			fmt.Sprintf(`DELETE FROM %s WHERE coordination_key = ? AND user_id = ?`,
				ownerService.tableStateInitializationLeases,
			),
			coordinationKey[:],
			key.UserID,
		)
		require.NoError(t, err)
	})

	t.Run("expired session recreation reusing row id is fenced", func(t *testing.T) {
		key := newStateInitializationIntegrationKey()
		_, err := ownerService.CreateSession(context.Background(), key, nil)
		require.NoError(t, err)
		var oldRowID int64
		var oldCreatedAt time.Time
		err = rawDB.QueryRowContext(
			context.Background(),
			fmt.Sprintf(`SELECT id, created_at FROM %s
				WHERE app_name = ? AND user_id = ? AND session_id = ? AND deleted_at IS NULL`,
				ownerService.tableSessionStates,
			),
			key.AppName,
			key.UserID,
			key.SessionID,
		).Scan(&oldRowID, &oldCreatedAt)
		require.NoError(t, err)

		ownerStarted := make(chan struct{})
		releaseOwner := make(chan struct{})
		ownerDone := make(chan error, 1)
		go func() {
			_, _, ownerErr := ownerService.LoadOrInitializeSessionState(
				context.Background(),
				key,
				"state",
				func(value []byte) bool { return string(value) == "old" },
				func(context.Context) ([]byte, error) {
					close(ownerStarted)
					<-releaseOwner
					return []byte("old"), nil
				},
			)
			ownerDone <- ownerErr
		}()
		<-ownerStarted
		_, err = rawDB.ExecContext(
			context.Background(),
			fmt.Sprintf(`UPDATE %s SET expires_at = TIMESTAMPADD(SECOND, -1, CURRENT_TIMESTAMP(6))
				WHERE id = ? AND user_id = ?`, ownerService.tableSessionStates),
			oldRowID,
			key.UserID,
		)
		require.NoError(t, err)
		_, err = waiterService.CreateSession(context.Background(), key, nil)
		require.NoError(t, err)
		var newRowID int64
		var newCreatedAt time.Time
		err = rawDB.QueryRowContext(
			context.Background(),
			fmt.Sprintf(`SELECT id, created_at FROM %s
				WHERE app_name = ? AND user_id = ? AND session_id = ? AND deleted_at IS NULL`,
				ownerService.tableSessionStates,
			),
			key.AppName,
			key.UserID,
			key.SessionID,
		).Scan(&newRowID, &newCreatedAt)
		require.NoError(t, err)
		require.Equal(t, oldRowID, newRowID)
		require.False(t, oldCreatedAt.Equal(newCreatedAt))
		close(releaseOwner)
		require.ErrorIs(t, <-ownerDone, errStateInitializationGenerationChanged)

		stored, err := waiterService.GetSession(context.Background(), key)
		require.NoError(t, err)
		_, present := stored.GetState("state")
		require.False(t, present)
		value, initialized, err := waiterService.LoadOrInitializeSessionState(
			context.Background(),
			key,
			"state",
			func(value []byte) bool { return string(value) == "new" },
			func(context.Context) ([]byte, error) { return []byte("new"), nil },
		)
		require.NoError(t, err)
		require.True(t, initialized)
		require.Equal(t, "new", string(value))
	})

	t.Run("expired lease takeover and session ttl refresh", func(t *testing.T) {
		key := newStateInitializationIntegrationKey()
		_, err := ownerService.CreateSession(context.Background(), key, nil)
		require.NoError(t, err)
		var rowID int64
		var createdAt time.Time
		err = rawDB.QueryRowContext(
			context.Background(),
			fmt.Sprintf(`SELECT id, created_at FROM %s
				WHERE app_name = ? AND user_id = ? AND session_id = ? AND deleted_at IS NULL`,
				ownerService.tableSessionStates,
			),
			key.AppName,
			key.UserID,
			key.SessionID,
		).Scan(&rowID, &createdAt)
		require.NoError(t, err)
		coordinationKey := stateInitializationCoordinationKey(key, "state")
		_, err = rawDB.ExecContext(
			context.Background(),
			fmt.Sprintf(`INSERT INTO %s
				(coordination_key, user_id, owner_token, session_row_id,
				 session_created_at, expires_at, updated_at)
				VALUES (?, ?, ?, ?, ?, TIMESTAMPADD(SECOND, -1, CURRENT_TIMESTAMP(6)), CURRENT_TIMESTAMP(6))`,
				ownerService.tableStateInitializationLeases,
			),
			coordinationKey[:],
			key.UserID,
			"expired-owner",
			rowID,
			createdAt,
		)
		require.NoError(t, err)
		_, err = rawDB.ExecContext(
			context.Background(),
			fmt.Sprintf(`UPDATE %s
				SET expires_at = TIMESTAMPADD(SECOND, 1, CURRENT_TIMESTAMP(6))
				WHERE id = ? AND user_id = ?`, ownerService.tableSessionStates),
			rowID,
			key.UserID,
		)
		require.NoError(t, err)

		value, initialized, err := waiterService.LoadOrInitializeSessionState(
			context.Background(),
			key,
			"state",
			func(value []byte) bool { return string(value) == "value" },
			func(context.Context) ([]byte, error) { return []byte("value"), nil },
		)
		require.NoError(t, err)
		require.True(t, initialized)
		require.Equal(t, "value", string(value))
		var remainingMicros int64
		err = rawDB.QueryRowContext(
			context.Background(),
			fmt.Sprintf(`SELECT TIMESTAMPDIFF(MICROSECOND, CURRENT_TIMESTAMP(6), expires_at)
				FROM %s WHERE id = ? AND user_id = ?`, ownerService.tableSessionStates),
			rowID,
			key.UserID,
		).Scan(&remainingMicros)
		require.NoError(t, err)
		require.Greater(t, remainingMicros, int64(1500*time.Millisecond/time.Microsecond))
		require.Zero(t, mysqlStateInitializationLeaseCount(
			t,
			rawDB,
			ownerService,
			key,
			"state",
		))
	})
}
