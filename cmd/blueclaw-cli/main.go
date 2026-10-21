package main

import (
	"flag"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/yeomyeonggeori/blueclaw/internal/capability"
	"github.com/yeomyeonggeori/blueclaw/internal/config"
	"github.com/yeomyeonggeori/blueclaw/internal/enrollment"
	"github.com/yeomyeonggeori/blueclaw/internal/policy"
	"github.com/yeomyeonggeori/blueclaw/internal/tui"
)

func main() {
	baseURL := flag.String("base-url", "", "base URL of the blueclaw admin API, taken from the install when empty")
	runtimeConfigurationPath := flag.String("runtime", "", "path to the daemon's runtime configuration JSON, used to show the configured agent harness")
	approverEmail := flag.String("approver", "", "the address of the person deciding held calls from this terminal, who must be the person each call was held for; taken from the install's roster when it names exactly one person")
	flag.Parse()

	home := enrollment.ResolveHome()
	if !home.IsEnrolled() {
		if errorValue := runSetup(home); errorValue != nil {
			fmt.Fprintln(os.Stderr, "blueclaw:", errorValue)
			os.Exit(1)
		}
		if !home.IsEnrolled() {
			return
		}
	}

	configurationPath := *runtimeConfigurationPath
	if configurationPath == "" {
		configurationPath = home.RuntimeConfigurationPath()
	}
	client := tui.NewClient(resolveBaseURL(*baseURL, configurationPath), adminAPIClient(configurationPath)).DecidingAs(resolveApproverEmail(*approverEmail, home.PolicyPath()))
	program := tea.NewProgram(tui.NewModel(client, configurationPath))
	if _, errorValue := program.Run(); errorValue != nil {
		fmt.Fprintln(os.Stderr, "blueclaw:", errorValue)
		os.Exit(1)
	}
}

func resolveBaseURL(configuredBaseURL string, runtimeConfigurationPath string) string {
	if strings.TrimSpace(configuredBaseURL) != "" {
		return configuredBaseURL
	}
	runtimeConfiguration, errorValue := config.LoadRuntimeConfiguration(runtimeConfigurationPath)
	if errorValue != nil || strings.TrimSpace(runtimeConfiguration.BaseURL) == "" {
		return "http://127.0.0.1:8080"
	}
	return runtimeConfiguration.BaseURL
}

func resolveApproverEmail(configuredApproverEmail string, policyPath string) string {
	if strings.TrimSpace(configuredApproverEmail) != "" {
		return configuredApproverEmail
	}
	policyDocument, errorValue := policy.PolicyLoader{}.LoadPolicyDocument(policyPath)
	if errorValue != nil || len(policyDocument.People) != 1 || len(policyDocument.People[0].Emails) == 0 {
		return ""
	}
	return policyDocument.People[0].Emails[0]
}

func adminAPIClient(runtimeConfigurationPath string) *http.Client {
	runtimeConfiguration, errorValue := config.LoadRuntimeConfiguration(runtimeConfigurationPath)
	if errorValue != nil || strings.TrimSpace(runtimeConfiguration.ServiceTokenPath) == "" {
		return nil
	}
	return &http.Client{Timeout: 15 * time.Second, Transport: capability.BearerTokenTransport(nil, runtimeConfiguration.ServiceTokenPath)}
}

func runSetup(home enrollment.Home) error {
	program := tea.NewProgram(tui.NewSetupModel(home))
	_, errorValue := program.Run()
	return errorValue
}
