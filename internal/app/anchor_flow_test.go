package app

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNormalizeRepoIdentity(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"https with .git", "https://host.example.com/group/repo.git", "host.example.com/group/repo"},
		{"https without .git", "https://host.example.com/group/repo", "host.example.com/group/repo"},
		{"ssh standard", "ssh://git@host.example.com/group/repo.git", "host.example.com/group/repo"},
		{"ssh non-standard port", "ssh://git@host.example.com:1022/group/repo.git", "host.example.com:1022/group/repo"},
		{"scp style", "git@host.example.com:group/repo.git", "host.example.com/group/repo"},
		{"scp style no .git", "git@host.example.com:group/repo", "host.example.com/group/repo"},
		{"trailing slash", "https://host.example.com/group/repo/", "host.example.com/group/repo"},
		{"oci prefix", "oci://host.example.com/group/repo", "host.example.com/group/repo"},
		{"lowercase host", "HTTPS://Host.Example.Com/group/repo.git", "host.example.com/group/repo"},
		{"local path", "/tmp/foo.git", "/tmp/foo"},
		{"empty", "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assert.Equal(t, c.want, normalizeRepoIdentity(c.in))
		})
	}
}

func TestRepoIdentityMatches(t *testing.T) {
	cases := []struct {
		name      string
		a, b      string
		wantMatch bool
	}{
		{"https vs ssh same repo", "https://host.example.com/group/repo.git", "ssh://git@host.example.com/group/repo.git", true},
		{"scp vs https same repo", "git@host.example.com:group/repo.git", "https://host.example.com/group/repo.git", true},
		{"different repo", "https://host.example.com/group/repo.git", "https://host.example.com/group/other.git", false},
		{"different host", "https://a.example.com/group/repo.git", "https://b.example.com/group/repo.git", false},
		{"one empty", "", "https://host.example.com/group/repo.git", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assert.Equal(t, c.wantMatch, repoIdentityMatches(c.a, c.b))
		})
	}
}
