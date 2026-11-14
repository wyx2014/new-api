package mcp

import (
	"bufio"
	"errors"
	"io"
	"net/url"
	"strings"
	"sync"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
)

const maxMCPSSEEventBytes = 4 << 20
const mcpMessageHandleLength = 32

type mcpMessageEndpoint struct {
	serverID int
	tokenID  int
	baseURL  string
	target   *url.URL
}

// Handles live only while their SSE connection is open and require the same token.
var mcpMessageEndpoints sync.Map

func rewriteMCPEndpoints(body io.ReadCloser, base *url.URL, server *MCPServer, token *model.Token) io.ReadCloser {
	reader, writer := io.Pipe()
	go func() {
		defer body.Close()
		defer writer.Close()
		var handles []string
		defer func() {
			for _, handle := range handles {
				mcpMessageEndpoints.Delete(handle)
			}
		}()
		scanner := bufio.NewScanner(body)
		scanner.Buffer(nil, maxMCPSSEEventBytes)
		var lines []string
		size := 0
		for scanner.Scan() {
			line := scanner.Text()
			size += len(line)
			if size > maxMCPSSEEventBytes {
				_ = writer.CloseWithError(errors.New("MCP event too large"))
				return
			}
			if line != "" {
				lines = append(lines, line)
				continue
			}
			event := ""
			var data []string
			for _, field := range lines {
				if value, ok := strings.CutPrefix(field, "event:"); ok {
					event = strings.TrimSpace(value)
				}
				if value, ok := strings.CutPrefix(field, "data:"); ok {
					data = append(data, strings.TrimPrefix(value, " "))
				}
			}
			if event == "endpoint" {
				endpoint, err := url.Parse(strings.Join(data, "\n"))
				if err != nil {
					_ = writer.CloseWithError(errors.New("Invalid MCP endpoint"))
					return
				}
				target := base.ResolveReference(endpoint)
				if target.Scheme != base.Scheme || target.Host != base.Host || target.User != nil || target.Fragment != "" {
					_ = writer.CloseWithError(errors.New("Invalid MCP endpoint"))
					return
				}
				// Base URL credentials stay on the server, including on relative POST endpoints.
				query := target.Query()
				for name, values := range base.Query() {
					query[name] = values
				}
				target.RawQuery = query.Encode()
				handle, err := common.GenerateRandomCharsKey(mcpMessageHandleLength)
				if err != nil {
					_ = writer.CloseWithError(errors.New("MCP endpoint unavailable"))
					return
				}
				tokenID := 0
				if token != nil {
					tokenID = token.Id
				}
				mcpMessageEndpoints.Store(handle, mcpMessageEndpoint{server.Id, tokenID, server.BaseURL, target})
				handles = append(handles, handle)
				lines = []string{"event: endpoint", "data: " + common.ModelGatewayPrefix + "/mcp/" + server.Name + "/messages/" + handle}
			}
			if _, err := io.WriteString(writer, strings.Join(lines, "\n")+"\n\n"); err != nil {
				return
			}
			lines = nil
			size = 0
		}
		if scanner.Err() != nil {
			_ = writer.CloseWithError(errors.New("MCP stream interrupted"))
		}
	}()
	return &mcpSSEBody{PipeReader: reader, upstream: body}
}

type mcpSSEBody struct {
	*io.PipeReader
	upstream io.ReadCloser
}

func (body *mcpSSEBody) Close() error {
	_ = body.upstream.Close()
	return body.PipeReader.Close()
}
