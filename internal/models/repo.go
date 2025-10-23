package models

// RepoCredentials stores authentication details for Helm repositories.
type RepoCredentials struct {
	Url      string `json:"url"`
	Username string `json:"username"`
	Password string `json:"password"`
}
