//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main demonstrates the Evolution (Agent Self-Learning) feature.
//
// It runs a series of multi-step tasks and shows how the agent extracts
// skills in the background, then loads them on subsequent tasks.
//
// Usage:
//
//	export OPENAI_API_KEY="sk-..."
//	cd examples/evolution
//	go run main.go
//	go run main.go -model gpt-4o-mini -rounds 3
//	go run main.go -clean  # reset managed skills and start fresh
//
// What you'll see:
//
//  1. Round 1: agent solves a multi-step task from scratch (no skills)
//  2. Background: reviewer extracts a reusable skill → managed_skills/
//  3. Round 2+: agent loads the skill via skill_load → follows the checklist
//
// The managed_skills/ directory persists between runs, so restarting
// shows warm-start behavior immediately.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/evolution"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/skill"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

var (
	flagModel  = flag.String("model", "gpt-4o-mini", "LLM model name")
	flagRounds = flag.Int("rounds", 3, "number of task rounds to run")
	flagClean  = flag.Bool("clean", false, "remove managed_skills/ before starting")
)

// Each task requires multiple tool calls (≥4), which triggers the
// evolution reviewer. Tasks share a common "compare cities" pattern so
// the skill extracted from round 1 helps subsequent rounds.
var tasks = []string{
	"Compare Tokyo, Paris, and Cairo: look up each city's details, then write a comparison summary covering population, continent, and which is largest.",
	"Compare London, Sydney, and New York: look up each city's details, then write a comparison summary covering population, continent, and which is largest.",
	"Compare Berlin, Mumbai, and Toronto: look up each city's details, then write a comparison summary covering population, continent, and which is largest.",
	"Compare Seoul, Lagos, and Mexico City: look up each city's details, then write a comparison summary covering population, continent, and which is largest.",
}

func main() {
	flag.Parse()
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	if *flagClean {
		os.RemoveAll("./managed_skills")
		os.RemoveAll("./managed_skills_revisions")
		fmt.Println("Cleaned managed_skills/ and managed_skills_revisions/")
	}

	app := &evolutionDemo{}
	if err := app.setup(); err != nil {
		return err
	}
	defer app.close()

	return app.runRounds()
}

// ---------------------------------------------------------------------------
// Application struct
// ---------------------------------------------------------------------------

type evolutionDemo struct {
	mdl    model.Model
	repo   *skill.FSRepository
	evoSvc evolution.Service
	runner runner.Runner
}

func (d *evolutionDemo) setup() error {
	skillsDir := "./managed_skills"
	revisionsDir := "./managed_skills_revisions"
	os.MkdirAll(skillsDir, 0o755)
	os.MkdirAll(revisionsDir, 0o755)

	// Model.
	d.mdl = openai.New(*flagModel)

	// Skill repository.
	repo, err := skill.NewFSRepository(skillsDir)
	if err != nil {
		return fmt.Errorf("create skill repo: %w", err)
	}
	d.repo = repo

	// Evolution service with full quality gate pipeline.
	// Use AlwaysReviewPolicy so even short tasks trigger skill extraction.
	d.evoSvc = evolution.NewService(d.mdl,
		evolution.WithManagedSkillsDir(skillsDir),
		evolution.WithSkillRepository(repo),
		evolution.WithPolicy(alwaysReviewPolicy{}),
		evolution.WithCandidateStore(evolution.NewFileCandidateStore(revisionsDir)),
		evolution.WithActivePointer(evolution.NewFileActivePointer(revisionsDir)),
		evolution.WithSpecGate(evolution.NewDefaultSpecGate()),
		evolution.WithSafetyGate(evolution.NewDefaultSafetyGate()),
		evolution.WithEffectivenessGate(evolution.NewOutcomeBasedEffectivenessGate()),
	)

	// Agent with tools and skills.
	ag := llmagent.New("city-comparison-agent",
		llmagent.WithModel(d.mdl),
		llmagent.WithSkills(repo),
		llmagent.WithMaxOverviewSkills(5),
		llmagent.WithTools([]tool.Tool{newCityTool()}),
	)

	// Runner.
	d.runner = runner.NewRunner("evolution-demo", ag,
		runner.WithEvolutionService(d.evoSvc),
	)
	return nil
}

func (d *evolutionDemo) close() {
	d.runner.Close()
}

func (d *evolutionDemo) runRounds() error {
	ctx := context.Background()
	numRounds := min(*flagRounds, len(tasks))

	for i := range numRounds {
		d.printRoundHeader(i, numRounds)

		response, toolCalls, elapsed, err := d.executeTask(ctx, tasks[i], i)
		if err != nil {
			fmt.Printf("  Error: %v\n", err)
			continue
		}

		d.printRoundResult(response, toolCalls, elapsed)
		d.waitForReviewer()
	}

	d.printFinalState()
	return nil
}

func (d *evolutionDemo) executeTask(ctx context.Context, task string, idx int) (string, int, time.Duration, error) {
	sessionID := fmt.Sprintf("evo-demo-%d-%d", time.Now().Unix(), idx)

	start := time.Now()
	eventCh, err := d.runner.Run(ctx, "demo-user", sessionID, model.NewUserMessage(task))
	if err != nil {
		return "", 0, 0, err
	}

	var response string
	var toolCalls int
	for ev := range eventCh {
		if ev.Response == nil {
			continue
		}
		for _, choice := range ev.Response.Choices {
			if choice.Delta.Content != "" {
				response += choice.Delta.Content
			} else if choice.Message.Content != "" {
				response = choice.Message.Content
			}
			if len(choice.Message.ToolCalls) > 0 || len(choice.Delta.ToolCalls) > 0 {
				toolCalls++
			}
		}
	}
	return response, toolCalls, time.Since(start), nil
}

func (d *evolutionDemo) waitForReviewer() {
	fmt.Print("  Waiting for background skill extraction...")
	time.Sleep(5 * time.Second)
	d.repo.Refresh()
	fmt.Println(" done.")
}

func (d *evolutionDemo) printRoundHeader(idx, total int) {
	sums := d.repo.Summaries()
	fmt.Printf("\n%s\n", strings.Repeat("=", 60))
	fmt.Printf("Round %d/%d\n", idx+1, total)
	fmt.Printf("Task: %s\n", tasks[idx])
	fmt.Printf("Skills available: %d", len(sums))
	if len(sums) > 0 {
		names := make([]string, len(sums))
		for j, s := range sums {
			names[j] = s.Name
		}
		fmt.Printf(" [%s]", strings.Join(names, ", "))
	}
	fmt.Printf("\n%s\n", strings.Repeat("-", 60))
}

func (d *evolutionDemo) printRoundResult(response string, toolCalls int, elapsed time.Duration) {
	// Truncate long responses for readability.
	resp := strings.TrimSpace(response)
	if len(resp) > 200 {
		resp = resp[:200] + "..."
	}
	fmt.Printf("\n  Response: %s\n", resp)
	fmt.Printf("  Tool calls: %d | Time: %s\n", toolCalls, elapsed.Round(time.Millisecond))
}

func (d *evolutionDemo) printFinalState() {
	fmt.Printf("\n%s\n", strings.Repeat("=", 60))
	fmt.Println("FINAL STATE")
	fmt.Printf("%s\n", strings.Repeat("=", 60))

	sums := d.repo.Summaries()
	if len(sums) == 0 {
		fmt.Println("\nNo skills extracted.")
	} else {
		fmt.Printf("\nManaged skills (%d):\n", len(sums))
		for _, s := range sums {
			fmt.Printf("  - %s: %s\n", s.Name, s.Description)
		}
	}

	if svcW, ok := d.evoSvc.(evolution.ServiceWithWorker); ok {
		m := svcW.Worker().ApprovalGateMetricsJSON()
		fmt.Printf("\nQuality gate metrics:\n")
		fmt.Printf("  Candidates seen:       %d\n", m.CandidatesSeen)
		fmt.Printf("  Revisions promoted:    %d\n", m.RevisionsPromoted)
		fmt.Printf("  Spec-gate rejected:    %d\n", m.SpecGateRejected)
		fmt.Printf("  Safety-gate rejected:  %d\n", m.SafetyGateRejected)
		fmt.Printf("  Effect-gate held:      %d\n", m.EffectivenessGateRejected)
		fmt.Printf("  Human-gate held:       %d\n", m.HumanGateHeld)
	}

	fmt.Println("\nTip: re-run without -clean to see warm-start (skills loaded immediately).")
}

// ---------------------------------------------------------------------------
// City lookup tool (simulated data for demo)
// ---------------------------------------------------------------------------

// cityData provides simulated data so the example works without network.
var cityData = map[string]string{
	"tokyo":       "Population: 13.96M, Country: Japan, Continent: Asia",
	"paris":       "Population: 2.16M, Country: France, Continent: Europe",
	"cairo":       "Population: 10.1M, Country: Egypt, Continent: Africa",
	"london":      "Population: 8.98M, Country: United Kingdom, Continent: Europe",
	"sydney":      "Population: 5.31M, Country: Australia, Continent: Oceania",
	"new york":    "Population: 8.34M, Country: United States, Continent: North America",
	"berlin":      "Population: 3.75M, Country: Germany, Continent: Europe",
	"mumbai":      "Population: 20.7M, Country: India, Continent: Asia",
	"toronto":     "Population: 2.93M, Country: Canada, Continent: North America",
	"seoul":       "Population: 9.77M, Country: South Korea, Continent: Asia",
	"lagos":       "Population: 15.4M, Country: Nigeria, Continent: Africa",
	"mexico city": "Population: 21.8M, Country: Mexico, Continent: North America",
}

type cityInput struct {
	City string `json:"city" jsonschema:"description=city name to look up"`
}

func newCityTool() tool.Tool {
	return function.NewFunctionTool(
		func(_ context.Context, input cityInput) (string, error) {
			key := strings.ToLower(strings.TrimSpace(input.City))
			if info, ok := cityData[key]; ok {
				return info, nil
			}
			return fmt.Sprintf("No data found for %q", input.City), nil
		},
		function.WithName("city_lookup"),
		function.WithDescription("Look up facts about a city: population, country, and continent."),
	)
}

// ---------------------------------------------------------------------------
// Custom policy: always review (for demo; production uses DefaultPolicy)
// ---------------------------------------------------------------------------

// alwaysReviewPolicy triggers a review after every task, regardless of
// tool call count. In production, use evolution.DefaultPolicy{} which
// requires ≥4 tool calls.
type alwaysReviewPolicy struct{}

func (alwaysReviewPolicy) ShouldReview(_ *evolution.ReviewContext) bool { return true }
