package client

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/tmc/langchaingo/llms"
	"github.com/tmc/langchaingo/tools"
	"golang.org/x/sync/errgroup"

	lcgomcp "github.com/akihiro-fukuchi/langchaingo-mcp-adapters/prompt"
	lcgomcptool "github.com/akihiro-fukuchi/langchaingo-mcp-adapters/tool"
)

// EncodingErrorHandler defines how encoding errors are handled.
type EncodingErrorHandler string

const (
	Strict  EncodingErrorHandler = "strict"
	Ignore  EncodingErrorHandler = "ignore"
	Replace EncodingErrorHandler = "replace"
)

const (
	DefaultEncoding               = "utf-8"
	DefaultEncodingErrorHandler   = Strict
	DefaultHTTPTimeout            = 5 * time.Second
	DefaultSSEReadTimeout         = 5 * 60 * time.Second
	DefaultStdioConnectionTimeout = 30 * time.Second
)

// StdioConnection defines parameters for connecting to an MCP server via stdio.
type StdioConnection struct {
	Transport              string               `json:"transport"` // Should always be "stdio"
	Command                string               `json:"command"`
	Args                   []string             `json:"args"`
	Env                    map[string]string    `json:"env,omitempty"`
	Cwd                    string               `json:"cwd,omitempty"`
	Encoding               string               `json:"encoding,omitempty"`
	EncodingErrorHandler   EncodingErrorHandler `json:"encoding_error_handler,omitempty"`
	SessionKwargs          map[string]any       `json:"session_kwargs,omitempty"` // Note: mcp-go client doesn't directly support session kwargs like Python's mcp-sdk
	ConnectionTimeout      time.Duration        `json:"-"`                        // Go specific timeout for establishing connection
	InitializationTimeout  time.Duration        `json:"-"`                        // Go specific timeout for MCP initialize handshake
	NotificationBufferSize int                  `json:"-"`                        // Go specific buffer size for notification channel
}

// SSEConnection defines parameters for connecting to an MCP server via SSE.
type SSEConnection struct {
	Transport             string            `json:"transport"` // Should always be "sse"
	URL                   string            `json:"url"`
	Headers               map[string]string `json:"headers,omitempty"`
	Timeout               time.Duration     `json:"-"` // Go specific HTTP timeout
	SSEReadTimeout        time.Duration     `json:"-"` // Go specific SSE read timeout
	SessionKwargs         map[string]any    `json:"session_kwargs,omitempty"`
	InitializationTimeout time.Duration     `json:"-"` // Go specific timeout for MCP initialize handshake
}

// ConnectionConfig represents either an StdioConnection or SSEConnection.
type ConnectionConfig interface{}

// MultiServerMCPClient manages connections to multiple MCP servers.
type MultiServerMCPClient struct {
	connections        map[string]ConnectionConfig
	sessions           map[string]client.MCPClient
	serverNameToTools  map[string][]tools.Tool
	mu                 sync.RWMutex
	eg                 *errgroup.Group
	cancel             context.CancelFunc
	clientInfo         mcp.Implementation
	clientCapabilities mcp.ClientCapabilities
}

// NewMultiServerMCPClient creates a new client for managing multiple MCP server connections.
func NewMultiServerMCPClient(
	connections map[string]ConnectionConfig,
	clientInfo mcp.Implementation, // Optional client info
	clientCapabilities mcp.ClientCapabilities, // Optional client capabilities
) *MultiServerMCPClient {
	if clientInfo.Name == "" {
		clientInfo.Name = "langchaingo-mcp-client"
	}
	if clientInfo.Version == "" {
		clientInfo.Version = "0.0.1" // TODO: Consider using a dynamic version
	}
	return &MultiServerMCPClient{
		connections:        connections,
		sessions:           make(map[string]client.MCPClient),
		serverNameToTools:  make(map[string][]tools.Tool),
		clientInfo:         clientInfo,
		clientCapabilities: clientCapabilities,
	}
}

// Start establishes connections to all configured MCP servers and initializes them.
// It returns an error if any connection or initialization fails.
func (c *MultiServerMCPClient) Start(ctx context.Context) error {
	slog.Debug("MultiServerMCPClient Start: Acquiring lock...")
	c.mu.Lock() // Lock at the beginning to prevent concurrent Start calls
	slog.Debug("MultiServerMCPClient Start: Lock acquired.")
	// Unlock will happen explicitly before waiting, not deferred

	if c.eg != nil {
		slog.Debug("MultiServerMCPClient Start: Client already started.")
		c.mu.Unlock() // Unlock if already started
		return fmt.Errorf("client already started")
	}

	slog.Debug("MultiServerMCPClient Start: Setting up context and errgroup...")
	ctx, c.cancel = context.WithCancel(ctx)
	c.eg, ctx = errgroup.WithContext(ctx)
	slog.Debug("MultiServerMCPClient Start: Starting connection loop", "server_count", len(c.connections))

	for serverName, config := range c.connections {
		name := serverName
		cfg := config
		slog.Debug("MultiServerMCPClient Start: Launching goroutine", "server_name", name)

		c.eg.Go(func() error {
			slog.Debug("Goroutine starting connection", "server_name", name)
			mcpClient, err := c.connectToServer(ctx, name, cfg)
			if err != nil {
				slog.Error("Goroutine failed to connect", "server_name", name, "error", err)
				return fmt.Errorf("failed to connect to server %s: %w", name, err)
			}
			slog.Debug("Goroutine connection successful. Storing session...", "server_name", name)

			c.mu.Lock()
			slog.Debug("Goroutine acquired lock to store session", "server_name", name)
			c.sessions[name] = mcpClient
			c.mu.Unlock()
			slog.Debug("Goroutine released lock after storing session", "server_name", name)

			slog.Debug("Goroutine initializing session and loading tools...", "server_name", name)
			if err := c.initializeSessionAndLoadTools(ctx, name, mcpClient); err != nil {
				slog.Error("Goroutine failed to initialize/load tools", "server_name", name, "error", err)
				slog.Debug("Goroutine attempting to close client due to init error...", "server_name", name)
				_ = mcpClient.Close()
				slog.Debug("Goroutine acquiring lock to delete session after init error...", "server_name", name)
				c.mu.Lock()
				delete(c.sessions, name)
				c.mu.Unlock()
				slog.Debug("Goroutine released lock after deleting session", "server_name", name)
				return fmt.Errorf("failed to initialize/load tools for server %s: %w", name, err)
			}
			slog.Debug("Goroutine initialization and tool loading successful", "server_name", name)
			return nil
		})
	}

	// Unlock *before* waiting for goroutines, allowing them to acquire the lock
	slog.Debug("MultiServerMCPClient Start: Releasing lock before waiting for goroutines...")
	c.mu.Unlock()
	slog.Debug("MultiServerMCPClient Start: Lock released.")

	slog.Debug("MultiServerMCPClient Start: Waiting for all connection goroutines to finish...")
	err := c.eg.Wait()
	if err != nil {
		slog.Error("MultiServerMCPClient Start: Error occurred during connection/initialization", "error", err)
	} else {
		slog.Debug("MultiServerMCPClient Start: All connection goroutines finished successfully.")
	}
	return err
}

// Close terminates all active MCP server connections and waits for background tasks to finish.
func (c *MultiServerMCPClient) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.cancel != nil {
		c.cancel()
	}

	var closeErrors []error
	for name, session := range c.sessions {
		if err := session.Close(); err != nil {
			closeErrors = append(closeErrors, fmt.Errorf("failed to close session %s: %w", name, err))
		}
	}
	c.sessions = make(map[string]client.MCPClient) // Clear sessions map

	// Wait for errgroup goroutines to finish (if started)
	var egErr error
	if c.eg != nil {
		egErr = c.eg.Wait()
		c.eg = nil // Reset errgroup
	}

	if len(closeErrors) > 0 {
		// Combine errors if necessary
		// For simplicity, just return the first error for now
		return closeErrors[0]
	}
	return egErr
}

// connectToServer establishes a connection based on the config type.
func (c *MultiServerMCPClient) connectToServer(ctx context.Context, serverName string, config ConnectionConfig) (client.MCPClient, error) {
	switch cfg := config.(type) {
	case StdioConnection:
		return c.connectToServerViaStdio(ctx, serverName, cfg)
	case SSEConnection:
		return c.connectToServerViaSSE(ctx, serverName, cfg)
	default:
		return nil, fmt.Errorf("unknown connection type for server %s", serverName)
	}
}

// connectToServerViaStdio connects to an MCP server using stdio.
func (c *MultiServerMCPClient) connectToServerViaStdio(ctx context.Context, serverName string, config StdioConnection) (client.MCPClient, error) {
	slog.Debug("connectToServerViaStdio starting...", "server_name", serverName)

	if config.Encoding == "" {
		config.Encoding = DefaultEncoding // mcp-go client doesn't use this directly, but good practice
	}
	if config.EncodingErrorHandler == "" {
		config.EncodingErrorHandler = DefaultEncodingErrorHandler // mcp-go client doesn't use this directly
	}
	if config.ConnectionTimeout == 0 {
		config.ConnectionTimeout = DefaultStdioConnectionTimeout
	}

	// Prepare environment variables
	envList := os.Environ()
	if config.Env != nil {
		for k, v := range config.Env {
			envList = append(envList, fmt.Sprintf("%s=%s", k, v))
		}
	}
	// Ensure PATH is included if not explicitly set in config.Env
	if _, pathSet := config.Env["PATH"]; !pathSet {
		if pathVal, ok := os.LookupEnv("PATH"); ok {
			envList = append(envList, fmt.Sprintf("PATH=%s", pathVal))
		}
	}

	connectCtx, cancel := context.WithTimeout(ctx, config.ConnectionTimeout)
	defer cancel()

	slog.Debug("connectToServerViaStdio creating NewStdioMCPClient", "server_name", serverName, "command", config.Command, "args", config.Args)
	// mcp-go client handles command execution and stdio pipes internally
	mcpClient, err := client.NewStdioMCPClient(config.Command, envList, config.Args...)
	if err != nil {
		slog.Error("connectToServerViaStdio failed to create stdio client", "server_name", serverName, "error", err)
		return nil, fmt.Errorf("failed to start stdio client for %s: %w", serverName, err)
	}
	slog.Debug("connectToServerViaStdio stdio client created successfully", "server_name", serverName)

	// Check if context timed out during client creation/start
	if connectCtx.Err() != nil {
		slog.Error("connectToServerViaStdio context deadline exceeded during connection", "server_name", serverName, "error", connectCtx.Err())
		_ = mcpClient.Close() // Attempt cleanup
		return nil, fmt.Errorf("context deadline exceeded while connecting to %s: %w", serverName, connectCtx.Err())
	}

	slog.Debug("connectToServerViaStdio connection successful", "server_name", serverName)
	return mcpClient, nil
}

// connectToServerViaSSE connects to an MCP server using SSE.
func (c *MultiServerMCPClient) connectToServerViaSSE(ctx context.Context, serverName string, config SSEConnection) (client.MCPClient, error) {
	if config.Timeout == 0 {
		config.Timeout = DefaultHTTPTimeout
	}
	if config.SSEReadTimeout == 0 {
		config.SSEReadTimeout = DefaultSSEReadTimeout
	}

	opts := []transport.ClientOption{
		client.WithHeaders(config.Headers),
		// Note: mcp-go SSE client doesn't have a direct HTTP timeout option during creation,
		// but the underlying http client might respect context deadlines.
	}

	mcpClient, err := client.NewSSEMCPClient(config.URL, opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create SSE client for %s: %w", serverName, err)
	}

	// Start the SSE connection process
	startCtx, cancel := context.WithTimeout(ctx, config.Timeout)
	defer cancel()
	if err := mcpClient.Start(startCtx); err != nil {
		_ = mcpClient.Close() // Attempt cleanup
		return nil, fmt.Errorf("failed to start SSE connection for %s: %w", serverName, err)
	}

	return mcpClient, nil
}

// initializeSessionAndLoadTools initializes the MCP session and loads tools.
func (c *MultiServerMCPClient) initializeSessionAndLoadTools(ctx context.Context, serverName string, mcpClient client.MCPClient) error {
	slog.Debug("initializeSessionAndLoadTools starting...", "server_name", serverName)
	timeout := DefaultStdioConnectionTimeout
	slog.Debug("initializeSessionAndLoadTools acquiring read lock for config...", "server_name", serverName)
	c.mu.RLock()
	slog.Debug("initializeSessionAndLoadTools read lock acquired", "server_name", serverName)
	config, ok := c.connections[serverName]
	c.mu.RUnlock()
	slog.Debug("initializeSessionAndLoadTools read lock released", "server_name", serverName)
	if ok {
		slog.Debug("initializeSessionAndLoadTools found connection config", "server_name", serverName)
		switch cfg := config.(type) {
		case StdioConnection:
			if cfg.InitializationTimeout > 0 {
				timeout = cfg.InitializationTimeout
				slog.Debug("initializeSessionAndLoadTools using Stdio InitializationTimeout", "server_name", serverName, "timeout", timeout)
			}
		case SSEConnection:
			if cfg.InitializationTimeout > 0 {
				timeout = cfg.InitializationTimeout
				slog.Debug("initializeSessionAndLoadTools using SSE InitializationTimeout", "server_name", serverName, "timeout", timeout)
			}
		}
	} else {
		slog.Debug("initializeSessionAndLoadTools no specific config found, using default timeout", "server_name", serverName, "timeout", timeout)
	}

	initCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	slog.Debug("initializeSessionAndLoadTools preparing InitializeRequest...", "server_name", serverName)
	initRequest := mcp.InitializeRequest{}
	initRequest.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	initRequest.Params.ClientInfo = c.clientInfo
	initRequest.Params.Capabilities = c.clientCapabilities

	slog.Debug("initializeSessionAndLoadTools sending Initialize request...", "server_name", serverName)
	initResult, err := mcpClient.Initialize(initCtx, initRequest)
	if err != nil {
		slog.Error("initializeSessionAndLoadTools Initialize failed", "server_name", serverName, "error", err)
		return fmt.Errorf("MCP initialization failed for %s: %w", serverName, err)
	}
	slog.Debug("initializeSessionAndLoadTools Initialize successful", "server_name", serverName, "server_info_name", initResult.ServerInfo.Name, "server_info_version", initResult.ServerInfo.Version)

	slog.Debug("initializeSessionAndLoadTools loading tools...", "server_name", serverName)
	// Declare loadedTools here, use '=' for err as it's already declared from Initialize
	var loadedTools []tools.Tool
	loadedTools, err = lcgomcptool.LoadMCPTools(ctx, mcpClient)
	if err != nil {
		slog.Error("initializeSessionAndLoadTools failed to load tools", "server_name", serverName, "error", err)
		return fmt.Errorf("failed to load tools for %s: %w", serverName, err)
	}
	slog.Debug("initializeSessionAndLoadTools loaded tools", "server_name", serverName, "count", len(loadedTools))

	slog.Debug("initializeSessionAndLoadTools acquiring write lock to store tools...", "server_name", serverName)
	c.mu.Lock()
	slog.Debug("initializeSessionAndLoadTools write lock acquired", "server_name", serverName)
	c.serverNameToTools[serverName] = loadedTools
	c.mu.Unlock()
	slog.Debug("initializeSessionAndLoadTools write lock released", "server_name", serverName)

	slog.Debug("initializeSessionAndLoadTools finished successfully", "server_name", serverName)
	return nil
}

// GetTools returns a combined list of all tools loaded from all connected servers.
func (c *MultiServerMCPClient) GetTools() []tools.Tool {
	c.mu.RLock()
	defer c.mu.RUnlock()

	allTools := make([]tools.Tool, 0)
	for _, serverTools := range c.serverNameToTools {
		allTools = append(allTools, serverTools...)
	}
	return allTools
}

// GetPrompt retrieves a specific prompt from a named server.
func (c *MultiServerMCPClient) GetPrompt(ctx context.Context, serverName string, promptName string, arguments map[string]string) ([]llms.ChatMessage, error) {
	c.mu.RLock()
	session, ok := c.sessions[serverName]
	c.mu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("no active session for server: %s", serverName)
	}

	return lcgomcp.LoadMCPPrompt(ctx, session, promptName, arguments)
}
