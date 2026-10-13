// Command ashcloud runs Ash Cloud: a private cloud server on this laptop,
// used through a web browser and reached from the phone over Tailscale.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"syscall"

	"github.com/Aashish1107/AshAssistant/server/internal/httpapi"
)

const usage = `Ash Cloud - your laptop as a private cloud.

Usage:
  ashcloud serve [--listen 127.0.0.1:8080]   start the server (Ctrl+C to stop)
  ashcloud version                           print the version
  ashcloud help                              show this help
`

// version is set at build time: go build -ldflags "-X main.version=v0.1.0"
var version = "dev"

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "ashcloud:", err)
		os.Exit(1)
	}
}

func run(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return errors.New("missing command")
	}
	switch args[0] {
	case "serve":
		return serve(args[1:], stderr)
	case "version":
		fmt.Fprintln(stdout, version)
		return nil
	case "help", "-h", "--help":
		fmt.Fprint(stdout, usage)
		return nil
	default:
		fmt.Fprint(stderr, usage)
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func serve(args []string, stderr io.Writer) error {
	flags := flag.NewFlagSet("serve", flag.ContinueOnError)
	flags.SetOutput(stderr)
	listen := flags.String("listen", "127.0.0.1:8080",
		"address to listen on; keep it on 127.0.0.1 so only Tailscale can reach the server")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}

	logger := slog.New(slog.NewTextHandler(stderr, nil))

	ln, err := net.Listen("tcp", *listen)
	if err != nil {
		return fmt.Errorf("can't listen on %s (is something else using that port?): %w", *listen, err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	logger.Info("Ash Cloud started", "url", "http://"+ln.Addr().String(), "version", version)
	err = httpapi.Serve(ctx, ln, httpapi.NewRouter(logger), logger)
	logger.Info("Ash Cloud stopped")
	return err
}
