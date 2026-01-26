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

	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

// NewUnitConverterTool creates a unit converter tool.
func NewUnitConverterTool() tool.CallableTool {
	return function.NewFunctionTool(
		convertUnit,
		function.WithName("unit_converter"),
		function.WithDescription("Convert values between different units. Categories: 'length' (m, km, cm, mm, inch, foot, yard, mile), 'weight' (kg, g, mg, lb, oz), 'temperature' (C, F, K), 'area' (sqm, sqkm, sqft, acre), 'volume' (L, mL, gal, cup)"),
		function.WithInputSchema(&tool.Schema{
			Type:        "object",
			Description: "Unit conversion request",
			Required:    []string{"value", "from_unit", "to_unit", "category"},
			Properties: map[string]*tool.Schema{
				"value": {
					Type:        "number",
					Description: "The value to convert",
				},
				"from_unit": {
					Type:        "string",
					Description: "Source unit",
				},
				"to_unit": {
					Type:        "string",
					Description: "Target unit",
				},
				"category": {
					Type:        "string",
					Description: "Unit category",
					Enum:        []any{"length", "weight", "temperature", "area", "volume"},
				},
			},
		}),
	)
}

type unitRequest struct {
	Value    float64 `json:"value"`
	FromUnit string  `json:"from_unit"`
	ToUnit   string  `json:"to_unit"`
	Category string  `json:"category"`
}

type unitResponse struct {
	OriginalValue  float64 `json:"original_value"`
	FromUnit       string  `json:"from_unit"`
	ToUnit         string  `json:"to_unit"`
	ConvertedValue float64 `json:"converted_value"`
	Category       string  `json:"category"`
	Message        string  `json:"message"`
}

func convertUnit(_ context.Context, req unitRequest) (unitResponse, error) {
	var converted float64
	var err error

	switch req.Category {
	case "length":
		converted, err = convertLength(req.Value, req.FromUnit, req.ToUnit)
	case "weight":
		converted, err = convertWeight(req.Value, req.FromUnit, req.ToUnit)
	case "temperature":
		converted, err = convertTemperature(req.Value, req.FromUnit, req.ToUnit)
	case "area":
		converted, err = convertArea(req.Value, req.FromUnit, req.ToUnit)
	case "volume":
		converted, err = convertVolume(req.Value, req.FromUnit, req.ToUnit)
	default:
		return unitResponse{
			OriginalValue:  req.Value,
			FromUnit:       req.FromUnit,
			ToUnit:         req.ToUnit,
			ConvertedValue: req.Value,
			Category:       req.Category,
			Message:        "Unsupported unit category",
		}, fmt.Errorf("unsupported unit category: %s", req.Category)
	}

	if err != nil {
		return unitResponse{
			OriginalValue:  req.Value,
			FromUnit:       req.FromUnit,
			ToUnit:         req.ToUnit,
			ConvertedValue: req.Value,
			Category:       req.Category,
			Message:        err.Error(),
		}, err
	}

	return unitResponse{
		OriginalValue:  req.Value,
		FromUnit:       req.FromUnit,
		ToUnit:         req.ToUnit,
		ConvertedValue: converted,
		Category:       req.Category,
		Message:        fmt.Sprintf("%.4f %s = %.4f %s", req.Value, req.FromUnit, converted, req.ToUnit),
	}, nil
}

func convertLength(value float64, from, to string) (float64, error) {
	toMeters := map[string]float64{"m": 1, "km": 1000, "cm": 0.01, "mm": 0.001, "inch": 0.0254, "foot": 0.3048, "yard": 0.9144, "mile": 1609.34}
	fromM, fromOk := toMeters[from]
	toM, toOk := toMeters[to]
	if !fromOk || !toOk {
		return 0, fmt.Errorf("unsupported length unit")
	}
	return (value * fromM) / toM, nil
}

func convertWeight(value float64, from, to string) (float64, error) {
	toKgMap := map[string]float64{"kg": 1, "g": 0.001, "mg": 0.000001, "lb": 0.453592, "oz": 0.0283495}
	fromKg, fromOk := toKgMap[from]
	toKgVal, toOk := toKgMap[to]
	if !fromOk || !toOk {
		return 0, fmt.Errorf("unsupported weight unit")
	}
	return (value * fromKg) / toKgVal, nil
}

func convertTemperature(value float64, from, to string) (float64, error) {
	var celsius float64
	switch from {
	case "C":
		celsius = value
	case "F":
		celsius = (value - 32) * 5 / 9
	case "K":
		celsius = value - 273.15
	default:
		return 0, fmt.Errorf("unsupported temperature unit")
	}

	switch to {
	case "C":
		return celsius, nil
	case "F":
		return celsius*9/5 + 32, nil
	case "K":
		return celsius + 273.15, nil
	default:
		return 0, fmt.Errorf("unsupported temperature unit")
	}
}

func convertArea(value float64, from, to string) (float64, error) {
	toSqMMap := map[string]float64{"sqm": 1, "sqkm": 1000000, "sqft": 0.092903, "acre": 4046.86}
	fromSqM, fromOk := toSqMMap[from]
	toSqMVal, toOk := toSqMMap[to]
	if !fromOk || !toOk {
		return 0, fmt.Errorf("unsupported area unit")
	}
	return (value * fromSqM) / toSqMVal, nil
}

func convertVolume(value float64, from, to string) (float64, error) {
	toLMap := map[string]float64{"L": 1, "mL": 0.001, "gal": 3.78541, "cup": 0.236588}
	fromL, fromOk := toLMap[from]
	toLVal, toOk := toLMap[to]
	if !fromOk || !toOk {
		return 0, fmt.Errorf("unsupported volume unit")
	}
	return (value * fromL) / toLVal, nil
}
