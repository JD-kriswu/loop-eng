// Package config handles daemon configuration persistence.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

const (
	DeviceFile = "device_token"
	ServerFile = "server_url"
	ConfigDir  = ".loopany"
	PIDFile    = "daemon.pid"
)

// Config holds daemon configuration.
type Config struct {
	Token  string
	Server string
	Roots  []string // workdir jail
}

// Load reads persisted config from home directory and environment.
func Load() (*Config, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("get home dir: %w", err)
	}

	cfg := &Config{}

	dir := filepath.Join(home, ConfigDir)

	// Load device token (file first, then env override)
	if token, err := os.ReadFile(filepath.Join(dir, DeviceFile)); err == nil {
		cfg.Token = strings.TrimSpace(string(token))
	}
	if token := os.Getenv("LOOPANY_TOKEN"); token != "" {
		cfg.Token = strings.TrimSpace(token)
	}

	// Load server URL (file first, then env override)
	if server, err := os.ReadFile(filepath.Join(dir, ServerFile)); err == nil {
		cfg.Server = strings.TrimSpace(string(server))
	}
	if server := os.Getenv("LOOPANY_SERVER_URL"); server != "" {
		cfg.Server = strings.TrimSpace(server)
	}

	// Load roots from env (comma-separated)
	if roots := os.Getenv("LOOPANY_ROOTS"); roots != "" {
		cfg.Roots = splitAndTrim(roots, ",")
	}

	return cfg, nil
}

// Save persists config to home directory.
func (c *Config) Save() error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("get home dir: %w", err)
	}

	dir := filepath.Join(home, ConfigDir)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}

	if c.Token != "" {
		if err := os.WriteFile(filepath.Join(dir, DeviceFile), []byte(c.Token), 0600); err != nil {
			return fmt.Errorf("write token: %w", err)
		}
	}

	if c.Server != "" {
		if err := os.WriteFile(filepath.Join(dir, ServerFile), []byte(c.Server), 0600); err != nil {
			return fmt.Errorf("write server: %w", err)
		}
	}

	return nil
}

// LoadMachineInfo returns the machine identity.
func LoadMachineInfo(version string) map[string]string {
	host, _ := os.Hostname()
	return map[string]string{
		"host":     host,
		"platform": runtime.GOOS,
		"arch":     runtime.GOARCH,
		"version":  version,
	}
}

// WritePIDFile writes the daemon PID.
func WritePIDFile() error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}

	dir := filepath.Join(home, ConfigDir)
	os.MkdirAll(dir, 0755)

	pid := fmt.Sprintf("%d", os.Getpid())
	return os.WriteFile(filepath.Join(dir, PIDFile), []byte(pid), 0644)
}

// ReadPIDFile reads the daemon PID.
func ReadPIDFile() (int, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return 0, err
	}

	data, err := os.ReadFile(filepath.Join(home, ConfigDir, PIDFile))
	if err != nil {
		return 0, err
	}

	var pid int
	_, err = fmt.Sscanf(string(data), "%d", &pid)
	return pid, err
}

// ClearPIDFile removes the PID file.
func ClearPIDFile() error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	return os.Remove(filepath.Join(home, ConfigDir, PIDFile))
}

// LoadLoopState loads a loop's persisted state.
func LoadLoopState(loopID string) (any, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}

	path := filepath.Join(home, ConfigDir, "states", loopID+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var state any
	err = json.Unmarshal(data, &state)
	return state, err
}

// SaveLoopState persists a loop's state.
func SaveLoopState(loopID string, state any) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}

	dir := filepath.Join(home, ConfigDir, "states")
	os.MkdirAll(dir, 0755)

	data, err := json.Marshal(state)
	if err != nil {
		return err
	}

	path := filepath.Join(dir, loopID+".json")
	return os.WriteFile(path, data, 0644)
}

func splitAndTrim(s, sep string) []string {
	parts := strings.Split(s, sep)
	result := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			result = append(result, p)
		}
	}
	return result
}