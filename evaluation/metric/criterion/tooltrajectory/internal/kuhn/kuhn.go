//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package kuhn implements the Kuhn algorithm for maximum cardinality matching in an unweighted bipartite graph.
package kuhn

import "errors"

// unMatched marks an unmatched vertex index in match arrays.
const unMatched = -1

// Kuhn computes maximum cardinality matching in an unweighted bipartite graph using DFS augmenting paths.
type Kuhn struct {
	leftSize   int     // leftSize is the number of vertices on the left side.
	rightSize  int     // rightSize is the number of vertices on the right side.
	leftAdj    [][]int // leftAdj is the adjacency list from left to right.
	matchRight []int   // matchRight stores the current matching from right to left.
	visitMark  []int   // visitMark stores the last visitStamp when a left vertex was visited during DFS.
	visitStamp int     // visitStamp is incremented per DFS attempt to avoid clearing visitMark each time.
}

// New creates a Kuhn matcher for a bipartite graph with the given sizes.
func New(leftSize int, rightSize int) *Kuhn {
	k := &Kuhn{
		leftSize:   leftSize,
		rightSize:  rightSize,
		leftAdj:    make([][]int, leftSize),
		matchRight: make([]int, rightSize),
		visitMark:  make([]int, leftSize),
		visitStamp: 1,
	}
	for right := range k.matchRight {
		k.matchRight[right] = unMatched
	}
	return k
}

// AddEdge adds an edge from a left vertex to a right vertex.
func (k *Kuhn) AddEdge(left int, right int) {
	k.leftAdj[left] = append(k.leftAdj[left], right)
}

// FullLeftMatch checks whether every left vertex can be matched to a distinct right vertex.
// If a full left matching exists, it returns (nil, nil). Otherwise it returns (unmatchedLeft, error),
// where unmatchedLeft contains the left vertex indices that are unmatched in the computed maximum matching.
func (k *Kuhn) FullLeftMatch() ([]int, error) {
	if k.leftSize == 0 {
		return nil, nil
	}
	if k.rightSize == 0 {
		return k.collectUnmatchedLeft(), errors.New("cannot match all left vertices")
	}
	matchedCount := 0
	for left := 0; left < k.leftSize; left++ {
		k.visitStamp++
		if k.findAugmentingPath(left) {
			matchedCount++
		}
	}
	if matchedCount == k.leftSize {
		return nil, nil
	}
	return k.collectUnmatchedLeft(), errors.New("cannot match all left vertices")
}

// findAugmentingPath attempts to find an augmenting path starting from the given left vertex.
// It returns true if it can increase the matching (or rewire it) to match this left vertex.
func (k *Kuhn) findAugmentingPath(left int) bool {
	if k.visitMark[left] == k.visitStamp {
		return false
	}
	k.visitMark[left] = k.visitStamp
	for _, right := range k.leftAdj[left] {
		previousLeft := k.matchRight[right]
		if previousLeft == unMatched || k.findAugmentingPath(previousLeft) {
			k.matchRight[right] = left
			return true
		}
	}
	return false
}

// collectUnmatchedLeft returns the list of left vertices that are unmatched in the current matching.
func (k *Kuhn) collectUnmatchedLeft() []int {
	matchLeft := make([]int, k.leftSize)
	for left := range matchLeft {
		matchLeft[left] = unMatched
	}
	for right := range k.matchRight {
		left := k.matchRight[right]
		if left != unMatched {
			matchLeft[left] = right
		}
	}
	unmatched := make([]int, 0)
	for left := range matchLeft {
		if matchLeft[left] == unMatched {
			unmatched = append(unmatched, left)
		}
	}
	return unmatched
}
