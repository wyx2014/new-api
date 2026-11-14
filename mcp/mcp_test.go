package mcp

import (
	"bufio"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestMCPSSEEndpointLifecycle(t *testing.T) {
	setupTestMCPDB(t)
	upstreamCanceled := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "url-secret", r.URL.Query().Get("token"))
		assert.Empty(t, r.Header.Get("Cookie"))
		if r.Method == http.MethodPost {
			assert.Equal(t, "/messages", r.URL.Path)
			assert.Equal(t, "session-1", r.URL.Query().Get("session_id"))
			w.WriteHeader(http.StatusAccepted)
			return
		}
		assert.Equal(t, "/sse", r.URL.Path)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "event: endpoint\ndata: /messages?session_id=session-1&token=url-secret\n\n")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
		close(upstreamCanceled)
	}))
	defer upstream.Close()
	server := &MCPServer{Id: 1, Name: "sse_test", BaseURL: upstream.URL + "/sse?token=url-secret", ProtocolType: "sse", Timeout: 1}
	engine := gin.New()
	handler := func(c *gin.Context) {
		id := 1
		if c.GetHeader("Authorization") == "Bearer other" {
			id = 2
		}
		ProxyMCPRequest(c, server, &model.Token{Id: id})
	}
	engine.Any("/modelgw/mcp/:name", handler)
	engine.Any("/modelgw/mcp/:name/*any", handler)
	gateway := httptest.NewServer(engine)
	defer gateway.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, gateway.URL+"/modelgw/mcp/sse_test", nil)
	require.NoError(t, err)
	response, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer response.Body.Close()
	reader := bufio.NewReader(response.Body)
	line, err := reader.ReadString('\n')
	require.NoError(t, err)
	assert.Equal(t, "event: endpoint\n", line)
	line, err = reader.ReadString('\n')
	require.NoError(t, err)
	assert.NotContains(t, line, "url-secret")
	assert.NotContains(t, line, upstream.URL)
	path := strings.TrimSpace(strings.TrimPrefix(line, "data: "))
	require.True(t, strings.HasPrefix(path, "/modelgw/mcp/sse_test/messages/"))
	for _, tc := range []struct {
		auth   string
		status int
	}{{"Bearer other", http.StatusForbidden}, {"Bearer owner", http.StatusAccepted}} {
		post, err := http.NewRequest(http.MethodPost, gateway.URL+path, strings.NewReader(`{"jsonrpc":"2.0","method":"initialize","id":1}`))
		require.NoError(t, err)
		post.Header.Set("Authorization", tc.auth)
		result, err := http.DefaultClient.Do(post)
		require.NoError(t, err)
		assert.Equal(t, tc.status, result.StatusCode)
		result.Body.Close()
	}
	// A response-header timeout must not become a total SSE lifetime limit.
	select {
	case <-upstreamCanceled:
		t.Fatal("active SSE was canceled by response-header timeout")
	case <-time.After(1100 * time.Millisecond):
	}
	cancel()
	response.Body.Close()
	select {
	case <-upstreamCanceled:
	case <-time.After(5 * time.Second):
		t.Fatal("upstream was not canceled")
	}
	assert.Eventually(t, func() bool {
		_, ok := mcpMessageEndpoints.Load(strings.TrimPrefix(path, "/modelgw/mcp/sse_test/messages/"))
		return !ok
	}, time.Second, time.Millisecond)
}

func TestMCPResponseHeaderTimeout(t *testing.T) {
	setupTestMCPDB(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	defer upstream.Close()
	engine := gin.New()
	server := &MCPServer{Name: "timeout", BaseURL: upstream.URL + "/?token=hidden", Timeout: 1}
	engine.GET("/mcp/:name", func(c *gin.Context) { ProxyMCPRequest(c, server, nil) })
	rec := newCloseNotifyingRecorder()
	engine.ServeHTTP(rec, httptest.NewRequest("GET", "/mcp/timeout", nil))
	assert.Equal(t, http.StatusGatewayTimeout, rec.Code)
	assert.NotContains(t, rec.Body.String(), "hidden")
	assert.NotContains(t, rec.Body.String(), upstream.URL)
}

type legacyMCPServer struct {
	Id           int    `gorm:"primaryKey;autoIncrement"`
	Name         string `gorm:"type:varchar(64);uniqueIndex;not null"`
	DisplayName  string `gorm:"type:varchar(128);not null"`
	Description  string `gorm:"type:text"`
	BaseURL      string `gorm:"type:varchar(512);not null"`
	Headers      string `gorm:"type:text"`
	Status       int    `gorm:"default:1;index"`
	QuotaPerCall int    `gorm:"default:0"`
	CreatedAt    int64
	UpdatedAt    int64
	DeletedAt    gorm.DeletedAt `gorm:"index"`
}

func TestMCPDatabaseMigration(t *testing.T) {
	var dialector gorm.Dialector = sqlite.Open(":memory:")
	switch os.Getenv("MCP_TEST_DIALECT") {
	case "mysql":
		dialector = mysql.Open(os.Getenv("MCP_TEST_DSN"))
	case "postgres":
		dialector = postgres.Open(os.Getenv("MCP_TEST_DSN"))
	}
	for _, upgrade := range []bool{false, true} {
		db, err := gorm.Open(dialector, &gorm.Config{PrepareStmt: true})
		require.NoError(t, err)
		if os.Getenv("MCP_TEST_DIALECT") == "" {
			var version string
			require.NoError(t, db.Raw("SELECT sqlite_version()").Scan(&version).Error)
			t.Log("SQLite", version)
		}
		sqlDB, err := db.DB()
		require.NoError(t, err)
		defer sqlDB.Close()
		require.NoError(t, db.Migrator().DropTable(&MCPServer{}))
		if upgrade {
			require.NoError(t, db.Table("mcp_servers").AutoMigrate(&legacyMCPServer{}))
			require.NoError(t, db.Table("mcp_servers").Create(&legacyMCPServer{Name: "existing", DisplayName: "Existing", BaseURL: "http://example.com/mcp?token=stored", Headers: `{"Authorization":"stored-secret"}`}).Error)
		}
		for range 2 {
			require.NoError(t, db.AutoMigrate(&MCPServer{}))
		}
		if upgrade {
			var old MCPServer
			require.NoError(t, db.First(&old, 1).Error)
			assert.Equal(t, DefaultMCPTimeout, old.Timeout)
			assert.Equal(t, MCPProtocolSSE, old.ProtocolType)
			assert.Contains(t, old.Headers, "stored-secret")
			assert.Contains(t, old.BaseURL, "token=stored")
		}
		row := MCPServer{Name: "new", DisplayName: "New", BaseURL: "http://example.com/mcp", Headers: `{"X-Test":"new-secret"}`, ProtocolType: MCPProtocolStreamableHTTP, Timeout: 120}
		require.NoError(t, db.Create(&row).Error)
		duplicate := row
		duplicate.Id = 0
		require.Error(t, db.Create(&duplicate).Error)
		require.NoError(t, db.Model(&row).Updates(map[string]any{"headers": "{}", "timeout": 180, "protocol_type": MCPProtocolSSE}).Error)
		var found MCPServer
		require.NoError(t, db.First(&found, row.Id).Error)
		assert.Equal(t, "{}", found.Headers)
		assert.Equal(t, 180, found.Timeout)
		assert.Equal(t, MCPProtocolSSE, found.ProtocolType)
		require.NoError(t, db.Delete(&found).Error)
		assert.ErrorIs(t, db.First(&MCPServer{}, row.Id).Error, gorm.ErrRecordNotFound)
	}
}

func TestMCPStreamableHTTP(t *testing.T) {
	setupTestMCPDB(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/endpoint/mcp", r.URL.Path)
		assert.Equal(t, "url-secret", r.URL.Query().Get("token"))
		assert.Equal(t, "Bearer upstream-secret", r.Header.Get("Authorization"))
		for _, name := range []string{"Cookie", "X-Api-Key", "X-Goog-Api-Key", "Mj-Api-Secret", "Proxy-Authorization", "X-Auth-Session"} {
			assert.Empty(t, r.Header.Get(name))
		}
		if r.Header.Get("Mcp-Session-Id") != "" {
			assert.Equal(t, "session-123", r.Header.Get("Mcp-Session-Id"))
		}
		w.Header().Set("Mcp-Session-Id", "session-123")
		w.Header().Set("Set-Cookie", "upstream-secret=value")
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, "event: message\ndata: {\"jsonrpc\":\"2.0\",\"id\":2,\"result\":{\"tools\":[]}}\n\n")
			return
		}
		if r.Method == http.MethodDelete {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		var message map[string]any
		require.NoError(t, common.DecodeJson(r.Body, &message))
		if message["method"] == "initialize" {
			_, _ = io.WriteString(w, `{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2025-03-26","capabilities":{"tools":{}},"serverInfo":{"name":"fixture","version":"1"}}}`)
		} else {
			assert.Equal(t, "tools/list", message["method"])
			_, _ = io.WriteString(w, `{"jsonrpc":"2.0","id":2,"result":{"tools":[]}}`)
		}
	}))
	defer upstream.Close()
	server := &MCPServer{ProtocolType: MCPProtocolStreamableHTTP, Name: "transport", BaseURL: upstream.URL + "/endpoint/mcp?token=url-secret", Headers: `{"Authorization":"Bearer upstream-secret"}`}
	engine := gin.New()
	engine.Any("/mcp/:name", func(c *gin.Context) { ProxyMCPRequest(c, server, nil) })
	for index, method := range []string{"POST", "POST", "GET", "DELETE"} {
		body := `{"jsonrpc":"2.0","method":"initialize","id":1}`
		if index == 1 {
			body = `{"jsonrpc":"2.0","method":"tools/list","id":2}`
		}
		req := httptest.NewRequest(method, "/mcp/transport", strings.NewReader(body))
		for _, name := range []string{"Authorization", "Cookie", "X-Api-Key", "X-Goog-Api-Key", "Mj-Api-Secret", "Proxy-Authorization", "X-Auth-Session"} {
			req.Header.Set(name, "employee-secret")
		}
		if index > 0 {
			req.Header.Set("Mcp-Session-Id", "session-123")
		}
		rec := newCloseNotifyingRecorder()
		engine.ServeHTTP(rec, req)
		if method == http.MethodDelete {
			assert.Equal(t, http.StatusNoContent, rec.Code)
		} else {
			assert.Equal(t, http.StatusOK, rec.Code)
		}
		assert.Equal(t, "session-123", rec.Header().Get("Mcp-Session-Id"))
		assert.Empty(t, rec.Header().Get("Set-Cookie"))
		if index == 0 {
			assert.Contains(t, rec.Body.String(), `"protocolVersion":"2025-03-26"`)
		}
		if index == 1 || method == http.MethodGet {
			assert.Contains(t, rec.Body.String(), `"tools":[]`)
		}
	}
}

func TestMCPServerSecretEditing(t *testing.T) {
	setupTestMCPDB(t)
	engine := gin.New()
	engine.POST("/servers", AdminCreateMCPServer)
	engine.PUT("/servers", AdminUpdateMCPServer)
	engine.GET("/servers", AdminGetAllMCPServers)
	engine.GET("/servers/:id", AdminGetMCPServer)
	request := func(method, path, body string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		engine.ServeHTTP(rec, req)
		return rec
	}
	created := request("POST", "/servers", `{"name":"secret_test","display_name":"Secret test","base_url":"https://example.com/mcp?token=url-secret","headers":"{\"Authorization\":\"Bearer upstream-secret\"}","protocol_type":"streamableHttp","timeout":120}`)
	assert.NotContains(t, created.Body.String(), "upstream-secret")
	assert.NotContains(t, created.Body.String(), "url-secret")
	stored, err := GetMCPServerByName("secret_test")
	require.NoError(t, err)
	assert.Contains(t, stored.Headers, "upstream-secret")
	for _, path := range []string{"/servers", "/servers/1"} {
		response := request("GET", path, "")
		assert.NotContains(t, response.Body.String(), "upstream-secret")
		assert.NotContains(t, response.Body.String(), "url-secret")
		assert.Contains(t, response.Body.String(), `streamableHttp`)
	}
	unchanged := request("PUT", "/servers", `{"id":1,"name":"secret_test","display_name":"Renamed","protocol_type":"streamableHttp","timeout":120}`)
	assert.Contains(t, unchanged.Body.String(), `"success":true`)
	retained, err := GetMCPServerByName("secret_test")
	require.NoError(t, err)
	assert.Equal(t, stored.Headers, retained.Headers)
	for _, invalid := range []map[string]any{
		{"protocol_type": "stdio"}, {"timeout": -1}, {"timeout": MaxMCPTimeout + 1},
		{"base_url": "file:///tmp/mcp"}, {"base_url": "https://user:password@example.com/mcp"},
	} {
		payload := map[string]any{"name": "invalid_config", "display_name": "Invalid", "base_url": "https://example.com/mcp"}
		for key, value := range invalid {
			payload[key] = value
		}
		body, err := common.Marshal(payload)
		require.NoError(t, err)
		assert.Contains(t, request("POST", "/servers", string(body)).Body.String(), `"success":false`)
	}
	for _, tc := range []struct{ headers, want string }{
		{`{"Authorization":null}`, "Bearer upstream-secret"},
		{`{"authorization":"literal****secret"}`, "literal****secret"},
		{`{}`, ""},
	} {
		payload, err := common.Marshal(map[string]any{"id": stored.Id, "name": stored.Name, "display_name": stored.DisplayName, "headers": tc.headers, "protocol_type": "streamableHttp", "timeout": 120, "status": 1})
		require.NoError(t, err)
		response := request("PUT", "/servers", string(payload))
		assert.Contains(t, response.Body.String(), `"success":true`)
		stored, err = GetMCPServerByName("secret_test")
		require.NoError(t, err)
		var headers map[string]string
		require.NoError(t, common.UnmarshalJsonStr(stored.Headers, &headers))
		assert.Equal(t, tc.want, headers["Authorization"])
		assert.Equal(t, "https://example.com/mcp?token=url-secret", stored.BaseURL)
	}
	for _, invalid := range []string{`{"Host":"evil"}`, `{"Accept-Encoding":"gzip"}`, `{"X-Test":"bad\r\nvalue"}`, `{"X-Test":"a","x-test":"b"}`, `{"Authorization":null}`, `{"X-Test":123}`} {
		payload, err := common.Marshal(map[string]any{"name": "invalid", "display_name": "Invalid", "base_url": "https://example.com/mcp", "headers": invalid})
		require.NoError(t, err)
		assert.Contains(t, request("POST", "/servers", string(payload)).Body.String(), `"success":false`)
	}
}

func setupTestMCPDB(t *testing.T) *gorm.DB {
	common.RedisEnabled = false
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)

	model.DB = db
	model.LOG_DB = db
	require.NoError(t, db.AutoMigrate(
		&MCPServer{},
		&MCPRole{},
		&model.Token{},
		&model.User{},
		&model.Log{},
	))
	return db
}

func TestMCPServerAndRoleCRUD(t *testing.T) {
	setupTestMCPDB(t)

	// 1. 创建 MCP Server
	s1 := &MCPServer{
		Name:         "internal_kb",
		DisplayName:  "企业内部知识库",
		Description:  "内部文档检索",
		BaseURL:      "http://127.0.0.1:8000",
		Status:       MCPServerStatusEnabled,
		QuotaPerCall: 0,
	}
	require.NoError(t, s1.Insert())
	assert.Greater(t, s1.Id, 0)

	// 重名校验
	sDuplicate := &MCPServer{
		Name:        "internal_kb",
		DisplayName: "重复知识库",
		BaseURL:     "http://127.0.0.1:8001",
	}
	require.Error(t, sDuplicate.Insert())

	// 查询
	found, err := GetMCPServerByName("internal_kb")
	require.NoError(t, err)
	assert.Equal(t, "企业内部知识库", found.DisplayName)

	// 2. 创建 MCP Role
	r1 := &MCPRole{
		Name:        "dev_pack",
		DisplayName: "研发工具包",
		IsDefault:   false,
	}
	require.NoError(t, r1.SetMCPServers([]string{"internal_kb"}))
	require.NoError(t, r1.Insert())

	// 3. 全员默认角色
	rDefault := &MCPRole{
		Name:        "all_staff",
		DisplayName: "全员默认包",
		IsDefault:   true,
	}
	require.NoError(t, rDefault.SetMCPServers([]string{"internal_kb"}))
	require.NoError(t, rDefault.Insert())

	defaultRoles, err := GetDefaultMCPRoles()
	require.NoError(t, err)
	assert.Len(t, defaultRoles, 1)
	assert.Equal(t, "all_staff", defaultRoles[0].Name)
}

func TestPermissionCheckAndResolution(t *testing.T) {
	setupTestMCPDB(t)

	// 准备 MCP 实例
	kb := &MCPServer{
		Name:        "internal_kb",
		DisplayName: "企业知识库",
		BaseURL:     "http://127.0.0.1:8000",
		Status:      MCPServerStatusEnabled,
	}
	require.NoError(t, kb.Insert())

	sqlTool := &MCPServer{
		Name:        "sql_tool",
		DisplayName: "数据库查询工具",
		BaseURL:     "http://127.0.0.1:8001",
		Status:      MCPServerStatusEnabled,
	}
	require.NoError(t, sqlTool.Insert())

	// 创建研发工具包包含 sql_tool
	devRole := &MCPRole{
		Name:        "dev",
		DisplayName: "研发",
		IsDefault:   false,
	}
	require.NoError(t, devRole.SetMCPServers([]string{"sql_tool"}))
	require.NoError(t, devRole.Insert())

	// 创建全员默认工具包包含 internal_kb
	defaultRole := &MCPRole{
		Name:        "public",
		DisplayName: "公共工具",
		IsDefault:   true,
	}
	require.NoError(t, defaultRole.SetMCPServers([]string{"internal_kb"}))
	require.NoError(t, defaultRole.Insert())

	// Case 1: 普通用户令牌（未关联任何 MCP 角色，严格遵循“没有就是没有”原则）
	normalToken := &model.Token{
		Id:         1,
		UserId:     10,
		Key:        "sk-normal",
		ApikeyName: "张之清01",
		Name:       "zzq01",
	}
	require.NoError(t, normalToken.SetMCPRoles([]string{}))
	require.NoError(t, model.DB.Create(normalToken).Error)

	serversMap, err := GetAuthorizedMCPServersForToken(normalToken)
	require.NoError(t, err)
	assert.Empty(t, serversMap, "未显式配置 MCP 角色的普通令牌不应自动获得任何工具，必须为空")

	// Case 2: 研发人员令牌（显式关联 dev 角色）
	devToken := &model.Token{
		Id:         2,
		UserId:     11,
		Key:        "sk-dev",
		ApikeyName: "研发01",
		Name:       "dev01",
	}
	require.NoError(t, devToken.SetMCPRoles([]string{"dev"}))
	require.NoError(t, model.DB.Create(devToken).Error)

	devServers, err := GetAuthorizedMCPServersForToken(devToken)
	require.NoError(t, err)
	assert.NotContains(t, devServers, "internal_kb", "未显式关联公共角色时不自动继承默认工具")
	assert.Contains(t, devServers, "sql_tool", "显式关联 dev 角色后拥有对应的研发工具")
	engine := gin.New()
	engine.GET("/catalog", func(c *gin.Context) { c.Set("token", devToken); GetMyMCPServers(c) })
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, httptest.NewRequest("GET", "/catalog", nil))
	assert.Contains(t, rec.Body.String(), `"proxy_url":"/modelgw/mcp/sql_tool"`)
	assert.Contains(t, rec.Body.String(), `"protocol_type":"sse"`)
	assert.NotContains(t, rec.Body.String(), "base_url")
	assert.NotContains(t, rec.Body.String(), "headers")

}

func TestMCPTransparentReverseProxy(t *testing.T) {
	setupTestMCPDB(t)

	// 1. 模拟内网真实 MCP 服务 (Echo Server)
	mockUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Upstream-Path", r.URL.Path)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("event: message\ndata: hello from mock mcp\n\n"))
	}))
	defer mockUpstream.Close()

	// 注册内网工具指向 mockUpstream
	server := &MCPServer{
		Name:        "test_mcp",
		DisplayName: "测试工具",
		BaseURL:     mockUpstream.URL + "/sse",
		Status:      MCPServerStatusEnabled,
	}
	require.NoError(t, server.Insert())

	// 全员默认开放
	role := &MCPRole{
		Name:      "all",
		IsDefault: true,
	}
	require.NoError(t, role.SetMCPServers([]string{"test_mcp"}))
	require.NoError(t, role.Insert())

	token := &model.Token{
		Id:     1,
		UserId: 1,
		Key:    "sk-test-proxy",
		Name:   "test_token",
	}
	require.NoError(t, token.SetMCPRoles([]string{"all"}))
	require.NoError(t, model.DB.Create(token).Error)

	// 构建 Gin Engine
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.Use(func(c *gin.Context) {
		// 模拟 TokenAuth 中间件注入上下文
		c.Set("id", token.UserId)
		c.Set("token_id", token.Id)
		c.Set("token_key", token.Key)
		c.Set("token_name", token.Name)
		c.Set("token", token)
		c.Next()
	})
	engine.Any("/mcp/:name", MCPPermissionCheck(), RelayMCP)
	engine.Any("/mcp/:name/*any", MCPPermissionCheck(), RelayMCP)

	// 2. 正常代理测试（网关完整端点映射至配置的上游 /sse）
	req := httptest.NewRequest("GET", "/mcp/test_mcp", nil)
	rec := newCloseNotifyingRecorder()
	engine.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "/sse", rec.Header().Get("X-Upstream-Path"))
	assert.Contains(t, rec.Body.String(), "hello from mock mcp")

	// 3. 越权拦截测试
	reqForbidden := httptest.NewRequest("GET", "/mcp/unauthorized_tool/sse", nil)
	recForbidden := newCloseNotifyingRecorder()
	engine.ServeHTTP(recForbidden, reqForbidden)

	assert.Equal(t, http.StatusForbidden, recForbidden.Code)
	var errResp map[string]string
	require.NoError(t, common.Unmarshal(recForbidden.Body.Bytes(), &errResp))
	assert.Contains(t, errResp["error"], "Forbidden")
}

type closeNotifyingRecorder struct {
	*httptest.ResponseRecorder
	closed chan bool
}

func newCloseNotifyingRecorder() *closeNotifyingRecorder {
	return &closeNotifyingRecorder{
		ResponseRecorder: httptest.NewRecorder(),
		closed:           make(chan bool, 1),
	}
}

func (c *closeNotifyingRecorder) CloseNotify() <-chan bool {
	return c.closed
}

func TestTokenApikeyNameAndSearch(t *testing.T) {
	setupTestMCPDB(t)

	token1 := &model.Token{
		UserId:     100,
		Key:        "sk-test-1",
		ApikeyName: "张之清01",
		Name:       "zzq01",
		Status:     1,
	}
	require.NoError(t, token1.Insert())

	token2 := &model.Token{
		UserId:     100,
		Key:        "sk-test-2",
		ApikeyName: "李四02",
		Name:       "ls02",
		Status:     1,
	}
	require.NoError(t, token2.Insert())

	// 1. 按 ApikeyName 搜索
	results, total, err := model.SearchUserTokens(100, "张之清%", "", 0, 10)
	require.NoError(t, err)
	assert.EqualValues(t, 1, total)
	require.Len(t, results, 1)
	assert.Equal(t, "张之清01", results[0].ApikeyName)
	assert.Equal(t, "zzq01", results[0].Name)

	// 2. 按 Name 搜索
	results, total, err = model.SearchUserTokens(100, "zzq%", "", 0, 10)
	require.NoError(t, err)
	assert.EqualValues(t, 1, total)
	require.Len(t, results, 1)
	assert.Equal(t, "张之清01", results[0].ApikeyName)

	// 3. 更新 ApikeyName
	token1.ApikeyName = "张之清-已修改"
	require.NoError(t, token1.Update())

	found, err := model.GetTokenById(token1.Id)
	require.NoError(t, err)
	assert.Equal(t, "张之清-已修改", found.ApikeyName)
}

func TestMCPProxyCredentialIsolation(t *testing.T) {
	setupTestMCPDB(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Empty(t, r.Header.Get("Authorization"))
		assert.Empty(t, r.Header.Get("Cookie"))
		assert.Empty(t, r.Header.Get("X-Auth-Session"))
		assert.Empty(t, r.URL.Query().Get("key"))
		if r.URL.Query().Get("redirect") != "" {
			w.Header().Set("Location", "https://example.com/?token=hidden")
			w.WriteHeader(http.StatusTemporaryRedirect)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()
	server := &MCPServer{Name: "isolation", BaseURL: upstream.URL, ProtocolType: MCPProtocolStreamableHTTP}
	engine := gin.New()
	engine.GET("/mcp/:name", func(c *gin.Context) { ProxyMCPRequest(c, server, nil) })
	for _, redirect := range []string{"", "yes"} {
		req := httptest.NewRequest("GET", "/mcp/isolation?key=employee-secret&redirect="+redirect, nil)
		req.Header.Set("Authorization", "Bearer employee-secret")
		req.Header.Set("Cookie", "session=employee-secret")
		req.Header.Set("X-Auth-Session", "employee-session")
		rec := newCloseNotifyingRecorder()
		engine.ServeHTTP(rec, req)
		if redirect == "" {
			assert.Equal(t, http.StatusOK, rec.Code)
		} else {
			assert.Equal(t, http.StatusBadGateway, rec.Code)
			assert.Empty(t, rec.Header().Get("Location"))
			assert.NotContains(t, rec.Body.String(), "hidden")
		}
	}
	base, err := url.Parse(upstream.URL + "/sse")
	require.NoError(t, err)
	body := rewriteMCPEndpoints(io.NopCloser(strings.NewReader("event: endpoint\ndata: https://other.example/messages?token=hidden\n\n")), base, server, nil)
	defer body.Close()
	data, err := io.ReadAll(body)
	require.Error(t, err)
	assert.NotContains(t, string(data), "hidden")
	assert.NotContains(t, err.Error(), "hidden")
}

func TestMCPInspectStreamable(t *testing.T) {
	setupTestMCPDB(t)
	initialized := false
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "Bearer diagnostic-secret", r.Header.Get("Authorization"))
		assert.Empty(t, r.Header.Get("Cookie"))
		assert.Empty(t, r.Header.Get("X-Auth-Session"))
		assert.Equal(t, "url-secret", r.URL.Query().Get("token"))
		if r.Method == http.MethodDelete {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		var request struct {
			ID     any    `json:"id"`
			Method string `json:"method"`
		}
		require.NoError(t, common.DecodeJson(r.Body, &request))
		w.Header().Set("Content-Type", "application/json")
		switch request.Method {
		case "initialize":
			w.Header().Set("Mcp-Session-Id", "fixture-session")
			body, err := common.Marshal(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": map[string]any{"protocolVersion": "2025-03-26", "capabilities": map[string]any{"tools": map[string]any{}}, "serverInfo": map[string]string{"name": "fixture", "version": "1"}}})
			require.NoError(t, err)
			_, _ = w.Write(body)
		case "notifications/initialized":
			assert.Equal(t, "fixture-session", r.Header.Get("Mcp-Session-Id"))
			initialized = true
			w.WriteHeader(http.StatusAccepted)
		case "tools/list":
			assert.True(t, initialized)
			assert.Equal(t, "fixture-session", r.Header.Get("Mcp-Session-Id"))
			body, err := common.Marshal(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": map[string]any{"tools": []any{map[string]any{"name": "search", "description": "Search documents", "inputSchema": map[string]any{"type": "object"}}}}})
			require.NoError(t, err)
			_, _ = w.Write(body)
		default:
			t.Errorf("unexpected method %s", request.Method)
		}
	}))
	defer upstream.Close()
	server := &MCPServer{Name: "inspect", DisplayName: "Inspect", BaseURL: upstream.URL + "/mcp?token=url-secret", Headers: `{"Authorization":"Bearer diagnostic-secret"}`, ProtocolType: MCPProtocolStreamableHTTP, Timeout: 1, Status: MCPServerStatusDisabled, QuotaPerCall: 500}
	require.NoError(t, server.Insert())
	engine := gin.New()
	engine.POST("/servers/:id/inspect", AdminInspectMCPServer)
	req := httptest.NewRequest("POST", "/servers/1/inspect", strings.NewReader(`{"mode":"tools"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer employee-secret")
	req.Header.Set("Cookie", "staff=secret")
	req.Header.Set("X-Auth-Session", "employee-session")
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), `"connected":true`)
	assert.Contains(t, rec.Body.String(), `"tools_status":"complete"`)
	assert.Contains(t, rec.Body.String(), `"name":"search"`)
	assert.NotContains(t, rec.Body.String(), "diagnostic-secret")
	assert.NotContains(t, rec.Body.String(), "url-secret")
	stored, err := GetMCPServerById(server.Id)
	require.NoError(t, err)
	assert.Equal(t, server.Headers, stored.Headers)
	assert.Equal(t, MCPServerStatusDisabled, stored.Status)
}

func TestMCPInspectResults(t *testing.T) {
	for _, tc := range []struct {
		name, status, message string
		connected             bool
	}{
		{"connection", "not_requested", "", true},
		{"empty", "complete", "", true},
		{"unsupported", "unsupported", "", true},
		{"auth", "not_requested", "MCP upstream authentication failed", false},
		{"http", "not_requested", "MCP upstream HTTP request failed", false},
		{"invalid", "not_requested", "", false},
		{"version", "not_requested", "", false},
		{"missing_capabilities", "not_requested", "Invalid MCP initialization response", false},
		{"missing_capabilities_connection", "not_requested", "Invalid MCP initialization response", false},
		{"rpc", "failed", "MCP protocol request failed", true},
		{"pagination", "complete", "", true},
		{"repeat", "partial", "MCP tools pagination repeated a cursor", true},
		{"limit", "failed", "MCP inspection response limit reached", true},
		{"tools_limit", "partial", "MCP inspection result limit reached", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			setupTestMCPDB(t)
			requests := 0
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == "DELETE" {
					w.WriteHeader(204)
					return
				}
				var req struct {
					ID     any            `json:"id"`
					Method string         `json:"method"`
					Params map[string]any `json:"params"`
				}
				require.NoError(t, common.DecodeJson(r.Body, &req))
				w.Header().Set("Content-Type", "application/json")
				if tc.name == "auth" {
					w.WriteHeader(401)
					_, _ = io.WriteString(w, "hidden-secret")
					return
				}
				if tc.name == "http" {
					w.Header().Set("Location", "https://other.invalid/?key=hidden-secret")
					w.WriteHeader(302)
					return
				}
				if tc.name == "invalid" {
					_, _ = io.WriteString(w, `{"bad":true}`)
					return
				}
				var result any
				switch req.Method {
				case "initialize":
					version := "2025-03-26"
					if tc.name == "version" {
						version = "2099-01-01"
					}
					caps := map[string]any{}
					if tc.name != "unsupported" {
						caps["tools"] = map[string]any{}
					}
					result = map[string]any{"protocolVersion": version, "serverInfo": map[string]string{"name": "fixture", "version": "1"}, "capabilities": caps}
					if strings.HasPrefix(tc.name, "missing_capabilities") {
						delete(result.(map[string]any), "capabilities")
					}
				case "notifications/initialized":
					w.WriteHeader(202)
					return
				case "notifications/cancelled":
					w.WriteHeader(202)
					return
				case "tools/list":
					requests++
					if tc.name == "rpc" {
						raw, err := common.Marshal(map[string]any{"jsonrpc": "2.0", "id": req.ID, "error": map[string]any{"code": -32603, "message": "hidden-secret"}})
						require.NoError(t, err)
						_, _ = w.Write(raw)
						return
					}
					if tc.name == "limit" {
						_, _ = io.WriteString(w, strings.Repeat(" ", mcpInspectMaxBytes+1))
						return
					}
					tools := []any{}
					if tc.name == "pagination" || tc.name == "repeat" || tc.name == "tools_limit" {
						count := 1
						if tc.name == "tools_limit" {
							count = mcpInspectMaxTools + 1
						}
						for range count {
							tools = append(tools, map[string]any{"name": "search", "description": "hidden-secret", "inputSchema": map[string]any{"type": "object", "description": "hidden-secret"}})
						}
					}
					result = map[string]any{"tools": tools}
					if (tc.name == "pagination" && requests == 1) || tc.name == "repeat" {
						result.(map[string]any)["nextCursor"] = "page2"
					}
					if tc.name == "pagination" && requests == 2 {
						assert.Equal(t, "page2", req.Params["cursor"])
					}
				default:
					t.Errorf("unexpected method %s", req.Method)
				}
				raw, err := common.Marshal(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": result})
				require.NoError(t, err)
				_, _ = w.Write(raw)
			}))
			defer upstream.Close()
			server := &MCPServer{Name: "results", DisplayName: "Results", BaseURL: upstream.URL, Headers: `{"X-Api-Key":"hidden-secret"}`, ProtocolType: MCPProtocolStreamableHTTP, Timeout: 1}
			require.NoError(t, server.Insert())
			mode := "tools"
			if tc.name == "connection" || tc.name == "missing_capabilities_connection" {
				mode = "connection"
			}
			engine := gin.New()
			engine.POST("/servers/:id/inspect", AdminInspectMCPServer)
			rec := httptest.NewRecorder()
			req := httptest.NewRequest("POST", "/servers/1/inspect", strings.NewReader(`{"mode":"`+mode+`"}`))
			req.Header.Set("Content-Type", "application/json")
			// A hanging invalid response must be bounded by the caller's cancellation too.
			ctx, cancel := context.WithTimeout(req.Context(), 2*time.Second)
			defer cancel()
			engine.ServeHTTP(rec, req.WithContext(ctx))
			var envelope struct {
				Success bool             `json:"success"`
				Data    MCPInspectResult `json:"data"`
			}
			require.NoError(t, common.Unmarshal(rec.Body.Bytes(), &envelope))
			require.True(t, envelope.Success)
			assert.Equal(t, tc.connected, envelope.Data.Connected)
			assert.Equal(t, tc.status, envelope.Data.ToolsStatus)
			assert.NotContains(t, rec.Body.String(), "hidden-secret")
			if tc.message != "" {
				require.NotNil(t, envelope.Data.Error)
				assert.Equal(t, tc.message, envelope.Data.Error.Message)
			}
			if tc.name == "rpc" {
				require.NotNil(t, envelope.Data.Error.RPCCode)
				assert.EqualValues(t, -32603, *envelope.Data.Error.RPCCode)
				assert.Equal(t, "tools", envelope.Data.Stage)
			}
			if tc.name == "pagination" {
				assert.Len(t, envelope.Data.Tools, 2)
				assert.True(t, envelope.Data.Complete)
			}
			if tc.name == "connection" || tc.name == "unsupported" {
				assert.Zero(t, requests)
			}
		})
	}
}

func TestMCPInspectSSE(t *testing.T) {
	setupTestMCPDB(t)
	messages := make(chan string, 4)
	ended := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "upstream-secret", r.Header.Get("X-Api-Key"))
		assert.Equal(t, "url-secret", r.URL.Query().Get("token"))
		if r.Method == "GET" {
			assert.Equal(t, "/sse", r.URL.Path)
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, "event: endpoint\ndata: /messages?session=fixture\n\n")
			w.(http.Flusher).Flush()
			defer close(ended)
			for {
				select {
				case message := <-messages:
					_, _ = io.WriteString(w, "event: message\ndata: "+message+"\n\n")
					w.(http.Flusher).Flush()
				case <-r.Context().Done():
					return
				}
			}
		}
		assert.Equal(t, "/messages", r.URL.Path)
		var req struct {
			ID     any    `json:"id"`
			Method string `json:"method"`
		}
		require.NoError(t, common.DecodeJson(r.Body, &req))
		if req.Method == "notifications/initialized" {
			w.WriteHeader(202)
			return
		}
		var result any
		if req.Method == "initialize" {
			result = map[string]any{"protocolVersion": "2024-11-05", "serverInfo": map[string]string{"name": "sse-fixture", "version": "1"}, "capabilities": map[string]any{"tools": map[string]any{}}}
		} else {
			assert.Equal(t, "tools/list", req.Method)
			result = map[string]any{"tools": []any{}}
		}
		raw, err := common.Marshal(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": result})
		require.NoError(t, err)
		messages <- string(raw)
		w.WriteHeader(202)
	}))
	defer upstream.Close()
	server := &MCPServer{Name: "sse_inspect", DisplayName: "SSE", BaseURL: upstream.URL + "/sse?token=url-secret", Headers: `{"X-Api-Key":"upstream-secret"}`, ProtocolType: MCPProtocolSSE, Timeout: 1}
	require.NoError(t, server.Insert())
	engine := gin.New()
	engine.POST("/servers/:id/inspect", AdminInspectMCPServer)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/servers/1/inspect", strings.NewReader(`{"mode":"tools"}`))
	req.Header.Set("Content-Type", "application/json")
	engine.ServeHTTP(rec, req)
	assert.Contains(t, rec.Body.String(), `"connected":true`)
	assert.Contains(t, rec.Body.String(), `"tools_status":"complete"`)
	select {
	case <-ended:
	case <-time.After(time.Second):
		t.Fatal("SSE connection not cleaned up")
	}
}

func TestMCPInspectAccessAndValidation(t *testing.T) {
	setupTestMCPDB(t)
	engine := gin.New()
	engine.POST("/inspect/:id", middleware.AdminAuth(), AdminInspectMCPServer)
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, httptest.NewRequest("POST", "/inspect/1", strings.NewReader(`{"mode":"tools"}`)))
	assert.NotEqual(t, http.StatusOK, rec.Code)
	engine = gin.New()
	engine.POST("/inspect/:id", AdminInspectMCPServer)
	for _, tc := range []struct{ path, body string }{{"/inspect/0", `{"mode":"tools"}`}, {"/inspect/1", `{"mode":"execute"}`}, {"/inspect/1", `{"mode":"tools"}`}} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("POST", tc.path, strings.NewReader(tc.body))
		req.Header.Set("Content-Type", "application/json")
		engine.ServeHTTP(rec, req)
		assert.Contains(t, rec.Body.String(), `"success":false`)
	}
}

func TestMCPInspectStreamableEventResponse(t *testing.T) {
	setupTestMCPDB(t)
	canceled := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			ID     any    `json:"id"`
			Method string `json:"method"`
		}
		require.NoError(t, common.DecodeJson(r.Body, &request))
		if request.Method == "notifications/initialized" {
			w.WriteHeader(202)
			return
		}
		var result any
		if request.Method == "initialize" {
			result = map[string]any{"protocolVersion": "2025-03-26", "serverInfo": map[string]string{"name": "events", "version": "1"}, "capabilities": map[string]any{"tools": map[string]any{}}}
		} else {
			result = map[string]any{"tools": []any{}}
		}
		raw, err := common.Marshal(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": result})
		require.NoError(t, err)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "event: message\ndata: "+string(raw)+"\n\n")
		w.(http.Flusher).Flush()
		if request.Method == "tools/list" {
			<-r.Context().Done()
			close(canceled)
		}
	}))
	defer upstream.Close()
	server := &MCPServer{Name: "events", DisplayName: "Events", BaseURL: upstream.URL, ProtocolType: MCPProtocolStreamableHTTP, Timeout: 1}
	require.NoError(t, server.Insert())
	engine := gin.New()
	engine.POST("/inspect/:id", AdminInspectMCPServer)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/inspect/1", strings.NewReader(`{"mode":"tools"}`))
	req.Header.Set("Content-Type", "application/json")
	engine.ServeHTTP(rec, req)
	assert.Contains(t, rec.Body.String(), `"tools_status":"complete"`)
	select {
	case <-canceled:
	case <-time.After(time.Second):
		t.Fatal("response SSE body leaked")
	}
}

func TestMCPInspectCancelAndEndpointIsolation(t *testing.T) {
	for _, kind := range []string{"headers", "result", "id", "cross-origin"} {
		t.Run(kind, func(t *testing.T) {
			setupTestMCPDB(t)
			started := make(chan struct{})
			ended := make(chan struct{})
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == "DELETE" {
					w.WriteHeader(204)
					return
				}
				if kind == "cross-origin" {
					w.Header().Set("Content-Type", "text/event-stream")
					_, _ = io.WriteString(w, "event: endpoint\ndata: http://other.invalid/messages?token=secret\n\n")
					w.(http.Flusher).Flush()
					<-r.Context().Done()
					return
				}
				_, _ = io.Copy(io.Discard, r.Body)
				close(started)
				defer close(ended)
				if kind != "headers" {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(200)
					if kind == "id" {
						_, _ = io.WriteString(w, `{"jsonrpc":"2.0","id":999,"result":{"protocolVersion":"2025-03-26","serverInfo":{"name":"wrong","version":"1"},"capabilities":{}}}`)
					}
					w.(http.Flusher).Flush()
				}
				<-r.Context().Done()
			}))
			defer upstream.Close()
			defer upstream.CloseClientConnections()
			protocol := MCPProtocolStreamableHTTP
			if kind == "cross-origin" {
				protocol = MCPProtocolSSE
			}
			server := &MCPServer{Name: "cancel", DisplayName: "Cancel", BaseURL: upstream.URL, ProtocolType: protocol, Timeout: 1}
			require.NoError(t, server.Insert())
			engine := gin.New()
			engine.POST("/inspect/:id", AdminInspectMCPServer)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if kind == "id" {
				var release context.CancelFunc
				ctx, release = context.WithTimeout(ctx, 100*time.Millisecond)
				defer release()
			}
			req := httptest.NewRequest("POST", "/inspect/1", strings.NewReader(`{"mode":"tools"}`)).WithContext(ctx)
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			done := make(chan struct{})
			go func() { engine.ServeHTTP(rec, req); close(done) }()
			if kind == "headers" || kind == "result" {
				<-started
				cancel()
			}
			select {
			case <-done:
			case <-time.After(3 * time.Second):
				t.Fatal("inspection did not stop")
			}
			assert.Contains(t, rec.Body.String(), `"connected":false`)
			assert.NotContains(t, rec.Body.String(), "secret")
			if kind != "cross-origin" {
				select {
				case <-ended:
				case <-time.After(time.Second):
					t.Fatal("upstream did not cancel")
				}
			}
		})
	}
}
