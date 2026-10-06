//go:build !nobundledharness

package defaultharness

import (
	"github.com/yeomyeonggeori/bluecollar/approval"
	"github.com/yeomyeonggeori/blueprotocol/holdrecord"
	"github.com/yeomyeonggeori/blueprotocol/model"
)

func NewQuestionWorder(languageModel model.LanguageModelProvider) holdrecord.QuestionWorder {
	return approval.NewWorder(languageModel)
}
