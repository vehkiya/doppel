package main

import "runtime/debug"

// Version information injected at build time via -ldflags.
var (
	Version   = "dev"
	Commit    = "none"
	BuildDate = "unknown"
)

// Builds without -ldflags, such as `go install github.com/vehkiya/doppel@latest`,
// take their version and VCS details from the module build info instead.
func init() {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return
	}
	applyBuildInfo(info)
}

func applyBuildInfo(info *debug.BuildInfo) {
	if Version == "dev" && info.Main.Version != "" && info.Main.Version != "(devel)" {
		Version = info.Main.Version
	}
	for _, setting := range info.Settings {
		switch setting.Key {
		case "vcs.revision":
			if Commit == "none" && len(setting.Value) >= 7 {
				Commit = setting.Value[:7]
			}
		case "vcs.time":
			if BuildDate == "unknown" {
				BuildDate = setting.Value
			}
		}
	}
}
