package version

import "runtime/debug"

// String identifies the binary actually running, rather than its current path
// on disk. It remains useful after a binary is replaced without a restart.
var build = readBuild()

func String() string { return build }

func readBuild() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "devel"
	}
	var revision string
	modified := false
	for _, setting := range info.Settings {
		switch setting.Key {
		case "vcs.revision":
			revision = setting.Value
		case "vcs.modified":
			modified = setting.Value == "true"
		}
	}
	if revision != "" {
		revision = revision[:min(12, len(revision))]
		if modified {
			revision += "+dirty"
		}
		return revision
	}
	if info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	return "devel"
}
