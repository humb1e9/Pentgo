package bootstrap

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// ErrConfigCreated tells the entrypoint that an editable first-run template was written.
var ErrConfigCreated = errors.New("pentgo config created")

func Load() (Config, error) {
	path, err := ConfigFile()
	if err != nil {
		return Config{}, err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		config := Default()
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return Config{}, err
		}
		data, err := json.MarshalIndent(config, "", "  ")
		if err != nil {
			return Config{}, err
		}
		if err := os.WriteFile(path, append(data, '\n'), 0o600); err != nil {
			return Config{}, err
		}
		return config, fmt.Errorf("%w: edit %s and set model.model and model.api_key", ErrConfigCreated, path)
	}
	if err != nil {
		return Config{}, err
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return Config{}, fmt.Errorf("set config permissions: %w", err)
	}
	config := Default()
	if err := json.Unmarshal(data, &config); err != nil {
		return Config{}, fmt.Errorf("decode config: %w", err)
	}
	config.Tools = config.Tools.Effective()
	config.Project = config.Project.Effective()
	if err := config.Model.Validate(); err != nil {
		return Config{}, err
	}
	if err := config.Tools.Validate(); err != nil {
		return Config{}, err
	}
	if err := config.Project.Validate(); err != nil {
		return Config{}, err
	}
	return config, nil
}
