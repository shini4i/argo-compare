package app

// Shared chart-fixture paths used across the path-based rendering tests. These
// relative paths recur in many table-driven fixtures; naming them keeps the
// fixtures consistent and satisfies goconst.
const (
	fooChartYAML  = "charts/foo/Chart.yaml"
	fooValuesYAML = "charts/foo/values.yaml"
)
