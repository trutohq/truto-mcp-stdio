# Truto MCP Stdio Proxy

A CLI program that bridges stdio-based MCP clients to HTTP Streamable MCP servers. It reads newline-delimited JSON-RPC messages from stdin, forwards them to a specified MCP server endpoint via HTTP POST, handles SSE streaming responses, and writes the responses to stdout.

Implements the [MCP Streamable HTTP transport](https://modelcontextprotocol.io/specification/2025-11-25/basic/transports#streamable-http) (spec version 2025-11-25).

## Features

- **Streamable HTTP transport** -- full support for both `application/json` and `text/event-stream` responses
- **Session management** -- automatically tracks `MCP-Session-Id` across requests
- **Protocol version negotiation** -- extracts `protocolVersion` from the initialize handshake and sends `MCP-Protocol-Version` on subsequent requests
- **Server-initiated messages** -- opens a background GET SSE stream for server-to-client notifications and requests, with automatic reconnection
- **Session cleanup** -- sends HTTP DELETE on shutdown to cleanly terminate the session

## Requirements

- Go 1.24.2 or later (for building from source)

## Installation

### Download Pre-built Binaries

Download the latest release from the [GitHub releases page](https://github.com/truto/truto-mcp-stdio/releases). Choose the appropriate binary for your platform:

- Linux (amd64): `truto-mcp-stdio-linux-amd64-v<version>`
- macOS (Intel): `truto-mcp-stdio-darwin-amd64-v<version>`
- macOS (Apple Silicon): `truto-mcp-stdio-darwin-arm64-v<version>`
- Windows (amd64): `truto-mcp-stdio-windows-amd64-v<version>.exe`

After downloading, make the binary executable (on Unix-like systems):
```bash
chmod +x truto-mcp-stdio-<platform>-v<version>
```

### Building from Source

```bash
go build -o truto-mcp-stdio
```

## Usage

```bash
./truto-mcp-stdio <API_URL>
```

### Arguments

- `<API_URL>`: The MCP server HTTP endpoint URL (required)

### Example

```bash
echo '{"jsonrpc": "2.0", "method": "initialize", "params": {"protocolVersion": "2025-11-25", "capabilities": {}, "clientInfo": {"name": "test", "version": "1.0.0"}}, "id": 1}' | ./truto-mcp-stdio https://api.truto.one/mcp/<your-mcp-id>
```

## Client Setup

Below are setup instructions for all major MCP clients. Replace `/path/to/truto-mcp-stdio` with the actual path to the binary and `<your-mcp-id>` with your Truto MCP endpoint ID.

### Cursor

Config file: `~/.cursor/mcp.json` (global) or `.cursor/mcp.json` (project-scoped)

```json
{
  "mcpServers": {
    "truto": {
      "command": "/path/to/truto-mcp-stdio",
      "args": ["https://api.truto.one/mcp/<your-mcp-id>"]
    }
  }
}
```

Cursor hot-reloads config changes -- no restart needed.

### Claude Desktop

Config file locations:
- macOS: `~/Library/Application Support/Claude/claude_desktop_config.json`
- Windows: `%APPDATA%\Claude\claude_desktop_config.json`

```json
{
  "mcpServers": {
    "truto": {
      "command": "/path/to/truto-mcp-stdio",
      "args": ["https://api.truto.one/mcp/<your-mcp-id>"]
    }
  }
}
```

Requires a full restart of Claude Desktop after editing.

### Claude Code (CLI)

Add via the CLI:

```bash
claude mcp add truto -- /path/to/truto-mcp-stdio https://api.truto.one/mcp/<your-mcp-id>
```

Or create `.mcp.json` at your project root:

```json
{
  "mcpServers": {
    "truto": {
      "type": "stdio",
      "command": "/path/to/truto-mcp-stdio",
      "args": ["https://api.truto.one/mcp/<your-mcp-id>"]
    }
  }
}
```

### VS Code (GitHub Copilot)

Create `.vscode/mcp.json` in your project:

```json
{
  "servers": {
    "truto": {
      "type": "stdio",
      "command": "/path/to/truto-mcp-stdio",
      "args": ["https://api.truto.one/mcp/<your-mcp-id>"]
    }
  }
}
```

> **Note:** VS Code uses `"servers"` as the root key, not `"mcpServers"`.

### Windsurf

Config file locations:
- macOS/Linux: `~/.codeium/windsurf/mcp_config.json`
- Windows: `%USERPROFILE%\.codeium\windsurf\mcp_config.json`

```json
{
  "mcpServers": {
    "truto": {
      "command": "/path/to/truto-mcp-stdio",
      "args": ["https://api.truto.one/mcp/<your-mcp-id>"]
    }
  }
}
```

Requires a restart of Windsurf after editing.

### Cline (VS Code Extension)

Add via the MCP Servers panel in the Cline sidebar, or edit the config file directly:

- macOS: `~/Library/Application Support/Code/User/globalStorage/saoudrizwan.claude-dev/settings/cline_mcp_settings.json`
- Windows: `%APPDATA%\Code\User\globalStorage\saoudrizwan.claude-dev\settings\cline_mcp_settings.json`

```json
{
  "mcpServers": {
    "truto": {
      "command": "/path/to/truto-mcp-stdio",
      "args": ["https://api.truto.one/mcp/<your-mcp-id>"]
    }
  }
}
```

## Development

```bash
go run truto-mcp-stdio.go <API_URL>
```

## How It Works

1. Reads JSON-RPC messages line-by-line from stdin
2. Sends each message as an HTTP POST to the MCP endpoint with the required `Accept`, `MCP-Session-Id`, and `MCP-Protocol-Version` headers
3. Handles responses in both `application/json` and `text/event-stream` (SSE) formats
4. Extracts `MCP-Session-Id` from the server's initialize response and includes it on all subsequent requests
5. Extracts the negotiated `protocolVersion` and sends it as the `MCP-Protocol-Version` header
6. Opens a background GET SSE stream after initialization for server-initiated messages, with automatic reconnection
7. On shutdown (stdin close or signal), sends HTTP DELETE to cleanly terminate the session
8. Errors and diagnostics are written to stderr
