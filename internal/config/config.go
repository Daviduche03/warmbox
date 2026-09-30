// Package config stores where warmbox keeps its cloud credentials: one global
// remote (S3/R2) that volume chunks are uploaded to. It can be written by
// `warmbox cloud` or from the dashboard's settings page.
package config

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/rclone/rclone/fs"
	"github.com/rclone/rclone/fs/config/configmap"
)

type RemoteConfig struct {
	Provider      string `json:"provider,omitempty"`
	Endpoint      string `json:"endpoint,omitempty"`
	Region        string `json:"region,omitempty"`
	AccessKey     string `json:"access_key,omitempty"`
	SecretKey     string `json:"secret_key,omitempty"`
	DefaultBucket string `json:"default_bucket,omitempty"`
	Token         string `json:"token,omitempty"`
	APIBase       string `json:"api_base,omitempty"`
	// EnvKey is the hex-encoded 32-byte key for client-side .env encryption.
	// It never leaves the device (only ciphertext is uploaded).
	EnvKey string `json:"env_key,omitempty"`
}

const (
	configDirName  = ".warmbox"
	configFileName = "config.json"
	// remoteName is the rclone remote name these settings are exposed as.
	remoteName = "warmbox"
	// legacyDirName is where runmesh kept the same file. Read as a fallback so
	// an existing setup keeps working after the rename.
	legacyDirName = ".runmesh"
)

// Dir is warmbox's config directory (~/.warmbox).
func Dir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, configDirName), nil
}

// GlobalPath is the cloud credentials file (~/.warmbox/config.json).
func GlobalPath() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, configFileName), nil
}

func legacyGlobalPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, legacyDirName, configFileName), nil
}

// LoadGlobal reads the cloud settings. It falls back to runmesh's old location
// if the new one doesn't exist yet, so a rename isn't a data loss.
func LoadGlobal() (*RemoteConfig, error) {
	p, err := GlobalPath()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(p)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("reading global config: %w", err)
		}
		legacy, lerr := legacyGlobalPath()
		if lerr != nil {
			return nil, nil
		}
		data, err = os.ReadFile(legacy)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return nil, nil
			}
			return nil, fmt.Errorf("reading global config: %w", err)
		}
	}
	var cfg RemoteConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing global config: %w", err)
	}
	return &cfg, nil
}

// SaveGlobal writes the cloud settings to ~/.warmbox/config.json.
func SaveGlobal(cfg *RemoteConfig) error {
	dir, err := Dir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("creating config dir: %w", err)
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding global config: %w", err)
	}
	p, err := GlobalPath()
	if err != nil {
		return err
	}
	return os.WriteFile(p, data, 0o600)
}

// Clear removes the stored cloud settings.
func Clear() error {
	p, err := GlobalPath()
	if err != nil {
		return err
	}
	if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// Configured reports whether a bucket is set, i.e. volumes can be cloud-backed.
func (r *RemoteConfig) Configured() bool {
	return r != nil && r.DefaultBucket != ""
}

// NewFs opens the remote at bucket/prefix. Everything is stored under a single
// bucket, so volumes across machines share chunks.
func (r *RemoteConfig) NewFs(ctx context.Context, bucket, prefix string) (fs.Fs, error) {
	ri, err := fs.Find("s3")
	if err != nil {
		return nil, fmt.Errorf("finding s3 backend: %w", err)
	}

	userCfg := configmap.Simple{
		"provider":          r.Provider,
		"access_key_id":     r.AccessKey,
		"secret_access_key": r.SecretKey,
		"region":            r.Region,
		"endpoint":          r.Endpoint,
		"env_auth":          "false",
		"no_check_bucket":   "true",
		"force_path_style":  "true",
	}

	cfg := fs.ConfigMap(ri.Prefix, ri.Options, remoteName, userCfg)
	return ri.NewFs(ctx, remoteName, bucket+"/"+prefix, cfg)
}
