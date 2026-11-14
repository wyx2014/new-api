package mcp

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/gin-gonic/gin"
	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	mcpInspectTimeout         = 60 * time.Second
	mcpInspectMaxBytes        = 4 << 20
	mcpInspectMaxPages        = 100
	mcpInspectMaxTools        = 1000
	mcpInspectProtocolVersion = "2025-11-25"
	inspectConnection         = "connection"
	inspectInitialize         = "initialize"
	inspectTools              = "tools"
	inspectComplete           = "complete"
	toolsNotRequested         = "not_requested"
	toolsUnsupported          = "unsupported"
	toolsComplete             = "complete"
	toolsFailed               = "failed"
	toolsPartial              = "partial"
)

var errMCPInspectLimit = errors.New("MCP inspection response limit reached")

type MCPInspectError struct {
	Message    string `json:"message"`
	HTTPStatus int    `json:"http_status,omitempty"`
	RPCCode    *int64 `json:"rpc_code,omitempty"`
}

type MCPInspectTool struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	InputSchema any    `json:"inputSchema"`
}

type MCPInspectResult struct {
	Connected       bool             `json:"connected"`
	ToolsStatus     string           `json:"tools_status"`
	CheckedAt       string           `json:"checked_at"`
	DurationMS      int64            `json:"duration_ms"`
	ProtocolVersion string           `json:"protocol_version"`
	Stage           string           `json:"stage"`
	Error           *MCPInspectError `json:"error,omitempty"`
	Tools           []MCPInspectTool `json:"tools"`
	Complete        bool             `json:"complete"`
}

// AdminInspectMCPServer diagnoses saved configuration without using the relay billing path.
func AdminInspectMCPServer(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		common.ApiErrorT(c, "Invalid server ID")
		return
	}
	var input struct {
		Mode string `json:"mode"`
	}
	if c.ShouldBindJSON(&input) != nil || (input.Mode != inspectConnection && input.Mode != inspectTools) {
		common.ApiErrorT(c, "Invalid MCP inspection mode")
		return
	}
	server, err := GetMCPServerById(id)
	if err != nil {
		common.ApiErrorT(c, "MCP server not found")
		return
	}
	common.ApiSuccessT(c, "", inspectMCPServer(c.Request.Context(), server, input.Mode))
}

func inspectMCPServer(parent context.Context, server *MCPServer, mode string) (result MCPInspectResult) {
	started := time.Now()
	result = MCPInspectResult{ToolsStatus: toolsNotRequested, CheckedAt: started.UTC().Format(time.RFC3339), Stage: inspectConnection, Tools: []MCPInspectTool{}}
	defer func() { result.DurationMS = time.Since(started).Milliseconds() }()
	ctx, cancel := context.WithTimeout(parent, mcpInspectTimeout)
	defer cancel()
	if err := server.validate(); err != nil {
		result.Error = &MCPInspectError{Message: err.Error()}
		return
	}
	target, _ := url.Parse(server.BaseURL)
	var headers map[string]string
	if server.Headers != "" && common.UnmarshalJsonStr(server.Headers, &headers) != nil {
		result.Error = &MCPInspectError{Message: "Invalid MCP headers"}
		return
	}
	// Validate persisted headers as well, before handing them to the HTTP client.
	if _, err := mergeMCPHeaders(server.Headers, ""); err != nil {
		result.Error = &MCPInspectError{Message: "Invalid MCP headers"}
		return
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.ResponseHeaderTimeout = time.Duration(server.Timeout) * time.Second
	defer transport.CloseIdleConnections()
	bounded := &mcpInspectTransport{base: transport, target: target, headers: headers}
	bounded.remaining.Store(mcpInspectMaxBytes)
	client := &http.Client{Transport: bounded, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	var protocol sdk.Transport
	if server.ProtocolType == MCPProtocolSSE {
		protocol = &sdk.SSEClientTransport{Endpoint: server.BaseURL, HTTPClient: client, MaxEventSize: mcpInspectMaxBytes}
	} else {
		protocol = &sdk.StreamableClientTransport{Endpoint: server.BaseURL, HTTPClient: client, MaxRetries: -1, DisableStandaloneSSE: true, MaxEventSize: mcpInspectMaxBytes}
	}
	// SDK diagnostics can include raw upstream errors; only our fixed messages leave this scope.
	mcpClient := sdk.NewClient(&sdk.Implementation{Name: "new-api-inspect", Version: "1"}, &sdk.ClientOptions{Capabilities: &sdk.ClientCapabilities{}, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	session, err := mcpClient.Connect(ctx, protocol, &sdk.ClientSessionOptions{ProtocolVersion: mcpInspectProtocolVersion})
	if err != nil {
		if bounded.initializing.Load() {
			result.Stage = inspectInitialize
		}
		result.Error = bounded.diagnosticError(err)
		return
	}
	defer session.Close()
	initialized := session.InitializeResult()
	if initialized == nil || initialized.Capabilities == nil || initialized.ServerInfo == nil || initialized.ServerInfo.Name == "" || initialized.ProtocolVersion == "" {
		result.Stage = inspectInitialize
		result.Error = &MCPInspectError{Message: "Invalid MCP initialization response"}
		return
	}
	result.Connected = true
	result.ProtocolVersion = initialized.ProtocolVersion
	result.Stage = inspectComplete
	if mode == inspectConnection {
		result.Complete = true
		return
	}
	if initialized.Capabilities.Tools == nil {
		result.ToolsStatus = toolsUnsupported
		result.Complete = true
		return
	}
	result.Stage = inspectTools
	result.ToolsStatus = toolsFailed
	cursor := ""
	seen := map[string]bool{}
	secrets := []string{server.BaseURL}
	for _, value := range headers {
		if value != "" {
			secrets = append(secrets, value)
			if _, v, ok := strings.Cut(value, " "); ok && v != "" {
				secrets = append(secrets, v)
			}
		}
	}
	for _, values := range target.Query() {
		for _, value := range values {
			if value != "" {
				secrets = append(secrets, value)
			}
		}
	}
	for range mcpInspectMaxPages {
		page, err := session.ListTools(ctx, &sdk.ListToolsParams{Cursor: cursor})
		if err != nil {
			result.Error = bounded.diagnosticError(err)
			if len(result.Tools) > 0 {
				result.ToolsStatus = toolsPartial
			}
			return
		}
		for _, tool := range page.Tools {
			if tool == nil || tool.Name == "" {
				result.Error = &MCPInspectError{Message: "Invalid MCP tools response"}
				result.ToolsStatus = toolsPartial
				return
			}
			if _, ok := tool.InputSchema.(map[string]any); !ok {
				result.Error = &MCPInspectError{Message: "Invalid MCP tools response"}
				result.ToolsStatus = toolsPartial
				return
			}
			if len(result.Tools) >= mcpInspectMaxTools {
				result.Error = &MCPInspectError{Message: "MCP inspection result limit reached"}
				result.ToolsStatus = toolsPartial
				return
			}
			// Keep only the discovery fields needed by the UI, and redact configured credentials recursively.
			value := MCPInspectTool{Name: tool.Name, Description: tool.Description, InputSchema: tool.InputSchema}
			raw, _ := common.Marshal(value)
			for _, secret := range secrets {
				encoded, _ := common.Marshal(secret)
				raw = []byte(strings.ReplaceAll(string(raw), string(encoded[1:len(encoded)-1]), "[redacted]"))
			}
			var safe MCPInspectTool
			if common.Unmarshal(raw, &safe) != nil {
				result.Error = &MCPInspectError{Message: "Invalid MCP tools response"}
				return
			}
			result.Tools = append(result.Tools, safe)
		}
		if page.NextCursor == "" {
			result.ToolsStatus = toolsComplete
			result.Stage = inspectComplete
			result.Complete = true
			return
		}
		if seen[page.NextCursor] {
			result.Error = &MCPInspectError{Message: "MCP tools pagination repeated a cursor"}
			result.ToolsStatus = toolsPartial
			return
		}
		seen[page.NextCursor] = true
		cursor = page.NextCursor
	}
	result.ToolsStatus = toolsPartial
	result.Error = &MCPInspectError{Message: "MCP inspection result limit reached"}
	return
}

// mcpInspectTransport is the trust boundary for SDK-created HTTP requests.
// No incoming browser request or authentication header is reused.
type mcpInspectTransport struct {
	base         http.RoundTripper
	target       *url.URL
	headers      map[string]string
	remaining    atomic.Int64
	status       atomic.Int64
	initializing atomic.Bool
}

func (t *mcpInspectTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if request.URL.Scheme != t.target.Scheme || request.URL.Host != t.target.Host || request.URL.User != nil || request.URL.Fragment != "" {
		return nil, errors.New("Invalid MCP endpoint")
	}
	request = request.Clone(request.Context())
	request.Header = request.Header.Clone()
	for name, value := range t.headers {
		request.Header.Set(name, value)
	}
	request.Header.Set("Accept-Encoding", "identity")
	query := request.URL.Query()
	for name, values := range t.target.Query() {
		query[name] = values
	}
	request.URL.RawQuery = query.Encode()
	if request.Method == http.MethodPost {
		t.initializing.Store(true)
	}
	response, err := t.base.RoundTrip(request)
	if err != nil {
		return nil, err
	}
	if response.StatusCode >= http.StatusBadRequest || (response.StatusCode >= http.StatusMultipleChoices && response.StatusCode < http.StatusBadRequest) {
		t.status.Store(int64(response.StatusCode))
	}
	response.Body = &mcpInspectBody{ReadCloser: response.Body, remaining: &t.remaining}
	return response, nil
}

type mcpInspectBody struct {
	io.ReadCloser
	remaining *atomic.Int64
}

func (b *mcpInspectBody) Read(p []byte) (int, error) {
	left := b.remaining.Load()
	if left <= 0 {
		return 0, errMCPInspectLimit
	}
	if int64(len(p)) > left {
		p = p[:left]
	}
	n, err := b.ReadCloser.Read(p)
	if b.remaining.Add(-int64(n)) < 0 {
		return 0, errMCPInspectLimit
	}
	return n, err
}

func (t *mcpInspectTransport) diagnosticError(err error) *MCPInspectError {
	result := &MCPInspectError{Message: "MCP protocol request failed", HTTPStatus: int(t.status.Load())}
	var rpc *jsonrpc.Error
	var network net.Error
	switch {
	case t.remaining.Load() <= 0:
		result.Message = "MCP inspection response limit reached"
	case errors.Is(err, context.Canceled):
		result.Message = "MCP inspection canceled"
	case errors.Is(err, context.DeadlineExceeded):
		result.Message = "MCP inspection timed out"
	case errors.As(err, &network) && network.Timeout():
		result.Message = "MCP inspection timed out"
	case result.HTTPStatus == http.StatusUnauthorized || result.HTTPStatus == http.StatusForbidden:
		result.Message = "MCP upstream authentication failed"
	case result.HTTPStatus >= http.StatusMultipleChoices:
		result.Message = "MCP upstream HTTP request failed"
	case errors.As(err, &rpc):
		result.RPCCode = &rpc.Code
	}
	return result
}
