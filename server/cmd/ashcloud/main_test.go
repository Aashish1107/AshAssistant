package main

import (
	"bytes"
	"net"
	"strings"
	"testing"
)

func TestRunVersion(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if err := run([]string{"version"}, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(stdout.String()); got != version {
		t.Errorf("version output = %q, want %q", got, version)
	}
}

func TestRunHelp(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if err := run([]string{"help"}, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "ashcloud serve") {
		t.Errorf("help output missing usage:\n%s", stdout.String())
	}
}

func TestRunRejectsMissingAndUnknownCommands(t *testing.T) {
	for _, args := range [][]string{nil, {"bogus"}} {
		var stdout, stderr bytes.Buffer
		if err := run(args, &stdout, &stderr); err == nil {
			t.Errorf("run(%q) returned nil, want an error", args)
		}
		if !strings.Contains(stderr.String(), "Usage:") {
			t.Errorf("run(%q) didn't print usage", args)
		}
	}
}

func TestServeHelpFlagIsNotAnError(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if err := run([]string{"serve", "-h"}, &stdout, &stderr); err != nil {
		t.Fatalf("serve -h returned %v, want nil", err)
	}
	if !strings.Contains(stderr.String(), "-listen") {
		t.Errorf("serve -h didn't describe the --listen flag:\n%s", stderr.String())
	}
}

func TestServeReportsBusyPort(t *testing.T) {
	busy, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer busy.Close()
	addr := busy.Addr().String()

	var stdout, stderr bytes.Buffer
	err = run([]string{"serve", "--listen", addr}, &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "can't listen on "+addr) {
		t.Fatalf("got %v, want a can't-listen error", err)
	}
}
