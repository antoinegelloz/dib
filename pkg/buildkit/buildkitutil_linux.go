package buildkit

func getRuntimeVariableDataDir() string {
	// Per Linux Foundation "Filesystem Hierarchy Standard" version 3.0 section 3.15.
	// Under version 2.3, this was "/var/run".
	return "/run"
}
