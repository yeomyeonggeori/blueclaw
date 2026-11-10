package llm

import (
	"net/http"
	"strings"
	"time"
)

const requesterNamingDecisionTimeout = 30 * time.Second

func nameRequester(request *http.Request, headerName string) {
	if headerName == "" {
		return
	}
	email := strings.TrimSpace(RequestContextFromContext(request.Context()).RequesterEmail)
	if email == "" {
		return
	}
	request.Header.Set(headerName, email)
}

type requesterNaming struct {
	headerName string
}

func (naming requesterNaming) RoundTrip(request *http.Request) (*http.Response, error) {
	named := request.Clone(request.Context())
	nameRequester(named, naming.headerName)
	return http.DefaultTransport.RoundTrip(named)
}

func requesterNamingDecisionClient(headerName string) *http.Client {
	trimmed := strings.TrimSpace(headerName)
	if trimmed == "" {
		return nil
	}
	return &http.Client{Timeout: requesterNamingDecisionTimeout, Transport: requesterNaming{headerName: trimmed}}
}
