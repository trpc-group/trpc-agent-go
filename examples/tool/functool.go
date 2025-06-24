package main

type getWeatherInput struct {
	Location string `json:"location"`
}
type getWeatherOutput struct {
	Weather string `json:"weather"`
}

func getWeather(i getWeatherInput) getWeatherOutput {
	// In a real implementation, this function would call a weather API
	return getWeatherOutput{Weather: "Sunny, 25°C"}
}

// getPopulationInput represents the input for the get_population tool.
type getPopulationInput struct {
	City string `json:"city"`
}

// getPopulationOutput represents the output for the get_population tool.
type getPopulationOutput struct {
	Population int `json:"population"`
}

func getPopulation(i getPopulationInput) getPopulationOutput {
	// In a real implementation, this function would call a population API
	return getPopulationOutput{Population: 8000000} // Example population for London
}
