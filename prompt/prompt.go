package prompt

import (
	"context"
	"fmt"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/tmc/langchaingo/llms"
)

// convertMCPPromptMessageToLangchainMessage converts an MCP prompt message to a LangchainGo message.
func convertMCPPromptMessageToLangchainMessage(message mcp.PromptMessage) (llms.ChatMessage, error) {
	switch content := message.Content.(type) {
	case mcp.TextContent:
		switch message.Role {
		case mcp.RoleUser:
			return llms.HumanChatMessage{Content: content.Text}, nil
		case mcp.RoleAssistant:
			return llms.AIChatMessage{Content: content.Text}, nil
		default:
			return nil, fmt.Errorf("unsupported prompt message role: %s", message.Role)
		}
	case mcp.ImageContent:
		// LangchainGo schema currently doesn't have a standard way to represent
		// multimodal content directly in HumanChatMessage/AIChatMessage in the same way Python does.
		// We might need to adapt this based on how LangchainGo evolves or use specific message types if available.
		// For now, return an error or a placeholder.
		return nil, fmt.Errorf("unsupported prompt message content type: image")
	case mcp.EmbeddedResource:
		// Similar to ImageContent, handling embedded resources needs clarification
		// within LangchainGo's schema.
		return nil, fmt.Errorf("unsupported prompt message content type: resource")
	default:
		return nil, fmt.Errorf("unknown prompt message content type: %T", message.Content)
	}
}

// LoadMCPPrompt fetches an MCP prompt by name and converts its messages to LangchainGo format.
func LoadMCPPrompt(ctx context.Context, mcpClient client.MCPClient, name string, arguments map[string]string) ([]llms.ChatMessage, error) {
	request := mcp.GetPromptRequest{}
	request.Params.Name = name
	request.Params.Arguments = arguments // mcp-go expects map[string]string

	response, err := mcpClient.GetPrompt(ctx, request)
	if err != nil {
		return nil, fmt.Errorf("failed to get MCP prompt '%s': %w", name, err)
	}

	langchainMessages := make([]llms.ChatMessage, 0, len(response.Messages))
	for _, mcpMessage := range response.Messages {
		lcMessage, err := convertMCPPromptMessageToLangchainMessage(mcpMessage)
		if err != nil {
			// Skip unsupported messages for now, or return error depending on desired behavior
			// For now, let's skip and log (or just skip)
			// fmt.Printf("Skipping unsupported message type in prompt %s: %v\n", name, err)
			continue
		}
		langchainMessages = append(langchainMessages, lcMessage)
	}

	return langchainMessages, nil
}
