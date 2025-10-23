package app

type Config struct {
	TargetBranch          string
	FileToCompare         string
	FilesToIgnore         []string
	PreserveHelmLabels    bool
	PrintAddedManifests   bool
	PrintRemovedManifests bool
	CacheDir              string
	TempDirBase           string
	ExternalDiffTool      string
	Debug                 bool
	Version               string
}
