package main

import (
	"bufio"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"time"

	"golang.org/x/term"

	"github.com/Aashish1107/AshAssistant/server/internal/auth"
	"github.com/Aashish1107/AshAssistant/server/internal/config"
	"github.com/Aashish1107/AshAssistant/server/internal/db"
	"github.com/Aashish1107/AshAssistant/server/internal/storage"
)

const userUsage = "usage: ashcloud user create [--admin] [--config FILE] NAME"

var usernamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,31}$`)

func runUser(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	if len(args) == 0 || args[0] != "create" {
		return errors.New(userUsage)
	}
	flags := newFlagSet("user create", stderr)
	admin := flags.Bool("admin", false, "give the user full control (your own account should be an admin)")
	configPath := flags.String("config", config.DefaultPath(), "settings file")
	positional, err := parseInterspersed(flags, args[1:])
	if err != nil {
		return err
	}
	if len(positional) != 1 {
		return errors.New(userUsage)
	}
	username := strings.ToLower(positional[0])
	if !usernamePattern.MatchString(username) {
		return fmt.Errorf("username %q: use 1-32 letters, digits, dots, dashes or underscores, starting with a letter or digit", positional[0])
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
	role := "user"
	if *admin {
		role = "admin"
	}
	id, dir, err := createUser(ctx, database, cfg.StorageDir, username, auth.HashPassword(password), role)
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "Created %s %q (id %d).\nFiles folder: %s\n", role, username, id, dir)
	return nil
}

// createUser adds the user and their files folder together: if the folder
// can't be created, the user isn't either.
func createUser(ctx context.Context, database *sql.DB, storageDir, username, passwordHash, role string) (int64, string, error) {
	tx, err := database.BeginTx(ctx, nil)
	if err != nil {
		return 0, "", err
	}
	defer tx.Rollback()

	id, err := db.New(tx).CreateUser(ctx, db.CreateUserParams{
		Username:     username,
		PasswordHash: passwordHash,
		Role:         role,
		CreatedAt:    time.Now().Unix(),
	})
	if db.IsUniqueViolation(err) {
		return 0, "", fmt.Errorf("a user named %q already exists", username)
	}
	if err != nil {
		return 0, "", err
	}
	dir, err := storage.CreateUserDir(storageDir, id)
	if err != nil {
		return 0, "", fmt.Errorf("create files folder: %w", err)
	}
	return id, dir, tx.Commit()
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
