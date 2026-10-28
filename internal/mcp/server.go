package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/ducconit/gvm/internal/manager"
	"github.com/ducconit/gvm/internal/platform"
	"github.com/ducconit/gvm/internal/version"
)

const (
	jsonRPCVersion  = "2.0"
	mcpProtocolVer  = "2024-11-05"
	maxScannerToken = 4 * 1024 * 1024 // 4 MiB max JSON-RPC line
)

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type toolDefinition struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}

type callToolParams struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments,omitempty"`
}

type toolContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type callToolResult struct {
	Content []toolContent `json:"content"`
	IsError bool          `json:"isError,omitempty"`
}

// Server implements a Model Context Protocol (MCP) JSON-RPC 2.0 server over stdio.
type Server struct {
	mgr *manager.Manager
}

// New creates a new MCP Server wrapping the given Manager.
func New(mgr *manager.Manager) *Server {
	return &Server{mgr: mgr}
}

// Serve processes newline-delimited JSON-RPC 2.0 messages from in and writes responses to out until EOF or context cancellation.
func (s *Server) Serve(ctx context.Context, in io.Reader, out io.Writer) error {
	scanner := bufio.NewScanner(in)
	scanner.Buffer(make([]byte, 0, 64*1024), maxScannerToken)
	enc := json.NewEncoder(out)

	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return err
		}
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		var req rpcRequest
		if err := json.Unmarshal([]byte(line), &req); err != nil {
			_ = enc.Encode(rpcResponse{
				JSONRPC: jsonRPCVersion,
				ID:      json.RawMessage("null"),
				Error:   &rpcError{Code: -32700, Message: "Parse error: " + err.Error()},
			})
			continue
		}

		// JSON-RPC notifications (no ID) do not receive a response
		if len(req.ID) == 0 || string(req.ID) == "null" {
			if strings.HasPrefix(req.Method, "notifications/") {
				continue
			}
		}

		resp := s.handleRequest(ctx, req)
		if resp != nil {
			if err := enc.Encode(resp); err != nil {
				return fmt.Errorf("encode mcp response: %w", err)
			}
		}
	}

	if err := scanner.Err(); err != nil && !errors.Is(err, io.EOF) {
		return fmt.Errorf("read mcp stdin: %w", err)
	}
	return nil
}

func (s *Server) handleRequest(ctx context.Context, req rpcRequest) *rpcResponse {
	switch req.Method {
	case "initialize":
		return &rpcResponse{
			JSONRPC: jsonRPCVersion,
			ID:      req.ID,
			Result: map[string]any{
				"protocolVersion": mcpProtocolVer,
				"capabilities": map[string]any{
					"tools": map[string]any{},
				},
				"serverInfo": map[string]any{
					"name":    "g-mcp-server",
					"version": version.Version,
				},
			},
		}

	case "ping":
		return &rpcResponse{
			JSONRPC: jsonRPCVersion,
			ID:      req.ID,
			Result:  map[string]any{},
		}

	case "tools/list":
		return &rpcResponse{
			JSONRPC: jsonRPCVersion,
			ID:      req.ID,
			Result: map[string]any{
				"tools": s.toolsList(),
			},
		}

	case "tools/call":
		var params callToolParams
		if err := json.Unmarshal(req.Params, &params); err != nil {
			return &rpcResponse{
				JSONRPC: jsonRPCVersion,
				ID:      req.ID,
				Error:   &rpcError{Code: -32602, Message: "Invalid params: " + err.Error()},
			}
		}
		res := s.dispatchTool(ctx, params)
		return &rpcResponse{
			JSONRPC: jsonRPCVersion,
			ID:      req.ID,
			Result:  res,
		}

	default:
		if len(req.ID) == 0 {
			return nil
		}
		return &rpcResponse{
			JSONRPC: jsonRPCVersion,
			ID:      req.ID,
			Error:   &rpcError{Code: -32601, Message: "Method not found: " + req.Method},
		}
	}
}

func (s *Server) toolsList() []toolDefinition {
	return []toolDefinition{
		{
			Name:        "g_version",
			Description: "Get the g CLI version and the currently active Go version and path.",
			InputSchema: map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			},
		},
		{
			Name:        "g_env",
			Description: "Display the active Go environment variables managed by g (GVM_HOME, GOROOT, GOPATH, GOBIN, GOOS, GOARCH).",
			InputSchema: map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			},
		},
		{
			Name:        "g_list_installed",
			Description: "List all locally installed Go versions, their installation paths, active status, and aliases.",
			InputSchema: map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			},
		},
		{
			Name:        "g_list_remote",
			Description: "List official Go versions released on go.dev/dl for the current OS and architecture.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"all": map[string]any{
						"type":        "boolean",
						"description": "Include unstable RC/beta versions when true.",
					},
				},
			},
		},
		{
			Name:        "g_install",
			Description: "Install a specified Go version, minor series prefix (e.g. '1.27'), 'latest', or alias.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"version": map[string]any{
						"type":        "string",
						"description": "Go version or alias to install (e.g. '1.27.3', '1.27', 'latest', or custom alias).",
					},
				},
				"required": []string{"version"},
			},
		},
		{
			Name:        "g_use",
			Description: "Switch the default active Go version to the specified installed version or alias.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"version": map[string]any{
						"type":        "string",
						"description": "Installed Go version, prefix, or alias to activate.",
					},
				},
				"required": []string{"version"},
			},
		},
		{
			Name:        "g_alias",
			Description: "Set a favorite alias pointing to a Go version or prefix (e.g. name='like', version='1.27').",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"name": map[string]any{
						"type":        "string",
						"description": "Alias name (e.g. 'like').",
					},
					"version": map[string]any{
						"type":        "string",
						"description": "Target Go version or prefix (e.g. '1.27').",
					},
				},
				"required": []string{"name", "version"},
			},
		},
		{
			Name:        "g_uninstall_version",
			Description: "Uninstall a specific Go version/alias, or set all=true to remove all installed Go versions except the active one.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"version": map[string]any{
						"type":        "string",
						"description": "Go version or alias to uninstall (optional when all=true).",
					},
					"all": map[string]any{
						"type":        "boolean",
						"description": "If true, remove all installed Go versions except the currently active version.",
					},
				},
			},
		},
	}
}

func (s *Server) dispatchTool(ctx context.Context, params callToolParams) callToolResult {
	strArg := func(key string) string {
		if params.Arguments == nil {
			return ""
		}
		v, _ := params.Arguments[key].(string)
		return strings.TrimSpace(v)
	}
	boolArg := func(key string) bool {
		if params.Arguments == nil {
			return false
		}
		v, _ := params.Arguments[key].(bool)
		return v
	}

	switch params.Name {
	case "g_version":
		active, _ := s.mgr.Store().ActiveVersion()
		cfg := s.mgr.Config()
		payload := map[string]any{
			"cli_version":       version.Version,
			"platform":          cfg.GOOS + "/" + cfg.GOARCH,
			"active_go_version": active,
			"goroot":            cfg.CurrentDir(),
		}
		return jsonToolResult(payload)

	case "g_env":
		active, _ := s.mgr.Store().ActiveVersion()
		cfg := s.mgr.Config()
		return textToolResult(platform.FormatEnvExport(cfg, active), false)

	case "g_list_installed":
		list, err := s.mgr.ListLocal()
		if err != nil {
			return textToolResult(err.Error(), true)
		}
		return jsonToolResult(list)

	case "g_list_remote":
		list, err := s.mgr.ListRemote(ctx, boolArg("all"))
		if err != nil {
			return textToolResult(err.Error(), true)
		}
		return jsonToolResult(list)

	case "g_install":
		v := strArg("version")
		if v == "" {
			return textToolResult("missing required argument: version", true)
		}
		iv, err := s.mgr.Install(ctx, v)
		if err != nil {
			return textToolResult(err.Error(), true)
		}
		return jsonToolResult(iv)

	case "g_use":
		v := strArg("version")
		if v == "" {
			return textToolResult("missing required argument: version", true)
		}
		iv, err := s.mgr.Use(ctx, v)
		if err != nil {
			return textToolResult(err.Error(), true)
		}
		return jsonToolResult(iv)

	case "g_alias":
		name := strArg("name")
		ver := strArg("version")
		if name == "" || ver == "" {
			return textToolResult("missing required arguments: name and version", true)
		}
		if err := s.mgr.Alias(name, ver); err != nil {
			return textToolResult(err.Error(), true)
		}
		return jsonToolResult(map[string]string{"alias": name, "target": ver})

	case "g_uninstall_version":
		removed, err := s.mgr.UninstallVersion(strArg("version"), boolArg("all"))
		if err != nil {
			return textToolResult(err.Error(), true)
		}
		return jsonToolResult(map[string]any{"removed": removed})

	default:
		return textToolResult("unknown tool: "+params.Name, true)
	}
}

func jsonToolResult(v any) callToolResult {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return textToolResult(err.Error(), true)
	}
	return textToolResult(string(b), false)
}

func textToolResult(text string, isErr bool) callToolResult {
	return callToolResult{
		Content: []toolContent{{Type: "text", Text: text}},
		IsError: isErr,
	}
}
