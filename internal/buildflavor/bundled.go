//go:build !nobundledharness

package buildflavor

const IsBundledHarnessBuild = true

func GoFlags() []string {
	return nil
}
