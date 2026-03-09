package models

// RepoCredentials stores authentication details for Helm repositories.
type RepoCredentials struct {
	Url      string `json:"url"`
	Username string `json:"username"`
	Password string `json:"password"` // #nosec G117 -- credential field for Helm repo auth, populated from env config
}
