//go:build nobundledharness

package main

import "github.com/yeomyeonggeori/blueclaw/internal/harnessdriver"

func bundledHarnessFactory() harnessdriver.Factory {
	return nil
}

func bundledACPFactory() harnessdriver.ACPFactory {
	return nil
}
