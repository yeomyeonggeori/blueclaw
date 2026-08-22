// Package buildrevision answers which revision this binary was built from.
//
// A green build says something was compiled and a green deploy says something was
// uploaded. Neither says which revision landed, and this repository has paid for
// that twice: a setup that installed months-old script text and reported the step
// done, and a deploy that skipped an unchanged SHA while the working tree held the
// fix.
package buildrevision

import (
	"runtime/debug"
	"strings"
)

const shortLength = 12

// Set through -ldflags when a build has no VCS information of its own, such as one
// made from an exported tree.
var injected string

func Revision() string {
	if trimmed := strings.TrimSpace(injected); trimmed != "" {
		return trimmed
	}
	buildInfo, isAvailable := debug.ReadBuildInfo()
	if !isAvailable {
		return ""
	}
	revision, isModified := "", false
	for _, setting := range buildInfo.Settings {
		switch setting.Key {
		case "vcs.revision":
			revision = setting.Value
		case "vcs.modified":
			isModified = setting.Value == "true"
		}
	}
	if revision == "" {
		return ""
	}
	if isModified {
		return revision + "+"
	}
	return revision
}

func Short() string {
	revision := Revision()
	isModified := strings.HasSuffix(revision, "+")
	revision = strings.TrimSuffix(revision, "+")
	if len(revision) > shortLength {
		revision = revision[:shortLength]
	}
	if isModified {
		return revision + "+"
	}
	return revision
}
