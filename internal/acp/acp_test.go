package acp

import (
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
)

func TestStartNamesAMissingFolderNotTheCommand(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "gone")

	_, err := Start([]string{"true"}, nil, dir, io.Discard, slog.New(slog.DiscardHandler), nil)

	if err == nil || !strings.Contains(err.Error(), dir) || strings.Contains(err.Error(), "start true") {
		t.Fatalf("Start in a missing folder: %v, want an error naming %s", err, dir)
	}
}
