package mcp

import (
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/tidwall/gjson"
	"golang.org/x/net/http/httpguts"
)

var mcpIdentifier = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,64}$`)

func reservedMCPHeader(name string) bool {
	name = strings.ToLower(name)
	switch name {
	case "host", "cookie", "set-cookie", "connection", "forwarded", "content-length", "transfer-encoding", "trailer", "te", "upgrade", "accept", "accept-encoding", "content-type", "last-event-id":
		return true
	}
	return strings.HasPrefix(name, "proxy-") || strings.HasPrefix(name, "x-forwarded-") || strings.HasPrefix(name, "mcp-")
}

// Null retains an existing value; omitted names are deleted; strings replace values.
func mergeMCPHeaders(input string, stored string) (string, error) {
	invalid := errors.New("Invalid MCP headers")
	if strings.TrimSpace(input) == "" {
		input = "{}"
	}
	if !gjson.Valid(input) || !gjson.Parse(input).IsObject() {
		return "", invalid
	}
	old := make(map[string]string)
	if stored != "" {
		if err := common.UnmarshalJsonStr(stored, &old); err != nil {
			return "", invalid
		}
	}
	canonicalOld := make(map[string]string, len(old))
	for key, value := range old {
		canonicalOld[http.CanonicalHeaderKey(key)] = value
	}
	merged := make(map[string]string)
	valid := true
	gjson.Parse(input).ForEach(func(key, value gjson.Result) bool {
		name := http.CanonicalHeaderKey(key.String())
		if !httpguts.ValidHeaderFieldName(name) || reservedMCPHeader(name) {
			valid = false
			return false
		}
		if _, exists := merged[name]; exists {
			valid = false
			return false
		}
		if value.Type == gjson.Null {
			previous, exists := canonicalOld[name]
			if !exists {
				valid = false
				return false
			}
			merged[name] = previous
		} else if value.Type == gjson.String && httpguts.ValidHeaderFieldValue(value.String()) {
			merged[name] = value.String()
		} else {
			valid = false
			return false
		}
		return true
	})
	if !valid {
		return "", invalid
	}
	data, err := common.Marshal(merged)
	return string(data), err
}

func (server *MCPServer) validate() error {
	if !mcpIdentifier.MatchString(server.Name) || strings.TrimSpace(server.DisplayName) == "" || len(server.DisplayName) > 128 {
		return errors.New("Invalid MCP server configuration")
	}
	target, err := url.Parse(server.BaseURL)
	if err != nil || target.Host == "" || (target.Scheme != "http" && target.Scheme != "https") || target.User != nil || target.Fragment != "" || len(server.BaseURL) > 512 {
		return errors.New("Invalid MCP upstream URL")
	}
	if server.ProtocolType == "" {
		server.ProtocolType = MCPProtocolSSE
	}
	if server.ProtocolType != MCPProtocolSSE && server.ProtocolType != MCPProtocolStreamableHTTP {
		return errors.New("Invalid MCP protocol type")
	}
	if server.Timeout == 0 {
		server.Timeout = DefaultMCPTimeout
	}
	if server.Timeout < 1 || server.Timeout > MaxMCPTimeout {
		return errors.New("MCP timeout must be between 1 and 86400 seconds")
	}
	if server.Status == 0 {
		server.Status = MCPServerStatusEnabled
	}
	if server.Status != MCPServerStatusEnabled && server.Status != MCPServerStatusDisabled {
		return errors.New("Invalid MCP server status")
	}
	if server.QuotaPerCall < 0 {
		return errors.New("Invalid MCP quota per call")
	}
	return nil
}

type mcpServerInput struct {
	MCPServer
	Headers *string `json:"headers"`
}

type mcpServerView struct {
	*MCPServer
	Headers     string `json:"headers"`
	BaseURL     string `json:"base_url"`
	HasURLQuery bool   `json:"has_url_query"`
}

func serverView(server *MCPServer) mcpServerView {
	headers := make(map[string]any)
	var stored map[string]string
	if common.UnmarshalJsonStr(server.Headers, &stored) == nil {
		for name := range stored {
			headers[http.CanonicalHeaderKey(name)] = nil
		}
	}
	encoded, _ := common.Marshal(headers)
	view := mcpServerView{MCPServer: server, Headers: string(encoded)}
	if target, err := url.Parse(server.BaseURL); err == nil {
		view.HasURLQuery = target.RawQuery != ""
		target.RawQuery = ""
		target.User = nil
		target.Fragment = ""
		view.BaseURL = target.String()
	}
	return view
}
