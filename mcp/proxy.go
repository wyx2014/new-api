package mcp

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
)

// ProxyMCPRequest 执行通用透明流式反向代理
func ProxyMCPRequest(c *gin.Context, server *MCPServer, token *model.Token) {
	startTime := time.Now()

	// 1. 解析目标后端真实地址
	targetURL, err := url.Parse(server.BaseURL)
	if err != nil || targetURL.Host == "" || (targetURL.Scheme != "http" && targetURL.Scheme != "https") {
		c.AbortWithStatusJSON(http.StatusBadGateway, gin.H{
			"error": "Invalid MCP upstream URL",
		})
		return
	}

	subPath := c.Param("any")
	if subPath != "" {
		endpoint, ok := mcpMessageEndpoints.Load(strings.TrimPrefix(subPath, "/messages/"))
		if !strings.HasPrefix(subPath, "/messages/") || !ok || c.Request.Method != http.MethodPost {
			c.AbortWithStatus(http.StatusNotFound)
			return
		}
		message := endpoint.(mcpMessageEndpoint)
		tokenID := 0
		if token != nil {
			tokenID = token.Id
		}
		if message.serverID != server.Id || message.tokenID != tokenID || message.baseURL != server.BaseURL {
			c.AbortWithStatus(http.StatusForbidden)
			return
		}
		targetURL = message.target
	}
	var headers map[string]string
	if server.Headers != "" {
		if err := common.UnmarshalJsonStr(server.Headers, &headers); err != nil {
			c.AbortWithStatusJSON(http.StatusBadGateway, gin.H{"error": "Invalid MCP headers"})
			return
		}
	}
	timeout := server.Timeout
	if timeout <= 0 {
		timeout = DefaultMCPTimeout
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.ResponseHeaderTimeout = time.Duration(timeout) * time.Second
	defer transport.CloseIdleConnections()
	userId := c.GetInt("id")
	if userId == 0 && token != nil {
		userId = token.UserId
	}

	// 2. 配额原子预检与扣减 (如果配置了 quota_per_call > 0)
	quotaToDeduct := server.QuotaPerCall
	if quotaToDeduct > 0 && token != nil {
		reserved, err := model.TryReserveTokenQuota(token.Id, token.Key, quotaToDeduct, token.UnlimitedQuota)
		if err != nil || !reserved {
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
				"error": fmt.Sprintf("Token quota is insufficient to access MCP '%s' (requires %d quota)", server.Name, quotaToDeduct),
			})
			return
		}
		// 扣减用户总额度
		_ = model.DecreaseUserQuota(userId, quotaToDeduct, false)
	}

	// 3. 构建透明反向代理
	proxy := &httputil.ReverseProxy{
		Transport: transport,
		Rewrite: func(request *httputil.ProxyRequest) {
			req := request.Out
			req.URL.Scheme = targetURL.Scheme
			req.URL.Host = targetURL.Host
			req.URL.Path = targetURL.Path
			req.URL.RawPath = targetURL.RawPath
			query := request.In.URL.Query()
			for name := range query {
				lower := strings.ToLower(name)
				if lower == "key" || lower == "token" || lower == "api_key" || lower == "access_token" || lower == "authorization" || targetURL.Query().Has(name) {
					query.Del(name)
				}
			}
			req.URL.RawQuery = targetURL.RawQuery
			if extra := query.Encode(); extra != "" {
				if req.URL.RawQuery != "" {
					req.URL.RawQuery += "&"
				}
				req.URL.RawQuery += extra
			}
			req.Host = targetURL.Host
			for _, name := range []string{"Authorization", "Cookie", "X-Api-Key", "X-Goog-Api-Key", "Mj-Api-Secret", "Proxy-Authorization", "Sec-Websocket-Protocol", "X-Auth-Session"} {
				req.Header.Del(name)
			}
			// Endpoint rewriting requires an uncompressed SSE event stream.
			if server.ProtocolType == MCPProtocolSSE {
				req.Header.Set("Accept-Encoding", "identity")
			}
			for name, value := range headers {
				req.Header.Set(name, value)
			}
		},
		ModifyResponse: func(response *http.Response) error {
			response.Header.Del("Set-Cookie")
			if response.StatusCode >= http.StatusMultipleChoices && response.StatusCode < http.StatusBadRequest {
				return errors.New("MCP upstream redirect rejected")
			}
			response.Header.Del("Location")
			if server.ProtocolType == MCPProtocolSSE && strings.HasPrefix(response.Header.Get("Content-Type"), "text/event-stream") && c.Request.Method == http.MethodGet && subPath == "" {
				response.Body = rewriteMCPEndpoints(response.Body, targetURL, server, token)
				response.ContentLength = -1
				response.Header.Del("Content-Length")
			}
			return nil
		},
		FlushInterval: -1, // 立即刷新，确保 SSE 长连接实时推送
		ErrorHandler: func(rw http.ResponseWriter, req *http.Request, proxyErr error) {
			// Transport errors may contain upstream query credentials.
			logger.LogError(c, "MCP upstream request failed")
			status := http.StatusBadGateway
			var timeoutError net.Error
			if errors.As(proxyErr, &timeoutError) && timeoutError.Timeout() {
				status = http.StatusGatewayTimeout
			}
			rw.Header().Set("Content-Type", "application/json")
			rw.WriteHeader(status)
			_, _ = rw.Write([]byte(`{"error":"MCP upstream request failed"}`))
		},
	}

	// 4. 执行代理转发
	proxy.ServeHTTP(c.Writer, c.Request)

	// 5. 记录 MCP 审计日志
	duration := int(time.Since(startTime).Seconds())
	if duration == 0 {
		duration = 1
	}

	tokenName := c.GetString("token_name")
	if tokenName == "" && token != nil {
		tokenName = token.Name
	}
	tokenId := c.GetInt("token_id")
	if tokenId == 0 && token != nil {
		tokenId = token.Id
	}

	model.RecordMCPLog(c, userId, model.RecordMCPLogParams{
		TokenId:        tokenId,
		TokenName:      tokenName,
		MCPName:        server.Name,
		Quota:          quotaToDeduct,
		UseTimeSeconds: duration,
		Content:        fmt.Sprintf("Method=%s, Path=%s, Status=%d", c.Request.Method, c.Request.URL.Path, c.Writer.Status()),
	})
}
