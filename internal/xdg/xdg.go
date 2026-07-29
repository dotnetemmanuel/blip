// Package xdg resolves blip's config and cache directories.
package xdg

import (
	"os"
	"path/filepath"
)

const appName = "blip"

// ConfigDir is $XDG_CONFIG_HOME/blip, falling back to ~/.config/blip.
func ConfigDir() (string, error) {
	return dir("XDG_CONFIG_HOME", ".config")
}

// CacheDir is $XDG_CACHE_HOME/blip, falling back to ~/.cache/blip.
func CacheDir() (string, error) {
	return dir("XDG_CACHE_HOME", ".cache")
}

// CredentialsPath is where blip expects credentials.toml.
func CredentialsPath() (string, error) {
	d, err := ConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(d, "credentials.toml"), nil
}

// SpecCacheDir is where the cached spec for a named API lives.
func SpecCacheDir(apiName string) (string, error) {
	d, err := CacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(d, "specs", apiName), nil
}

// TokenCachePath is where an oauth2_cc token for a profile is cached.
func TokenCachePath(profile string) (string, error) {
	d, err := CacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(d, "tokens", profile+".json"), nil
}

func dir(envVar, fallback string) (string, error) {
	if base := os.Getenv(envVar); base != "" {
		return filepath.Join(base, appName), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, fallback, appName), nil
}
