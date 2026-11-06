package app

import (
	"context"

	"github.com/yeomyeonggeori/blueclaw/internal/config"
	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
)

func languageModelEndpoints(configuration config.LanguageModelConfiguration) []string {
	endpoints := []string{}
	for _, tier := range configuration.Tiers {
		for _, rung := range tier {
			endpoints = append(endpoints, rung.Endpoint)
		}
	}
	return append(endpoints, configuration.Decision.Endpoint, configuration.Embedding.Endpoint, configuration.VisualReview.Endpoint)
}

func companyZoneOf(companyProvider func() agentcontract.CompanyContext) func() string {
	return func() string {
		return companyProvider().TimeZone
	}
}

func (application *Application) startClockWatch() {
	if application.trustedClock == nil || application.clockWatchCancel != nil {
		return
	}
	application.clockWatchCancel = application.startBackgroundLoop(application.trustedClock.Watch)
}

func (application *Application) startOnceTheClockIsTrusted(run func(context.Context)) context.CancelFunc {
	return application.startBackgroundLoop(func(ctx context.Context) {
		if application.trustedClock != nil && !application.awaitTrustedClock(ctx) {
			return
		}
		run(ctx)
	})
}

func (application *Application) awaitTrustedClock(ctx context.Context) bool {
	select {
	case <-application.trustedClock.Trusted():
		return true
	case <-ctx.Done():
		return false
	}
}
