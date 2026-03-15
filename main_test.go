package main

import (
	"bytes"
	"encoding/binary"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestRootCommandMultiKillsOnlyDefaultValue(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	var gotOptions parserOptions
	var gotArgs []string

	command := newRootCommand(&stdout, &stderr,
		func(_ io.Writer, _ io.Writer, options parserOptions, args []string) error {
			gotOptions = options
			gotArgs = append([]string(nil), args...)
			return nil
		})
	command.SetArgs(normalizeOptionalIntFlags([]string{"--multikills-only", "demo.dm_84"}))

	if err := command.Execute(); err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	if gotOptions.multiKillMin != 2 {
		t.Fatalf("expected default multikill minimum 2, got %d", gotOptions.multiKillMin)
	}
	if !reflect.DeepEqual(gotArgs, []string{"demo.dm_84"}) {
		t.Fatalf("unexpected args: %#v", gotArgs)
	}
}

func TestExecuteCLIShowsHelpOnInvalidOptions(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	err := executeCLI(&stdout, &stderr, []string{"--definitely-not-a-flag"},
		func(_ io.Writer, _ io.Writer, _ parserOptions, _ []string) error {
			t.Fatal("run callback should not be called")
			return nil
		})
	if err == nil {
		t.Fatal("expected invalid CLI options to return an error")
	}
	if stdout.Len() != 0 {
		t.Fatalf("expected no stdout output, got %q", stdout.String())
	}
	if got := stderr.String(); !strings.Contains(got, "unknown flag") || !strings.Contains(got, "Usage:") {
		t.Fatalf("expected stderr to include the error and help text, got %q", got)
	}
}

func TestRootCommandMultiKillsOnlyExplicitValue(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	var gotOptions parserOptions

	command := newRootCommand(&stdout, &stderr,
		func(_ io.Writer, _ io.Writer, options parserOptions, _ []string) error {
			gotOptions = options
			return nil
		})
	command.SetArgs(normalizeOptionalIntFlags([]string{"--multikills-only", "3", "demo.dm_84"}))

	if err := command.Execute(); err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	if gotOptions.multiKillMin != 3 {
		t.Fatalf("expected explicit multikill minimum 3, got %d", gotOptions.multiKillMin)
	}
}

func TestRootCommandMultiKillWindowDefaultValue(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	var gotOptions parserOptions

	command := newRootCommand(&stdout, &stderr,
		func(_ io.Writer, _ io.Writer, options parserOptions, _ []string) error {
			gotOptions = options
			return nil
		})
	command.SetArgs(normalizeOptionalIntFlags([]string{"demo.dm_84"}))

	if err := command.Execute(); err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	if gotOptions.multiKillWindow != 3 {
		t.Fatalf("expected default multikill window 3, got %d", gotOptions.multiKillWindow)
	}
}

func TestRootCommandMultiKillWindowExplicitValue(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	var gotOptions parserOptions

	command := newRootCommand(&stdout, &stderr,
		func(_ io.Writer, _ io.Writer, options parserOptions, _ []string) error {
			gotOptions = options
			return nil
		})
	command.SetArgs(normalizeOptionalIntFlags([]string{"--multikill-window", "5", "demo.dm_84"}))

	if err := command.Execute(); err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	if gotOptions.multiKillWindow != 5 {
		t.Fatalf("expected explicit multikill window 5, got %d", gotOptions.multiKillWindow)
	}
}

func TestRootCommandRejectsTooSmallMultiKillMinimum(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	command := newRootCommand(&stdout, &stderr,
		func(_ io.Writer, _ io.Writer, _ parserOptions, _ []string) error {
			t.Fatal("run callback should not be called")
			return nil
		})
	command.SetArgs(normalizeOptionalIntFlags([]string{"--multikills-only=1", "demo.dm_84"}))

	if err := command.Execute(); err == nil {
		t.Fatal("expected validation error for --multikills-only=1")
	}
}

func TestRootCommandMultiKillHeadshotsOnlyDefaultValue(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	var gotOptions parserOptions

	command := newRootCommand(&stdout, &stderr,
		func(_ io.Writer, _ io.Writer, options parserOptions, _ []string) error {
			gotOptions = options
			return nil
		})
	command.SetArgs(normalizeOptionalIntFlags([]string{"--multikill-headshots-only", "demo.dm_84"}))

	if err := command.Execute(); err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	if gotOptions.multiKillHeadshotMin != 2 {
		t.Fatalf("expected default headshot multikill minimum 2, got %d", gotOptions.multiKillHeadshotMin)
	}
}

func TestRootCommandRejectsConflictingMultiKillModes(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	command := newRootCommand(&stdout, &stderr,
		func(_ io.Writer, _ io.Writer, _ parserOptions, _ []string) error {
			t.Fatal("run callback should not be called")
			return nil
		})
	command.SetArgs(normalizeOptionalIntFlags([]string{"--multikills-only", "--multikill-headshots-only", "demo.dm_84"}))

	if err := command.Execute(); err == nil {
		t.Fatal("expected validation error for conflicting multikill modes")
	}
}

func TestRootCommandKillsOnlyFrom(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	var gotOptions parserOptions

	command := newRootCommand(&stdout, &stderr,
		func(_ io.Writer, _ io.Writer, options parserOptions, _ []string) error {
			gotOptions = options
			return nil
		})
	command.SetArgs(normalizeOptionalIntFlags([]string{"--kills-only-from", "^1Killer", "demo.dm_84"}))

	if err := command.Execute(); err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	if gotOptions.killsOnlyFrom != "Killer" {
		t.Fatalf("expected cleaned attacker filter, got %q", gotOptions.killsOnlyFrom)
	}
}

func TestDemoLogPath(t *testing.T) {
	got := demoLogPath("/tmp/bin", "./foo/demo.dm_84")
	want := filepath.Join("/tmp/bin", "log-demo.txt")

	if got != want {
		t.Fatalf("unexpected log path: got %q want %q", got, want)
	}
}

func TestRunParserWritesDemoLog(t *testing.T) {
	tempDir := t.TempDir()
	demoPath := filepath.Join(tempDir, "demo.dm_84")

	file, err := os.Create(demoPath)
	if err != nil {
		t.Fatalf("create demo file: %v", err)
	}
	if err := binary.Write(file, binary.LittleEndian, int32(0)); err != nil {
		t.Fatalf("write sequence: %v", err)
	}
	if err := binary.Write(file, binary.LittleEndian, int32(-1)); err != nil {
		t.Fatalf("write end marker: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close demo file: %v", err)
	}

	logPath := filepath.Join(tempDir, "log-demo.txt")
	if err := os.WriteFile(logPath, []byte("stale"), 0o644); err != nil {
		t.Fatalf("write stale log file: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer

	if err := runParserInOutputDir(&stdout, &stderr, parserOptions{}, []string{demoPath}, tempDir); err != nil {
		t.Fatalf("run parser: %v", err)
	}

	wantOutput := "--- START - " + demoPath + " ---\n" +
		"---  END  - " + demoPath + " ---\n"
	if stdout.String() != wantOutput {
		t.Fatalf("unexpected stdout: %q", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("unexpected stderr: %q", stderr.String())
	}

	logData, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read log file: %v", err)
	}
	if string(logData) != wantOutput {
		t.Fatalf("unexpected log file contents: %q", string(logData))
	}
}
