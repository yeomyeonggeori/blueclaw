package httpserver

import (
	"crypto/subtle"
	"net/http"
	"os"
	"strings"
)

const healthPath = "/admin/api/health"

var pathsOnlyTheHostServicesMayCall = []string{"/admin/api/", "/connectors/"}

func withServiceToken(serviceTokenPath string, nextHandler http.Handler) http.Handler {
	trimmedServiceTokenPath := strings.TrimSpace(serviceTokenPath)
	if trimmedServiceTokenPath == "" {
		return nextHandler
	}
	return http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		if isForTheHostServicesOnly(request.URL.Path) && !carriesServiceToken(request, trimmedServiceTokenPath) {
			http.Error(responseWriter, "this answers only the host services that hold the service token", http.StatusUnauthorized)
			return
		}
		nextHandler.ServeHTTP(responseWriter, request)
	})
}

func isForTheHostServicesOnly(path string) bool {
	if path == healthPath {
		return false
	}
	for _, prefix := range pathsOnlyTheHostServicesMayCall {
		if strings.HasPrefix(path, prefix) {
			return true
		}
	}
	return false
}

func carriesServiceToken(request *http.Request, serviceTokenPath string) bool {
	kept, errorValue := os.ReadFile(serviceTokenPath)
	expected := strings.TrimSpace(string(kept))
	if errorValue != nil || expected == "" {
		return false
	}
	offered, hasBearer := strings.CutPrefix(request.Header.Get("Authorization"), "Bearer ")
	return hasBearer && subtle.ConstantTimeCompare([]byte(offered), []byte(expected)) == 1
}
