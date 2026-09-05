//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package optimization

import (
	"errors"
	"fmt"
	"math"
	"math/rand"
	"sort"
)

type scoreMatrix struct {
	caseIDs      []string
	candidateIDs []string
	scores       map[string]map[string]float64
}

func newScoreMatrix(cases []Case) *scoreMatrix {
	caseIDs := make([]string, 0, len(cases))
	for _, item := range cases {
		caseIDs = append(caseIDs, item.ID)
	}
	return &scoreMatrix{
		caseIDs: caseIDs,
		scores:  make(map[string]map[string]float64),
	}
}

func (m *scoreMatrix) add(candidateID string, batch evaluationBatch) error {
	if m == nil {
		return errors.New("nil score matrix")
	}
	if _, duplicate := m.scores[candidateID]; duplicate {
		return fmt.Errorf("candidate %q already exists in score matrix", candidateID)
	}
	row := make(map[string]float64, len(m.caseIDs))
	for _, caseID := range m.caseIDs {
		evaluation, ok := batch.byID[caseID]
		if !ok {
			return fmt.Errorf("candidate %q is missing validation case %q", candidateID, caseID)
		}
		row[caseID] = evaluation.Score
	}
	m.scores[candidateID] = row
	m.candidateIDs = append(m.candidateIDs, candidateID)
	return nil
}

func (m *scoreMatrix) mean(candidateID string) float64 {
	row := m.scores[candidateID]
	if len(row) == 0 {
		return math.Inf(-1)
	}
	var total float64
	for _, caseID := range m.caseIDs {
		total += row[caseID]
	}
	return total / float64(len(m.caseIDs))
}

func (m *scoreMatrix) bestCandidateID() string {
	bestID := ""
	bestScore := math.Inf(-1)
	for _, candidateID := range m.candidateIDs {
		score := m.mean(candidateID)
		if score > bestScore {
			bestScore = score
			bestID = candidateID
		}
	}
	return bestID
}

func (m *scoreMatrix) selectParent(rng *rand.Rand) (string, error) {
	if m == nil || len(m.candidateIDs) == 0 {
		return "", errors.New("cannot select from an empty score matrix")
	}
	fronts := m.instanceFronts()
	fronts = m.removeCoverageDominated(fronts)
	weighted := make([]string, 0, len(m.caseIDs))
	for _, caseID := range m.caseIDs {
		front := fronts[caseID]
		for _, candidateID := range m.candidateIDs {
			if front[candidateID] {
				weighted = append(weighted, candidateID)
			}
		}
	}
	if len(weighted) == 0 {
		return m.bestCandidateID(), nil
	}
	return weighted[rng.Intn(len(weighted))], nil
}

func (m *scoreMatrix) instanceFronts() map[string]map[string]bool {
	fronts := make(map[string]map[string]bool, len(m.caseIDs))
	for _, caseID := range m.caseIDs {
		best := math.Inf(-1)
		for _, candidateID := range m.candidateIDs {
			if score := m.scores[candidateID][caseID]; score > best {
				best = score
			}
		}
		front := make(map[string]bool)
		for _, candidateID := range m.candidateIDs {
			if m.scores[candidateID][caseID] == best {
				front[candidateID] = true
			}
		}
		fronts[caseID] = front
	}
	return fronts
}

// removeCoverageDominated mirrors GEPA's instance-front pruning: a candidate
// is removable when every case frontier it covers is also covered by another
// surviving candidate. Lower aggregate candidates are considered first.
func (m *scoreMatrix) removeCoverageDominated(
	fronts map[string]map[string]bool,
) map[string]map[string]bool {
	programSet := make(map[string]bool)
	for _, front := range fronts {
		for candidateID := range front {
			programSet[candidateID] = true
		}
	}
	programs := make([]string, 0, len(programSet))
	for candidateID := range programSet {
		programs = append(programs, candidateID)
	}
	sort.Slice(programs, func(i, j int) bool {
		left, right := m.mean(programs[i]), m.mean(programs[j])
		if left == right {
			return programs[i] < programs[j]
		}
		return left < right
	})

	dominated := make(map[string]bool)
	for {
		removed := false
		for _, candidateID := range programs {
			if dominated[candidateID] {
				continue
			}
			if coverageDominated(candidateID, programs, dominated, fronts) {
				dominated[candidateID] = true
				removed = true
				break
			}
		}
		if !removed {
			break
		}
	}

	pruned := make(map[string]map[string]bool, len(fronts))
	for caseID, front := range fronts {
		remaining := make(map[string]bool)
		for candidateID := range front {
			if !dominated[candidateID] {
				remaining[candidateID] = true
			}
		}
		pruned[caseID] = remaining
	}
	return pruned
}

func coverageDominated(
	candidateID string,
	programs []string,
	dominated map[string]bool,
	fronts map[string]map[string]bool,
) bool {
	covered := false
	for _, front := range fronts {
		if !front[candidateID] {
			continue
		}
		covered = true
		replaced := false
		for _, otherID := range programs {
			if otherID == candidateID || dominated[otherID] {
				continue
			}
			if front[otherID] {
				replaced = true
				break
			}
		}
		if !replaced {
			return false
		}
	}
	return covered
}
