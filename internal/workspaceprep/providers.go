//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package workspaceprep

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/internal/workspaceinput"
	"trpc.group/trpc-go/trpc-agent-go/model"
	rootskill "trpc.group/trpc-go/trpc-agent-go/skill"
)

// BootstrapSpec is the declarative workspace bootstrap description
// exposed to business code through llmagent.WithWorkspaceBootstrap.
//
// Files are staged first, then Commands run in declaration order. All
// entries are converted to Requirements by newBootstrapProvider.
type BootstrapSpec struct {
	// Files are static inputs (artifact://, host://, workspace://,
	// inline bytes) that must exist before commands run.
	Files []FileSpec
	// Commands are one-shot initialization commands such as
	// "python3 -m venv .venv" or "pip install -r requirements.txt".
	Commands []CommandSpec
}

// NewBootstrapProvider builds a Provider that emits the same set of
// static Requirements on every reconcile. Business code typically
// constructs one BootstrapSpec at agent construction time and passes
// it to llmagent.WithWorkspaceBootstrap.
func NewBootstrapProvider(
	spec BootstrapSpec,
) (Provider, error) {
	reqs := make([]Requirement, 0, len(spec.Files)+len(spec.Commands))
	for i, f := range spec.Files {
		req, err := NewFileRequirement(f)
		if err != nil {
			return nil, fmt.Errorf(
				"bootstrap file[%d]: %w", i, err,
			)
		}
		reqs = append(reqs, req)
	}
	for i, c := range spec.Commands {
		req, err := NewCommandRequirement(c)
		if err != nil {
			return nil, fmt.Errorf(
				"bootstrap command[%d]: %w", i, err,
			)
		}
		reqs = append(reqs, req)
	}
	return &staticProvider{name: "bootstrap", reqs: reqs}, nil
}

type staticProvider struct {
	name string
	reqs []Requirement
}

func (p *staticProvider) Name() string { return p.name }

func (p *staticProvider) Requirements(
	_ context.Context, _ *agent.Invocation,
) ([]Requirement, error) {
	out := make([]Requirement, len(p.reqs))
	copy(out, p.reqs)
	return out, nil
}

// NewLoadedSkillsProvider returns a Provider that walks the active
// invocation's session state (via skill.LoadedPrefix /
// StateKeyLoadedPrefix) and emits one SkillRequirement per skill the
// model has loaded for the current agent. Repository is used to
// resolve skill source paths; it should be the same repository that
// skill_load validates against.
func NewLoadedSkillsProvider(
	repo rootskill.Repository,
) (Provider, error) {
	if repo == nil {
		return nil, fmt.Errorf(
			"workspaceprep: loaded-skills provider needs a repo",
		)
	}
	return &loadedSkillsProvider{repo: repo}, nil
}

type loadedSkillsProvider struct {
	repo rootskill.Repository
}

func (p *loadedSkillsProvider) Name() string { return "loaded_skills" }

func (p *loadedSkillsProvider) Requirements(
	ctx context.Context, inv *agent.Invocation,
) ([]Requirement, error) {
	names := loadedSkillsFromInvocation(inv)
	if len(names) == 0 {
		return nil, nil
	}
	reqs := make([]Requirement, 0, len(names))
	for _, name := range names {
		req, err := NewSkillRequirement(SkillSpec{
			Name:       name,
			Repository: p.repo,
		})
		if err != nil {
			return nil, err
		}
		reqs = append(reqs, req)
	}
	return reqs, nil
}

// loadedSkillsFromInvocation reads the session state for skills loaded
// by the active agent. It uses Session.SnapshotState() so that
// concurrent writers (parallel tool calls, async event handlers) do
// not race with the reconcile read. It checks both the agent-scoped
// and legacy unscoped prefixes so that older sessions keep working.
func loadedSkillsFromInvocation(inv *agent.Invocation) []string {
	if inv == nil || inv.Session == nil {
		return nil
	}
	state := inv.Session.SnapshotState()
	if len(state) == 0 {
		return nil
	}
	agentName := inv.AgentName
	seen := make(map[string]struct{})
	addFromPrefix := func(prefix string) {
		for key := range state {
			if !strings.HasPrefix(key, prefix) {
				continue
			}
			name := strings.TrimPrefix(key, prefix)
			name = strings.TrimSpace(name)
			if name == "" {
				continue
			}
			seen[name] = struct{}{}
		}
	}
	addFromPrefix(rootskill.LoadedPrefix(agentName))
	// Also include legacy unscoped keys so any older skill_load
	// calls that happened before scoping was introduced still
	// materialize the skill.
	if agentName != "" {
		addFromPrefix(rootskill.StateKeyLoadedPrefix)
	}
	out := make([]string, 0, len(seen))
	for name := range seen {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// NewConversationFilesProvider returns a Provider that emits a single
// Requirement wrapping the existing StageConversationFiles helper.
// The wrapping requirement fingerprints every file (file_id or
// sha256 over inline bytes) so that new uploads between reconciles
// force a re-stage, while repeated invocations with the same files
// are a fast no-op.
//
// Design note: v1 intentionally exposes one batch Requirement instead
// of one-per-file. The reasons are:
//
//   - StageConversationFiles already deduplicates per
//     file_id/sha256 inside the workspace metadata; reproducing
//     that logic at requirement granularity would duplicate the
//     source of truth.
//   - Conversation files are a tightly coupled set; they are staged
//     together, observed together, and their on-disk paths are
//     chosen by the helper rather than the requirement.
//
// If finer-grained Prepared/sentinel control becomes necessary we
// can split this into per-file requirements without changing the
// public surface (this provider stays an internal API).
func NewConversationFilesProvider() Provider {
	return &conversationFilesProvider{}
}

type conversationFilesProvider struct{}

func (p *conversationFilesProvider) Name() string {
	return "conversation_files"
}

func (p *conversationFilesProvider) Requirements(
	_ context.Context, inv *agent.Invocation,
) ([]Requirement, error) {
	// allConversationFiles already handles inv.Session == nil safely
	// by falling back to the current invocation's message parts, so we
	// only require the invocation itself to exist. Gating on Session !=
	// nil would regress the pre-refactor behavior where a user message
	// carrying a file part but no session history still got staged.
	if inv == nil {
		return nil, nil
	}
	return []Requirement{&conversationFilesRequirement{}}, nil
}

type conversationFilesRequirement struct{}

func (r *conversationFilesRequirement) Key() string {
	return "conversation-files"
}

func (r *conversationFilesRequirement) Kind() Kind {
	return KindConversationFile
}

func (r *conversationFilesRequirement) Phase() Phase { return PhaseFile }

func (r *conversationFilesRequirement) Required() bool { return false }

func (r *conversationFilesRequirement) Target() string {
	return codeexecutor.DirWork + "/inputs"
}

// Fingerprint hashes the list of conversation files so that the same
// set of uploads produces a stable digest. We re-run StageConversationFiles
// whenever the fingerprint changes; the helper itself is idempotent
// for unchanged files because it tracks InputRecord entries in the
// workspace metadata.
func (r *conversationFilesRequirement) Fingerprint(
	ctx context.Context, rctx ApplyContext,
) (string, error) {
	inv := rctx.Invocation
	if inv == nil {
		return "", nil
	}
	files := allConversationFiles(inv)
	h := sha256.New()
	h.Write([]byte("conv-files|"))
	for _, key := range files {
		h.Write([]byte(key))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// SentinelExists always returns true: StageConversationFiles is
// self-verifying via workspace metadata's existingByKey map, so a
// manual deletion of work/inputs/<name> on disk will not be
// re-materialized automatically. That matches the behavior users
// have today and keeps the provider a drop-in replacement for the
// current auto-staging path.
func (r *conversationFilesRequirement) SentinelExists(
	_ context.Context, _ ApplyContext,
) (bool, error) {
	return true, nil
}

// Apply delegates to StageConversationFiles. Because this requirement
// is Optional, the reconciler converts a non-nil error into a
// warning rather than aborting; that matches the legacy auto-staging
// behavior which never blocked execution on a partial stage.
func (r *conversationFilesRequirement) Apply(
	ctx context.Context, rctx ApplyContext,
) error {
	_, warnings := workspaceinput.StageConversationFiles(
		ctx, rctx.Engine, rctx.Workspace,
	)
	if len(warnings) > 0 {
		return fmt.Errorf(
			"conversation files warnings: %s",
			strings.Join(warnings, "; "),
		)
	}
	return nil
}

// allConversationFiles collects a stable, sorted list of file
// identifiers from the current invocation. The traversal mirrors
// workspaceinput.StageConversationFiles exactly so that the
// fingerprint and the eventual stage operation agree on which files
// are part of the workspace. In particular:
//
//   - Session events are only considered when the message Role is
//     model.RoleUser. Tool/assistant messages may carry file parts
//     too, but they are not user-supplied inputs.
//   - The current invocation message contributes its file parts
//     unconditionally because the helper treats it as the active
//     user turn.
//   - Empty identifiers are dropped so that files that cannot be
//     fingerprinted (for example provider-side ids with no bytes)
//     do not perturb the digest.
//
// Events are read through a short locked snapshot to avoid racing
// with concurrent appenders (parallel tool calls, async event
// handlers).
func allConversationFiles(inv *agent.Invocation) []string {
	if inv == nil {
		return nil
	}
	keys := make([]string, 0)
	if inv.Session != nil {
		inv.Session.EventMu.RLock()
		events := append([]event.Event(nil), inv.Session.Events...)
		inv.Session.EventMu.RUnlock()
		for _, ev := range events {
			if ev.Response == nil {
				continue
			}
			for _, choice := range ev.Response.Choices {
				if choice.Message.Role != model.RoleUser {
					continue
				}
				for _, part := range choice.Message.ContentParts {
					if part.Type != model.ContentTypeFile ||
						part.File == nil {
						continue
					}
					if k := fileDigest(part.File.FileID,
						part.File.Data); k != "" {
						keys = append(keys, k)
					}
				}
			}
		}
	}
	for _, part := range inv.Message.ContentParts {
		if part.Type != model.ContentTypeFile || part.File == nil {
			continue
		}
		if k := fileDigest(part.File.FileID, part.File.Data); k != "" {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	out := keys[:0]
	var prev string
	for _, k := range keys {
		if k == prev {
			continue
		}
		out = append(out, k)
		prev = k
	}
	return out
}

func fileDigest(fileID string, data []byte) string {
	id := strings.TrimSpace(fileID)
	if id != "" {
		return "id:" + id
	}
	if len(data) == 0 {
		return ""
	}
	sum := sha256.Sum256(data)
	return "sha:" + hex.EncodeToString(sum[:])
}
