package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"strconv"
	"strings"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/tmc/langchaingo/callbacks"
	"github.com/tmc/langchaingo/tools"
)

// LangchainMCPTool wraps an mcp.Tool to make it compatible with langchaingo/tools.Tool interface.
type LangchainMCPTool struct {
	mcpTool   mcp.Tool
	mcpClient client.MCPClient
	callbacks callbacks.Handler // Optional callback handler
}

var _ tools.Tool = (*LangchainMCPTool)(nil)

// NewLangchainMCPTool creates a new LangchainMCPTool wrapper.
func NewLangchainMCPTool(mcpTool mcp.Tool, mcpClient client.MCPClient, handler callbacks.Handler) *LangchainMCPTool {
	return &LangchainMCPTool{
		mcpTool:   mcpTool,
		mcpClient: mcpClient,
		callbacks: handler,
	}
}

// Name returns the name of the MCP tool.
func (t *LangchainMCPTool) Name() string {
	return t.mcpTool.Name
}

// Description returns the description of the MCP tool.
func (t *LangchainMCPTool) Description() string {
	return t.mcpTool.Description
}

// Call executes the MCP tool.
// The input string is expected to be a JSON object representing the arguments.
func (t *LangchainMCPTool) Call(ctx context.Context, input string) (string, error) {
	slog.Debug("LangchainMCPTool.Call received input", "tool_name", t.Name(), "input", input)
	if t.callbacks != nil {
		t.callbacks.HandleToolStart(ctx, input)
	}

	// Parse the JSON input string into arguments map
	var arguments map[string]interface{}
	jsonErr := json.Unmarshal([]byte(input), &arguments)
	if jsonErr != nil {
		slog.Debug("LangchainMCPTool.Call input is not valid JSON, attempting other parsing methods", "tool_name", t.Name(), "input", input, "error", jsonErr)

		// Attempt 1: Comma-separated numbers for multi-arg number tools
		parts := strings.Split(input, ",")
		requiredArgs := t.mcpTool.InputSchema.Required // Get required arg names
		isMultiNumberTool := true
		argNames := make([]string, 0, len(t.mcpTool.InputSchema.Properties))

		// Check if all properties are numbers and collect names
		for name, schema := range t.mcpTool.InputSchema.Properties {
			propSchema, ok := schema.(map[string]interface{})
			if !ok || propSchema["type"] != "number" {
				isMultiNumberTool = false
			}
			argNames = append(argNames, name)
		}
		// Sort argNames to have a deterministic order for assignment when using comma-separated values
		sort.Strings(argNames)

		// Only attempt comma-separated parsing if it looks like a multi-number tool
		// and the number of parts matches the number of *required* arguments.
		// This is an assumption based on common agent behavior.
		if len(parts) > 1 && isMultiNumberTool && len(parts) == len(requiredArgs) {
			slog.Debug("LangchainMCPTool.Call attempting to parse as comma-separated numbers", "tool_name", t.Name(), "input", input, "required_args", requiredArgs, "sorted_arg_names", argNames)
			parsedArgs := make(map[string]interface{})
			parseSuccess := true
			// Assign parts based on the *sorted* order of required argument names
			// This assumes the agent provides values in the order defined by the sorted required keys.
			sortedRequiredArgs := make([]string, len(requiredArgs))
			copy(sortedRequiredArgs, requiredArgs)
			sort.Strings(sortedRequiredArgs)

			for i, part := range parts {
				trimmedPart := strings.TrimSpace(part)
				num, parseErr := strconv.ParseFloat(trimmedPart, 64)
				if parseErr != nil {
					slog.Debug("LangchainMCPTool.Call failed to parse part as float64", "tool_name", t.Name(), "part", trimmedPart, "error", parseErr)
					parseSuccess = false
					break
				}
				// Assign to the i-th required argument name (after sorting)
				if i < len(sortedRequiredArgs) {
					targetArgName := sortedRequiredArgs[i]
					parsedArgs[targetArgName] = num
					slog.Debug("LangchainMCPTool.Call parsed part for arg", "tool_name", t.Name(), "part_index", i, "part_value", trimmedPart, "parsed_number", num, "arg_name", targetArgName)
				} else {
					// This case should ideally not be reached due to the len(parts) == len(requiredArgs) check
					slog.Warn("LangchainMCPTool.Call more parts than required arguments, skipping part", "tool_name", t.Name(), "part_value", trimmedPart)
					parseSuccess = false
					break
				}
			}
			if parseSuccess {
				slog.Debug("LangchainMCPTool.Call successfully parsed comma-separated numbers", "tool_name", t.Name())
				arguments = parsedArgs
				jsonErr = nil // Clear the JSON error as we succeeded with another method
			} else {
				slog.Debug("LangchainMCPTool.Call failed to parse all parts as comma-separated numbers", "tool_name", t.Name())
			}
		}

		// Attempt 2: Single string argument fallback (if JSON and comma-sep failed)
		if jsonErr != nil && len(t.mcpTool.InputSchema.Properties) == 1 {
			singleArgName := argNames[0]
			propSchema, ok := t.mcpTool.InputSchema.Properties[singleArgName].(map[string]interface{})
			if ok && propSchema["type"] == "string" {
				slog.Debug("LangchainMCPTool.Call fallback successful, using input as single string argument", "tool_name", t.Name(), "arg_name", singleArgName)
				arguments = map[string]interface{}{singleArgName: input}
				jsonErr = nil // Clear the JSON error
			}
		}

		// If all parsing attempts failed
		if jsonErr != nil {
			err := fmt.Errorf("failed to parse tool input '%s': not valid JSON and other parsing attempts failed: %w", input, jsonErr)
			slog.Error("LangchainMCPTool.Call all parsing attempts failed", "tool_name", t.Name(), "error", err)
			if t.callbacks != nil {
				t.callbacks.HandleToolError(ctx, err)
			}
			return fmt.Sprintf("Error: %s", err.Error()), nil
		}
	}

	slog.Debug("LangchainMCPTool.Call parsed arguments", "tool_name", t.Name(), "arguments", arguments)

	// Create the MCP CallToolRequest
	request := mcp.CallToolRequest{}
	request.Params.Name = t.mcpTool.Name
	request.Params.Arguments = arguments

	// Call the MCP tool via the client
	slog.Debug("LangchainMCPTool.Call calling MCP client...", "tool_name", t.Name())
	result, err := t.mcpClient.CallTool(ctx, request)
	if err != nil {
		err = fmt.Errorf("failed to call MCP tool %s: %w", t.mcpTool.Name, err)
		slog.Error("LangchainMCPTool.Call MCP client call failed", "tool_name", t.Name(), "error", err)
		if t.callbacks != nil {
			t.callbacks.HandleToolError(ctx, err)
		}
		// Return error message as string output, nil error
		return fmt.Sprintf("Error calling tool %s: %s", t.mcpTool.Name, err.Error()), nil
	}
	slog.Debug("LangchainMCPTool.Call MCP client call successful", "tool_name", t.Name(), "result", result)

	// Process the result
	output, toolErr := processCallToolResult(result) // toolErr will contain the error message if result.IsError is true
	if toolErr != nil {
		// The error message from the tool is already in 'output' (processCallToolResult returns the text content even on error)
		slog.Error("LangchainMCPTool.Call tool execution resulted in error", "tool_name", t.Name(), "error", toolErr, "output", output)
		if t.callbacks != nil {
			t.callbacks.HandleToolError(ctx, fmt.Errorf("tool %s execution failed: %w", t.Name(), toolErr))
		}
		// Return the error message from the tool as the output string, nil error
		return output, nil
	}

	slog.Debug("LangchainMCPTool.Call returning output", "tool_name", t.Name(), "output", output)
	if t.callbacks != nil {
		t.callbacks.HandleToolEnd(ctx, output)
	}
	return output, nil
}

// processCallToolResult extracts the text content from the MCP tool result.
// If result.IsError is true, it returns the extracted text and a non-nil error containing that text.
func processCallToolResult(result *mcp.CallToolResult) (string, error) {
	var outputBuilder strings.Builder

	for _, content := range result.Content {
		if textContent, ok := content.(mcp.TextContent); ok {
			if outputBuilder.Len() > 0 {
				outputBuilder.WriteString("\n") // Add newline between multiple text parts
			}
			outputBuilder.WriteString(textContent.Text)
		}
		// Ignore non-text content for now as planned
	}

	outputText := outputBuilder.String()

	if result.IsError {
		// Return the output text AND a non-nil error containing the same text
		return outputText, fmt.Errorf("%s", outputText)
	}

	return outputText, nil
}

// LoadMCPTools fetches the list of tools from the MCP server and converts them
// into LangchainGo compatible tools.
func LoadMCPTools(ctx context.Context, mcpClient client.MCPClient) ([]tools.Tool, error) {
	listRequest := mcp.ListToolsRequest{}
	listResult, err := mcpClient.ListTools(ctx, listRequest)
	if err != nil {
		return nil, fmt.Errorf("failed to list MCP tools: %w", err)
	}

	langchainTools := make([]tools.Tool, 0, len(listResult.Tools))
	for _, mcpTool := range listResult.Tools {
		// Assuming no specific callback handler for now, pass nil
		lcTool := NewLangchainMCPTool(mcpTool, mcpClient, nil)
		langchainTools = append(langchainTools, lcTool)
	}

	return langchainTools, nil
}
