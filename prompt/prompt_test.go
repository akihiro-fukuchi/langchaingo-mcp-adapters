package prompt

import (
	"context"
	"errors"
	"testing"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
	. "github.com/ovechkin-dm/mockio/mock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tmc/langchaingo/llms"
)

func TestConvertMCPPromptMessageToLangchainMessage_Text(t *testing.T) {
	tests := []struct {
		name        string
		mcpMessage  mcp.PromptMessage
		expectedMsg llms.ChatMessage
		expectError bool
	}{
		{
			name: "User Text Message",
			mcpMessage: mcp.PromptMessage{
				Role:    mcp.RoleUser,
				Content: mcp.TextContent{Text: "Hello"},
			},
			expectedMsg: llms.HumanChatMessage{Content: "Hello"},
			expectError: false,
		},
		{
			name: "Assistant Text Message",
			mcpMessage: mcp.PromptMessage{
				Role:    mcp.RoleAssistant,
				Content: mcp.TextContent{Text: "Hi there"},
			},
			expectedMsg: llms.AIChatMessage{Content: "Hi there"},
			expectError: false,
		},
		{
			name: "User Empty Text Message",
			mcpMessage: mcp.PromptMessage{
				Role:    mcp.RoleUser,
				Content: mcp.TextContent{Text: ""}, // Empty text
			},
			expectedMsg: llms.HumanChatMessage{Content: ""},
			expectError: false,
		},
		{
			name: "Unsupported Role (system string)",
			mcpMessage: mcp.PromptMessage{
				Role:    "system",
				Content: mcp.TextContent{Text: "System prompt"},
			},
			expectedMsg: nil,
			expectError: true,
		},
		{
			name: "Unsupported Content (Image)",
			mcpMessage: mcp.PromptMessage{
				Role:    mcp.RoleUser,
				Content: mcp.ImageContent{ /* ... fields ... */ },
			},
			expectedMsg: nil,
			expectError: true,
		},
		{
			name: "Unsupported Content (Resource)",
			mcpMessage: mcp.PromptMessage{
				Role:    mcp.RoleUser,
				Content: mcp.EmbeddedResource{ /* ... fields ... */ },
			},
			expectedMsg: nil,
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lcMessage, err := convertMCPPromptMessageToLangchainMessage(tt.mcpMessage)

			if tt.expectError {
				assert.Error(t, err)
				assert.Nil(t, lcMessage)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expectedMsg, lcMessage)
			}
		})
	}
}

type MockMCPClient interface {
	client.MCPClient
}

func TestLoadMCPPrompt_Success_WithMessages(t *testing.T) {
	SetUp(t)
	mockClient := Mock[MockMCPClient]()

	promptName := "test-prompt-with-messages"
	args := map[string]string{"arg1": "value1"}

	mcpMessages := []mcp.PromptMessage{
		{Role: mcp.RoleUser, Content: mcp.TextContent{Text: "User message 1"}},
		{Role: mcp.RoleAssistant, Content: mcp.TextContent{Text: "Assistant message 1"}},
		{Role: mcp.RoleUser, Content: mcp.ImageContent{}}, // This unsupported type should be skipped
		{Role: mcp.RoleUser, Content: mcp.TextContent{Text: "User message 2"}},
	}
	expectedLangchainMessages := []llms.ChatMessage{
		llms.HumanChatMessage{Content: "User message 1"},
		llms.AIChatMessage{Content: "Assistant message 1"},
		llms.HumanChatMessage{Content: "User message 2"},
	}

	expectedRequest := mcp.GetPromptRequest{}
	expectedRequest.Params.Name = promptName
	expectedRequest.Params.Arguments = args

	When(mockClient.GetPrompt(Any[context.Context](), Equal(expectedRequest))).
		ThenReturn(&mcp.GetPromptResult{Messages: mcpMessages}, nil)

	langchainMessages, err := LoadMCPPrompt(context.Background(), mockClient, promptName, args)

	require.NoError(t, err)
	assert.Equal(t, expectedLangchainMessages, langchainMessages)

	Verify(mockClient, Once()).GetPrompt(Any[context.Context](), Equal(expectedRequest))
}

func TestLoadMCPPrompt_Success_EmptyMessages(t *testing.T) {
	SetUp(t)
	mockClient := Mock[MockMCPClient]()

	promptName := "test-prompt-empty"
	args := map[string]string{}

	mcpMessages := []mcp.PromptMessage{}
	expectedLangchainMessages := []llms.ChatMessage{}

	expectedRequest := mcp.GetPromptRequest{}
	expectedRequest.Params.Name = promptName
	expectedRequest.Params.Arguments = args

	When(mockClient.GetPrompt(Any[context.Context](), Equal(expectedRequest))).
		ThenReturn(&mcp.GetPromptResult{Messages: mcpMessages}, nil)

	langchainMessages, err := LoadMCPPrompt(context.Background(), mockClient, promptName, args)

	require.NoError(t, err)
	assert.Equal(t, expectedLangchainMessages, langchainMessages)
	assert.Empty(t, langchainMessages)

	Verify(mockClient, Once()).GetPrompt(Any[context.Context](), Equal(expectedRequest))
}

func TestLoadMCPPrompt_Success_NilArguments(t *testing.T) {
	SetUp(t)
	mockClient := Mock[MockMCPClient]()

	promptName := "test-prompt-nil-args"
	var args map[string]string = nil

	mcpMessages := []mcp.PromptMessage{
		{Role: mcp.RoleUser, Content: mcp.TextContent{Text: "User message"}},
	}
	expectedLangchainMessages := []llms.ChatMessage{
		llms.HumanChatMessage{Content: "User message"},
	}

	expectedRequest := mcp.GetPromptRequest{}
	expectedRequest.Params.Name = promptName
	expectedRequest.Params.Arguments = args // Arguments will be nil in the request

	When(mockClient.GetPrompt(Any[context.Context](), Equal(expectedRequest))).
		ThenReturn(&mcp.GetPromptResult{Messages: mcpMessages}, nil)

	langchainMessages, err := LoadMCPPrompt(context.Background(), mockClient, promptName, args)

	require.NoError(t, err)
	assert.Equal(t, expectedLangchainMessages, langchainMessages)

	Verify(mockClient, Once()).GetPrompt(Any[context.Context](), Equal(expectedRequest))
}

func TestLoadMCPPrompt_GetPromptError(t *testing.T) {
	SetUp(t)
	mockClient := Mock[MockMCPClient]()

	promptName := "error-prompt"
	args := map[string]string{}
	expectedError := errors.New("MCP client error")

	expectedRequest := mcp.GetPromptRequest{}
	expectedRequest.Params.Name = promptName
	expectedRequest.Params.Arguments = args

	When(mockClient.GetPrompt(Any[context.Context](), Equal(expectedRequest))).
		ThenReturn(nil, expectedError)

	langchainMessages, err := LoadMCPPrompt(context.Background(), mockClient, promptName, args)

	require.Error(t, err)
	assert.Nil(t, langchainMessages)
	assert.Contains(t, err.Error(), "failed to get MCP prompt")
	assert.Contains(t, err.Error(), expectedError.Error())

	Verify(mockClient, Once()).GetPrompt(Any[context.Context](), Equal(expectedRequest))
}

func TestLoadMCPPrompt_ConversionErrorSkipped(t *testing.T) {
	SetUp(t)
	mockClient := Mock[MockMCPClient]()

	promptName := "conversion-error-prompt"
	args := map[string]string{}

	mcpMessages := []mcp.PromptMessage{
		{Role: mcp.RoleUser, Content: mcp.TextContent{Text: "Valid user message"}},
		{Role: "system", Content: mcp.TextContent{Text: "Unsupported system message"}}, // This should be skipped
		{Role: mcp.RoleAssistant, Content: mcp.TextContent{Text: "Valid assistant message"}},
	}
	expectedLangchainMessages := []llms.ChatMessage{
		llms.HumanChatMessage{Content: "Valid user message"},
		llms.AIChatMessage{Content: "Valid assistant message"},
	}

	expectedRequest := mcp.GetPromptRequest{}
	expectedRequest.Params.Name = promptName
	expectedRequest.Params.Arguments = args

	When(mockClient.GetPrompt(Any[context.Context](), Equal(expectedRequest))).
		ThenReturn(&mcp.GetPromptResult{Messages: mcpMessages}, nil)

	langchainMessages, err := LoadMCPPrompt(context.Background(), mockClient, promptName, args)

	require.NoError(t, err) // LoadMCPPrompt itself shouldn't error, it just skips invalid messages
	assert.Equal(t, expectedLangchainMessages, langchainMessages)

	Verify(mockClient, Once()).GetPrompt(Any[context.Context](), Equal(expectedRequest))
}
