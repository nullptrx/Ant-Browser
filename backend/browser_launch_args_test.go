package backend

import (
	"reflect"
	"testing"
)

func TestSanitizeManagedLaunchArgsDropsCommandExecutionSwitches(t *testing.T) {
	sanitized, removed := sanitizeManagedLaunchArgs([]string{
		"--no-first-run",
		"--renderer-cmd-prefix=calc.exe",
		"-utility-cmd-prefix=/bin/sh -c id",
		"/GPU-LAUNCHER=calc.exe",
		"--Browser-Subprocess-Path=/tmp/evil",
		"--remote-allow-origins=*",
		"--lang=en-US",
	})

	wantSanitized := []string{"--no-first-run", "--lang=en-US"}
	if !reflect.DeepEqual(sanitized, wantSanitized) {
		t.Fatalf("sanitized = %v, want %v", sanitized, wantSanitized)
	}
	wantRemoved := []string{"--renderer-cmd-prefix", "--utility-cmd-prefix", "--gpu-launcher", "--browser-subprocess-path", "--remote-allow-origins"}
	if !reflect.DeepEqual(removed, wantRemoved) {
		t.Fatalf("removed = %v, want %v", removed, wantRemoved)
	}
}

func TestSanitizeManagedLaunchArgsKeepsStartURLs(t *testing.T) {
	sanitized, removed := sanitizeManagedLaunchArgs([]string{"https://example.com/path", "/Users/me/page.html"})
	if len(removed) != 0 {
		t.Fatalf("removed = %v, want none", removed)
	}
	if len(sanitized) != 2 {
		t.Fatalf("sanitized = %v, want both targets kept", sanitized)
	}
}
