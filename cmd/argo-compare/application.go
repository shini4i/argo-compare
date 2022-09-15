package main

import (
	m "github.com/shini4i/argo-compare/internal/models"
	"gopkg.in/yaml.v3"
	"os"
)

type Application struct {
	File string
	App  m.Application
}

func (a *Application) parse() {
	app := m.Application{}

	yamlFile, err := os.ReadFile(a.File)
	if err != nil {
		panic(err)
	}

	err = yaml.Unmarshal(yamlFile, &app)
	if err != nil {
		panic(err)
	}

	a.App = app
}

func (a *Application) writeValuesYaml() {
	yamlFile, err := os.Create("tmp/values.yaml")
	if err != nil {
		panic(err)
	}

	_, err = yamlFile.WriteString(a.App.Spec.Source.Helm.Values)
	if err != nil {
		panic(err)
	}
}
