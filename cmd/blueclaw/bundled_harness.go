//go:build !nobundledharness

package main

import (
	"github.com/yeomyeonggeori/blueclaw/internal/bluecollaracp"
	"github.com/yeomyeonggeori/blueclaw/internal/harnessdriver"
)

func bundledACPFactory() harnessdriver.ACPFactory {
	return bluecollaracp.NewFactory
}
