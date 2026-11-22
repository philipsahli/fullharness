package app

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"

	"github.com/philipsahli/fullharness/internal/agent"
)

const configFileName = "config.toml"

// config is the user's config.toml. Later tickets add Projects, Agent
// definitions and the scan folder.
type config struct {
	Port *int `toml:"port"`
	// ScanFolder is where Add Project looks for repos to suggest, for
	// example "~/work".
	ScanFolder string `toml:"scan_folder"`
	// Agents overrides or adds Agent definitions ([agents.<id>]).
	Agents map[string]agent.Override `toml:"agents"`
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
	if rest, ok := strings.CutPrefix(c.ScanFolder, "~/"); ok {
		home, err := os.UserHomeDir()
		if err != nil {
			return config{}, err
		}
		c.ScanFolder = filepath.Join(home, rest)
	}
	return c, nil
}
