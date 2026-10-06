//go:build nobundledharness

package main

import (
	"github.com/yeomyeonggeori/blueclaw/internal/app"
	"github.com/yeomyeonggeori/blueclaw/internal/harnessdriver"
)

func bundledACPFactory() harnessdriver.ACPFactory {
	return nil
}

func bundledQuestionWorder() app.QuestionWorderFactory {
	return nil
}

func bundledToolSelector() app.BundledToolSelectorFactory {
	return nil
}
