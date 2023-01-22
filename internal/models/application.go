package models

type Application struct {
	Kind     string `yaml:"kind"`
	Metadata struct {
		Name      string `yaml:"name"`
		Namespace string `yaml:"namespace"`
	} `yaml:"metadata"`
	Spec struct {
		Source struct {
			RepoURL        string `yaml:"repoURL"`
			Chart          string `yaml:"chart,omitempty"`
			TargetRevision string `yaml:"targetRevision"`
			Path           string `yaml:"path,omitempty"`
			Helm           struct {
				ReleaseName string   `yaml:"releaseName,omitempty"`
				Values      string   `yaml:"values,omitempty"`
				ValueFiles  []string `yaml:"valueFiles,omitempty"`
			} `yaml:"helm"`
		} `yaml:"source"`
	} `yaml:"spec"`
}
