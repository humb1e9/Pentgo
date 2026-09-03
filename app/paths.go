package app

import (
	"os"
	"path/filepath"
)

func configDir() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "pentgo"), nil
}

// ConfigFile returns the user-level PentGo configuration path.
func ConfigFile() (string, error) {
	dir, err := configDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "config.json"), nil
}

// SkillsDir returns PentGo's fixed user-owned skill directory.
func SkillsDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "share", "pentgo", "skills"), nil
}
