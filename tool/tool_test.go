package tool

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
	. "github.com/ovechkin-dm/mockio/mock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/tmc/langchaingo/llms"
	"github.com/tmc/langchaingo/schema"
)

// --- Mocks ---

type MockMCPClient interface {
	client.MCPClient
}

type MockCallbackHandler struct {
	mock.Mock
}

func (m *MockCallbackHandler) HandleText(ctx context.Context, text string) {}
func (m *MockCallbackHandler) HandleLLMStart(ctx context.Context, prompts []string) {
	m.Called(ctx, prompts)
}
func (m *MockCallbackHandler) HandleLLMGenerateContentStart(ctx context.Context, ms []llms.MessageContent) {
	m.Called(ctx, ms)
}
func (m *MockCallbackHandler) HandleLLMError(ctx context.Context, err error) { m.Called(ctx, err) }
func (m *MockCallbackHandler) HandleLLMGenerateContentEnd(ctx context.Context, res *llms.ContentResponse) {
	m.Called(ctx, res)
}
func (m *MockCallbackHandler) HandleChainStart(ctx context.Context, inputs map[string]any) {
	m.Called(ctx, inputs)
}
func (m *MockCallbackHandler) HandleChainEnd(ctx context.Context, outputs map[string]any) {
	m.Called(ctx, outputs)
}
func (m *MockCallbackHandler) HandleChainError(ctx context.Context, err error) { m.Called(ctx, err) }
func (m *MockCallbackHandler) HandleToolStart(ctx context.Context, input string) {
	m.Called(ctx, input)
}
func (m *MockCallbackHandler) HandleToolEnd(ctx context.Context, output string) {
	m.Called(ctx, output)
}
func (m *MockCallbackHandler) HandleToolError(ctx context.Context, err error) { m.Called(ctx, err) }
func (m *MockCallbackHandler) HandleAgentAction(ctx context.Context, action schema.AgentAction) {
	m.Called(ctx, action)
}
func (m *MockCallbackHandler) HandleAgentFinish(ctx context.Context, finish schema.AgentFinish) {
	m.Called(ctx, finish)
}
func (m *MockCallbackHandler) HandleRetrieverStart(ctx context.Context, query string) {
	m.Called(ctx, query)
}
func (m *MockCallbackHandler) HandleRetrieverEnd(ctx context.Context, query string, documents []schema.Document) {
	m.Called(ctx, query, documents)
}
func (m *MockCallbackHandler) HandleStreamingFunc(ctx context.Context, chunk []byte) {
	m.Called(ctx, chunk)
}

// --- Tests for processCallToolResult ---

func TestProcessCallToolResult(t *testing.T) {
	tests := []struct {
		name          string
		result        *mcp.CallToolResult
		expectedText  string
		expectedError bool
		errorContains string
	}{
		{
			name: "Success with single text content",
			result: &mcp.CallToolResult{
				Content: []mcp.Content{mcp.TextContent{Text: "Success output"}},
				IsError: false,
			},
			expectedText:  "Success output",
			expectedError: false,
		},
		{
			name: "Success with multiple text content",
			result: &mcp.CallToolResult{
				Content: []mcp.Content{
					mcp.TextContent{Text: "Part 1"},
					mcp.TextContent{Text: "Part 2"},
				},
				IsError: false,
			},
			expectedText:  "Part 1\nPart 2",
			expectedError: false,
		},
		{
			name: "Success with mixed content (ignores non-text)",
			result: &mcp.CallToolResult{
				Content: []mcp.Content{
					mcp.TextContent{Text: "Text part"},
					mcp.ImageContent{},
					mcp.TextContent{Text: "Another text part"},
				},
				IsError: false,
			},
			expectedText:  "Text part\nAnother text part",
			expectedError: false,
		},
		{
			name: "Error result with text content",
			result: &mcp.CallToolResult{
				Content: []mcp.Content{mcp.TextContent{Text: "Tool failed: reason"}},
				IsError: true,
			},
			expectedText:  "Tool failed: reason",
			expectedError: true,
			errorContains: "Tool failed: reason",
		},
		{
			name: "Error result with multiple text content",
			result: &mcp.CallToolResult{
				Content: []mcp.Content{
					mcp.TextContent{Text: "Error occurred."},
					mcp.TextContent{Text: "Details here."},
				},
				IsError: true,
			},
			expectedText:  "Error occurred.\nDetails here.",
			expectedError: true,
			errorContains: "Error occurred.\nDetails here.",
		},
		{
			name: "Error result with no text content",
			result: &mcp.CallToolResult{
				Content: []mcp.Content{mcp.ImageContent{}},
				IsError: true,
			},
			expectedText:  "",
			expectedError: true,
			errorContains: "",
		},
		{
			name:          "Nil result",
			result:        nil,
			expectedText:  "",
			expectedError: true,
			errorContains: "nil result",
		},
		{
			name:          "Empty result",
			result:        &mcp.CallToolResult{},
			expectedText:  "",
			expectedError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var output string
			var err error
			defer func() {
				if r := recover(); r != nil && tt.name == "Nil result" {
					err = fmt.Errorf("panic processing nil result: %v", r)
				} else if r != nil {
					panic(r)
				}
			}()

			if tt.result == nil && tt.name == "Nil result" {
				err = errors.New("cannot process nil result")
			} else if tt.result != nil {
				output, err = processCallToolResult(tt.result)
			}

			assert.Equal(t, tt.expectedText, output)
			if tt.expectedError {
				assert.Error(t, err)
				if tt.errorContains != "" {
					assert.Contains(t, err.Error(), tt.errorContains)
				}
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// --- Tests for LangchainMCPTool ---

func TestLangchainMCPTool_NameDescription(t *testing.T) {
	mcpTool := mcp.Tool{
		Name:        "test-tool",
		Description: "A tool for testing",
		InputSchema: mcp.ToolInputSchema{},
	}
	lcTool := NewLangchainMCPTool(mcpTool, nil, nil)

	assert.Equal(t, "test-tool", lcTool.Name())
	assert.Equal(t, "A tool for testing", lcTool.Description())
}

func TestLangchainMCPTool_Call_Success_JSONInput(t *testing.T) {
	SetUp(t)
	mockClient := Mock[MockMCPClient]()
	mockHandler := new(MockCallbackHandler)

	mcpTool := mcp.Tool{
		Name:        "json-tool",
		Description: "Accepts JSON",
		InputSchema: mcp.ToolInputSchema{
			Type: "object",
			Properties: map[string]any{
				"param1": map[string]any{"type": "string"},
				"param2": map[string]any{"type": "number"},
			},
			Required: []string{"param1"},
		},
	}
	lcTool := NewLangchainMCPTool(mcpTool, mockClient, mockHandler)

	inputJSON := `{"param1": "value1", "param2": 123}`
	expectedArgs := map[string]interface{}{"param1": "value1", "param2": float64(123)}
	expectedRequest := mcp.CallToolRequest{}
	expectedRequest.Params.Name = mcpTool.Name
	expectedRequest.Params.Arguments = expectedArgs

	mockResult := &mcp.CallToolResult{
		Content: []mcp.Content{mcp.TextContent{Text: "JSON success"}},
		IsError: false,
	}

	mockHandler.On("HandleToolStart", mock.Anything, inputJSON).Return()
	When(mockClient.CallTool(Any[context.Context](), Equal(expectedRequest))).ThenReturn(mockResult, nil)
	mockHandler.On("HandleToolEnd", mock.Anything, "JSON success").Return()

	output, err := lcTool.Call(context.Background(), inputJSON)

	require.NoError(t, err)
	assert.Equal(t, "JSON success", output)
	mockHandler.AssertExpectations(t)
	Verify(mockClient, Once()).CallTool(Any[context.Context](), Equal(expectedRequest))
}

func TestLangchainMCPTool_Call_Success_CommaSeparatedNumbers(t *testing.T) {
	SetUp(t)
	mockClient := Mock[MockMCPClient]()
	mockHandler := new(MockCallbackHandler)

	mcpTool := mcp.Tool{
		Name:        "multi-number-tool",
		Description: "Accepts multiple numbers",
		InputSchema: mcp.ToolInputSchema{
			Type: "object",
			Properties: map[string]any{
				"numA": map[string]any{"type": "number"},
				"numB": map[string]any{"type": "number"},
			},
			Required: []string{"numA", "numB"},
		},
	}
	lcTool := NewLangchainMCPTool(mcpTool, mockClient, mockHandler)

	input := `123.4, 567`
	expectedArgs := map[string]interface{}{"numA": 123.4, "numB": 567.0}
	expectedRequest := mcp.CallToolRequest{}
	expectedRequest.Params.Name = mcpTool.Name
	expectedRequest.Params.Arguments = expectedArgs

	mockResult := &mcp.CallToolResult{
		Content: []mcp.Content{mcp.TextContent{Text: "Comma success"}},
		IsError: false,
	}

	mockHandler.On("HandleToolStart", mock.Anything, input).Return()
	When(mockClient.CallTool(Any[context.Context](), Equal(expectedRequest))).ThenReturn(mockResult, nil)
	mockHandler.On("HandleToolEnd", mock.Anything, "Comma success").Return()

	output, err := lcTool.Call(context.Background(), input)

	require.NoError(t, err)
	assert.Equal(t, "Comma success", output)
	mockHandler.AssertExpectations(t)
	Verify(mockClient, Once()).CallTool(Any[context.Context](), Equal(expectedRequest))
}

func TestLangchainMCPTool_Call_Success_SingleStringFallback(t *testing.T) {
	SetUp(t)
	mockClient := Mock[MockMCPClient]()
	mockHandler := new(MockCallbackHandler)

	mcpTool := mcp.Tool{
		Name:        "single-string-tool",
		Description: "Accepts a single string",
		InputSchema: mcp.ToolInputSchema{
			Type: "object",
			Properties: map[string]any{
				"query": map[string]any{"type": "string"},
			},
			Required: []string{"query"},
		},
	}
	lcTool := NewLangchainMCPTool(mcpTool, mockClient, mockHandler)

	input := `this is just a plain string`
	expectedArgs := map[string]interface{}{"query": input}
	expectedRequest := mcp.CallToolRequest{}
	expectedRequest.Params.Name = mcpTool.Name
	expectedRequest.Params.Arguments = expectedArgs

	mockResult := &mcp.CallToolResult{
		Content: []mcp.Content{mcp.TextContent{Text: "String success"}},
		IsError: false,
	}

	mockHandler.On("HandleToolStart", mock.Anything, input).Return()
	When(mockClient.CallTool(Any[context.Context](), Equal(expectedRequest))).ThenReturn(mockResult, nil)
	mockHandler.On("HandleToolEnd", mock.Anything, "String success").Return()

	output, err := lcTool.Call(context.Background(), input)

	require.NoError(t, err)
	assert.Equal(t, "String success", output)
	mockHandler.AssertExpectations(t)
	Verify(mockClient, Once()).CallTool(Any[context.Context](), Equal(expectedRequest))
}

func TestLangchainMCPTool_Call_Error_InputParsing(t *testing.T) {
	SetUp(t)
	mockClient := Mock[MockMCPClient]()
	mockHandler := new(MockCallbackHandler)

	mcpTool := mcp.Tool{
		Name:        "json-tool-strict",
		Description: "Accepts JSON only",
		InputSchema: mcp.ToolInputSchema{
			Type: "object",
			Properties: map[string]any{
				"paramA": map[string]any{"type": "string"},
				"paramB": map[string]any{"type": "string"},
			},
			Required: []string{"paramA", "paramB"},
		},
	}
	lcTool := NewLangchainMCPTool(mcpTool, mockClient, mockHandler)

	input := `not json, not numbers, not single string compatible`

	mockHandler.On("HandleToolStart", mock.Anything, input).Return()
	mockHandler.On("HandleToolError", mock.Anything, mock.MatchedBy(func(err error) bool {
		return assert.ErrorContains(t, err, "failed to parse tool input") && assert.ErrorContains(t, err, "not valid JSON")
	})).Return()

	output, err := lcTool.Call(context.Background(), input)

	require.NoError(t, err)
	assert.Contains(t, output, "Error: failed to parse tool input")
	assert.Contains(t, output, "not valid JSON")
	mockHandler.AssertExpectations(t)
	Verify(mockClient, Never()).CallTool(Any[context.Context](), Any[mcp.CallToolRequest]())
}

func TestLangchainMCPTool_Call_Error_MCPClientError(t *testing.T) {
	SetUp(t)
	mockClient := Mock[MockMCPClient]()
	mockHandler := new(MockCallbackHandler)

	mcpTool := mcp.Tool{Name: "error-client-tool"}
	lcTool := NewLangchainMCPTool(mcpTool, mockClient, mockHandler)

	inputJSON := `{}`
	expectedRequest := mcp.CallToolRequest{}
	expectedRequest.Params.Name = mcpTool.Name
	expectedRequest.Params.Arguments = map[string]interface{}{}
	clientError := errors.New("network connection failed")

	mockHandler.On("HandleToolStart", mock.Anything, inputJSON).Return()
	When(mockClient.CallTool(Any[context.Context](), Equal(expectedRequest))).ThenReturn(nil, clientError)
	mockHandler.On("HandleToolError", mock.Anything, mock.MatchedBy(func(err error) bool {
		return assert.ErrorContains(t, err, "failed to call MCP tool") && assert.ErrorContains(t, err, clientError.Error())
	})).Return()

	output, err := lcTool.Call(context.Background(), inputJSON)

	require.NoError(t, err)
	assert.Contains(t, output, "Error calling tool error-client-tool")
	assert.Contains(t, output, clientError.Error())
	mockHandler.AssertExpectations(t)
	Verify(mockClient, Once()).CallTool(Any[context.Context](), Equal(expectedRequest))
}

func TestLangchainMCPTool_Call_Error_ToolExecutionError(t *testing.T) {
	SetUp(t)
	mockClient := Mock[MockMCPClient]()
	mockHandler := new(MockCallbackHandler)

	mcpTool := mcp.Tool{Name: "error-exec-tool"}
	lcTool := NewLangchainMCPTool(mcpTool, mockClient, mockHandler)

	inputJSON := `{}`
	expectedRequest := mcp.CallToolRequest{}
	expectedRequest.Params.Name = mcpTool.Name
	expectedRequest.Params.Arguments = map[string]interface{}{}

	toolErrorMessage := "Internal tool failure: invalid state"
	mockResult := &mcp.CallToolResult{
		Content: []mcp.Content{mcp.TextContent{Text: toolErrorMessage}},
		IsError: true,
	}

	mockHandler.On("HandleToolStart", mock.Anything, inputJSON).Return()
	When(mockClient.CallTool(Any[context.Context](), Equal(expectedRequest))).ThenReturn(mockResult, nil)
	mockHandler.On("HandleToolError", mock.Anything, mock.MatchedBy(func(err error) bool {
		return assert.ErrorContains(t, err, "tool error-exec-tool execution failed") && assert.ErrorContains(t, err, toolErrorMessage)
	})).Return()

	output, err := lcTool.Call(context.Background(), inputJSON)

	require.NoError(t, err)
	assert.Equal(t, toolErrorMessage, output)
	mockHandler.AssertExpectations(t)
	Verify(mockClient, Once()).CallTool(Any[context.Context](), Equal(expectedRequest))
}

// --- Tests for LoadMCPTools ---

func TestLoadMCPTools_Success(t *testing.T) {
	SetUp(t)
	mockClient := Mock[MockMCPClient]()

	mcpToolsList := []mcp.Tool{
		{Name: "tool1", Description: "Desc 1"},
		{Name: "tool2", Description: "Desc 2"},
	}
	listResult := &mcp.ListToolsResult{Tools: mcpToolsList}

	When(mockClient.ListTools(Any[context.Context](), Any[mcp.ListToolsRequest]())).
		ThenReturn(listResult, nil)

	loadedTools, err := LoadMCPTools(context.Background(), mockClient)

	require.NoError(t, err)
	require.Len(t, loadedTools, 2)

	foundTool1 := false
	foundTool2 := false
	for _, tool := range loadedTools {
		lcTool, ok := tool.(*LangchainMCPTool)
		require.True(t, ok, "Tool should be of type *LangchainMCPTool")
		assert.Equal(t, mockClient, lcTool.mcpClient)
		if lcTool.Name() == "tool1" {
			assert.Equal(t, "Desc 1", lcTool.Description())
			foundTool1 = true
		}
		if lcTool.Name() == "tool2" {
			assert.Equal(t, "Desc 2", lcTool.Description())
			foundTool2 = true
		}
	}
	assert.True(t, foundTool1, "Tool 'tool1' not found or not correctly wrapped")
	assert.True(t, foundTool2, "Tool 'tool2' not found or not correctly wrapped")

	Verify(mockClient, Once()).ListTools(Any[context.Context](), Any[mcp.ListToolsRequest]())
}

func TestLoadMCPTools_ListError(t *testing.T) {
	SetUp(t)
	mockClient := Mock[MockMCPClient]()
	expectedError := errors.New("failed to list tools from server")

	When(mockClient.ListTools(Any[context.Context](), Any[mcp.ListToolsRequest]())).
		ThenReturn(nil, expectedError)

	loadedTools, err := LoadMCPTools(context.Background(), mockClient)

	require.Error(t, err)
	assert.Nil(t, loadedTools)
	assert.Contains(t, err.Error(), "failed to list MCP tools")
	assert.Contains(t, err.Error(), expectedError.Error())

	Verify(mockClient, Once()).ListTools(Any[context.Context](), Any[mcp.ListToolsRequest]())
}
