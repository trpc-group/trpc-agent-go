//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.
// All rights reserved.
//
// If you have downloaded a copy of the tRPC source code from Tencent,
// please note that tRPC source code is licensed under the  Apache 2.0 License,
// A copy of the Apache 2.0 License is included in this file.
//
//

package main

import (
	"context"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

type getWeatherInput struct {
	Location string `json:"location"`
}
type getWeatherOutput struct {
	Weather string `json:"weather"`
}

func getWeather(_ context.Context, i getWeatherInput) (getWeatherOutput, error) {
	// In a real implementation, this function would call a weather API
	return getWeatherOutput{Weather: "Sunny, 25°C"}, nil
}

func getStreamableWeather(input getWeatherInput) *tool.StreamReader {
	stream := tool.NewStream(10)
	go func() {
		result := "Sunny, 25°C"
		for i := 0; i < len(result); i++ {
			output := tool.StreamChunk{
				Content: getWeatherOutput{
					Weather: result[i : i+1],
				},
				Metadata: tool.Metadata{CreatedAt: time.Now()},
			}
			if closed := stream.Writer.Send(output, nil); closed {
				break
			}
			time.Sleep(10 * time.Millisecond) // Simulate delay
		}
		stream.Writer.Close()
	}()

	return stream.Reader
}

// getPopulationInput represents the input for the get_population tool.
type getPopulationInput struct {
	City string `json:"city"`
}

// getPopulationOutput represents the output for the get_population tool.
type getPopulationOutput struct {
	Population int `json:"population"`
}

func getPopulation(_ context.Context, i getPopulationInput) (getPopulationOutput, error) {
	// In a real implementation, this function would call a population API
	return getPopulationOutput{Population: 8000000}, nil // Example population for London
}
