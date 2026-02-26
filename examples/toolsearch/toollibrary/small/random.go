//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package small

import (
	"context"
	"fmt"
	"math/rand"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

// NewRandomGeneratorTool creates a random generator tool.
func NewRandomGeneratorTool() tool.CallableTool {
	return function.NewFunctionTool(
		generateRandom,
		function.WithName("random_generator"),
		function.WithDescription("Generate random numbers. Supports integer and float types within specified range. Can generate multiple values at once."),
		function.WithInputSchema(&tool.Schema{
			Type:        "object",
			Description: "Random number generation request",
			Required:    []string{"min", "max", "type"},
			Properties: map[string]*tool.Schema{
				"min": {
					Type:        "number",
					Description: "Minimum value (inclusive)",
				},
				"max": {
					Type:        "number",
					Description: "Maximum value (exclusive)",
				},
				"type": {
					Type:        "string",
					Description: "Type of random value",
					Enum:        []any{"integer", "float"},
				},
				"count": {
					Type:        "integer",
					Description: "Number of random values to generate",
					Default:     1,
				},
			},
		}),
	)
}

type randomRequest struct {
	Min   float64 `json:"min"`
	Max   float64 `json:"max"`
	Type  string  `json:"type"`
	Count int     `json:"count"`
}

type randomResponse struct {
	Values  []float64 `json:"values"`
	Type    string    `json:"type"`
	Count   int       `json:"count"`
	Message string    `json:"message"`
}

func generateRandom(_ context.Context, req randomRequest) (randomResponse, error) {
	if req.Min >= req.Max {
		return randomResponse{
			Values:  []float64{},
			Type:    req.Type,
			Count:   0,
			Message: "Error: Min must be less than Max",
		}, fmt.Errorf("min must be less than max")
	}

	if req.Count < 1 {
		req.Count = 1
	}
	if req.Count > 1000 {
		req.Count = 1000
	}

	rand.Seed(time.Now().UnixNano())
	values := make([]float64, req.Count)

	for i := 0; i < req.Count; i++ {
		if req.Type == "integer" {
			values[i] = float64(rand.Intn(int(req.Max)-int(req.Min)) + int(req.Min))
		} else {
			values[i] = req.Min + rand.Float64()*(req.Max-req.Min)
		}
	}

	return randomResponse{
		Values:  values,
		Type:    req.Type,
		Count:   req.Count,
		Message: fmt.Sprintf("Generated %d %s random values between %.2f and %.2f", req.Count, req.Type, req.Min, req.Max),
	}, nil
}
