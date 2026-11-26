package config_test

import (
	"strings"
	"testing"

	"github.com/smarniw/metamorphosis/internal/config"
)

func TestJWTSecretMustBeLongEnough(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://x@localhost/x")
	for _, c := range []struct {
		secret string
		ok     bool
	}{
		{"", false},
		{"replace-me", false},
		{strings.Repeat("a", config.MinJWTSecretLen-1), false},
		{strings.Repeat("a", config.MinJWTSecretLen), true},
		{strings.Repeat("0123456789abcdef", 4), true}, // what `openssl rand -hex 32` makes
	} {
		t.Setenv("JWT_SECRET", c.secret)
		_, err := config.FromEnv()
		if (err == nil) != c.ok {
			t.Errorf("%d characters: ok=%v, err=%v", len(c.secret), c.ok, err)
		}
	}
}
