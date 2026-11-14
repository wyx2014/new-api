package router

import (
	"bufio"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGatewayMount(t *testing.T) {
	engine := gin.New()
	engine.GET("/api/probe/", func(context *gin.Context) {
		context.String(http.StatusOK, context.Request.RequestURI)
	})
	handler := BuildGatewayHandler(engine)
	for _, test := range []struct {
		path     string
		status   int
		location string
		body     string
	}{
		{path: "/", status: http.StatusNotFound},
		{path: "/api/probe/", status: http.StatusNotFound},
		{path: "/modelgw-other/api/probe/", status: http.StatusNotFound},
		{path: common.ModelGatewayPrefix + "?q=one", status: http.StatusPermanentRedirect, location: common.ModelGatewayPrefix + "/?q=one"},
		{path: common.ModelGatewayPrefix + "/api/probe/?q=one", status: http.StatusOK, body: "/api/probe/?q=one"},
		{path: common.ModelGatewayPrefix + "/api/probe", status: http.StatusMovedPermanently, location: common.ModelGatewayPrefix + "/api/probe/"},
	} {
		t.Run(test.path, func(t *testing.T) {
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, test.path, nil))
			require.Equal(t, test.status, response.Code)
			assert.Equal(t, test.location, response.Header().Get("Location"))
			if test.body != "" {
				assert.Equal(t, test.body, response.Body.String())
			}
		})
	}
}

func TestGatewayPreservesStreamingAndWebSocket(t *testing.T) {
	engine := gin.New()
	engine.GET("/events", func(context *gin.Context) {
		context.Header("Content-Type", "text/event-stream")
		_, _ = context.Writer.WriteString("data: ready\n\n")
		context.Writer.Flush()
		<-context.Request.Context().Done()
	})
	engine.GET("/socket", func(context *gin.Context) {
		upgrader := websocket.Upgrader{}
		connection, err := upgrader.Upgrade(context.Writer, context.Request, nil)
		if err != nil {
			return
		}
		defer connection.Close()
		kind, message, err := connection.ReadMessage()
		if err == nil {
			_ = connection.WriteMessage(kind, message)
		}
	})
	server := httptest.NewServer(BuildGatewayHandler(engine))
	t.Cleanup(server.Close)
	requestContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(requestContext, http.MethodGet, server.URL+common.ModelGatewayPrefix+"/events", nil)
	require.NoError(t, err)
	response, err := server.Client().Do(request)
	require.NoError(t, err)
	defer response.Body.Close()
	line, err := bufio.NewReader(response.Body).ReadString('\n')
	require.NoError(t, err)
	assert.Equal(t, "data: ready\n", line)
	cancel()
	connection, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+common.ModelGatewayPrefix+"/socket", nil)
	require.NoError(t, err)
	defer connection.Close()
	require.NoError(t, connection.WriteMessage(websocket.TextMessage, []byte("gateway")))
	_, message, err := connection.ReadMessage()
	require.NoError(t, err)
	assert.Equal(t, "gateway", string(message))
}
