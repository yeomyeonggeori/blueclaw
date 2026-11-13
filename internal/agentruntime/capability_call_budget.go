package agentruntime

import (
	"time"
)

const capabilityAnswerMargin = 5 * time.Second

func capabilityToolBudget(descriptor CapabilityToolDescriptor) time.Duration {
	return time.Duration(descriptor.TimeoutSecond) * time.Second
}

func capabilityToolTimeoutMS(descriptor CapabilityToolDescriptor) int {
	if descriptor.TimeoutSecond <= 0 {
		return 0
	}
	return int((capabilityToolBudget(descriptor) + capabilityAnswerMargin) / time.Millisecond)
}

func (toolCatalogBuilder *ToolCatalogBuilder) capabilityInvocationLimit(descriptor CapabilityToolDescriptor) time.Duration {
	if descriptor.TimeoutSecond <= 0 {
		return toolCatalogBuilder.capabilityClient.CallTimeout
	}
	return capabilityToolBudget(descriptor) + capabilityAnswerMargin
}
