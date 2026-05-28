//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package app

import (
	"os"
	"path/filepath"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/evolution"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/skill"
)

// maybeCreateEvolutionService creates an evolution service when the
// runtime has a skills repository and state directory configured.
// The service uses the agent's model as its reviewer.
//
// Directory layout under state_dir:
//
//	<state_dir>/skills/evolution/    — published SKILL.md files (agent-visible)
//	<state_dir>/evolution/revisions/ — immutable revision store + audit logs
//
// Returns nil when evolution cannot be enabled (no repo, no state dir,
// or no model).
func maybeCreateEvolutionService(
	opts runOptions,
	repo skill.Repository,
	providers ...skill.RepositoryProvider,
) evolution.Service {
	if repo == nil {
		return nil
	}

	// Determine directories.
	stateDir := opts.StateDir
	if stateDir == "" {
		return nil
	}
	// Published SKILL.md files go into <state_dir>/skills/evolution/ —
	// a subdirectory within the app's skill repository root. FSRepository
	// recursively walks <state_dir>/skills/ so these are discovered
	// alongside bundled/ and local/ skills after repo.Refresh().
	// Keeping evolution in its own subdirectory prevents mixing with
	// user-authored or bundled content.
	managedDir := filepath.Join(stateDir, defaultSkillsDir, "evolution")
	// Immutable revision snapshots (meta.json, audit.log) go into a
	// separate directory that is NOT in the skill repo roots. This
	// keeps the revision store out of the agent's skill overview.
	revisionsDir := filepath.Join(stateDir, "evolution", "revisions")

	if err := os.MkdirAll(managedDir, 0o755); err != nil {
		log.Errorf("evolution: create managed skills dir: %v", err)
		return nil
	}
	if err := os.MkdirAll(revisionsDir, 0o755); err != nil {
		log.Errorf("evolution: create revisions dir: %v", err)
		return nil
	}
	var repoProvider skill.RepositoryProvider
	if len(providers) > 0 && providers[0] != nil {
		repoProvider = providers[0]
	}

	// Build evolution options.
	evoOpts := []evolution.Option{
		evolution.WithManagedSkillsDir(managedDir),
		evolution.WithSkillRepository(repo),
		evolution.WithSkillScopeMode(opts.EvolutionSkillScopeMode),
		evolution.WithCandidateStore(evolution.NewFileCandidateStore(revisionsDir)),
		evolution.WithActivePointer(evolution.NewFileActivePointer(revisionsDir)),
		evolution.WithSpecGate(evolution.NewDefaultSpecGate()),
		evolution.WithSafetyGate(evolution.NewDefaultSafetyGate()),
		evolution.WithEffectivenessGate(evolution.NewOutcomeBasedEffectivenessGate()),
	}
	if repoProvider != nil {
		evoOpts = append(evoOpts, evolution.WithSkillRepositoryProvider(repoProvider))
	}

	// Optional human approval gate, configured via yaml or env var:
	//   evolution:
	//     human_gate: "always" | "create" | "" (disabled, default)
	if gate := resolveHumanGate(opts.EvolutionHumanGate); gate != nil {
		evoOpts = append(evoOpts, evolution.WithHumanGate(gate))
		log.Infof("evolution: human gate enabled (mode=%s)", opts.EvolutionHumanGate)
	}

	// Use the same model configuration as the agent for reviewing.
	mdl := newEvolutionReviewerModel(opts)
	if mdl == nil {
		return nil
	}

	svc := evolution.NewService(mdl, evoOpts...)
	log.Infof("evolution: enabled (skills_dir=%s, revisions_dir=%s)", managedDir, revisionsDir)
	return svc
}

// newEvolutionReviewerModel creates a model for the evolution reviewer.
// It reuses the same provider config as the agent (OpenAI base URL, etc).
func newEvolutionReviewerModel(opts runOptions) model.Model {
	if opts.ModelMode == modeMock {
		return nil
	}
	modelName := opts.OpenAIModel
	if modelName == "" {
		modelName = defaultOpenAIModel
	}
	var modelOpts []openai.Option
	if opts.OpenAIBaseURL != "" {
		modelOpts = append(modelOpts, openai.WithBaseURL(opts.OpenAIBaseURL))
	}
	return openai.New(modelName, modelOpts...)
}

// resolveHumanGate returns the HumanGate implementation for the given
// mode string, or nil if human approval is disabled.
func resolveHumanGate(mode string) evolution.HumanGate {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "always":
		return evolution.NewAlwaysHoldGate()
	case "create":
		return evolution.NewCreateOnlyHoldGate()
	default:
		return nil
	}
}
