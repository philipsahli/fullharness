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
	"github.com/philipsahli/fullharness/internal/classify"
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
	// Classifier sets the turn-end classifier's Provider (ADR-0010).
	Classifier classifierConfig `toml:"classifier"`
}

// classifierConfig is [classifier]. Command runs with the Agent's last
// message on stdin and prints what `claude -p --output-format json` prints;
// unset, it is classify.DefaultCommand. Name is what the replay set records
// it as, so measurements of two Providers stay apart.
type classifierConfig struct {
	Name    string   `toml:"name"`
	Command []string `toml:"command"`
}

// provider returns the configured Provider.
func (c classifierConfig) provider() classify.Provider {
	if len(c.Command) == 0 {
		return classify.NewClaude()
	}
	name := c.Name
	if name == "" {
		name = strings.Join(c.Command, " ")
	}
	return classify.NewCommand(name, c.Command)
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
