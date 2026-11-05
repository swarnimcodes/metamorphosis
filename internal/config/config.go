// Package config reads the same environment variables the Rust API did, so /etc/metamorph/env works unchanged.
package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
)

type Config struct {
	DatabaseURL string
	JWTSecret   string
	// AccessTokenTTLSecs is how long an access token lives.
	AccessTokenTTLSecs int64
	// DevLogin mounts POST /auth/dev-login. Must stay off in production.
	DevLogin bool
	BindAddr string
	// GoogleClientIDs are the OAuth client ids whose ID tokens we accept. Empty disables Google sign-in.
	GoogleClientIDs []string
	// CORSOrigins are the origins allowed to call the API from a browser.
	CORSOrigins   []string
	RunMigrations bool
}

func list(name string) []string {
	var out []string
	for _, part := range strings.Split(os.Getenv(name), ",") {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}

func flag(name string) bool {
	v := os.Getenv(name)
	return v == "true" || v == "1"
}

func FromEnv() (*Config, error) {
	c := &Config{
		DatabaseURL:        os.Getenv("DATABASE_URL"),
		JWTSecret:          os.Getenv("JWT_SECRET"),
		AccessTokenTTLSecs: 3600,
		DevLogin:           flag("DEV_LOGIN"),
		BindAddr:           "127.0.0.1:3000",
		GoogleClientIDs:    list("GOOGLE_CLIENT_IDS"),
		CORSOrigins:        list("CORS_ORIGINS"),
		RunMigrations:      flag("RUN_MIGRATIONS"),
	}
	if c.JWTSecret == "" {
		return nil, errors.New("JWT_SECRET must be set")
	}
	if c.DatabaseURL == "" {
		return nil, errors.New("DATABASE_URL must be set")
	}
	if v := os.Getenv("ACCESS_TOKEN_TTL_SECS"); v != "" {
		ttl, err := strconv.ParseInt(v, 10, 64)
		if err != nil || ttl <= 0 {
			return nil, fmt.Errorf("ACCESS_TOKEN_TTL_SECS must be a positive integer, got %q", v)
		}
		c.AccessTokenTTLSecs = ttl
	}
	if v := os.Getenv("BIND_ADDR"); v != "" {
		c.BindAddr = v
	}
	return c, nil
}
