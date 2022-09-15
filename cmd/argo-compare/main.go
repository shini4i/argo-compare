package main

import "fmt"

func main() {
	app := Application{File: "tmp/example.yaml"}
	app.parse()
	fmt.Println(app.App.Spec.Source.Helm.Values)
	app.writeValuesYaml()
}
