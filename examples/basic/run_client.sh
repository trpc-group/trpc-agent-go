#!/bin/bash

# Check if the server is already running
if ! nc -z localhost 8080 &>/dev/null; then
    echo "Server is not running. Starting server in the background..."
    go run examples/basic/main.go &
    SERVER_PID=$!
    echo "Server started with PID: $SERVER_PID"
    
    # Give the server a moment to start
    sleep 2
    
    # Check if server started successfully
    if ! nc -z localhost 8080 &>/dev/null; then
        echo "Failed to start server. Please check for errors."
        kill $SERVER_PID 2>/dev/null
        exit 1
    fi
    echo "Server is running on http://localhost:8080"
else
    echo "Server is already running on http://localhost:8080"
    SERVER_STARTED=false
fi

# Run the client
echo "Starting client..."
go run examples/basic/cmd/client/main.go "$@"
CLIENT_EXIT_CODE=$?

# If we started the server, shut it down
if [ -n "$SERVER_PID" ]; then
    echo "Shutting down server (PID: $SERVER_PID)..."
    kill $SERVER_PID
fi

exit $CLIENT_EXIT_CODE 