package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	mcp "trpc.group/trpc-go/trpc-mcp-go"
)

func main() {
	server := mcp.NewStdioServer("stdio-mcp-server", "1.0.0",
		mcp.WithStdioServerLogger(mcp.GetDefaultLogger()),
	)

	// Register all tools.
	registerTools(server)

	log.Printf("Starting Advanced STDIO MCP Server...")
	log.Printf("Available tools: echo, text_transform, get_time, calculator, file_info, env_info, random")
	log.Printf("Using latest high-level API - much simpler implementation!")

	// Start server with one line of code!
	if err := server.Start(); err != nil {
		log.Fatalf("Server error: %v", err)
	}
}

// registerTools registers all available tools.
func registerTools(server *mcp.StdioServer) {
	// 1. Echo tool.
	echoTool := mcp.NewTool("echo",
		mcp.WithDescription("Echo tool, return the input message, optionally add a prefix"),
		mcp.WithString("message", mcp.Required(), mcp.Description("The message to echo")),
		mcp.WithString("prefix", mcp.Description("Optional prefix, default is 'Echo: '")),
	)
	server.RegisterTool(echoTool, handleEcho)

	// 2. Text processing tool.
	textTransformTool := mcp.NewTool("text_transform",
		mcp.WithDescription("Text processing tool, support uppercase, lowercase, capitalize, title"),
		mcp.WithString("text", mcp.Required(), mcp.Description("The text to transform")),
		mcp.WithString("transform", mcp.Required(), mcp.Description("Transform type: uppercase, lowercase, capitalize, title")),
	)
	server.RegisterTool(textTransformTool, handleTextTransform)

	// 3. Time tool.
	timeTool := mcp.NewTool("get_time",
		mcp.WithDescription("Get current time, support multiple formats and timezones"),
		mcp.WithString("format", mcp.Description("Time format, default is '2006-01-02 15:04:05'")),
		mcp.WithString("timezone", mcp.Description("Timezone, default is local timezone")),
	)
	server.RegisterTool(timeTool, handleGetTime)

	// 4. Calculator tool.
	calculatorTool := mcp.NewTool("calculator",
		mcp.WithDescription("Basic math calculator, support four arithmetic operations"),
		mcp.WithNumber("a", mcp.Required(), mcp.Description("First number")),
		mcp.WithNumber("b", mcp.Required(), mcp.Description("Second number")),
		mcp.WithString("operation", mcp.Required(), mcp.Description("Operation type: add, subtract, multiply, divide")),
	)
	server.RegisterTool(calculatorTool, handleCalculator)

	// 5. File system tool.
	fileInfoTool := mcp.NewTool("file_info",
		mcp.WithDescription("Get file or directory information"),
		mcp.WithString("path", mcp.Required(), mcp.Description("File or directory path")),
		mcp.WithString("type", mcp.Description("Information type: basic, detailed, list (directory list)")),
	)
	server.RegisterTool(fileInfoTool, handleFileInfo)

	// 6. Environment info tool.
	envInfoTool := mcp.NewTool("env_info",
		mcp.WithDescription("Get system environment information"),
		mcp.WithString("type", mcp.Description("Information type: all, user, hostname, pwd, env")),
	)
	server.RegisterTool(envInfoTool, handleEnvInfo)

	// 7. Random number generator.
	randomTool := mcp.NewTool("random",
		mcp.WithDescription("Generate random number or random string"),
		mcp.WithString("type", mcp.Required(), mcp.Description("Type: number, string, uuid")),
		mcp.WithNumber("min", mcp.Description("Minimum value (number type)")),
		mcp.WithNumber("max", mcp.Description("Maximum value (number type)")),
		mcp.WithNumber("length", mcp.Description("Length (string type)")),
	)
	server.RegisterTool(randomTool, handleRandom)
}

// handleEcho handles the echo tool.
func handleEcho(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	// Parse message parameter.
	message := ""
	if msgArg, ok := req.Params.Arguments["message"]; ok {
		if msgStr, ok := msgArg.(string); ok {
			message = msgStr
		}
	}
	if message == "" {
		return nil, fmt.Errorf("missing required parameter: message")
	}

	// Parse prefix parameter.
	prefix := "Echo: "
	if prefixArg, ok := req.Params.Arguments["prefix"]; ok {
		if prefixStr, ok := prefixArg.(string); ok && prefixStr != "" {
			prefix = prefixStr
		}
	}

	result := prefix + message

	return &mcp.CallToolResult{
		Content: []mcp.Content{
			mcp.NewTextContent(result),
		},
	}, nil
}

// handleTextTransform handles the text transform tool.
func handleTextTransform(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	// Parse text parameter.
	text := ""
	if textArg, ok := req.Params.Arguments["text"]; ok {
		if textStr, ok := textArg.(string); ok {
			text = textStr
		}
	}
	if text == "" {
		return nil, fmt.Errorf("missing required parameter: text")
	}

	// Parse transform parameter.
	transform := ""
	if transformArg, ok := req.Params.Arguments["transform"]; ok {
		if transformStr, ok := transformArg.(string); ok {
			transform = transformStr
		}
	}
	if transform == "" {
		return nil, fmt.Errorf("missing required parameter: transform")
	}

	var result string
	switch transform {
	case "uppercase":
		result = strings.ToUpper(text)
	case "lowercase":
		result = strings.ToLower(text)
	case "capitalize":
		if len(text) > 0 {
			result = strings.ToUpper(string(text[0])) + strings.ToLower(text[1:])
		}
	case "title":
		result = strings.Title(strings.ToLower(text))
	default:
		return nil, fmt.Errorf("unsupported transform type: %s. Use: uppercase, lowercase, capitalize, title", transform)
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{
			mcp.NewTextContent(fmt.Sprintf("Transformed text (%s): %s", transform, result)),
		},
	}, nil
}

// handleGetTime handles the time get tool.
func handleGetTime(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	// Parse format parameter.
	format := "2006-01-02 15:04:05"
	if formatArg, ok := req.Params.Arguments["format"]; ok {
		if formatStr, ok := formatArg.(string); ok && formatStr != "" {
			format = formatStr
		}
	}

	// Parse timezone parameter.
	timezone := ""
	if timezoneArg, ok := req.Params.Arguments["timezone"]; ok {
		if timezoneStr, ok := timezoneArg.(string); ok {
			timezone = timezoneStr
		}
	}

	now := time.Now()

	// If timezone is specified, try to load it.
	if timezone != "" {
		if loc, err := time.LoadLocation(timezone); err == nil {
			now = now.In(loc)
		} else {
			return nil, fmt.Errorf("invalid timezone: %s", timezone)
		}
	}

	formattedTime := now.Format(format)
	result := fmt.Sprintf("Current time: %s", formattedTime)
	if timezone != "" {
		result += fmt.Sprintf(" (%s)", timezone)
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{
			mcp.NewTextContent(result),
		},
	}, nil
}

// handleCalculator handles the calculator tool.
func handleCalculator(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	// Parse a parameter.
	var a float64
	if aArg, ok := req.Params.Arguments["a"]; ok {
		if aFloat, ok := aArg.(float64); ok {
			a = aFloat
		} else if aInt, ok := aArg.(int); ok {
			a = float64(aInt)
		} else {
			return nil, fmt.Errorf("invalid parameter 'a': must be a number")
		}
	} else {
		return nil, fmt.Errorf("missing required parameter: a")
	}

	// Parse b parameter.
	var b float64
	if bArg, ok := req.Params.Arguments["b"]; ok {
		if bFloat, ok := bArg.(float64); ok {
			b = bFloat
		} else if bInt, ok := bArg.(int); ok {
			b = float64(bInt)
		} else {
			return nil, fmt.Errorf("invalid parameter 'b': must be a number")
		}
	} else {
		return nil, fmt.Errorf("missing required parameter: b")
	}

	// Parse operation parameter.
	operation := ""
	if opArg, ok := req.Params.Arguments["operation"]; ok {
		if opStr, ok := opArg.(string); ok {
			operation = opStr
		}
	}
	if operation == "" {
		return nil, fmt.Errorf("missing required parameter: operation")
	}

	var result float64
	var opSymbol string

	switch operation {
	case "add":
		result = a + b
		opSymbol = "+"
	case "subtract":
		result = a - b
		opSymbol = "-"
	case "multiply":
		result = a * b
		opSymbol = "*"
	case "divide":
		if b == 0 {
			return nil, fmt.Errorf("division by zero")
		}
		result = a / b
		opSymbol = "/"
	default:
		return nil, fmt.Errorf("unsupported operation: %s. Use: add, subtract, multiply, divide", operation)
	}

	resultText := fmt.Sprintf("Calculation: %.2f %s %.2f = %.2f", a, opSymbol, b, result)

	return &mcp.CallToolResult{
		Content: []mcp.Content{
			mcp.NewTextContent(resultText),
		},
	}, nil
}

// handleFileInfo handles the file info tool.
func handleFileInfo(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	// Parse path parameter.
	path := ""
	if pathArg, ok := req.Params.Arguments["path"]; ok {
		if pathStr, ok := pathArg.(string); ok {
			path = pathStr
		}
	}
	if path == "" {
		return nil, fmt.Errorf("missing required parameter: path")
	}

	// Parse type parameter.
	infoType := "basic"
	if typeArg, ok := req.Params.Arguments["type"]; ok {
		if typeStr, ok := typeArg.(string); ok && typeStr != "" {
			infoType = typeStr
		}
	}

	// Get absolute path.
	absPath, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("invalid path: %v", err)
	}

	// Get file information.
	info, err := os.Stat(absPath)
	if err != nil {
		return nil, fmt.Errorf("cannot access path: %v", err)
	}

	var result string
	switch infoType {
	case "basic":
		fileType := "File"
		if info.IsDir() {
			fileType = "Directory"
		}
		result = fmt.Sprintf("Path: %s\nType: %s\nSize: %d bytes\nModified: %s",
			absPath, fileType, info.Size(), info.ModTime().Format("2006-01-02 15:04:05"))

	case "detailed":
		fileType := "File"
		if info.IsDir() {
			fileType = "Directory"
		}
		result = fmt.Sprintf("Path: %s\nType: %s\nSize: %d bytes\nMode: %s\nModified: %s",
			absPath, fileType, info.Size(), info.Mode().String(), info.ModTime().Format("2006-01-02 15:04:05"))

	case "list":
		if !info.IsDir() {
			return nil, fmt.Errorf("path is not a directory")
		}

		entries, err := os.ReadDir(absPath)
		if err != nil {
			return nil, fmt.Errorf("cannot read directory: %v", err)
		}

		var items []string
		for _, entry := range entries {
			entryType := "File"
			if entry.IsDir() {
				entryType = "Dir"
			}
			items = append(items, fmt.Sprintf("%-20s %s", entry.Name(), entryType))
		}
		result = fmt.Sprintf("Directory listing for: %s\n%s", absPath, strings.Join(items, "\n"))
	default:
		return nil, fmt.Errorf("unsupported info type: %s", infoType)
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{
			mcp.NewTextContent(result),
		},
	}, nil
}

// handleEnvInfo handles the environment info tool.
func handleEnvInfo(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	// Parse type parameter.
	infoType := "all"
	if typeArg, ok := req.Params.Arguments["type"]; ok {
		if typeStr, ok := typeArg.(string); ok && typeStr != "" {
			infoType = typeStr
		}
	}

	var result string
	switch infoType {
	case "user":
		if u, err := user.Current(); err == nil {
			result = fmt.Sprintf("Username: %s\nUID: %s\nGID: %s\nHome: %s",
				u.Username, u.Uid, u.Gid, u.HomeDir)
		} else {
			result = "Could not get user information"
		}
	case "hostname":
		if hostname, err := os.Hostname(); err == nil {
			result = fmt.Sprintf("Hostname: %s", hostname)
		} else {
			result = "Could not get hostname"
		}
	case "pwd":
		if pwd, err := os.Getwd(); err == nil {
			result = fmt.Sprintf("Current directory: %s", pwd)
		} else {
			result = "Could not get current directory"
		}
	case "env":
		env := os.Environ()
		result = fmt.Sprintf("Environment variables (%d total):\n%s",
			len(env), strings.Join(env[:min(10, len(env))], "\n"))
		if len(env) > 10 {
			result += "\n... (showing first 10)"
		}
	case "all":
		parts := []string{}

		if hostname, err := os.Hostname(); err == nil {
			parts = append(parts, fmt.Sprintf("Hostname: %s", hostname))
		}

		if u, err := user.Current(); err == nil {
			parts = append(parts, fmt.Sprintf("User: %s (%s)", u.Username, u.Uid))
		}

		if pwd, err := os.Getwd(); err == nil {
			parts = append(parts, fmt.Sprintf("Working directory: %s", pwd))
		}

		result = strings.Join(parts, "\n")
	default:
		return nil, fmt.Errorf("unsupported info type: %s", infoType)
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{
			mcp.NewTextContent(result),
		},
	}, nil
}

// handleRandom handles the random number generator tool.
func handleRandom(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	// Parse type parameter.
	randType := ""
	if typeArg, ok := req.Params.Arguments["type"]; ok {
		if typeStr, ok := typeArg.(string); ok {
			randType = typeStr
		}
	}
	if randType == "" {
		return nil, fmt.Errorf("missing required parameter: type")
	}

	var result string
	switch randType {
	case "number":
		// Parse min parameter.
		min := 0.0
		if minArg, ok := req.Params.Arguments["min"]; ok {
			if minFloat, ok := minArg.(float64); ok {
				min = minFloat
			} else if minInt, ok := minArg.(int); ok {
				min = float64(minInt)
			}
		}

		// Parse max parameter.
		max := 100.0
		if maxArg, ok := req.Params.Arguments["max"]; ok {
			if maxFloat, ok := maxArg.(float64); ok {
				max = maxFloat
			} else if maxInt, ok := maxArg.(int); ok {
				max = float64(maxInt)
			}
		}

		if max <= min {
			return nil, fmt.Errorf("max must be greater than min")
		}

		// Simple random number generation (based on time).
		import_time := time.Now().UnixNano()
		randomValue := min + float64(import_time%int64(max-min))
		result = strconv.FormatFloat(randomValue, 'f', 2, 64)

	case "string":
		// Parse length parameter.
		length := 8
		if lengthArg, ok := req.Params.Arguments["length"]; ok {
			if lengthFloat, ok := lengthArg.(float64); ok {
				length = int(lengthFloat)
			} else if lengthInt, ok := lengthArg.(int); ok {
				length = lengthInt
			}
		}

		if length <= 0 || length > 100 {
			return nil, fmt.Errorf("length must be between 1 and 100")
		}

		chars := "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
		timeNano := time.Now().UnixNano()
		var randomStr strings.Builder
		for i := 0; i < length; i++ {
			randomStr.WriteByte(chars[(timeNano+int64(i))%int64(len(chars))])
		}
		result = randomStr.String()

	case "uuid":
		// Simple UUID v4 format generation.
		timeNow := time.Now().UnixNano()
		result = fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
			timeNow&0xFFFFFFFF,
			(timeNow>>32)&0xFFFF,
			((timeNow>>48)&0x0FFF)|0x4000,
			((timeNow>>16)&0x3FFF)|0x8000,
			timeNow&0xFFFFFFFFFFFF,
		)
	default:
		return nil, fmt.Errorf("unsupported random type: %s", randType)
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{
			mcp.NewTextContent(result),
		},
	}, nil
}
