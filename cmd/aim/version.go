package main

import (
	"runtime/debug"
	"strings"
)

// Set only by the linker (-X); the package's only mutable package-level state.
var version, commit, date string

// stamp holds the linker values.
type stamp struct{ version, commit, date string }

// buildInfo returns the binary's build info, or nil when there is none.
func buildInfo() *debug.BuildInfo {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return nil
	}

	return info
}

// versionString returns the version line without the leading "aim ".
func versionString(ld stamp, info *debug.BuildInfo) string {
	settings := map[string]string{}

	if info != nil {
		for _, s := range info.Settings {
			settings[s.Key] = s.Value
		}
	}

	ver := "dev"

	switch {
	case ld.version != "":
		ver = ld.version
		if ver[0] >= '0' && ver[0] <= '9' {
			ver = "v" + ver
		}
	case info != nil && settings["vcs.revision"] == "" &&
		info.Main.Version != "" && info.Main.Version != "(devel)":
		ver = info.Main.Version
	}

	var parts []string

	rev := ld.commit
	if rev == "" {
		rev = settings["vcs.revision"]
		if len(rev) > 7 {
			rev = rev[:7]
		}
	}

	if rev != "" {
		parts = append(parts, "commit "+rev)
	}

	if settings["vcs.modified"] == "true" {
		parts = append(parts, "modified")
	}

	switch {
	case ld.date != "":
		parts = append(parts, "built "+ld.date)
	case settings["vcs.time"] != "":
		parts = append(parts, "committed "+settings["vcs.time"])
	}

	if len(parts) == 0 {
		return ver
	}

	return ver + " (" + strings.Join(parts, ", ") + ")"
}
