package bluecollaracp

import (
	"context"
	"errors"
	"io"
	"net"

	"github.com/yeomyeonggeori/blueclaw/internal/acpharness"
	"github.com/yeomyeonggeori/blueclaw/internal/harnessdriver"
	"github.com/yeomyeonggeori/bluecollar/acpagent"
	"github.com/yeomyeonggeori/bluecollar/agentcontract"
)

type agentProcess struct {
	dependencies   harnessdriver.Dependencies
	skillRetriever agentcontract.SkillRetriever
}

func (process agentProcess) Start(ctx context.Context) (io.Writer, io.Reader, func() error, error) {
	request, isPresent := acpharness.TurnRequestFrom(ctx)
	if !isPresent {
		return nil, nil, nil, errors.New("the bluecollar agent starts for a turn, and this start carried none")
	}
	agentSide, hostSide := net.Pipe()
	served := make(chan error, 1)
	go func() {
		served <- acpagent.Serve(process.optionsFor(request), agentSide, agentSide)
	}()
	return hostSide, hostSide, func() error {
		_ = hostSide.Close()
		servedError := <-served
		_ = agentSide.Close()
		return servedError
	}, nil
}

func (process agentProcess) optionsFor(request agentcontract.AgentTurnRequest) acpagent.Options {
	return acpagent.Options{
		AgentName:         request.AgentIdentity.Name,
		LanguageModels:    process.dependencies.TaskTierLanguageModels,
		DecisionModel:     process.dependencies.DecisionModel,
		LLMCallRepository: process.dependencies.LLMCallRepository,
		Skills: acpagent.Skills{
			InstructionBundleLoader: process.dependencies.InstructionBundleLoader,
			Retriever:               process.skillRetriever,
			PinnedSkillNames:        request.PinnedSkillNames,
		},
		HostCheckedToolNames: hostCheckedToolNames(request),
	}
}

func hostCheckedToolNames(request agentcontract.AgentTurnRequest) []string {
	toolNames := []string{}
	for _, toolDefinition := range request.ToolSet.ListDescribedToolDefinitions() {
		if toolDefinition.RequiresApproval {
			toolNames = append(toolNames, toolDefinition.Name)
		}
	}
	return toolNames
}
