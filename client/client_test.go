package client

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
	. "github.com/ovechkin-dm/mockio/mock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tmc/langchaingo/llms"
	"github.com/tmc/langchaingo/tools"
)

// --- Mocks ---

type MockMCPClientInternal interface {
	client.MCPClient
}

type MockTool struct {
	name        string
	description string
}

func (m *MockTool) Name() string                                           { return m.name }
func (m *MockTool) Description() string                                    { return m.description }
func (m *MockTool) Call(ctx context.Context, input string) (string, error) { return "mock output", nil }

// --- Tests ---

func TestNewMultiServerMCPClient(t *testing.T) {
	conns := map[string]ConnectionConfig{
		"server1": StdioConnection{Command: "cmd1"},
	}
	clientInfo := mcp.Implementation{Name: "test-client", Version: "1.0"}
	caps := mcp.ClientCapabilities{}

	msc := NewMultiServerMCPClient(conns, clientInfo, caps)

	require.NotNil(t, msc)
	assert.Equal(t, conns, msc.connections)
	assert.Equal(t, clientInfo, msc.clientInfo)
	assert.Equal(t, caps, msc.clientCapabilities)
	assert.NotNil(t, msc.sessions)
	assert.NotNil(t, msc.serverNameToTools)
}

func TestNewMultiServerMCPClient_Defaults(t *testing.T) {
	conns := map[string]ConnectionConfig{}
	msc := NewMultiServerMCPClient(conns, mcp.Implementation{}, mcp.ClientCapabilities{})

	require.NotNil(t, msc)
	assert.Equal(t, "langchaingo-mcp-client", msc.clientInfo.Name)
	assert.Equal(t, "0.0.1", msc.clientInfo.Version)
}

// Tests focus on the logic within initializeSessionAndLoadTools,
// assuming the connection part of Start works and provides the mocked clients.

func TestMultiServerMCPClient_InitializeAndLoad_Success(t *testing.T) {
	SetUp(t)

	mockClient1 := Mock[MockMCPClientInternal]()
	mockClient2 := Mock[MockMCPClientInternal]()

	conns := map[string]ConnectionConfig{
		"server1": StdioConnection{Command: "cmd1", InitializationTimeout: 1 * time.Second},
		"server2": SSEConnection{URL: "http://localhost:8080"},
	}
	msc := NewMultiServerMCPClient(conns, mcp.Implementation{}, mcp.ClientCapabilities{})

	initResult := &mcp.InitializeResult{ServerInfo: mcp.Implementation{Name: "mock-server"}}
	tools1 := []mcp.Tool{{Name: "tool-s1"}}
	tools2 := []mcp.Tool{{Name: "tool-s2"}}
	listResult1 := &mcp.ListToolsResult{Tools: tools1}
	listResult2 := &mcp.ListToolsResult{Tools: tools2}

	When(mockClient1.Initialize(Any[context.Context](), Any[mcp.InitializeRequest]())).ThenReturn(initResult, nil)
	When(mockClient1.ListTools(Any[context.Context](), Any[mcp.ListToolsRequest]())).ThenReturn(listResult1, nil)
	When(mockClient1.Close()).ThenReturn(nil)

	When(mockClient2.Initialize(Any[context.Context](), Any[mcp.InitializeRequest]())).ThenReturn(initResult, nil)
	When(mockClient2.ListTools(Any[context.Context](), Any[mcp.ListToolsRequest]())).ThenReturn(listResult2, nil)
	When(mockClient2.Close()).ThenReturn(nil)

	msc.sessions["server1"] = mockClient1
	msc.sessions["server2"] = mockClient2

	var wg sync.WaitGroup
	var err1, err2 error
	wg.Add(2)
	go func() {
		defer wg.Done()
		err1 = msc.initializeSessionAndLoadTools(context.Background(), "server1", mockClient1)
	}()
	go func() {
		defer wg.Done()
		err2 = msc.initializeSessionAndLoadTools(context.Background(), "server2", mockClient2)
	}()
	wg.Wait()

	require.NoError(t, err1, "Init/Load for server1 failed")
	require.NoError(t, err2, "Init/Load for server2 failed")

	msc.mu.RLock()
	require.Contains(t, msc.sessions, "server1")
	require.Contains(t, msc.sessions, "server2")
	require.Contains(t, msc.serverNameToTools, "server1")
	require.Contains(t, msc.serverNameToTools, "server2")
	assert.Len(t, msc.serverNameToTools["server1"], 1)
	assert.Equal(t, "tool-s1", msc.serverNameToTools["server1"][0].Name())
	assert.Len(t, msc.serverNameToTools["server2"], 1)
	assert.Equal(t, "tool-s2", msc.serverNameToTools["server2"][0].Name())
	msc.mu.RUnlock()

	Verify(mockClient1, Once()).Initialize(Any[context.Context](), Any[mcp.InitializeRequest]())
	Verify(mockClient1, Once()).ListTools(Any[context.Context](), Any[mcp.ListToolsRequest]())
	Verify(mockClient2, Once()).Initialize(Any[context.Context](), Any[mcp.InitializeRequest]())
	Verify(mockClient2, Once()).ListTools(Any[context.Context](), Any[mcp.ListToolsRequest]())

	msc.Close()
	Verify(mockClient1, Once()).Close()
	Verify(mockClient2, Once()).Close()
}

func TestMultiServerMCPClient_InitializeAndLoad_InitializeError(t *testing.T) {
	SetUp(t)

	mockClient1 := Mock[MockMCPClientInternal]()
	mockClient2 := Mock[MockMCPClientInternal]()

	initResult := &mcp.InitializeResult{ServerInfo: mcp.Implementation{Name: "mock-server"}}
	listResult1 := &mcp.ListToolsResult{Tools: []mcp.Tool{{Name: "tool-s1"}}}
	initError := errors.New("init failed")

	When(mockClient1.Initialize(Any[context.Context](), Any[mcp.InitializeRequest]())).ThenReturn(initResult, nil)
	When(mockClient1.ListTools(Any[context.Context](), Any[mcp.ListToolsRequest]())).ThenReturn(listResult1, nil)
	When(mockClient1.Close()).ThenReturn(nil)

	When(mockClient2.Initialize(Any[context.Context](), Any[mcp.InitializeRequest]())).ThenReturn(nil, initError)
	When(mockClient2.Close()).ThenReturn(nil)

	conns := map[string]ConnectionConfig{
		"server1": StdioConnection{Command: "cmd1"},
		"server2": SSEConnection{URL: "http://localhost:8080"},
	}
	msc := NewMultiServerMCPClient(conns, mcp.Implementation{}, mcp.ClientCapabilities{})

	msc.sessions["server1"] = mockClient1
	msc.sessions["server2"] = mockClient2

	var wg sync.WaitGroup
	var err1, err2 error
	wg.Add(2)
	go func() {
		defer wg.Done()
		err1 = msc.initializeSessionAndLoadTools(context.Background(), "server1", mockClient1)
	}()
	go func() {
		defer wg.Done()
		err2 = msc.initializeSessionAndLoadTools(context.Background(), "server2", mockClient2)
		if err2 != nil {
			msc.mu.Lock()
			delete(msc.sessions, "server2")
			msc.mu.Unlock()
		}
	}()
	wg.Wait()

	require.NoError(t, err1, "Init/Load for server1 should succeed")
	require.Error(t, err2, "Init/Load for server2 should fail")
	assert.ErrorContains(t, err2, initError.Error())

	msc.mu.RLock()
	assert.Contains(t, msc.sessions, "server1", "Server1 session should exist")
	assert.NotContains(t, msc.sessions, "server2", "Server2 session should have been removed after init failure")
	assert.Contains(t, msc.serverNameToTools, "server1", "Server1 tools should exist")
	assert.NotContains(t, msc.serverNameToTools, "server2", "Server2 tools should not exist")
	msc.mu.RUnlock()

	Verify(mockClient1, Once()).Initialize(Any[context.Context](), Any[mcp.InitializeRequest]())
	Verify(mockClient1, Once()).ListTools(Any[context.Context](), Any[mcp.ListToolsRequest]())
	Verify(mockClient2, Once()).Initialize(Any[context.Context](), Any[mcp.InitializeRequest]())
	Verify(mockClient2, Never()).ListTools(Any[context.Context](), Any[mcp.ListToolsRequest]())

	msc.Close()
	Verify(mockClient1, Once()).Close()
	Verify(mockClient2, Never()).Close()
}

// Test for LoadTools failure within initializeSessionAndLoadTools
func TestMultiServerMCPClient_InitializeAndLoad_LoadToolsError(t *testing.T) {
	SetUp(t)

	mockClient1 := Mock[MockMCPClientInternal]()
	loadToolsError := errors.New("failed to list tools")

	initResult := &mcp.InitializeResult{ServerInfo: mcp.Implementation{Name: "mock-server"}}

	When(mockClient1.Initialize(Any[context.Context](), Any[mcp.InitializeRequest]())).ThenReturn(initResult, nil)
	When(mockClient1.ListTools(Any[context.Context](), Any[mcp.ListToolsRequest]())).ThenReturn(nil, loadToolsError)
	When(mockClient1.Close()).ThenReturn(nil)

	conns := map[string]ConnectionConfig{"server1": StdioConnection{}}
	msc := NewMultiServerMCPClient(conns, mcp.Implementation{}, mcp.ClientCapabilities{})
	msc.sessions["server1"] = mockClient1

	err := msc.initializeSessionAndLoadTools(context.Background(), "server1", mockClient1)

	require.Error(t, err, "Init/Load for server1 should fail")
	assert.ErrorContains(t, err, loadToolsError.Error())

	msc.mu.RLock()
	assert.NotContains(t, msc.serverNameToTools, "server1", "Server1 tools should not exist")
	msc.mu.RUnlock()

	Verify(mockClient1, Once()).Initialize(Any[context.Context](), Any[mcp.InitializeRequest]())
	Verify(mockClient1, Once()).ListTools(Any[context.Context](), Any[mcp.ListToolsRequest]())

	msc.Close()
	Verify(mockClient1, Once()).Close()
}


func TestMultiServerMCPClient_Close(t *testing.T) {
	SetUp(t)

	mockClient1 := Mock[MockMCPClientInternal]()
	mockClient2 := Mock[MockMCPClientInternal]()

	initResult := &mcp.InitializeResult{ServerInfo: mcp.Implementation{}}
	listResult := &mcp.ListToolsResult{Tools: []mcp.Tool{}}
	When(mockClient1.Initialize(Any[context.Context](), Any[mcp.InitializeRequest]())).ThenReturn(initResult, nil)
	When(mockClient1.ListTools(Any[context.Context](), Any[mcp.ListToolsRequest]())).ThenReturn(listResult, nil)
	When(mockClient1.Close()).ThenReturn(nil)
	When(mockClient2.Initialize(Any[context.Context](), Any[mcp.InitializeRequest]())).ThenReturn(initResult, nil)
	When(mockClient2.ListTools(Any[context.Context](), Any[mcp.ListToolsRequest]())).ThenReturn(listResult, nil)
	When(mockClient2.Close()).ThenReturn(nil)

	conns := map[string]ConnectionConfig{"server1": StdioConnection{}, "server2": SSEConnection{}}
	msc := NewMultiServerMCPClient(conns, mcp.Implementation{}, mcp.ClientCapabilities{})

	msc.sessions["server1"] = mockClient1
	msc.sessions["server2"] = mockClient2
	ctx, cancel := context.WithCancel(context.Background())
	msc.cancel = cancel

	err := msc.Close()

	require.NoError(t, err)
	assert.Empty(t, msc.sessions, "Sessions map should be empty after Close")
	select {
	case <-ctx.Done():
	default:
		t.Error("Context should have been cancelled by Close()")
	}

	Verify(mockClient1, Once()).Close()
	Verify(mockClient2, Once()).Close()
}

func TestMultiServerMCPClient_GetTools(t *testing.T) {
	msc := NewMultiServerMCPClient(map[string]ConnectionConfig{}, mcp.Implementation{}, mcp.ClientCapabilities{})
	tool1 := &MockTool{name: "t1"}
	tool2 := &MockTool{name: "t2"}
	tool3 := &MockTool{name: "t3"}
	msc.serverNameToTools = map[string][]tools.Tool{
		"server1": {tool1, tool2},
		"server2": {tool3},
	}

	allTools := msc.GetTools()

	assert.Len(t, allTools, 3)
	assert.Contains(t, allTools, tool1)
	assert.Contains(t, allTools, tool2)
	assert.Contains(t, allTools, tool3)
}

func TestMultiServerMCPClient_GetPrompt(t *testing.T) {
	SetUp(t)

	mockClient1 := Mock[MockMCPClientInternal]()
	serverName := "prompt-server"
	promptName := "my-prompt"
	args := map[string]string{"user": "test"}
	expectedRequest := mcp.GetPromptRequest{}
	expectedRequest.Params.Name = promptName
	expectedRequest.Params.Arguments = args

	mcpMessages := []mcp.PromptMessage{
		{Role: mcp.RoleUser, Content: mcp.TextContent{Text: "Input"}},
		{Role: mcp.RoleAssistant, Content: mcp.TextContent{Text: "Output"}},
	}
	promptResult := &mcp.GetPromptResult{Messages: mcpMessages}
	expectedLcMessages := []llms.ChatMessage{
		llms.HumanChatMessage{Content: "Input"},
		llms.AIChatMessage{Content: "Output"},
	}

	When(mockClient1.GetPrompt(Any[context.Context](), Equal(expectedRequest))).ThenReturn(promptResult, nil)

	msc := NewMultiServerMCPClient(map[string]ConnectionConfig{}, mcp.Implementation{}, mcp.ClientCapabilities{})
	msc.sessions[serverName] = mockClient1

	lcMessages, err := msc.GetPrompt(context.Background(), serverName, promptName, args)

	require.NoError(t, err)
	assert.Equal(t, expectedLcMessages, lcMessages)
	Verify(mockClient1, Once()).GetPrompt(Any[context.Context](), Equal(expectedRequest))
}

func TestMultiServerMCPClient_GetPrompt_NoSession(t *testing.T) {
	msc := NewMultiServerMCPClient(map[string]ConnectionConfig{}, mcp.Implementation{}, mcp.ClientCapabilities{})

	lcMessages, err := msc.GetPrompt(context.Background(), "nonexistent-server", "p", nil)

	require.Error(t, err)
	assert.Nil(t, lcMessages)
	assert.ErrorContains(t, err, "no active session")
}
