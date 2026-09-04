package enrollment

import (
	"context"
	"errors"
	"strings"
)

type RunMode string

const (
	RunModeHost  RunMode = "host"
	RunModeGuest RunMode = "guest"
)

type Person struct {
	PersonID    string
	DisplayName string
	Email       string
}

type LanguageModelAccess struct {
	EndpointURL        string
	ModelName          string
	APIKey             string
	EmbeddingModelName string
}

func (access LanguageModelAccess) IsConfigured() bool {
	return strings.TrimSpace(access.EndpointURL) != "" && strings.TrimSpace(access.ModelName) != ""
}

type HarnessChoice struct {
	Name             string
	AgentCommandPath string
}

type Enrollment struct {
	TenantID                 string
	Mode                     RunMode
	Operator                 Person
	WorkspaceRootPath        string
	DatabaseConnectionString string
	LanguageModel            LanguageModelAccess
	Harness                  HarnessChoice
}

type Provider interface {
	Enroll(context.Context) (Enrollment, error)
}

var ErrEnrollmentIncomplete = errors.New("this install has no enrollment yet, so there is nobody for the agent to run as")

func (enrollment Enrollment) Validate() error {
	if strings.TrimSpace(enrollment.TenantID) == "" {
		return errors.New("an enrollment needs a tenant, because every person and every task belongs to one")
	}
	if strings.TrimSpace(enrollment.Operator.PersonID) == "" || strings.TrimSpace(enrollment.Operator.Email) == "" {
		return errors.New("an enrollment needs the person the agent runs as, because tools execute under their identity")
	}
	if enrollment.Mode != RunModeHost && enrollment.Mode != RunModeGuest {
		return errors.New("an enrollment needs a run mode, host or guest, because they join the workspace differently")
	}
	if strings.TrimSpace(enrollment.WorkspaceRootPath) == "" {
		return errors.New("an enrollment needs a workspace root, because that is where the agent's work lives")
	}
	if !enrollment.LanguageModel.IsConfigured() {
		return errors.New("an enrollment needs a way to reach a language model: a model endpoint and the name of a model it serves")
	}
	return nil
}
