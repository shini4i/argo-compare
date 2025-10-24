package comment

// Poster can publish a formatted diff comment to an upstream system.
type Poster interface {
	Post(body string) error
}
