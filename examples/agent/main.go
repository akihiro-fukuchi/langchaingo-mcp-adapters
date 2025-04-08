package main

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/tmc/langchaingo/agents"
	"github.com/tmc/langchaingo/chains"
	"github.com/tmc/langchaingo/llms/openai" // Using OpenAI, ensure OPENAI_API_KEY is set

	mcpclient "github.com/akihiro-fukuchi/langchaingo-mcp-adapters/client"
	"github.com/mark3labs/mcp-go/mcp"
)

func main() {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, nil)))

	// Ensure OPENAI_API_KEY is set in your environment
	llm, err := openai.New()
	if err != nil {
		slog.Error("Failed to create OpenAI client. Make sure OPENAI_API_KEY is set.", "error", err)
		os.Exit(1)
	}

	// Assume math-server executable is in the parent directory relative to this example
	// You might need to adjust this path depending on your build process/structure
	serverExecutableName := "math-server" // Name of the built server executable
	serverDir := "."                      // Relative path to the directory containing the executable

	serverPath, err := filepath.Abs(filepath.Join(serverDir, serverExecutableName))
	if err != nil {
		slog.Error("Error getting absolute path for server", "error", err)
		os.Exit(1)
	}
	if _, err := os.Stat(serverPath); os.IsNotExist(err) {
		slog.Error("Math server executable not found. Build it first", "path", serverPath)
		os.Exit(1)
	}

	connections := map[string]mcpclient.ConnectionConfig{
		"math": mcpclient.StdioConnection{
			Transport: "stdio",
			Command:   serverPath,
			Args:      []string{},
		},
	}

	// Create the MultiServerMCPClient
	// Using default client info and capabilities for this example
	client := mcpclient.NewMultiServerMCPClient(connections, mcp.Implementation{}, mcp.ClientCapabilities{})

	ctx := context.Background()
	slog.Info("Starting MCP client and connecting to servers...")
	err = client.Start(ctx)
	if err != nil {
		slog.Error("Failed to start MCP client", "error", err)
		os.Exit(1)
	}

	defer func() {
		slog.Info("Closing MCP client connections...")
		if closeErr := client.Close(); closeErr != nil {
			slog.Error("Error closing MCP client", "error", closeErr)
		}
	}()

	mcpTools := client.GetTools()
	if len(mcpTools) == 0 {
		slog.Error("No tools loaded from MCP servers. Ensure the math-server is running and accessible.")
		os.Exit(1)
	}
	slog.Info("Successfully loaded tools from MCP servers", "count", len(mcpTools))
	for _, tool := range mcpTools {
		slog.Info("Loaded tool", "name", tool.Name(), "description", tool.Description())
	}

	// Create a LangchainGo agent using the loaded tools
	// ZeroShotReactDescription is a common choice
	slog.Info("Creating LangchainGo agent...")
	agent := agents.NewOneShotAgent(
		llm,
		mcpTools,
		agents.WithMaxIterations(5), // Limit iterations to prevent infinite loops
	)
	executor := agents.NewExecutor(agent)

	// --- Run Agent Example 1 ---
	prompt1 := "what's (3 + 5) * 12?"
	slog.Info("Running agent", "prompt", prompt1)
	result1, err := chains.Run(ctx, executor, prompt1)
	if err != nil {
		slog.Error("Agent execution failed for prompt 1", "error", err)
		os.Exit(1)
	}
	slog.Info("Agent Result 1", "result", result1)

	// --- Run Agent Example 2 ---
	prompt2 := "what is 7 multiplied by 6?"
	slog.Info("Running agent", "prompt", prompt2)
	// It's good practice to use a fresh executor run for distinct tasks if state might interfere,
	// though for OneShotAgent it might not matter as much.
	result2, err := chains.Run(ctx, executor, prompt2)
	if err != nil {
		slog.Error("Agent execution failed for prompt 2", "error", err)
		os.Exit(1)
	}
	slog.Info("Agent Result 2", "result", result2)

	slog.Info("Example finished.")
}
