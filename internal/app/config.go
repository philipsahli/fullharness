package app

import (
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

const configFileName = "config.toml"

// config is the user's config.toml. Later tickets add Projects, Agent
// definitions and the scan folder.
type config struct {
	Port *int `toml:"port"`
}

// loadConfig reads config.toml from dir. A missing file is an empty config.
func loadConfig(dir string) (config, error) {
	var c config
	path := filepath.Join(dir, configFileName)
	if _, err := toml.DecodeFile(path, &c); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return config{}, nil
		}
		return config{}, fmt.Errorf("read %s: %w", path, err)
	}
	return c, nil
}
