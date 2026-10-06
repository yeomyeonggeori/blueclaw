//go:build !nobundledharness

package main

import (
	"github.com/yeomyeonggeori/blueclaw/internal/bluecollaracp"
	"github.com/yeomyeonggeori/blueclaw/internal/bluecollarharness"
	"github.com/yeomyeonggeori/blueclaw/internal/harnessdriver"
)

func bundledHarnessFactory() harnessdriver.Factory {
	return bluecollarharness.New
}

func bundledACPFactory() harnessdriver.ACPFactory {
	return bluecollaracp.NewFactory
}
