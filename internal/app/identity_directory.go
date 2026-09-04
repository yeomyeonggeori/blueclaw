package app

import (
	"log/slog"

	"github.com/yeomyeonggeori/blueclaw/internal/adminapi"
	"github.com/yeomyeonggeori/blueclaw/internal/identity"
	"github.com/yeomyeonggeori/blueclaw/internal/policy"
	"github.com/yeomyeonggeori/blueclaw/internal/store/postgres"
	"github.com/yeomyeonggeori/bluecollar/agentcontract"
)

type identityDirectory struct {
	policyProjectionService policy.PolicyProjectionService
	identityService         *identity.IdentityService
	platformAccountLister   adminapi.PlatformAccountLister
	policyWatcher           *policy.PolicyWatcher
	companyProvider         func() agentcontract.CompanyContext
	companyLocaleProvider   func() string
}

func newIdentityDirectory(database postgres.Database, policyDocument policy.PolicyDocument, logger *slog.Logger) identityDirectory {
	logger.Info("application.initializing", "stage", "identity")
	policyProjectionService := policy.PolicyProjectionService{}
	identityService := identity.NewIdentityService(policyProjectionService.ReplacePolicyProjectionTransactionally(policyDocument))
	var platformAccountLister adminapi.PlatformAccountLister
	if database.SQL != nil {
		platformAccountRepository := postgres.NewPlatformAccountRepository(database)
		identityService.UsePlatformAccountRepository(platformAccountRepository)
		platformAccountLister = platformAccountRepository
	}
	policyWatcher := &policy.PolicyWatcher{}
	directory := identityDirectory{
		policyProjectionService: policyProjectionService,
		identityService:         identityService,
		platformAccountLister:   platformAccountLister,
		policyWatcher:           policyWatcher,
		companyProvider:         newCompanyProvider(policyWatcher),
		companyLocaleProvider:   newCompanyLocaleProvider(policyWatcher),
	}
	policyWatcher.ReloadPolicyDocument(policyDocument)
	return directory
}

func newCompanyProvider(policyWatcher *policy.PolicyWatcher) func() agentcontract.CompanyContext {
	return func() agentcontract.CompanyContext {
		company := policyWatcher.CurrentPolicyDocument().Company
		return agentcontract.CompanyContext{
			Name:           company.Name,
			BrandName:      company.BrandName,
			Slogan:         company.Slogan,
			Description:    company.Description,
			Representative: company.Representative,
			Website:        company.Website,
			TimeZone:       company.TimeZone,
		}
	}
}

func newCompanyLocaleProvider(policyWatcher *policy.PolicyWatcher) func() string {
	return func() string {
		return policyWatcher.CurrentPolicyDocument().Company.Locale
	}
}
