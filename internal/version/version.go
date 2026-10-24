// Package version identifies the producer of recorded evidence and reports.
package version

import "runtime/debug"

// Version and Commit may be supplied by local release builds using -ldflags.
var Version = "dev"
var Commit string
var BuildDirty string

type Producer struct {
	Name    string `json:"name" yaml:"name"`
	Version string `json:"version" yaml:"version"`
	Commit  string `json:"commit,omitempty" yaml:"commit,omitempty"`
	Dirty   bool   `json:"dirty,omitempty" yaml:"dirty,omitempty"`
}

func Current() Producer {
	p := Producer{Name: "wiretap", Version: Version, Commit: Commit, Dirty: BuildDirty == "true"}
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, setting := range info.Settings {
			if setting.Key == "vcs.revision" && p.Commit == "" {
				p.Commit = setting.Value
			}
			if setting.Key == "vcs.modified" {
				p.Dirty = p.Dirty || setting.Value == "true"
			}
		}
	}
	return p
}
