package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/term"

	"github.com/Aashish1107/AshAssistant/server/internal/auth"
	"github.com/Aashish1107/AshAssistant/server/internal/config"
	"github.com/Aashish1107/AshAssistant/server/internal/db"
	"github.com/Aashish1107/AshAssistant/server/internal/storage"
)

const userUsage = `usage: ashcloud user create [--admin] [--config FILE] NAME
       ashcloud user reset-password [--config FILE] NAME`

func runUser(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return errors.New(userUsage)
	}
	switch args[0] {
	case "create":
		return runUserCreate(ctx, args[1:], stdin, stdout, stderr)
	case "reset-password":
		return runUserResetPassword(ctx, args[1:], stdin, stdout, stderr)
	default:
		return errors.New(userUsage)
	}
}

func runUserCreate(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	flags := newFlagSet("user create", stderr)
	admin := flags.Bool("admin", false, "give the user full control (your own account should be an admin)")
	configPath := flags.String("config", config.DefaultPath(), "settings file")
	positional, err := parseInterspersed(flags, args)
	if err != nil {
		return err
	}
	if len(positional) != 1 {
		return errors.New(userUsage)
	}
	username, err := auth.NormalizeUsername(positional[0])
	if err != nil {
		return err
	}

	cfg, err := loadConfig(*configPath)
	if err != nil {
		return err
	}
	database, _, err := openDatabase(ctx, cfg)
	if err != nil {
		return err
	}
	defer database.Close()

	// Check before asking for a password, so nobody types one for nothing.
	taken, err := db.New(database).UsernameTaken(ctx, username)
	if err != nil {
		return err
	}
	if taken {
		return fmt.Errorf("a user named %q already exists", username)
	}

	password, err := readNewPassword(stdin, stdout)
	if err != nil {
		return err
	}
	role := auth.RoleUser
	if *admin {
		role = auth.RoleAdmin
	}
	user, err := auth.NewService(database, auth.Options{}).CreateUser(ctx, username, password, role, auth.Client{IP: "command line"})
	if errors.Is(err, auth.ErrUsernameTaken) {
		return fmt.Errorf("a user named %q already exists", username)
	}
	if err != nil {
		return err
	}
	dir, err := storage.CreateUserDir(cfg.StorageDir, user.ID)
	if err != nil {
		return fmt.Errorf("create files folder: %w", err)
	}
	fmt.Fprintf(stdout, "Created %s %q (id %s).\nFiles folder: %s\n", user.Role, user.Username, user.ID, dir)
	return nil
}

// runUserResetPassword sets a new password and logs the user out everywhere.
// It's run on the laptop itself, so it also works when you're locked out.
func runUserResetPassword(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	flags := newFlagSet("user reset-password", stderr)
	configPath := flags.String("config", config.DefaultPath(), "settings file")
	positional, err := parseInterspersed(flags, args)
	if err != nil {
		return err
	}
	if len(positional) != 1 {
		return errors.New(userUsage)
	}
	username := strings.ToLower(positional[0])

	cfg, err := loadConfig(*configPath)
	if err != nil {
		return err
	}
	database, _, err := openDatabase(ctx, cfg)
	if err != nil {
		return err
	}
	defer database.Close()

	// Check before asking for a password, so nobody types one for nothing.
	exists, err := db.New(database).UsernameTaken(ctx, username)
	if err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("there is no user named %q", username)
	}

	password, err := readNewPassword(stdin, stdout)
	if err != nil {
		return err
	}
	ended, err := auth.NewService(database, auth.Options{}).ResetPassword(ctx, username, password, auth.Client{IP: "command line"})
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "Password changed for %q. Logged out of %d device(s); log in again with the new password.\n", username, ended)
	return nil
}

// readNewPassword asks for a password twice and checks it follows the rules.
func readNewPassword(stdin io.Reader, stdout io.Writer) (string, error) {
	prompt := &passwordPrompt{in: stdin, out: stdout}
	fmt.Fprintf(stdout, "Choose a password (at least %d characters). What you type stays hidden.\n", auth.MinPasswordLength)
	first, err := prompt.read("Password: ")
	if err != nil {
		return "", err
	}
	if err := auth.CheckPasswordRules(first); err != nil {
		return "", err
	}
	second, err := prompt.read("Same password again: ")
	if err != nil {
		return "", err
	}
	if first != second {
		return "", errors.New("the two passwords didn't match - nothing was changed")
	}
	return first, nil
}

type passwordPrompt struct {
	in    io.Reader
	out   io.Writer
	lines *bufio.Reader
}

// read shows label and reads one password. In a terminal (PowerShell, cmd)
// typing is hidden; piped input (scripts, tests) is read one line at a time.
func (p *passwordPrompt) read(label string) (string, error) {
	fmt.Fprint(p.out, label)
	defer fmt.Fprintln(p.out)
	if file, ok := p.in.(*os.File); ok && term.IsTerminal(int(file.Fd())) {
		secret, err := term.ReadPassword(int(file.Fd()))
		return string(secret), err
	}
	if p.lines == nil {
		p.lines = bufio.NewReader(p.in)
	}
	line, err := p.lines.ReadString('\n')
	if err != nil && (err != io.EOF || line == "") {
		return "", errors.New("no password given")
	}
	return strings.TrimRight(line, "\r\n"), nil
}
