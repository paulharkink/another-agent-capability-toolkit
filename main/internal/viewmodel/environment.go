package viewmodel

// EnvironmentTarget is one target TOML found on disk. Error reports an invalid
// target while keeping it visible beside valid targets.
type EnvironmentTarget struct {
	SourceID, Environment, PackageID, Name, Path, Error string
}

type EnvironmentSnapshot struct {
	SourceID, Root string
	Environments   []string
	Targets        []EnvironmentTarget
}
