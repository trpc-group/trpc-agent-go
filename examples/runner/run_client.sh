#!/bin/bash

# Check if the executable exists
if [ ! -f "./a2a" ]; then
    echo "Building the application..."
    go build -o a2a .
fi

# Run the client with MCP-related query
echo "Starting the A2A client to test MCP integration..."
./a2a -server=false -message="Hello! Can you help me with the following tasks using MCP tools: 1) Look up the weather in Tokyo using the weather lookup tool, 2) Convert 100 USD to EUR using the currency converter, and 3) Analyze this data series with statistics: 10, 15, 12, 18, 20, 22, 19"

echo "Client request completed." 