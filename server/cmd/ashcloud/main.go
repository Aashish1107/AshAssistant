// Command ashcloud runs Ash Cloud: a private cloud server on this laptop,
// used through a web browser and reached from the phone over Tailscale.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/Aashish1107/AshAssistant/server/internal/config"
)

// version is set at build time: go build -ldflags "-X main.version=v0.1.0"
var version = "dev"

func usage() string {
	return fmt.Sprintf(`Ash Cloud - your laptop as a private cloud.

Usage:
  ashcloud init [--data-dir DIR]        create the settings file, database and files folder
                                        (DIR defaults to %s)
  ashcloud user create [--admin] NAME   add a user; asks for their password
  ashcloud serve [--listen ADDR]        start the server (Ctrl+C to stop)
  ashcloud version                      print the version
  ashcloud help                         show this help

"user" and "serve" read %s
unless you pass --config FILE.
`, config.DefaultDataDir(), config.DefaultPath())
}

func main() {
	if err := run(context.Background(), os.Args[1:], os.Stdin, os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "ashcloud:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage())
		return errors.New("missing command")
	}
	var err error
	switch args[0] {
	case "init":
		err = runInit(ctx, args[1:], stdout, stderr)
	case "user":
		err = runUser(ctx, args[1:], stdin, stdout, stderr)
	case "serve":
		err = runServe(ctx, args[1:], stderr)
	case "version":
		fmt.Fprintln(stdout, version)
	case "help", "-h", "--help":
		fmt.Fprint(stdout, usage())
	default:
		fmt.Fprint(stderr, usage())
		err = fmt.Errorf("unknown command %q", args[0])
	}
	if errors.Is(err, flag.ErrHelp) {
		return nil // the flag package already printed the command's help
	}
	return err
}

func newFlagSet(name string, stderr io.Writer) *flag.FlagSet {
	flags := flag.NewFlagSet("ashcloud "+name, flag.ContinueOnError)
	flags.SetOutput(stderr)
	return flags
}

// parseInterspersed parses flags that may come before or after the other
// arguments ("user create --admin aashish" and "user create aashish --admin"),
// and returns the non-flag arguments.
func parseInterspersed(flags *flag.FlagSet, args []string) ([]string, error) {
	var positional []string
	for {
		if err := flags.Parse(args); err != nil {
			return nil, err
		}
		args = flags.Args()
		if len(args) == 0 {
			return positional, nil
		}
		positional = append(positional, args[0])
		args = args[1:]
	}
}
