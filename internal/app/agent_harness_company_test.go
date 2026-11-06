package app

import (
	"testing"

	"github.com/yeomyeonggeori/blueclaw/internal/config"
	"github.com/yeomyeonggeori/blueclaw/internal/harnessdriver"
	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
)

func TestTheAgentHarnessReadsTheClockInTheCompanyZone(t *testing.T) {
	companyProvider := func() agentcontract.CompanyContext {
		return agentcontract.CompanyContext{Name: "주식회사 예시", TimeZone: "Asia/Seoul"}
	}
	var received harnessdriver.Dependencies
	capturingFactory := func(dependencies harnessdriver.Dependencies) (agentcontract.Harness, agentcontract.SkillRetriever) {
		received = dependencies
		return nil, nil
	}

	startAgentHarness(config.RuntimeConfiguration{}, capturingFactory, agentKernel{}, taskServices{}, companyProvider)

	if received.CompanyProvider == nil {
		t.Fatal("the harness got no company, so the agent loop reads every relative date in UTC")
	}
	if timeZone := received.CompanyProvider().TimeZone; timeZone != "Asia/Seoul" {
		t.Fatalf("the harness reads the clock in %q, want the company's Asia/Seoul", timeZone)
	}
}
