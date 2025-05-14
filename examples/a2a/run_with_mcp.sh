#!/bin/bash

# Set Google API key if not already set
if [ -z "$GOOGLE_API_KEY" ]; then
    echo "GOOGLE_API_KEY not set. Please set it before running this script."
    echo "Example: export GOOGLE_API_KEY=your_api_key_here"
    exit 1
fi

# Build the application
echo "Building the application..."
go build -o a2a_example .

# Run the server with MCP
echo "Starting the A2A server with MCP integration..."
./a2a_example -server -address localhost:8080 -mcp-address localhost:3000

echo "Server stopped." 