package officevisualcheck

import (
	"sync"

	"github.com/yeomyeonggeori/bluecollar/model"
)

const maximumParallelCalls = 16

func inParallel[Input, Output any](inputs []Input, work func(Input) Output) []Output {
	outputs := make([]Output, len(inputs))
	limiter := make(chan struct{}, maximumParallelCalls)
	var group sync.WaitGroup
	for index, input := range inputs {
		group.Go(func() {
			limiter <- struct{}{}
			defer func() { <-limiter }()
			outputs[index] = work(input)
		})
	}
	group.Wait()
	return outputs
}

func addedUsage(total model.Usage, added model.Usage) model.Usage {
	total.PromptTokens += added.PromptTokens
	total.CompletionTokens += added.CompletionTokens
	total.TotalTokens += added.TotalTokens
	total.CachedPromptTokens += added.CachedPromptTokens
	total.CacheWriteTokens += added.CacheWriteTokens
	total.ReasoningTokens += added.ReasoningTokens
	total.CostUSD += added.CostUSD
	total.UpstreamInferenceCost += added.UpstreamInferenceCost
	return total
}
