# LangchainGo MCP Adapters

This library provides a lightweight wrapper that makes [Model Context Protocol (MCP)](https://modelcontextprotocol.io/introduction) tools compatible with [LangchainGo](https://github.com/tmc/langchaingo).

## Features

- 🛠️ Convert MCP tools into [LangchainGo tools](https://github.com/tmc/langchaingo/tree/main/tools) that can be used with LangchainGo agents.
- 📦 A client implementation (`MultiServerMCPClient`) that allows you to connect to multiple MCP servers (via stdio or SSE) and load tools from them.

*References: The MCP adapters of [Python](https://github.com/langchain-ai/langchain-mcp-adapters) implementation and [Typescript](https://github.com/langchain-ai/langchainjs-mcp-adapters) implementation.*

## Installation

```bash
go get github.com/akihiro-fukuchi/langchaingo-mcp-adapters
```

You will also need to install the underlying MCP Go SDK and LangchainGo:

```bash
go get github.com/mark3labs/mcp-go
go get github.com/tmc/langchaingo
```

## Quickstart

Here is a simple example of using MCP tools with a LangchainGo agent.

### Server

First, let's build an example MCP server that can add and multiply numbers.

Build the server: `go build -o math-server examples/math-server/main.go`

### Client

Now, use the `MultiServerMCPClient` to connect to the server and use its tools with a LangchainGo agent.

Run the client: `go run examples/agent/main.go`

*Note: OPENAI_API_KEY is required to run the agent. It is expect to be set as an environment variable.*

## Multiple MCP Servers

The `MultiServerMCPClient` is designed to handle connections to multiple servers simultaneously. Simply add more entries to the `connections` map during initialization, specifying either `StdioConnection` or `SSEConnection` for each server.

```go
	// Example with Math (stdio) and Weather (sse) servers
	connections := map[string]mcpclient.ConnectionConfig{
		"math": mcpclient.StdioConnection{
			Transport: "stdio",
			Command:   "/path/to/your/go/math-server", // Absolute path
			Args:      []string{},
		},
		"weather": mcpclient.SSEConnection{
			Transport: "sse",
			URL:       "http://localhost:8081/sse", // URL of your Go weather server SSE endpoint
		},
	}

	client := mcpclient.NewMultiServerMCPClient(connections, mcp.Implementation{}, mcp.ClientCapabilities{})
	// ... start client, get tools, run agent ...
```

The `client.GetTools()` method will return a combined list of tools from all successfully connected and initialized servers.
