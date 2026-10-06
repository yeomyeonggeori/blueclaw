//go:build !nobundledharness

package main

import (
	"github.com/yeomyeonggeori/blueclaw/internal/app"
	"github.com/yeomyeonggeori/blueclaw/internal/defaultharness"
	"github.com/yeomyeonggeori/blueclaw/internal/harnessdriver"
)

func bundledACPFactory() harnessdriver.ACPFactory {
	return defaultharness.NewFactory
}

func bundledQuestionWorder() app.QuestionWorderFactory {
	return defaultharness.NewQuestionWorder
}

func bundledToolSelector() app.BundledToolSelectorFactory {
	return defaultharness.NewToolSelector
}
