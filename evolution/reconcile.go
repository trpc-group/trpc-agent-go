//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package evolution

import (
	"fmt"
	"regexp"
	"strings"
)

// reconcileEventKind classifies a single mechanical fix the reconciler
// applied to a reviewer decision after the LLM produced it.
type reconcileEventKind string

const (
	// reconcileRewriteToUpdate marks a `skills` candidate that was
	// rewritten as an `updates` entry against an existing skill because
	// its name was either identical to or a strict superset of an
	// existing skill name (with a task-variant separator in between).
	reconcileRewriteToUpdate reconcileEventKind = "rewrite_to_update"

	// reconcileDropIntraBatchDuplicate marks a `skills` candidate that
	// was dropped because an earlier candidate in the same batch shared
	// its normalized name OR shared the same normalized when_to_use plus
	// the same ordered step list.
	reconcileDropIntraBatchDuplicate reconcileEventKind = "drop_intra_batch_duplicate"

	// reconcileDropConflictsWithExistingUpdate marks a `skills` candidate
	// that would have been rewritten as an update but the reviewer
	// already emitted an explicit `updates` entry against the same
	// existing target — we drop the candidate to avoid double-writing
	// the same on-disk skill from two sources.
	reconcileDropConflictsWithExistingUpdate reconcileEventKind = "drop_conflicts_with_existing_update"

	// reconcileRewriteQuantifiedSiblingToUpdate marks a `skills`
	// candidate whose name is a quantified sibling of an existing
	// generic skill (for example "Foo - 3 Cities" vs
	// "Foo - Multi-City"). We rewrite it as an update against the
	// generic parent so the library does not accumulate count-specific
	// siblings.
	reconcileRewriteQuantifiedSiblingToUpdate reconcileEventKind = "rewrite_quantified_sibling_to_update"

	// reconcileRewriteWordOverlapToUpdate marks a `skills` candidate
	// whose name shares high word overlap (≥60% Jaccard on significant
	// words) with an existing skill. This catches near-duplicate names
	// that differ only in phrasing (e.g. "geopolitical-market-analysis"
	// vs "geopolitical-market-impact-analysis") and rewrites the
	// candidate as an update against the closest existing match.
	reconcileRewriteWordOverlapToUpdate reconcileEventKind = "rewrite_word_overlap_to_update"
)

// reconcileEvent describes one fix the reconciler applied. Callers log
// these for auditability; the data model intentionally has no LLM-side
// fields so it stays cheap to emit and easy to grep.
type reconcileEvent struct {
	Kind     reconcileEventKind
	Original string // candidate name before the fix
	Target   string // existing skill name (rule 1 only); empty otherwise
	Reason   string // short human-readable explanation
}

// reconcileWithLibrary applies deterministic, library-aware fixes to a
// reviewer decision after the reviewer has produced its raw output.
// The rules are intentionally pure string-shape rules (no domain
// knowledge of what skills are about) so they generalize across any
// agent vertical and stay safe to enable by default.
//
//	Rule 2 (intra-batch dedup) runs first so the strict-superset rule
//	does not need to consider candidate-vs-candidate collisions.
//
//	Rule 1 (strict-name-superset) rewrites a candidate skill whose
//	normalized name equals an existing skill name, OR is an existing
//	skill name plus a task-variant suffix (" - X" / " (X)" / " v2" / …),
//	into an `updates` entry against that existing parent.
//
//	Rule 3 (quantified sibling -> generic parent) rewrites a candidate
//	whose name encodes a specific quantity ("3 cities", "5 dishes")
//	into an `updates` entry against an existing generic sibling
//	("multi-city", "multiple dishes") when such a parent already
//	exists in the library.
//
// existing is the same library snapshot the worker passed to the
// reviewer; nil/empty disables Rule 1 and only Rule 2 applies.
func reconcileWithLibrary(decision *ReviewDecision, existing []ExistingSkill) (*ReviewDecision, []reconcileEvent) {
	if decision == nil {
		return decision, nil
	}
	var events []reconcileEvent

	decision.Skills, events = dedupCandidateSkills(decision.Skills, events)
	decision.Skills, decision.Updates, events = rewriteSupersetSkills(
		decision.Skills, decision.Updates, decision.Deletions, existing, events,
	)
	decision.Skills, decision.Updates, events = rewriteQuantifiedSiblingSkills(
		decision.Skills, decision.Updates, decision.Deletions, existing, events,
	)
	decision.Skills, decision.Updates, events = rewriteWordOverlapSkills(
		decision.Skills, decision.Updates, decision.Deletions, existing, events,
	)
	return decision, events
}

// dedupCandidateSkills implements Rule 2: collapse duplicate candidates
// inside a single decision. Two candidates are considered duplicates
// when they share the same normalized name, OR when they share the
// same normalized when_to_use plus an identical ordered step list (the
// reviewer typically produces near-byte-equal entries when it
// "remembers" a skill twice in the same review).
//
// First occurrence wins. The reviewer prompt asks for distinct skills,
// so duplicate emission is a sign of confusion, not intentional
// versioning — dropping the trailing copy is the safe choice.
func dedupCandidateSkills(skills []*SkillSpec, events []reconcileEvent) ([]*SkillSpec, []reconcileEvent) {
	if len(skills) <= 1 {
		return skills, events
	}
	seenName := make(map[string]string, len(skills))  // normalized name -> first candidate name
	seenShape := make(map[string]string, len(skills)) // when_to_use+steps signature -> first candidate name
	out := make([]*SkillSpec, 0, len(skills))
	for _, s := range skills {
		if s == nil {
			continue
		}
		normName := normalizeSkillName(s.Name)
		if first, ok := seenName[normName]; ok && normName != "" {
			events = append(events, reconcileEvent{
				Kind:     reconcileDropIntraBatchDuplicate,
				Original: s.Name,
				Reason:   fmt.Sprintf("normalized name matches earlier candidate %q", first),
			})
			continue
		}
		shape := candidateShapeKey(s)
		if first, ok := seenShape[shape]; ok && shape != "" {
			events = append(events, reconcileEvent{
				Kind:     reconcileDropIntraBatchDuplicate,
				Original: s.Name,
				Reason:   fmt.Sprintf("when_to_use+steps shape matches earlier candidate %q", first),
			})
			continue
		}
		if normName != "" {
			seenName[normName] = s.Name
		}
		if shape != "" {
			seenShape[shape] = s.Name
		}
		out = append(out, s)
	}
	return out, events
}

// rewriteSupersetSkills implements Rule 1: detect candidates whose
// names are exact matches or strict task-variant supersets of existing
// skill names and rewrite them as `updates` entries against the parent.
//
// Multiple candidates that map to the same parent collapse to one
// update — first one wins; subsequent collisions are dropped.
//
// Candidates targeting an existing skill that the reviewer also marked
// for deletion in the same decision are left as-is: removing-and-replacing
// a skill is the reviewer's prerogative and we should not silently
// rewrite that flow.
func rewriteSupersetSkills(
	skills []*SkillSpec,
	updates []*SkillUpdate,
	deletions []string,
	existing []ExistingSkill,
	events []reconcileEvent,
) ([]*SkillSpec, []*SkillUpdate, []reconcileEvent) {
	if len(skills) == 0 || len(existing) == 0 {
		return skills, updates, events
	}

	// Index existing skills by normalized name and keep them sorted by
	// descending normalized-name length so the LONGEST matching parent
	// wins. This matters when the library already has a parent + a
	// child (e.g. "Foo Workflow" and "Foo Workflow - Multi-City") and a
	// new candidate "Foo Workflow - Multi-City - 3 Cities" appears.
	parents := make([]existingNameIndex, 0, len(existing))
	parentByNorm := make(map[string]string, len(existing))
	for _, e := range existing {
		norm := normalizeSkillName(e.Name)
		if norm == "" {
			continue
		}
		parents = append(parents, existingNameIndex{norm: norm, name: e.Name})
		parentByNorm[norm] = e.Name
	}
	sortIndexByLengthDesc(parents)

	// Track which existing names already have an explicit update from
	// the reviewer or a pending deletion so we do not silently overwrite
	// either flow.
	claimedUpdates := make(map[string]struct{}, len(updates))
	for _, u := range updates {
		if u == nil {
			continue
		}
		claimedUpdates[normalizeSkillName(u.Name)] = struct{}{}
	}
	pendingDeletions := make(map[string]struct{}, len(deletions))
	for _, d := range deletions {
		pendingDeletions[normalizeSkillName(d)] = struct{}{}
	}

	keptSkills := make([]*SkillSpec, 0, len(skills))
	rewriteByTarget := make(map[string]struct{}, len(skills))
	for _, cand := range skills {
		if cand == nil {
			continue
		}
		candNorm := normalizeSkillName(cand.Name)
		parent := matchSupersetParent(candNorm, parents)
		if parent == "" {
			keptSkills = append(keptSkills, cand)
			continue
		}
		parentName := parentByNorm[parent]

		if _, dropped := pendingDeletions[parent]; dropped {
			// Reviewer is removing the parent in the same decision.
			// Leave the candidate alone so the delete-then-create flow
			// remains explicit and visible.
			keptSkills = append(keptSkills, cand)
			continue
		}
		// Order matters: an earlier candidate that already mapped to
		// this parent during reconciliation is reported as
		// drop_intra_batch_duplicate; only an *original* reviewer-emitted
		// update against the same parent counts as a conflict. This
		// keeps the two event kinds semantically distinct so logs are
		// useful for diagnosing where the duplication came from.
		if _, alreadyRewritten := rewriteByTarget[parent]; alreadyRewritten {
			events = append(events, reconcileEvent{
				Kind:     reconcileDropIntraBatchDuplicate,
				Original: cand.Name,
				Target:   parentName,
				Reason:   "another candidate already mapped to this parent during reconciliation",
			})
			continue
		}
		if _, alreadyUpdating := claimedUpdates[parent]; alreadyUpdating {
			events = append(events, reconcileEvent{
				Kind:     reconcileDropConflictsWithExistingUpdate,
				Original: cand.Name,
				Target:   parentName,
				Reason:   "reviewer already emitted an explicit `updates` entry for this parent",
			})
			continue
		}

		// Rewrite: build an update against the parent. NewSpec.Name is
		// forced to the parent's name later by applyUpdates; we set it
		// here too so the on-disk frontmatter stays consistent with the
		// directory name.
		newSpec := *cand
		newSpec.Name = parentName
		updates = append(updates, &SkillUpdate{
			Name:    parentName,
			NewSpec: &newSpec,
			Reason: fmt.Sprintf(
				"auto-merged by reconciler: candidate %q is a strict superset of existing skill %q",
				cand.Name, parentName,
			),
		})
		// Track the rewrite separately from claimedUpdates so a
		// later candidate that hits the same parent is reported as a
		// duplicate rewrite, not as a conflict with an explicit
		// reviewer update.
		rewriteByTarget[parent] = struct{}{}
		events = append(events, reconcileEvent{
			Kind:     reconcileRewriteToUpdate,
			Original: cand.Name,
			Target:   parentName,
			Reason:   "candidate name is identical to or a strict task-variant superset of an existing skill",
		})
	}
	return keptSkills, updates, events
}

// rewriteQuantifiedSiblingSkills implements Rule 3: if a candidate
// skill name is a quantified sibling of an existing GENERIC skill
// ("Foo - 3 Cities" vs "Foo - Multi-City"), rewrite it as an update
// against the generic parent instead of allowing a count-specific
// sibling into the library.
//
// Safety valve: we only rewrite when the library already contains a
// generic sibling (multi-/multiple/several or an equivalent non-count
// form). If the library only has other count-specific siblings, we
// keep the candidate untouched rather than guessing which concrete
// count should become canonical.
func rewriteQuantifiedSiblingSkills(
	skills []*SkillSpec,
	updates []*SkillUpdate,
	deletions []string,
	existing []ExistingSkill,
	events []reconcileEvent,
) ([]*SkillSpec, []*SkillUpdate, []reconcileEvent) {
	if len(skills) == 0 || len(existing) == 0 {
		return skills, updates, events
	}

	parents := make([]existingNameIndex, 0, len(existing))
	parentByNorm := make(map[string]string, len(existing))
	for _, e := range existing {
		norm := normalizeSkillName(e.Name)
		if norm == "" {
			continue
		}
		parents = append(parents, existingNameIndex{norm: norm, name: e.Name})
		parentByNorm[norm] = e.Name
	}
	sortIndexByLengthDesc(parents)

	claimedUpdates := make(map[string]struct{}, len(updates))
	for _, u := range updates {
		if u == nil {
			continue
		}
		claimedUpdates[normalizeSkillName(u.Name)] = struct{}{}
	}
	pendingDeletions := make(map[string]struct{}, len(deletions))
	for _, d := range deletions {
		pendingDeletions[normalizeSkillName(d)] = struct{}{}
	}

	keptSkills := make([]*SkillSpec, 0, len(skills))
	rewriteByTarget := make(map[string]struct{}, len(skills))
	for _, cand := range skills {
		if cand == nil {
			continue
		}
		candNorm := normalizeSkillName(cand.Name)
		parent := matchQuantifiedSiblingParent(candNorm, parents)
		if parent == "" {
			keptSkills = append(keptSkills, cand)
			continue
		}
		parentName := parentByNorm[parent]

		if _, dropped := pendingDeletions[parent]; dropped {
			keptSkills = append(keptSkills, cand)
			continue
		}
		if _, alreadyRewritten := rewriteByTarget[parent]; alreadyRewritten {
			events = append(events, reconcileEvent{
				Kind:     reconcileDropIntraBatchDuplicate,
				Original: cand.Name,
				Target:   parentName,
				Reason:   "another quantified sibling already mapped to this generic parent during reconciliation",
			})
			continue
		}
		if _, alreadyUpdating := claimedUpdates[parent]; alreadyUpdating {
			events = append(events, reconcileEvent{
				Kind:     reconcileDropConflictsWithExistingUpdate,
				Original: cand.Name,
				Target:   parentName,
				Reason:   "reviewer already emitted an explicit `updates` entry for the generic sibling parent",
			})
			continue
		}

		newSpec := *cand
		newSpec.Name = parentName
		updates = append(updates, &SkillUpdate{
			Name:    parentName,
			NewSpec: &newSpec,
			Reason: fmt.Sprintf(
				"auto-merged by reconciler: candidate %q is a quantified sibling of existing generic skill %q",
				cand.Name, parentName,
			),
		})
		rewriteByTarget[parent] = struct{}{}
		events = append(events, reconcileEvent{
			Kind:     reconcileRewriteQuantifiedSiblingToUpdate,
			Original: cand.Name,
			Target:   parentName,
			Reason:   "candidate name is a count-specific sibling of an existing generic skill",
		})
	}
	return keptSkills, updates, events
}

var (
	explicitCountPattern      = regexp.MustCompile(`\b\d+\s+([a-z][a-z0-9_-]*)\b`)
	multiplicityMarkerPattern = regexp.MustCompile(`\b(?:multi(?:-|\s+)|multiple\s+|several\s+|many\s+)([a-z][a-z0-9_-]*)\b`)
)

func matchQuantifiedSiblingParent(candNorm string, parents []existingNameIndex) string {
	family := quantifiedFamilyKey(candNorm)
	if family == "" || family == candNorm {
		return ""
	}
	for _, p := range parents {
		if p.norm == "" || p.norm == candNorm {
			continue
		}
		if quantifiedFamilyKey(p.norm) != family {
			continue
		}
		if isGenericQuantifiedParent(p.norm) {
			return p.norm
		}
	}
	return ""
}

func isGenericQuantifiedParent(norm string) bool {
	return hasMultiplicityMarker(norm) || !hasExplicitCount(norm)
}

func hasExplicitCount(norm string) bool {
	return explicitCountPattern.MatchString(norm)
}

func hasMultiplicityMarker(norm string) bool {
	return multiplicityMarkerPattern.MatchString(norm)
}

func quantifiedFamilyKey(norm string) string {
	if norm == "" {
		return ""
	}
	out := explicitCountPattern.ReplaceAllStringFunc(norm, func(match string) string {
		sub := explicitCountPattern.FindStringSubmatch(match)
		if len(sub) < 2 {
			return match
		}
		return "<count:" + normalizeCountedUnit(sub[1]) + ">"
	})
	out = multiplicityMarkerPattern.ReplaceAllStringFunc(out, func(match string) string {
		sub := multiplicityMarkerPattern.FindStringSubmatch(match)
		if len(sub) < 2 {
			return match
		}
		return "<count:" + normalizeCountedUnit(sub[1]) + ">"
	})
	return out
}

func normalizeCountedUnit(unit string) string {
	unit = strings.Trim(unit, " \t-_")
	if unit == "" {
		return ""
	}
	unit = strings.ReplaceAll(unit, "_", "-")
	parts := strings.Split(unit, "-")
	for i := range parts {
		parts[i] = singularizeCountedWord(parts[i])
	}
	return strings.Join(parts, "-")
}

func singularizeCountedWord(word string) string {
	switch {
	case strings.HasSuffix(word, "ies") && len(word) > 3:
		return word[:len(word)-3] + "y"
	case strings.HasSuffix(word, "ches"),
		strings.HasSuffix(word, "shes"),
		strings.HasSuffix(word, "sses"),
		strings.HasSuffix(word, "xes"),
		strings.HasSuffix(word, "zes"):
		return word[:len(word)-2]
	case strings.HasSuffix(word, "s") && len(word) > 1 &&
		!strings.HasSuffix(word, "ss"):
		return word[:len(word)-1]
	default:
		return word
	}
}

// existingNameIndex is a small helper for sorting existing skill names
// by descending normalized length without losing the original name.
type existingNameIndex struct {
	norm string
	name string
}

func sortIndexByLengthDesc(xs []existingNameIndex) {
	// Hand-rolled insertion sort: the existing-skill list is small (tens
	// to low hundreds) and we want a stable order so prompt-text diffs
	// are deterministic in tests.
	for i := 1; i < len(xs); i++ {
		j := i
		for j > 0 && len(xs[j-1].norm) < len(xs[j].norm) {
			xs[j-1], xs[j] = xs[j], xs[j-1]
			j--
		}
	}
}

// matchSupersetParent finds the longest existing parent name whose
// normalized form is either equal to candNorm or a strict prefix of
// candNorm followed by a task-variant separator. Returns "" when no
// parent qualifies. parents must already be sorted descending by
// normalized-name length.
func matchSupersetParent(candNorm string, parents []existingNameIndex) string {
	if candNorm == "" {
		return ""
	}
	for _, p := range parents {
		if p.norm == "" || len(p.norm) > len(candNorm) {
			continue
		}
		if candNorm == p.norm {
			return p.norm
		}
		if !strings.HasPrefix(candNorm, p.norm) {
			continue
		}
		rest := candNorm[len(p.norm):]
		if isTaskVariantSeparator(rest) {
			return p.norm
		}
	}
	return ""
}

// isTaskVariantSeparator reports whether rest starts with a separator
// pattern that strongly suggests the surrounding token is a per-instance
// suffix bolted onto a shared base name. The recognised heads are:
//
//	" -" / "-"   ...... " - 3 cities", "Foo-X"
//	" :" / ":"   ...... " : variant"
//	" (" / "("   ...... " (3 items)"
//	" [" / "["   ...... " [variant]"
//	" /" / "/"   ...... " / scale"
//	" |" / "|"   ...... " | tag"
//	"_"          ...... "foo_bar"
//	" v<digit>"  ...... " v2", "v10" (rest of the rune is a digit)
//
// The check is deliberately conservative: only obvious separators
// trigger a rewrite, so two genuinely different skills that happen to
// share a long prefix are NOT collapsed.
func isTaskVariantSeparator(rest string) bool {
	if rest == "" {
		return false
	}
	r := rest[0]
	switch r {
	case '-', ':', '(', '[', '/', '|', '_', '.':
		return true
	case ' ', '\t':
		// Trim leading whitespace and re-check.
		trimmed := strings.TrimLeft(rest, " \t")
		if trimmed == "" || trimmed == rest {
			return false
		}
		switch trimmed[0] {
		case '-', ':', '(', '[', '/', '|', '_', '.':
			return true
		case 'v', 'V':
			if len(trimmed) >= 2 && trimmed[1] >= '0' && trimmed[1] <= '9' {
				return true
			}
		}
		return false
	case 'v', 'V':
		if len(rest) >= 2 && rest[1] >= '0' && rest[1] <= '9' {
			return true
		}
	}
	return false
}

// normalizeSkillName lower-cases, trims, and collapses internal
// whitespace runs so display variations like "Foo  Bar" / "foo bar"
// compare equal. Other punctuation is left alone — the strict-superset
// detector relies on it (e.g. "(" must survive so the suffix detector
// can fire on " (3 items)").
func normalizeSkillName(name string) string {
	name = strings.TrimSpace(strings.ToLower(name))
	if name == "" {
		return ""
	}
	var b strings.Builder
	b.Grow(len(name))
	prevSpace := false
	for _, r := range name {
		if r == ' ' || r == '\t' || r == '\n' || r == '\r' {
			if !prevSpace {
				b.WriteByte(' ')
				prevSpace = true
			}
			continue
		}
		b.WriteRune(r)
		prevSpace = false
	}
	return strings.TrimSpace(b.String())
}

// candidateShapeKey builds a deterministic signature from a candidate's
// when_to_use plus its ordered steps. Two candidates with the same
// shape are considered structurally identical even if their names or
// descriptions differ. Empty when_to_use OR empty steps yields an
// empty key (Rule 2 then ignores the candidate to avoid false-positive
// dedup of skeleton entries).
func candidateShapeKey(s *SkillSpec) string {
	if s == nil {
		return ""
	}
	when := strings.TrimSpace(strings.ToLower(s.WhenToUse))
	if when == "" || len(s.Steps) == 0 {
		return ""
	}
	steps := make([]string, 0, len(s.Steps))
	for _, st := range s.Steps {
		steps = append(steps, strings.TrimSpace(strings.ToLower(st)))
	}
	return when + "\x1f" + strings.Join(steps, "\x1e")
}

// ---------------------------------------------------------------------------
// Rule 4: word-overlap dedup
// ---------------------------------------------------------------------------

// rewriteWordOverlapSkills catches near-duplicate create candidates that
// slipped past Rules 1 and 3. When a candidate's name shares ≥ 60% of
// its significant words (Jaccard similarity) with an existing skill,
// the candidate is rewritten as an update against the closest match.
//
// This handles cases like:
//   - "geopolitical-market-analysis" vs "geopolitical-commodity-market-snapshot"
//   - "weather-data-collector" vs "weather-data-collection"
//
// The threshold is deliberately conservative: short names (< 3 significant
// words) require exact word overlap to avoid false positives.
func rewriteWordOverlapSkills(
	skills []*SkillSpec,
	updates []*SkillUpdate,
	deletions []string,
	existing []ExistingSkill,
	events []reconcileEvent,
) ([]*SkillSpec, []*SkillUpdate, []reconcileEvent) {
	if len(skills) == 0 || len(existing) == 0 {
		return skills, updates, events
	}

	// Build word sets for existing skills.
	type existingWords struct {
		name  string
		words map[string]struct{}
	}
	existingIndex := make([]existingWords, 0, len(existing))
	for _, e := range existing {
		ws := significantWords(e.Name)
		if len(ws) < 2 {
			continue
		}
		existingIndex = append(existingIndex, existingWords{name: e.Name, words: ws})
	}
	if len(existingIndex) == 0 {
		return skills, updates, events
	}

	// Track which existing names already have an explicit update or
	// pending deletion.
	claimedUpdates := make(map[string]struct{}, len(updates))
	for _, u := range updates {
		if u == nil {
			continue
		}
		claimedUpdates[normalizeSkillName(u.Name)] = struct{}{}
	}
	pendingDeletions := make(map[string]struct{}, len(deletions))
	for _, d := range deletions {
		pendingDeletions[normalizeSkillName(d)] = struct{}{}
	}

	keptSkills := make([]*SkillSpec, 0, len(skills))
	rewriteByTarget := make(map[string]struct{})

	for _, cand := range skills {
		if cand == nil {
			continue
		}
		candWords := significantWords(cand.Name)
		if len(candWords) < 2 {
			keptSkills = append(keptSkills, cand)
			continue
		}

		bestMatch := ""
		bestScore := 0.0
		candNorm := normalizeSkillName(cand.Name)
		for _, e := range existingIndex {
			// Skip pairs where one name is a prefix of the other — those
			// are already handled by Rule 1 (superset) or Rule 3 (sibling).
			eNorm := normalizeSkillName(e.name)
			if strings.HasPrefix(candNorm, eNorm) || strings.HasPrefix(eNorm, candNorm) {
				continue
			}
			score := jaccardSimilarity(candWords, e.words)
			if score > bestScore {
				bestScore = score
				bestMatch = e.name
			}
		}

		// Threshold: 50% word overlap (Jaccard), and at least 2 overlapping words.
		minOverlap := wordOverlap(candWords, significantWords(bestMatch))
		if bestScore < 0.5 || minOverlap < 2 {
			keptSkills = append(keptSkills, cand)
			continue
		}

		targetNorm := normalizeSkillName(bestMatch)
		if _, dropped := pendingDeletions[targetNorm]; dropped {
			keptSkills = append(keptSkills, cand)
			continue
		}
		if _, already := rewriteByTarget[targetNorm]; already {
			events = append(events, reconcileEvent{
				Kind:     reconcileDropIntraBatchDuplicate,
				Original: cand.Name,
				Target:   bestMatch,
				Reason:   "another candidate already mapped to this target via word overlap",
			})
			continue
		}
		if _, alreadyClaimed := claimedUpdates[targetNorm]; alreadyClaimed {
			events = append(events, reconcileEvent{
				Kind:     reconcileDropConflictsWithExistingUpdate,
				Original: cand.Name,
				Target:   bestMatch,
				Reason:   "reviewer already emitted an explicit update for this target",
			})
			continue
		}

		// Rewrite as update.
		newSpec := *cand
		newSpec.Name = bestMatch
		updates = append(updates, &SkillUpdate{
			Name:    bestMatch,
			NewSpec: &newSpec,
			Reason: fmt.Sprintf(
				"auto-merged by reconciler: candidate %q has %.0f%% word overlap with existing skill %q",
				cand.Name, bestScore*100, bestMatch,
			),
		})
		rewriteByTarget[targetNorm] = struct{}{}
		events = append(events, reconcileEvent{
			Kind:     reconcileRewriteWordOverlapToUpdate,
			Original: cand.Name,
			Target:   bestMatch,
			Reason: fmt.Sprintf(
				"candidate name shares %.0f%% word overlap with existing skill",
				bestScore*100,
			),
		})
	}
	return keptSkills, updates, events
}

// significantWords extracts meaningful words from a skill name, excluding
// common stop words and short tokens. Returns a set for O(1) lookup.
func significantWords(name string) map[string]struct{} {
	name = strings.ToLower(name)
	// Replace common separators with spaces.
	name = strings.NewReplacer("-", " ", "_", " ", ".", " ").Replace(name)
	words := strings.Fields(name)
	result := make(map[string]struct{}, len(words))
	for _, w := range words {
		if len(w) < 3 || isStopWord(w) {
			continue
		}
		result[w] = struct{}{}
	}
	return result
}

// jaccardSimilarity returns |A ∩ B| / |A ∪ B| for two word sets.
func jaccardSimilarity(a, b map[string]struct{}) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	intersection := 0
	for w := range a {
		if _, ok := b[w]; ok {
			intersection++
		}
	}
	union := len(a) + len(b) - intersection
	if union == 0 {
		return 0
	}
	return float64(intersection) / float64(union)
}

// wordOverlap returns the count of words in common between two sets.
func wordOverlap(a, b map[string]struct{}) int {
	count := 0
	for w := range a {
		if _, ok := b[w]; ok {
			count++
		}
	}
	return count
}

// isStopWord returns true for English stop words commonly found in
// skill names that should not contribute to similarity matching.
func isStopWord(w string) bool {
	switch w {
	case "the", "and", "for", "with", "from", "that", "this",
		"are", "was", "were", "been", "being", "have", "has",
		"had", "does", "did", "will", "would", "could", "should",
		"may", "might", "shall", "can", "not", "but", "yet",
		"also", "then", "than", "when", "where", "how", "what",
		"which", "who", "whom", "its", "each", "every", "all",
		"any", "few", "more", "most", "other", "some", "such",
		"into", "over", "under", "between", "through", "about",
		"using", "multi", "multiple":
		return true
	}
	return false
}
