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
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/evolution"
	"trpc.group/trpc-go/trpc-agent-go/skill"
)

const subcmdEvolution = "evolution"

const (
	evoCmdPending  = "pending"
	evoCmdApprove  = "approve"
	evoCmdReject   = "reject"
	evoCmdDiff     = "diff"
	evoCmdAudit    = "audit"
	evoCmdRollback = "rollback"
)

// evoEnv holds the I/O context for evolution subcommands, making them
// testable without global os.Stdout/Stderr.
type evoEnv struct {
	stdout io.Writer
	stderr io.Writer
}

func runEvolution(args []string) int {
	env := evoEnv{stdout: os.Stdout, stderr: os.Stderr}
	return env.dispatch(args)
}

func (e *evoEnv) dispatch(args []string) int {
	if len(args) == 0 {
		e.usage()
		return 2
	}
	switch cmd := strings.ToLower(strings.TrimSpace(args[0])); cmd {
	case evoCmdPending:
		return e.pending(args[1:])
	case evoCmdApprove:
		return e.decide(args[1:], true)
	case evoCmdReject:
		return e.decide(args[1:], false)
	case evoCmdDiff:
		return e.diff(args[1:])
	case evoCmdAudit:
		return e.audit(args[1:])
	case evoCmdRollback:
		return e.rollback(args[1:])
	case "help", "-h", "--help":
		e.usageTo(e.stdout)
		return 0
	default:
		fmt.Fprintf(e.stderr, "unknown evolution command: %s\n", cmd)
		e.usage()
		return 2
	}
}

func (e *evoEnv) usage()                    { e.usageTo(e.stderr) }
func (e *evoEnv) usageTo(w io.Writer)       { fmt.Fprintln(w, evoUsageText) }
func (e *evoEnv) errorf(f string, a ...any) { fmt.Fprintf(e.stderr, "error: "+f+"\n", a...) }

const evoUsageText = `Usage: openclaw evolution <command> [options]

Commands:
  pending              List revisions awaiting human approval
  approve <rev-id>     Approve a pending revision (promote to active)
  reject  <rev-id>     Reject a pending revision
  diff    <rev-id>     Show the skill content of a revision
  audit                Show recent audit log entries
  rollback <skill-id>  Restore the previous active revision of a skill

Global options:
  --dir <path>         Path to evolution revisions directory
  --app <app-name>    Select app-scoped revisions under --dir
  --user <user-id>    Select user-scoped revisions under --dir
                       (or set EVOLUTION_REVISIONS_DIR env var)`

// ---------------------------------------------------------------------------
// Flag parsing helpers
// ---------------------------------------------------------------------------

// evoFlags wraps a FlagSet with a mandatory --dir flag. Call parse()
// to get the resolved directory and positional args.
type evoFlags struct {
	fs   *flag.FlagSet
	dir  *string
	app  *string
	user *string
}

func newEvoFlags(name string) *evoFlags {
	fs := flag.NewFlagSet("evolution "+name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	dir := fs.String("dir", "", "path to evolution revisions directory")
	app := fs.String("app", "", "app name for scoped revision directory")
	user := fs.String("user", "", "user id for user-scoped revision directory")
	return &evoFlags{fs: fs, dir: dir, app: app, user: user}
}

// parse runs the underlying FlagSet against args while accepting flags
// in any position. The standard flag package stops at the first
// non-flag token, which trips up CLI invocations like
// `evolution approve <rev-id> --comment foo` — flags after the
// positional argument would be silently treated as positionals.
//
// We work around this by repeatedly parsing the remaining args,
// appending leading non-flag tokens to `positional`, then continuing
// to parse from the next flag token. This stays compatible with the
// stock flag.FlagSet semantics (long/short flags, `=` syntax, `--`
// terminator) without re-implementing flag parsing.
func (f *evoFlags) parse(args []string) (dir string, positional []string, err error) {
	rem := args
	for {
		if err = f.fs.Parse(rem); err != nil {
			return "", nil, err
		}
		rem = f.fs.Args()
		if len(rem) == 0 {
			break
		}
		// `--` is the explicit "no more flags" terminator handled by
		// the FlagSet — anything left after a stop on `--` is purely
		// positional.
		if !strings.HasPrefix(rem[0], "-") || rem[0] == "-" {
			positional = append(positional, rem[0])
			rem = rem[1:]
			continue
		}
		break
	}
	if *f.dir == "" {
		*f.dir = os.Getenv("EVOLUTION_REVISIONS_DIR")
	}
	if *f.dir == "" {
		return "", nil, fmt.Errorf("--dir is required (or set EVOLUTION_REVISIONS_DIR)")
	}
	dir, err = scopedEvolutionCLIPath(*f.dir, *f.app, *f.user)
	if err != nil {
		return "", nil, err
	}
	return dir, positional, nil
}

func scopedEvolutionCLIPath(root, appName, userID string) (string, error) {
	if strings.TrimSpace(appName) == "" && strings.TrimSpace(userID) == "" {
		return root, nil
	}
	mode := skill.SkillScopeApp
	if strings.TrimSpace(userID) != "" {
		mode = skill.SkillScopeUser
	}
	scope, err := skill.NewSkillScope(mode, appName, userID)
	if err != nil {
		return "", err
	}
	parts, err := skill.ScopePathParts(mode, scope)
	if err != nil {
		return "", err
	}
	return filepath.Join(append([]string{root}, parts...)...), nil
}

func managedSkillsDirForRevisionsDir(revisionsDir string) string {
	clean := filepath.Clean(revisionsDir)
	volume := filepath.VolumeName(clean)
	rest := strings.TrimPrefix(clean, volume)
	absolute := strings.HasPrefix(rest, string(filepath.Separator))
	rest = strings.Trim(rest, string(filepath.Separator))
	parts := []string{}
	if rest != "" && rest != "." {
		parts = strings.Split(rest, string(filepath.Separator))
	}
	for i := 0; i+1 < len(parts); i++ {
		if parts[i] != "evolution" || parts[i+1] != "revisions" {
			continue
		}
		out := append([]string{}, parts[:i]...)
		out = append(out, defaultSkillsDir, "evolution")
		out = append(out, parts[i+2:]...)
		joined := filepath.Join(out...)
		if absolute {
			joined = string(filepath.Separator) + joined
		}
		return volume + joined
	}
	return filepath.Join(filepath.Dir(clean), defaultSkillsDir, "evolution")
}

// ---------------------------------------------------------------------------
// Subcommands
// ---------------------------------------------------------------------------

func (e *evoEnv) pending(args []string) int {
	fl := newEvoFlags(evoCmdPending)
	dir, positional, err := fl.parse(args)
	if err != nil {
		e.errorf("%v", err)
		return 2
	}
	if len(positional) > 0 {
		e.errorf("unexpected arguments: %v", positional)
		return 2
	}

	store := evolution.NewFileCandidateStore(dir)
	svc := evolution.NewApprovalService(store, nil, nil)
	pending, err := svc.ListPending(context.Background(), evolution.ListPendingOpts{})
	if err != nil {
		e.errorf("%v", err)
		return 1
	}
	if len(pending) == 0 {
		fmt.Fprintln(e.stdout, "No revisions pending approval.")
		return 0
	}

	tw := tabwriter.NewWriter(e.stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "REVISION ID\tSKILL\tACTION\tCREATED")
	for _, rev := range pending {
		name := rev.SkillID
		if rev.Spec != nil {
			name = rev.Spec.Name
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n",
			rev.RevisionID, name, rev.Action,
			rev.CreatedAt.Format(time.RFC3339))
	}
	tw.Flush()
	fmt.Fprintf(e.stdout, "\n%d revision(s) pending approval.\n", len(pending))
	return 0
}

func (e *evoEnv) decide(args []string, approve bool) int {
	fl := newEvoFlags(evoCmdApprove)
	comment := fl.fs.String("comment", "", "optional review comment")
	reviewer := fl.fs.String("reviewer", "", "reviewer identity (default: $USER)")

	dir, positional, err := fl.parse(args)
	if err != nil {
		e.errorf("%v", err)
		return 2
	}
	if len(positional) == 0 {
		verb := evoCmdApprove
		if !approve {
			verb = evoCmdReject
		}
		e.errorf("revision ID required\nUsage: openclaw evolution %s <revision-id> --dir <path>", verb)
		return 2
	}
	revisionID := positional[0]

	if *reviewer == "" {
		if u := os.Getenv("USER"); u != "" {
			*reviewer = u
		} else {
			*reviewer = "cli-user"
		}
	}

	store := evolution.NewFileCandidateStore(dir)
	pointer := evolution.NewFileActivePointer(dir)
	var publisher evolution.Publisher
	if approve {
		publisher = evolution.NewFilePublisher(managedSkillsDirForRevisionsDir(dir))
	}
	ctx := context.Background()

	skillID, err := findSkillForRevision(ctx, store, revisionID)
	if err != nil {
		e.errorf("%v", err)
		return 1
	}

	svc := evolution.NewApprovalService(store, pointer, publisher)
	err = svc.Decide(ctx, evolution.ApprovalDecision{
		RevisionID: revisionID,
		SkillID:    skillID,
		Approved:   approve,
		Reviewer:   *reviewer,
		Comment:    *comment,
		DecidedAt:  time.Now().UTC(),
	})
	if err != nil {
		e.errorf("%v", err)
		return 1
	}

	if approve {
		fmt.Fprintf(e.stdout, "Revision %s promoted to active.\n", revisionID)
	} else {
		fmt.Fprintf(e.stdout, "Revision %s rejected.\n", revisionID)
	}
	return 0
}

func (e *evoEnv) diff(args []string) int {
	fl := newEvoFlags(evoCmdDiff)
	dir, positional, err := fl.parse(args)
	if err != nil {
		e.errorf("%v", err)
		return 2
	}
	if len(positional) == 0 {
		e.errorf("revision ID required\nUsage: openclaw evolution diff <revision-id> --dir <path>")
		return 2
	}
	revisionID := positional[0]
	ctx := context.Background()

	store := evolution.NewFileCandidateStore(dir)
	skillID, err := findSkillForRevision(ctx, store, revisionID)
	if err != nil {
		e.errorf("%v", err)
		return 1
	}
	rev, err := store.ReadRevision(ctx, skillID, revisionID)
	if err != nil {
		e.errorf("reading revision: %v", err)
		return 1
	}

	w := e.stdout
	fmt.Fprintf(w, "Skill:    %s\n", rev.SkillID)
	fmt.Fprintf(w, "Revision: %s\n", rev.RevisionID)
	fmt.Fprintf(w, "Action:   %s\n", rev.Action)
	fmt.Fprintf(w, "Status:   %s\n", rev.Status)
	fmt.Fprintf(w, "Created:  %s\n", rev.CreatedAt.Format(time.RFC3339))
	fmt.Fprintln(w, "---")
	if rev.Spec == nil {
		return 0
	}
	fmt.Fprintf(w, "Name:        %s\n", rev.Spec.Name)
	fmt.Fprintf(w, "Description: %s\n", rev.Spec.Description)
	fmt.Fprintf(w, "When to use: %s\n", rev.Spec.WhenToUse)
	if len(rev.Spec.Steps) > 0 {
		fmt.Fprintln(w, "\nSteps:")
		for i, step := range rev.Spec.Steps {
			fmt.Fprintf(w, "  %d. %s\n", i+1, step)
		}
	}
	if len(rev.Spec.Pitfalls) > 0 {
		fmt.Fprintln(w, "\nPitfalls:")
		for _, p := range rev.Spec.Pitfalls {
			fmt.Fprintf(w, "  - %s\n", p)
		}
	}
	return 0
}

func (e *evoEnv) audit(args []string) int {
	fl := newEvoFlags(evoCmdAudit)
	limit := fl.fs.Int("limit", 20, "max entries to show")

	dir, _, err := fl.parse(args)
	if err != nil {
		e.errorf("%v", err)
		return 2
	}

	store := evolution.NewFileCandidateStore(dir)
	skills, err := store.ListSkills(context.Background())
	if err != nil {
		e.errorf("%v", err)
		return 1
	}

	type entry struct {
		evolution.AuditEvent
		skill string
	}
	var all []entry
	for _, sid := range skills {
		events, readErr := readAuditLog(dir, sid)
		if readErr != nil {
			continue
		}
		for _, ev := range events {
			all = append(all, entry{AuditEvent: ev, skill: sid})
		}
	}
	// Most recent first.
	for i, j := 0, len(all)-1; i < j; i, j = i+1, j-1 {
		all[i], all[j] = all[j], all[i]
	}
	if *limit > 0 && len(all) > *limit {
		all = all[:*limit]
	}
	if len(all) == 0 {
		fmt.Fprintln(e.stdout, "No audit events found.")
		return 0
	}

	tw := tabwriter.NewWriter(e.stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "TIME\tSKILL\tACTION\tREVISION\tREASON")
	for _, ev := range all {
		reason := ev.Reason
		if len(reason) > 60 {
			reason = reason[:57] + "..."
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n",
			ev.At.Format("2006-01-02 15:04"),
			ev.skill, ev.Action, truncateID(ev.RevisionID), reason)
	}
	tw.Flush()
	return 0
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func (e *evoEnv) rollback(args []string) int {
	fl := newEvoFlags(evoCmdRollback)
	revision := fl.fs.String("revision", "", "specific archived revision id (default: latest archived in store order)")
	reviewer := fl.fs.String("reviewer", "", "reviewer identity (default: $USER)")
	comment := fl.fs.String("comment", "", "optional rollback comment")

	dir, positional, err := fl.parse(args)
	if err != nil {
		e.errorf("%v", err)
		return 2
	}
	if len(positional) == 0 {
		e.errorf("skill ID required\nUsage: openclaw evolution rollback <skill-id> --dir <path> [--revision <rev-id>]")
		return 2
	}
	skillID := positional[0]

	if *reviewer == "" {
		if u := os.Getenv("USER"); u != "" {
			*reviewer = u
		} else {
			*reviewer = "cli-user"
		}
	}

	store := evolution.NewFileCandidateStore(dir)
	pointer := evolution.NewFileActivePointer(dir)
	publisher := evolution.NewFilePublisher(managedSkillsDirForRevisionsDir(dir))
	svc := evolution.NewApprovalService(store, pointer, publisher)

	res, err := svc.Rollback(context.Background(), skillID, evolution.RollbackOpts{
		TargetRevisionID: *revision,
		Reviewer:         *reviewer,
		Comment:          *comment,
		DecidedAt:        time.Now().UTC(),
	})
	if err != nil {
		e.errorf("%v", err)
		return 1
	}
	if res.PreviousActiveID == "" {
		fmt.Fprintf(e.stdout, "Skill %q restored to revision %s.\n", skillID, res.RestoredID)
	} else {
		fmt.Fprintf(e.stdout, "Skill %q rolled back: %s → %s.\n",
			skillID, res.PreviousActiveID, res.RestoredID)
	}
	return 0
}

func readAuditLog(rootDir, skillID string) ([]evolution.AuditEvent, error) {
	data, err := os.ReadFile(filepath.Join(rootDir, skillID, "audit.log"))
	if err != nil {
		return nil, err
	}
	var events []evolution.AuditEvent
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var ev evolution.AuditEvent
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			continue
		}
		events = append(events, ev)
	}
	return events, nil
}

func findSkillForRevision(ctx context.Context, store evolution.CandidateStore, revisionID string) (string, error) {
	skills, err := store.ListSkills(ctx)
	if err != nil {
		return "", fmt.Errorf("list skills: %w", err)
	}
	for _, skillID := range skills {
		revIDs, listErr := store.ListRevisions(ctx, skillID)
		if listErr != nil {
			continue
		}
		for _, id := range revIDs {
			if id == revisionID {
				return skillID, nil
			}
		}
	}
	return "", fmt.Errorf("revision %q not found in any skill", revisionID)
}

func truncateID(id string) string {
	if len(id) > 12 {
		return id[:12]
	}
	return id
}
