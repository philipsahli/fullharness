// Command devwatch is make dev's Go half: it builds fh, runs it with --dev,
// and rebuilds and restarts it whenever a Go source file changes. A failed
// build leaves the running fh alone. It polls instead of using a file-watch
// library, so the repo needs no extra tool for development.
package main

import (
	"flag"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const pollEvery = 500 * time.Millisecond

func main() {
	port := flag.Int("port", 4816, "port for the dev fh")
	dir := flag.String("dir", ".dev", "state folder for the dev fh")
	uiURL := flag.String("ui-url", "http://127.0.0.1:5173", "where the UI is served (Vite)")
	flag.Parse()

	bin := filepath.Join(*dir, "fh")
	args := []string{"--dev", "--port", strconv.Itoa(*port), "--dir", *dir, "--ui-url", *uiURL}

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	var running *exec.Cmd
	last := fingerprint()
	if build(bin) {
		running = start(bin, args) // the first start opens the browser
	}
	tick := time.NewTicker(pollEvery)
	defer tick.Stop()
	for {
		select {
		case <-stop:
			halt(running)
			return
		case <-tick.C:
			now := fingerprint()
			if now == last {
				continue
			}
			last = now
			began := time.Now()
			if !build(bin) {
				continue // keep serving the last good build
			}
			halt(running)
			running = start(bin, append(args, "--no-open"))
			logf("rebuilt in %.1fs, fh restarted", time.Since(began).Seconds())
		}
	}
}

// fingerprint sums up the paths, sizes and times of the files a rebuild
// depends on. Test files are left out: they don't change the binary.
func fingerprint() string {
	var b strings.Builder
	for _, root := range []string{"cmd", "internal", "web/embed.go", "go.mod", "go.sum"} {
		_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil // a file deleted mid-walk shows up on the next poll
			}
			if d.IsDir() || (!strings.HasSuffix(path, ".go") && !strings.HasPrefix(path, "go.")) || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			info, err := d.Info()
			if err != nil {
				return nil // as above
			}
			fmt.Fprintf(&b, "%s %d %d\n", path, info.Size(), info.ModTime().UnixNano())
			return nil
		})
	}
	return b.String()
}

func build(bin string) bool {
	out, err := exec.Command("go", "build", "-o", bin, "./cmd/fh").CombinedOutput()
	if err != nil {
		logf("build failed; the running fh keeps the last good build:\n%s", out)
		return false
	}
	return true
}

func start(bin string, args []string) *exec.Cmd {
	cmd := exec.Command(bin, args...)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Start(); err != nil {
		logf("start fh: %v", err)
		return nil
	}
	return cmd
}

// halt stops fh the way Ctrl+C does and waits, so the next start can bind
// the same port.
func halt(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	_ = cmd.Process.Signal(syscall.SIGTERM) // fails only if fh already exited
	done := make(chan struct{})
	go func() { _ = cmd.Wait(); close(done) }() // fh's exit status doesn't matter here
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		_ = cmd.Process.Kill() // fh ignored SIGTERM; free the port anyway
		<-done
	}
}

func logf(format string, a ...any) {
	fmt.Fprintf(os.Stderr, "[devwatch] "+format+"\n", a...)
}
