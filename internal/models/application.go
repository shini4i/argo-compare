package models

type Metadata struct {
	Name      string `yaml:"name"`
	Namespace string `yaml:"namespace"`
}

type Helm struct {
	Values     string   `yaml:"values,omitempty"`
	ValueFiles []string `yaml:"valueFiles,omitempty"`
	Path       string   `yaml:"path,omitempty"`
}

type Source struct {
	RepoURL        string `yaml:"repoURL"`
	Chart          string `yaml:"chart,omitempty"`
	TargetRevision string `yaml:"targetRevision"`
	Helm           Helm   `yaml:"helm"`
}

type Spec struct {
	Source Source `yaml:"source"`
}

type Application struct {
	Kind     string   `yaml:"kind"`
	Metadata Metadata `yaml:"metadata"`
	Spec     Spec     `yaml:"spec"`
}
