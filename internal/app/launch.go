package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// launchFileName holds the running instance's port and token, so a second
// fh launch can open an authorised tab instead of starting another server.
// It contains the token, so it is written with mode 0600.
const launchFileName = "launch.json"

type launchInfo struct {
	PID   int    `json:"pid"`
	Port  int    `json:"port"`
	Token string `json:"token"`
}

func readLaunchFile(dir string) (launchInfo, bool) {
	var li launchInfo
	b, err := os.ReadFile(filepath.Join(dir, launchFileName))
	if err != nil {
		return li, false
	}
	if json.Unmarshal(b, &li) != nil || li.Port == 0 || li.Token == "" {
		return li, false
	}
	return li, true
}

// writeLaunchFile writes atomically, so a concurrent reader never sees a
// half-written file.
func writeLaunchFile(dir string, li launchInfo) error {
	b, err := json.Marshal(li)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, launchFileName+".*")
	if err != nil {
		return err
	}
	// After a successful Rename the temp name no longer exists, so this only
	// cleans up after a failure.
	defer func() { _ = os.Remove(tmp.Name()) }()
	// CreateTemp already uses 0600; Chmod guards against a permissive umask
	// policy on other platforms.
	err = tmp.Chmod(0o600)
	if err == nil {
		_, err = tmp.Write(b)
	}
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(tmp.Name(), filepath.Join(dir, launchFileName))
}

// removeLaunchFile removes the launch file only if it still describes this
// instance, so closing a stale instance never deletes a newer one's file.
func removeLaunchFile(dir, token string) error {
	li, ok := readLaunchFile(dir)
	if !ok || li.Token != token {
		return nil
	}
	err := os.Remove(filepath.Join(dir, launchFileName))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}

// running reports whether the instance in li still answers with its token.
// A launch file left behind by a crashed fh fails this check.
func running(li launchInfo) bool {
	c := http.Client{Timeout: 2 * time.Second}
	resp, err := c.Get(fmt.Sprintf("http://127.0.0.1:%d/api/state?token=%s", li.Port, li.Token))
	if err != nil {
		return false
	}
	_ = resp.Body.Close() // only the status matters
	return resp.StatusCode == http.StatusOK
}

func authURL(port int, token string) string {
	return fmt.Sprintf("http://127.0.0.1:%d/auth?token=%s", port, token)
}
