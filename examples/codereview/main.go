// Package main demonstrates a code review agent using trpc-agent-go with Hy3.
package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

var (
	apiKey  = getEnv("TRPC_AGENT_API_KEY", "EMPTY")
	baseURL = getEnv("TRPC_AGENT_BASE_URL", "http://127.0.0.1:8000/v1")
	modelID = getEnv("TRPC_AGENT_MODEL_NAME", "hy3")
	fileArg = flag.String("file", "", "Path to code file to review")
)

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// ---- Database ----

func initDB() (*sql.DB, error) {
	db, err := sql.Open("sqlite3", "code_reviews.db")
	if err != nil {
		return nil, err
	}
	_, err = db.Exec(`CREATE TABLE IF NOT EXISTS reviews (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		file_path TEXT, summary TEXT, bugs TEXT,
		improvements TEXT, score INTEGER, created_at TEXT)`)
	return db, err
}

// ---- Tools ----

type ReviewInput struct {
	Code     string `json:"code" description:"Source code to review"`
	FilePath string `json:"file_path" description:"Path to the file"`
}

type ReviewOutput struct {
	Summary      string `json:"summary"`
	Bugs         string `json:"bugs"`
	Improvements string `json:"improvements"`
	Score        int    `json:"score"`
}

func reviewCode(ctx context.Context, input ReviewInput) (ReviewOutput, error) {
	// The actual review is performed by the LLM through its instruction.
	// This tool structures the input for the agent.
	return ReviewOutput{
		Summary:      "Review requested",
		Bugs:         "",
		Improvements: "",
		Score:        0,
	}, nil
}

type SaveInput struct {
	FilePath     string `json:"file_path"`
	Summary      string `json:"summary"`
	Bugs         string `json:"bugs"`
	Improvements string `json:"improvements"`
	Score        int    `json:"score"`
}

func saveReview(ctx context.Context, input SaveInput) (string, error) {
	db, err := initDB()
	if err != nil {
		return "", fmt.Errorf("db open: %w", err)
	}
	defer db.Close()

	_, err = db.Exec(
		"INSERT INTO reviews (file_path, summary, bugs, improvements, score, created_at) VALUES (?, ?, ?, ?, ?, ?)",
		input.FilePath, input.Summary, input.Bugs, input.Improvements, input.Score, time.Now().Format(time.RFC3339),
	)
	if err != nil {
		return "", fmt.Errorf("insert: %w", err)
	}

	var count int
	db.QueryRow("SELECT COUNT(*) FROM reviews").Scan(&count)
	return fmt.Sprintf("Review saved. Total reviews: %d", count), nil
}

// ---- Agent ----

func main() {
	flag.Parse()

	// Model
	llm := openai.New(modelID,
		openai.WithAPIKey(apiKey),
		openai.WithBaseURL(baseURL),
	)

	// Tools
	reviewTool := function.NewFunctionTool(reviewCode,
		function.WithName("review_code"),
		function.WithDescription("Submit code for review"))
	saveTool := function.NewFunctionTool(saveReview,
		function.WithName("save_review"),
		function.WithDescription("Save review results to database"))

	// Agent
	genConfig := model.GenerationConfig{
		MaxTokens:   intPtr(2048),
		Temperature: floatPtr(0.3),
	}

	agent := llmagent.New("code-reviewer",
		llmagent.WithModel(llm),
		llmagent.WithDescription("AI-powered code review agent backed by Hy3"),
		llmagent.WithInstruction(`You are an expert code reviewer.
When given code, use review_code to structure the input,
then provide a detailed review covering:
1. Summary 2. Bugs 3. Style 4. Security 5. Improvements
Finally use save_review with a score from 0-10 to persist results.`),
		llmagent.WithTools([]tool.Tool{reviewTool, saveTool}),
		llmagent.WithGenerationConfig(genConfig),
	)

	// Runner
	r := runner.NewRunner("code-review-app", agent)

	// Run
	ctx := context.Background()
	if *fileArg != "" {
		code, err := os.ReadFile(*fileArg)
		if err != nil {
			log.Fatalf("read file: %v", err)
		}
		prompt := fmt.Sprintf("Review this code (%s):\n```\n%s\n```", *fileArg, string(code))
		events, err := r.Run(ctx, prompt)
		if err != nil {
			log.Fatalf("run: %v", err)
		}
		for evt := range events {
			if evt.Content != "" {
				fmt.Print(evt.Content)
			}
		}
		fmt.Println()
	} else {
		fmt.Println("Usage: go run main.go -file <path>")
		os.Exit(1)
	}
}

func intPtr(i int) *int       { return &i }
func floatPtr(f float64) *float64 { return &f }
