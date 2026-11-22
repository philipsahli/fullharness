// Package fileutil holds file helpers shared by fh's state files.
package fileutil

import (
	"os"
	"path/filepath"
)

// WriteAtomic writes data to path with mode 0600 through a temp file and a
// rename, so a reader never sees a half-written file. fh's state files hold
// tokens or the user's Project list, so only the user may read them.
func WriteAtomic(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".*")
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
		_, err = tmp.Write(data)
	}
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
