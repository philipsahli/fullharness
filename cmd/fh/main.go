// Command fh starts the fullharness server on 127.0.0.1 and opens its UI in
// the browser. If fh already runs, it opens a new tab on that instance.
package main

import (
	"context"
	"flag"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/philipsahli/fullharness/internal/app"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "fh:", err)
		os.Exit(1)
	}
}

func run() error {
	port := flag.Int("port", 0, fmt.Sprintf("port to listen on (default: config.toml's port, else %d)", app.DefaultPort))
	noOpen := flag.Bool("no-open", false, "print the URL instead of opening the browser")
	uiURL := flag.String("ui-url", "", "open the UI from this origin instead (make dev uses the Vite dev server)")
	dir := flag.String("dir", "", "config and state folder (default ~/.config/fullharness)")
	flag.Parse()

	if *dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return err
		}
		*dir = filepath.Join(home, ".config", "fullharness")
	}
	opts := app.Options{
		Dir: *dir,
		OpenBrowser: func(u string) {
			if *uiURL != "" {
				u = withOrigin(u, *uiURL)
			}
			if *noOpen {
				fmt.Println(u)
				return
			}
			if err := exec.Command("open", u).Run(); err != nil {
				fmt.Fprintf(os.Stderr, "fh: could not open the browser (%v); open this URL:\n%s\n", err, u)
			}
		},
	}
	flag.Visit(func(f *flag.Flag) {
		if f.Name == "port" {
			opts.Port = port
		}
	})

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	inst, err := app.Start(ctx, opts)
	if err != nil {
		return err
	}
	go func() {
		<-ctx.Done()
		inst.Close()
	}()
	return inst.Wait()
}

// withOrigin moves u onto origin, keeping its path and query.
func withOrigin(u, origin string) string {
	pu, err := url.Parse(u)
	if err != nil {
		return u
	}
	po, err := url.Parse(origin)
	if err != nil {
		return u
	}
	pu.Scheme, pu.Host = po.Scheme, po.Host
	return pu.String()
}
