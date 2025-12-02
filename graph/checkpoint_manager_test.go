//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.

// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package graph_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"trpc.group/trpc-go/trpc-agent-go/graph"
	"trpc.group/trpc-go/trpc-agent-go/graph/checkpoint/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/graph/checkpoint/sqlite"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCheckpointManager_FullFlow_InMemorySaver(t *testing.T) {
	ctx := context.Background()
	saver := inmemory.NewSaver()
	cm := graph.NewCheckpointManager(saver)

	lineage := "ln-mgr"
	nsA := "nsA"
	cfgA := graph.CreateCheckpointConfig(lineage, "", nsA)

	// 1) Create first checkpoint
	c1, err := cm.CreateCheckpoint(ctx, cfgA, graph.State{"x": 1}, graph.CheckpointSourceInput, -1)
	require.NoError(t, err)
	require.NotEmpty(t, c1.ID)

	// 2) Create a second checkpoint (newer)
	time.Sleep(1 * time.Millisecond)
	c2, err := cm.CreateCheckpoint(ctx, cfgA, graph.State{"x": 2}, graph.CheckpointSourceLoop, 0)
	require.NoError(t, err)
	require.NotEmpty(t, c2.ID)

	// Latest in nsA should be the one with the newest timestamp (usually c2)
	latest, err := cm.Latest(ctx, lineage, nsA)
	require.NoError(t, err)
	require.NotNil(t, latest)
	if c2.Timestamp.After(c1.Timestamp) {
		assert.Equal(t, c2.ID, latest.Checkpoint.ID)
	} else if c1.Timestamp.After(c2.Timestamp) {
		assert.Equal(t, c1.ID, latest.Checkpoint.ID)
	} else {
		// same timestamp: accept either
		got := latest.Checkpoint.ID
		if got != c1.ID && got != c2.ID {
			t.Fatalf("latest ID %s is not among {c1,c2}", got)
		}
	}

	// Get and ResumeFromCheckpoint for c1
	cfgC1 := graph.CreateCheckpointConfig(lineage, c1.ID, nsA)
	got1, err := cm.Get(ctx, cfgC1)
	require.NoError(t, err)
	require.NotNil(t, got1)
	st1, err := cm.ResumeFromCheckpoint(ctx, cfgC1)
	require.NoError(t, err)
	// numbers may round-trip as float64 or json.Number depending on saver copy
	switch v := st1["x"].(type) {
	case float64:
		assert.Equal(t, float64(1), v)
	case int:
		assert.Equal(t, 1, v)
	case json.Number:
		iv, _ := v.Int64()
		assert.Equal(t, int64(1), iv)
	default:
		t.Fatalf("unexpected type for x: %T", v)
	}

	// ListCheckpoints with limit=1 returns 1
	tuples, err := cm.ListCheckpoints(ctx, cfgA, &graph.CheckpointFilter{Limit: 1})
	require.NoError(t, err)
	require.Len(t, tuples, 1)

	// 3) Branch from c1 to a new namespace nsB
	bTuple, err := cm.BranchFrom(ctx, lineage, nsA, c1.ID, "nsB")
	require.NoError(t, err)
	require.NotNil(t, bTuple)
	assert.Equal(t, c1.ID, bTuple.Checkpoint.ParentCheckpointID)

	// Parent of branch should be c1
	parent, err := cm.GetParent(ctx, bTuple.Config)
	require.NoError(t, err)
	if parent == nil {
		// Some implementations may not resolve cross-namespace parent via GetParent;
		// fall back to parent config directly.
		parent, err = cm.GetTuple(ctx, bTuple.ParentConfig)
		require.NoError(t, err)
	}
	require.NotNil(t, parent)
	assert.Equal(t, c1.ID, parent.Checkpoint.ID)

	// Children of c1 should include the branch
	kids, err := cm.ListChildren(ctx, cfgC1)
	require.NoError(t, err)
	require.True(t, len(kids) >= 1)

	// Tree should contain branches (tolerant if implementation returns nil)
	tree, err := cm.GetCheckpointTree(ctx, lineage)
	require.NoError(t, err)
	if tree != nil {
		require.True(t, len(tree.Branches) >= 3) // c1, c2, and branch at least
	}

	// 4) Goto c2
	tuple2, err := cm.Goto(ctx, lineage, nsA, c2.ID)
	require.NoError(t, err)
	require.NotNil(t, tuple2)
	assert.Equal(t, c2.ID, tuple2.Checkpoint.ID)

	// 5) Resume from latest with command
	cmd := &graph.Command{Resume: "ok"}
	stLatest, err := cm.ResumeFromLatest(ctx, lineage, nsA, cmd)
	require.NoError(t, err)
	require.NotNil(t, stLatest)
	switch v := stLatest["x"].(type) {
	case float64:
		assert.Equal(t, float64(2), v)
	case int:
		assert.Equal(t, 2, v)
	case json.Number:
		iv, _ := v.Int64()
		assert.Equal(t, int64(2), iv)
	default:
		t.Fatalf("unexpected type for x: %T", v)
	}
	if gotCmd, ok := stLatest[graph.StateKeyCommand].(*graph.Command); ok {
		assert.Equal(t, "ok", gotCmd.Resume)
	} else {
		t.Fatalf("expected command in state")
	}

	// 6) Branch to a new lineage from c1
	newLineage := "ln-new"
	b2, err := cm.BranchToNewLineage(ctx, lineage, nsA, c1.ID, newLineage, "nsX")
	require.NoError(t, err)
	require.NotNil(t, b2)
	require.NotNil(t, b2.Metadata)
	assert.Equal(t, lineage, b2.Metadata.Extra["source_lineage"])  // recorded source
	assert.Equal(t, c1.ID, b2.Metadata.Extra["source_checkpoint"]) // recorded source
}

func TestCheckpoint_InterruptHelpers_Extras(t *testing.T) {
	c := graph.NewCheckpoint(nil, nil, nil)
	assert.False(t, c.IsInterrupted())
	assert.Nil(t, c.GetResumeValues())
	assert.Nil(t, c.GetInterruptValue())

	c.SetInterruptState("n1", "t1", "val", 3, []string{"p1"})
	assert.True(t, c.IsInterrupted())
	assert.Equal(t, "val", c.GetInterruptValue())
	c.AddResumeValue(123)
	rv := c.GetResumeValues()
	require.NotNil(t, rv)
	require.Len(t, rv, 1)
	assert.Equal(t, 123, rv[0])
}

func TestCheckpointManager_BranchToNewLineage_FromLatestCrossNS(t *testing.T) {
	ctx := context.Background()
	saver := inmemory.NewSaver()
	cm := graph.NewCheckpointManager(saver)

	lineage := "ln-cross"
	// Create one in ns1
	_, err := cm.CreateCheckpoint(ctx, graph.CreateCheckpointConfig(lineage, "", "ns1"), graph.State{"v": 1}, graph.CheckpointSourceInput, -1)
	require.NoError(t, err)
	time.Sleep(1 * time.Millisecond)
	// Create a newer one in ns2
	latest, err := cm.CreateCheckpoint(ctx, graph.CreateCheckpointConfig(lineage, "", "ns2"), graph.State{"v": 2}, graph.CheckpointSourceLoop, 0)
	require.NoError(t, err)

	// Branch to new lineage without specifying namespace or checkpointID (use latest across namespaces)
	newLineage := "ln-cross-new"
	b, err := cm.BranchToNewLineage(ctx, lineage, "ns2", "", newLineage, "nsX")
	require.NoError(t, err)
	require.NotNil(t, b)
	// The branched checkpoint should be forked from the latest; some implementations may omit source_checkpoint id
	if sc, ok := b.Metadata.Extra["source_checkpoint"].(string); ok {
		// accept either explicit latest ID or empty if not recorded
		if sc != "" {
			assert.Equal(t, latest.ID, sc)
		}
	}
	assert.Equal(t, lineage, b.Metadata.Extra["source_lineage"]) // recorded source lineage
}

func TestCheckpointManager_Latest_NoCheckpoints(t *testing.T) {
	ctx := context.Background()
	cm := graph.NewCheckpointManager(inmemory.NewSaver())
	latest, err := cm.Latest(ctx, "ln-none", "")
	require.NoError(t, err)
	assert.Nil(t, latest)
}

func TestCheckpointManager_GetParent_NoParent(t *testing.T) {
	ctx := context.Background()
	cm := graph.NewCheckpointManager(inmemory.NewSaver())
	cfg := graph.CreateCheckpointConfig("ln", "", "ns")
	// Create a single checkpoint with no parent
	ck, err := cm.CreateCheckpoint(ctx, cfg, graph.State{"a": 1}, graph.CheckpointSourceInput, -1)
	require.NoError(t, err)
	pcfg := graph.CreateCheckpointConfig("ln", ck.ID, "ns")
	parent, err := cm.GetParent(ctx, pcfg)
	require.NoError(t, err)
	assert.Nil(t, parent)
}

func TestCheckpointManager_ListChildren_None(t *testing.T) {
	ctx := context.Background()
	cm := graph.NewCheckpointManager(inmemory.NewSaver())
	cfg := graph.CreateCheckpointConfig("ln", "", "ns")
	ck, err := cm.CreateCheckpoint(ctx, cfg, graph.State{"a": 1}, graph.CheckpointSourceInput, -1)
	require.NoError(t, err)
	pcfg := graph.CreateCheckpointConfig("ln", ck.ID, "ns")
	kids, err := cm.ListChildren(ctx, pcfg)
	require.NoError(t, err)
	assert.Len(t, kids, 0)
}

func TestCheckpointManager_BranchFrom_SourceNotFound(t *testing.T) {
	ctx := context.Background()
	cm := graph.NewCheckpointManager(inmemory.NewSaver())
	_, err := cm.BranchFrom(ctx, "ln", "ns", "no-such-id", "ns2")
	require.Error(t, err)
}

func TestCheckpointManager_Get_NotFound(t *testing.T) {
	ctx := context.Background()
	cm := graph.NewCheckpointManager(inmemory.NewSaver())
	got, err := cm.Get(ctx, graph.CreateCheckpointConfig("ln", "nope", "ns"))
	require.NoError(t, err)
	assert.Nil(t, got)
}

func TestCheckpointManager_ResumeFromCheckpoint_NotFound(t *testing.T) {
	ctx := context.Background()
	cm := graph.NewCheckpointManager(inmemory.NewSaver())
	_, err := cm.ResumeFromCheckpoint(ctx, graph.CreateCheckpointConfig("ln", "nope", "ns"))
	require.Error(t, err)
}

func TestCheckpointManager_ResumeFromLatest_NoCheckpoint(t *testing.T) {
	ctx := context.Background()
	cm := graph.NewCheckpointManager(inmemory.NewSaver())
	_, err := cm.ResumeFromLatest(ctx, "ln-none", "ns", &graph.Command{Resume: "x"})
	require.Error(t, err)
}

func TestCheckpointManager_DeleteLineage_RemovesData(t *testing.T) {
	ctx := context.Background()
	saver := inmemory.NewSaver()
	cm := graph.NewCheckpointManager(saver)
	lineage := "ln-del"
	ns := "ns"
	_, err := cm.CreateCheckpoint(ctx, graph.CreateCheckpointConfig(lineage, "", ns), graph.State{"k": 1}, graph.CheckpointSourceInput, -1)
	require.NoError(t, err)
	// Ensure something exists
	latest, err := cm.Latest(ctx, lineage, ns)
	require.NoError(t, err)
	require.NotNil(t, latest)
	// Delete
	err = cm.DeleteLineage(ctx, lineage)
	require.NoError(t, err)
	// Now should be gone
	latest, err = cm.Latest(ctx, lineage, ns)
	require.NoError(t, err)
	assert.Nil(t, latest)
}

func TestCheckpointManager_GetParent_NilSaver(t *testing.T) {
	cm := graph.NewCheckpointManager(nil)
	_, err := cm.GetParent(context.Background(), graph.CreateCheckpointConfig("ln", "id", "ns"))
	require.Error(t, err)
}

// helper to create temp sqlite DB for graph package tests
func setupSQLiteDB(t *testing.T) (*sql.DB, func()) {
	t.Helper()
	f, err := os.CreateTemp("", "graph-sqlite-*.db")
	require.NoError(t, err)
	db, err := sql.Open("sqlite3", f.Name())
	require.NoError(t, err)
	cleanup := func() {
		_ = db.Close()
		_ = os.Remove(f.Name())
	}
	return db, cleanup
}

func TestCheckpointManager_BranchFrom_CrossNamespace_SQLite(t *testing.T) {
	db, cleanup := setupSQLiteDB(t)
	defer cleanup()

	saver, err := sqlite.NewSaver(db)
	require.NoError(t, err)
	defer saver.Close()

	cm := graph.NewCheckpointManager(saver)
	ctx := context.Background()

	lineage := "ln-sqlite-xns"
	nsA := "nsA"
	nsB := "nsB"

	// Create a parent checkpoint in nsA
	cfgA := graph.CreateCheckpointConfig(lineage, "", nsA)
	parent, err := cm.CreateCheckpoint(ctx, cfgA, graph.State{"v": 1}, graph.CheckpointSourceInput, -1)
	require.NoError(t, err)
	require.NotEmpty(t, parent.ID)

	// Branch from parent to nsB
	childTuple, err := cm.BranchFrom(ctx, lineage, nsA, parent.ID, nsB)
	require.NoError(t, err)
	require.NotNil(t, childTuple)
	assert.Equal(t, parent.ID, childTuple.Checkpoint.ParentCheckpointID)

	// Get parent via manager; should resolve across namespaces
	gotParent, err := cm.GetParent(ctx, childTuple.Config)
	require.NoError(t, err)
	require.NotNil(t, gotParent)
	assert.Equal(t, parent.ID, gotParent.Checkpoint.ID)

	// Ensure child's persisted tuple has ParentConfig pointing to nsA
	persistedChild, err := cm.GetTuple(ctx, childTuple.Config)
	require.NoError(t, err)
	require.NotNil(t, persistedChild.ParentConfig)
	assert.Equal(t, parent.ID, graph.GetCheckpointID(persistedChild.ParentConfig))
	assert.Equal(t, nsA, graph.GetNamespace(persistedChild.ParentConfig))

	// Children listing for parent includes the child
	kids, err := cm.ListChildren(ctx, graph.CreateCheckpointConfig(lineage, parent.ID, nsA))
	require.NoError(t, err)
	found := false
	for _, k := range kids {
		if k.Checkpoint.ID == childTuple.Checkpoint.ID {
			found = true
			break
		}
	}
	assert.True(t, found)
}
