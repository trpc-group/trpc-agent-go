//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package registry provides runtime registration and resolution for metric extensions.
package registry

import (
	"errors"
	"fmt"
	"os"
	"sync"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/finalresponse"
	criterionjson "trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/json"
	criterionrouge "trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/rouge"
	criteriontext "trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/text"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/tooltrajectory"
)

// Registry resolves runtime metric extensions from registered names.
type Registry interface {
	// RegisterTextCompare registers a named text compare function.
	RegisterTextCompare(name string, fn criteriontext.CompareFunc) error
	// RegisterJSONCompare registers a named JSON compare function.
	RegisterJSONCompare(name string, fn criterionjson.CompareFunc) error
	// RegisterToolTrajectoryCompare registers a named tool trajectory compare function.
	RegisterToolTrajectoryCompare(name string, fn tooltrajectory.CompareFunc) error
	// RegisterFinalResponseCompare registers a named final response compare function.
	RegisterFinalResponseCompare(name string, fn finalresponse.CompareFunc) error
	// RegisterRougeTokenizer registers a named ROUGE tokenizer.
	RegisterRougeTokenizer(name string, tok criterionrouge.Tokenizer) error
	// Resolve resolves registered names into runtime implementations on the metric.
	Resolve(evalMetric *metric.EvalMetric) error
}

type registry struct {
	mu                     sync.RWMutex
	textCompares           map[string]criteriontext.CompareFunc
	jsonCompares           map[string]criterionjson.CompareFunc
	toolTrajectoryCompares map[string]tooltrajectory.CompareFunc
	finalResponseCompares  map[string]finalresponse.CompareFunc
	rougeTokenizers        map[string]criterionrouge.Tokenizer
}

// New creates a metric extension registry.
func New() Registry {
	return &registry{
		textCompares:           make(map[string]criteriontext.CompareFunc),
		jsonCompares:           make(map[string]criterionjson.CompareFunc),
		toolTrajectoryCompares: make(map[string]tooltrajectory.CompareFunc),
		finalResponseCompares:  make(map[string]finalresponse.CompareFunc),
		rougeTokenizers:        make(map[string]criterionrouge.Tokenizer),
	}
}

// RegisterTextCompare registers a named text compare function.
func (r *registry) RegisterTextCompare(name string, fn criteriontext.CompareFunc) error {
	if name == "" {
		return errors.New("text compare name is empty")
	}
	if fn == nil {
		return errors.New("text compare is nil")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.textCompares[name] = fn
	return nil
}

// RegisterJSONCompare registers a named JSON compare function.
func (r *registry) RegisterJSONCompare(name string, fn criterionjson.CompareFunc) error {
	if name == "" {
		return errors.New("json compare name is empty")
	}
	if fn == nil {
		return errors.New("json compare is nil")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.jsonCompares[name] = fn
	return nil
}

// RegisterToolTrajectoryCompare registers a named tool trajectory compare function.
func (r *registry) RegisterToolTrajectoryCompare(name string, fn tooltrajectory.CompareFunc) error {
	if name == "" {
		return errors.New("tool trajectory compare name is empty")
	}
	if fn == nil {
		return errors.New("tool trajectory compare is nil")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.toolTrajectoryCompares[name] = fn
	return nil
}

// RegisterFinalResponseCompare registers a named final response compare function.
func (r *registry) RegisterFinalResponseCompare(name string, fn finalresponse.CompareFunc) error {
	if name == "" {
		return errors.New("final response compare name is empty")
	}
	if fn == nil {
		return errors.New("final response compare is nil")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.finalResponseCompares[name] = fn
	return nil
}

// RegisterRougeTokenizer registers a named ROUGE tokenizer.
func (r *registry) RegisterRougeTokenizer(name string, tok criterionrouge.Tokenizer) error {
	if name == "" {
		return errors.New("rouge tokenizer name is empty")
	}
	if tok == nil {
		return errors.New("rouge tokenizer is nil")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.rougeTokenizers[name] = tok
	return nil
}

// Resolve resolves registered names into runtime implementations on the metric.
func (r *registry) Resolve(evalMetric *metric.EvalMetric) error {
	if evalMetric == nil {
		return errors.New("eval metric is nil")
	}
	if evalMetric.Criterion == nil {
		return nil
	}
	if err := r.resolveToolTrajectoryCriterion(evalMetric.Criterion.ToolTrajectory); err != nil {
		return fmt.Errorf("resolve tool trajectory criterion: %w", err)
	}
	if err := r.resolveFinalResponseCriterion(evalMetric.Criterion.FinalResponse); err != nil {
		return fmt.Errorf("resolve final response criterion: %w", err)
	}
	return nil
}

func (r *registry) resolveToolTrajectoryCriterion(criterion *tooltrajectory.ToolTrajectoryCriterion) error {
	if criterion == nil {
		return nil
	}
	if criterion.Compare == nil && criterion.CompareName != "" {
		compare, err := r.lookupToolTrajectoryCompare(criterion.CompareName)
		if err != nil {
			return err
		}
		criterion.Compare = compare
	}
	if criterion.Compare != nil {
		return nil
	}
	if err := r.resolveToolTrajectoryStrategy(criterion.DefaultStrategy); err != nil {
		return fmt.Errorf("resolve default strategy: %w", err)
	}
	for toolName, strategy := range criterion.ToolStrategy {
		if err := r.resolveToolTrajectoryStrategy(strategy); err != nil {
			return fmt.Errorf("resolve tool strategy %s: %w", toolName, err)
		}
	}
	return nil
}

func (r *registry) resolveToolTrajectoryStrategy(strategy *tooltrajectory.ToolTrajectoryStrategy) error {
	if strategy == nil {
		return nil
	}
	if err := r.resolveTextCriterion(strategy.Name); err != nil {
		return fmt.Errorf("resolve name criterion: %w", err)
	}
	if err := r.resolveJSONCriterion(strategy.Arguments); err != nil {
		return fmt.Errorf("resolve arguments criterion: %w", err)
	}
	if err := r.resolveJSONCriterion(strategy.Result); err != nil {
		return fmt.Errorf("resolve result criterion: %w", err)
	}
	return nil
}

func (r *registry) resolveFinalResponseCriterion(criterion *finalresponse.FinalResponseCriterion) error {
	if criterion == nil {
		return nil
	}
	if criterion.Compare == nil && criterion.CompareName != "" {
		compare, err := r.lookupFinalResponseCompare(criterion.CompareName)
		if err != nil {
			return err
		}
		criterion.Compare = compare
	}
	if criterion.Compare != nil {
		return nil
	}
	if err := r.resolveTextCriterion(criterion.Text); err != nil {
		return fmt.Errorf("resolve text criterion: %w", err)
	}
	if err := r.resolveJSONCriterion(criterion.JSON); err != nil {
		return fmt.Errorf("resolve json criterion: %w", err)
	}
	if err := r.resolveRougeCriterion(criterion.Rouge); err != nil {
		return fmt.Errorf("resolve rouge criterion: %w", err)
	}
	return nil
}

func (r *registry) resolveTextCriterion(criterion *criteriontext.TextCriterion) error {
	if criterion == nil || criterion.Compare != nil || criterion.CompareName == "" {
		return nil
	}
	compare, err := r.lookupTextCompare(criterion.CompareName)
	if err != nil {
		return err
	}
	criterion.Compare = compare
	return nil
}

func (r *registry) resolveJSONCriterion(criterion *criterionjson.JSONCriterion) error {
	if criterion == nil || criterion.Compare != nil || criterion.CompareName == "" {
		return nil
	}
	compare, err := r.lookupJSONCompare(criterion.CompareName)
	if err != nil {
		return err
	}
	criterion.Compare = compare
	return nil
}

func (r *registry) resolveRougeCriterion(criterion *criterionrouge.RougeCriterion) error {
	if criterion == nil || criterion.Tokenizer != nil || criterion.TokenizerName == "" {
		return nil
	}
	tokenizer, err := r.lookupRougeTokenizer(criterion.TokenizerName)
	if err != nil {
		return err
	}
	criterion.Tokenizer = tokenizer
	return nil
}

func (r *registry) lookupTextCompare(name string) (criteriontext.CompareFunc, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	compare, ok := r.textCompares[name]
	if !ok {
		return nil, fmt.Errorf("text compare %s not found: %w", name, os.ErrNotExist)
	}
	return compare, nil
}

func (r *registry) lookupJSONCompare(name string) (criterionjson.CompareFunc, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	compare, ok := r.jsonCompares[name]
	if !ok {
		return nil, fmt.Errorf("json compare %s not found: %w", name, os.ErrNotExist)
	}
	return compare, nil
}

func (r *registry) lookupToolTrajectoryCompare(name string) (tooltrajectory.CompareFunc, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	compare, ok := r.toolTrajectoryCompares[name]
	if !ok {
		return nil, fmt.Errorf("tool trajectory compare %s not found: %w", name, os.ErrNotExist)
	}
	return compare, nil
}

func (r *registry) lookupFinalResponseCompare(name string) (finalresponse.CompareFunc, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	compare, ok := r.finalResponseCompares[name]
	if !ok {
		return nil, fmt.Errorf("final response compare %s not found: %w", name, os.ErrNotExist)
	}
	return compare, nil
}

func (r *registry) lookupRougeTokenizer(name string) (criterionrouge.Tokenizer, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	tokenizer, ok := r.rougeTokenizers[name]
	if !ok {
		return nil, fmt.Errorf("rouge tokenizer %s not found: %w", name, os.ErrNotExist)
	}
	return tokenizer, nil
}
