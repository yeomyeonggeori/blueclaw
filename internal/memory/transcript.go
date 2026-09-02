package memory

import (
	"strings"

	"github.com/yeomyeonggeori/bluecollar/taskstate"
)

const (
	transcriptStepCharacterLimit  = 1200
	transcriptTotalCharacterLimit = 12000
)

func RenderTaskTranscript(taskRun taskstate.TaskRun, steps []taskstate.TaskStep) string {
	sections := []string{"Request:\n" + strings.TrimSpace(taskRun.Prompt)}
	for _, step := range steps {
		instruction := strings.TrimSpace(step.Instruction)
		output := strings.TrimSpace(step.Output)
		if instruction == "" && output == "" {
			continue
		}
		section := "Step (" + string(step.Status) + "): " + clampRunes(instruction, transcriptStepCharacterLimit)
		if output != "" {
			section += "\nOutput: " + clampRunes(output, transcriptStepCharacterLimit)
		}
		sections = append(sections, section)
	}
	if result := strings.TrimSpace(taskRun.Result); result != "" {
		sections = append(sections, "Final reply:\n"+result)
	}
	if failureReason := strings.TrimSpace(taskRun.FailureReason); failureReason != "" {
		sections = append(sections, "Outcome: "+string(taskRun.Status)+" ("+failureReason+")")
	} else {
		sections = append(sections, "Outcome: "+string(taskRun.Status))
	}
	return clampRunes(strings.Join(sections, "\n\n"), transcriptTotalCharacterLimit)
}

func clampRunes(value string, limit int) string {
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit]) + " …"
}
