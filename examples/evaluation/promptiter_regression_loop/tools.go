//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"context"

	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

type weatherArgs struct {
	City string `json:"city" jsonschema:"description=Canonical English city name,required"`
}

type weatherResult struct {
	City         string `json:"city"`
	Condition    string `json:"condition"`
	TemperatureC int    `json:"temperatureC"`
}

type distanceArgs struct {
	From string `json:"from" jsonschema:"description=Canonical English origin city,required"`
	To   string `json:"to" jsonschema:"description=Canonical English destination city,required"`
}

type distanceResult struct {
	From       string `json:"from"`
	To         string `json:"to"`
	DistanceKM int    `json:"distanceKm"`
}

func cityServiceTools() []tool.Tool {
	return []tool.Tool{
		function.NewFunctionTool(
			lookupWeather,
			function.WithName("weather_lookup"),
			function.WithDescription("Look up deterministic weather for a canonical city name."),
		),
		function.NewFunctionTool(
			lookupDistance,
			function.WithName("distance_lookup"),
			function.WithDescription("Look up deterministic road distance between canonical city names."),
		),
	}
}

func lookupWeather(_ context.Context, args weatherArgs) (weatherResult, error) {
	switch args.City {
	case "Shanghai":
		return weatherResult{City: args.City, Condition: "sunny", TemperatureC: 25}, nil
	case "Guangzhou":
		return weatherResult{City: args.City, Condition: "cloudy", TemperatureC: 28}, nil
	default:
		return weatherResult{City: args.City, Condition: "unknown", TemperatureC: 0}, nil
	}
}

func lookupDistance(_ context.Context, args distanceArgs) (distanceResult, error) {
	switch {
	case args.From == "Beijing" && args.To == "Tianjin":
		return distanceResult{From: args.From, To: args.To, DistanceKM: 120}, nil
	case args.From == "Suzhou" && args.To == "Hangzhou":
		return distanceResult{From: args.From, To: args.To, DistanceKM: 160}, nil
	default:
		return distanceResult{From: args.From, To: args.To, DistanceKM: 0}, nil
	}
}
