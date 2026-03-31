package config

import (
	"encoding/json"
	"os"
	"path/filepath"
)

const appDirName = "sermon-uploader"

// Config holds all persisted settings and credentials.
type Config struct {
	// YouTube
	YouTubeClientID     string `json:"youtube_client_id"`
	YouTubeClientSecret string `json:"youtube_client_secret"`
	YouTubePrivacy      string `json:"youtube_privacy"` // "unlisted" | "public" | "private"

	// Facebook
	FacebookPageID      string `json:"facebook_page_id"`
	FacebookAccessToken string `json:"facebook_access_token"`

	// Spotify
	SpotifyEmail    string `json:"spotify_email"`
	SpotifyPassword string `json:"spotify_password"`
	SpotifyShowURL  string `json:"spotify_show_url"` // e.g. https://podcasters.spotify.com/pod/show/your-show

	// Output
	OutputDir string `json:"output_dir"` // where normalized files are placed; defaults to system temp
}

// Defaults returns a Config with sensible defaults.
func Defaults() Config {
	return Config{
		YouTubePrivacy: "unlisted",
		OutputDir:      os.TempDir(),
	}
}

// appConfigDir returns (and creates if needed) the per-user config directory
// for this application.
func appConfigDir() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(base, appDirName)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	return dir, nil
}

// configFilePath returns the path to config.json.
func configFilePath() (string, error) {
	dir, err := appConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "config.json"), nil
}

// TokenFilePath returns the path where the YouTube OAuth token is stored.
func TokenFilePath() (string, error) {
	dir, err := appConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "youtube_token.json"), nil
}

// Load reads config.json from disk. If the file does not exist, Defaults() is
// returned without error.
func Load() (Config, error) {
	path, err := configFilePath()
	if err != nil {
		return Defaults(), err
	}

	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return Defaults(), nil
	}
	if err != nil {
		return Defaults(), err
	}

	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return Defaults(), err
	}

	// Fill in zero-value defaults
	if cfg.YouTubePrivacy == "" {
		cfg.YouTubePrivacy = "unlisted"
	}
	if cfg.OutputDir == "" {
		cfg.OutputDir = os.TempDir()
	}

	return cfg, nil
}

// Save writes cfg to disk as config.json.
func Save(cfg Config) error {
	path, err := configFilePath()
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0600)
}
