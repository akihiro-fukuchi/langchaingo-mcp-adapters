package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

func handleAddTool(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	a, ok1 := request.Params.Arguments["a"].(float64)
	b, ok2 := request.Params.Arguments["b"].(float64)
	if !ok1 || !ok2 {
		return mcp.NewToolResultError("invalid number arguments"), nil
	}
	sum := a + b
	return mcp.NewToolResultText(fmt.Sprintf("%f", sum)), nil
}

func handleMultiplyTool(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	a, ok1 := request.Params.Arguments["a"].(float64)
	b, ok2 := request.Params.Arguments["b"].(float64)
	if !ok1 || !ok2 {
		return mcp.NewToolResultError("invalid number arguments"), nil
	}
	product := a * b
	return mcp.NewToolResultText(fmt.Sprintf("%f", product)), nil
}

// MCPServer is a simple math server that provides two tools: add and multiply.
func main() {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, nil)))

	mcpServer := server.NewMCPServer("math-server-go", "1.0.0", server.WithToolCapabilities(true))

	mcpServer.AddTool(mcp.NewTool("add",
		mcp.WithDescription("Add two numbers"),
		mcp.WithNumber("a", mcp.Description("First number"), mcp.Required()),
		mcp.WithNumber("b", mcp.Description("Second number"), mcp.Required()),
	), handleAddTool)

	mcpServer.AddTool(mcp.NewTool("multiply",
		mcp.WithDescription("Multiply two numbers"),
		mcp.WithNumber("a", mcp.Description("First number"), mcp.Required()),
		mcp.WithNumber("b", mcp.Description("Second number"), mcp.Required()),
	), handleMultiplyTool)

	slog.Info("Starting MCP Math Server via stdio...")
	if err := server.ServeStdio(mcpServer); err != nil {
		slog.Error("Server error", "error", err)
		os.Exit(1)
	}
}
