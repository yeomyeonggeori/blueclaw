package connectors

import "context"

type HTTPWebhookTransport struct {
	TransportName string
	PlatformName  string
}

func NewHTTPWebhookTransport(transportName string, platformName string) HTTPWebhookTransport {
	return HTTPWebhookTransport{
		TransportName: transportName,
		PlatformName:  platformName,
	}
}

func (transport HTTPWebhookTransport) Name() string {
	return transport.TransportName
}

func (transport HTTPWebhookTransport) Platform() string {
	return transport.PlatformName
}

func (transport HTTPWebhookTransport) Start(context.Context) {}

type capabilityMessageEditRequest struct {
	ReplyTargetID string `json:"replyTargetID"`
	MessageID     string `json:"messageID"`
	Message       string `json:"message"`
}
