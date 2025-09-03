package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/graph"
	"trpc.group/trpc-go/trpc-agent-go/graph/checkpoint/sqlite"
)

func main() {
	fmt.Println("🚀 SQLite Checkpoint Saver Example")
	fmt.Println("==================================")

	// 1. Initialize SQLite database.
	db, err := initDatabase()
	if err != nil {
		log.Fatalf("database init failed: %v", err)
	}
	defer db.Close()

	// 2. Run database migrations.
	if err := runMigrations(db); err != nil {
		log.Fatalf("database migration failed: %v", err)
	}

	// 3. Create SQLite saver.
	saver, err := sqlite.NewSaver(db)
	if err != nil {
		log.Fatalf("create SQLite saver failed: %v", err)
	}
	defer saver.Close()

	// 4. Demonstrate checkpoint operations.
	demoCheckpointOperations(saver)

	// 5. Demonstrate pagination and filtering.
	demoPaginationAndFiltering(saver)

	fmt.Println("\n✅ SQLite example finished")
}

// initDatabase initializes the SQLite database.
func initDatabase() (*sql.DB, error) {
	// Note: This example requires the SQLite driver.
	// In a real environment, please install: go get github.com/mattn/go-sqlite3
	// Or use another database driver.

	// Returning nil here indicates the SQLite driver is required.
	return nil, fmt.Errorf("SQLite driver not installed, run: go get github.com/mattn/go-sqlite3")
}

// runMigrations runs database migrations.
func runMigrations(db *sql.DB) error {
	fmt.Println("📊 Running database migrations...")

	// Check whether we need to add the seq column (backward compatible).
	var hasSeq bool
	row := db.QueryRow("PRAGMA table_info(checkpoint_writes)")
	for {
		var cid, notnull, pk int
		var name, typ string
		var dfltValue interface{}
		if err := row.Scan(&cid, &name, &typ, &notnull, &dfltValue, &pk); err != nil {
			break
		}
		if name == "seq" {
			hasSeq = true
			break
		}
	}

	if !hasSeq {
		fmt.Println("  ➕ Adding seq column...")
		if _, err := db.Exec("ALTER TABLE checkpoint_writes ADD COLUMN seq INTEGER NOT NULL DEFAULT 0"); err != nil {
			return fmt.Errorf("add seq column failed: %w", err)
		}
	}

	fmt.Println("  ✅ Migrations completed")
	return nil
}

// demoCheckpointOperations demonstrates checkpoint operations.
func demoCheckpointOperations(saver graph.CheckpointSaver) {
	fmt.Println("\n📝 Demonstrating checkpoint operations...")

	// Create config.
	config := graph.CreateCheckpointConfig("demo_lineage", "", "demo:prod:workflow")

	// Create checkpoint.
	checkpoint := graph.NewCheckpoint(nil, nil, nil)
	checkpoint.ChannelValues = map[string]any{
		"user_input": "Hello, SQLite!",
		"step":       1,
	}

	// Create metadata.
	metadata := graph.NewCheckpointMetadata(graph.CheckpointSourceUpdate, 1)
	metadata.Extra = map[string]any{
		"environment": "production",
		"version":     "1.0.0",
	}

	// Create pending writes.
	pendingWrites := []graph.PendingWrite{
		{
			Channel:  "output:result",
			Value:    "Processed: Hello, SQLite!",
			TaskID:   "task_001",
			Sequence: 1,
		},
		{
			Channel:  "output:status",
			Value:    "completed",
			TaskID:   "task_001",
			Sequence: 2,
		},
	}

	// Use PutFull to save atomically.
	updatedConfig, err := saver.PutFull(context.Background(), graph.PutFullRequest{
		Config:        config,
		Checkpoint:    checkpoint,
		Metadata:      metadata,
		PendingWrites: pendingWrites,
	})
	if err != nil {
		log.Printf("save checkpoint failed: %v", err)
		return
	}

	fmt.Printf("  ✅ Checkpoint saved, ID: %s\n", graph.GetCheckpointID(updatedConfig))

	// Read checkpoint.
	tuple, err := saver.GetTuple(context.Background(), updatedConfig)
	if err != nil {
		log.Printf("read checkpoint failed: %v", err)
		return
	}

	if tuple != nil {
		fmt.Printf("  📖 Checkpoint read succeeded:\n")
		fmt.Printf("    - Channel values: %v\n", tuple.Checkpoint.ChannelValues)
		fmt.Printf("    - Pending writes: %d\n", len(tuple.PendingWrites))
		fmt.Printf("    - Metadata: %v\n", tuple.Metadata.Extra)
	}
}

// demoPaginationAndFiltering demonstrates pagination and filtering.
func demoPaginationAndFiltering(saver graph.CheckpointSaver) {
	fmt.Println("\n📄 Demonstrating pagination and filtering...")

	// Create multiple checkpoints for pagination demo.
	lineageID := "pagination_demo"
	namespace := "demo:test:pagination"

	for i := 1; i <= 15; i++ {
		config := graph.CreateCheckpointConfig(lineageID, "", namespace)
		checkpoint := graph.NewCheckpoint(nil, nil, nil)
		checkpoint.ChannelValues = map[string]any{
			"step":      i,
			"message":   fmt.Sprintf("Step %d", i),
			"timestamp": time.Now().Unix(),
		}

		metadata := graph.NewCheckpointMetadata(graph.CheckpointSourceUpdate, i)
		metadata.Extra = map[string]any{
			"batch": i/5 + 1,
		}

		_, err := saver.PutFull(context.Background(), graph.PutFullRequest{
			Config:        config,
			Checkpoint:    checkpoint,
			Metadata:      metadata,
			PendingWrites: []graph.PendingWrite{},
		})
		if err != nil {
			log.Printf("create checkpoint %d failed: %v", i, err)
		}
	}

	fmt.Printf("  ✅ Created 15 checkpoints\n")

	// Demonstrate pagination query.
	baseConfig := graph.CreateCheckpointConfig(lineageID, "", namespace)

	// First page: get the first 5.
	fmt.Println("\n  📖 First page (first 5):")
	checkpoints, err := saver.List(context.Background(), baseConfig, &graph.CheckpointFilter{
		Limit: 5,
	})
	if err != nil {
		log.Printf("query first page failed: %v", err)
		return
	}

	for i, cp := range checkpoints {
		if i >= 5 {
			break
		}
		step := cp.Checkpoint.ChannelValues["step"]
		fmt.Printf("    %d. Checkpoint %s (step %v)\n", i+1, graph.GetCheckpointID(cp.Config), step)
	}

	// Use Before filter to get the next page.
	if len(checkpoints) > 0 {
		lastCheckpoint := checkpoints[len(checkpoints)-1]
		fmt.Printf("\n  📖 Next page (Before %s):\n", graph.GetCheckpointID(lastCheckpoint.Config))

		nextPage, err := saver.List(context.Background(), baseConfig, &graph.CheckpointFilter{
			Before: lastCheckpoint.Config,
			Limit:  5,
		})
		if err != nil {
			log.Printf("query next page failed: %v", err)
			return
		}

		for i, cp := range nextPage {
			if i >= 5 {
				break
			}
			step := cp.Checkpoint.ChannelValues["step"]
			fmt.Printf("    %d. Checkpoint %s (step %v)\n", i+1, graph.GetCheckpointID(cp.Config), step)
		}
	}

	// Demonstrate metadata filtering.
	fmt.Printf("\n  🔍 Filter by batch (batch=1):\n")
	batch1Checkpoints, err := saver.List(context.Background(), baseConfig, &graph.CheckpointFilter{
		Metadata: map[string]any{
			"batch": 1,
		},
		Limit: 10,
	})
	if err != nil {
		log.Printf("filter by batch failed: %v", err)
		return
	}

	for i, cp := range batch1Checkpoints {
		if i >= 5 {
			break
		}
		step := cp.Checkpoint.ChannelValues["step"]
		fmt.Printf("    %d. Checkpoint %s (step %v)\n", i+1, graph.GetCheckpointID(cp.Config), step)
	}
}
