package router

import (
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
)

func BuildGatewayHandler(base http.Handler) http.Handler {
	stripped := http.StripPrefix(common.ModelGatewayPrefix, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		request.RequestURI = request.URL.RequestURI()
		request.Header.Set("X-Forwarded-Prefix", common.ModelGatewayPrefix)
		base.ServeHTTP(writer, request)
	}))
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == common.ModelGatewayPrefix {
			target := common.ModelGatewayPrefix + "/"
			if request.URL.RawQuery != "" {
				target += "?" + request.URL.RawQuery
			}
			http.Redirect(writer, request, target, http.StatusPermanentRedirect)
			return
		}
		if !strings.HasPrefix(request.URL.Path, common.ModelGatewayPrefix+"/") {
			http.NotFound(writer, request)
			return
		}
		stripped.ServeHTTP(writer, request)
	})
}
