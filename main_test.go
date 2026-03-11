package main

import (
	"bytes"
	"io"
	"reflect"
	"testing"
)

func TestRootCommandMultiKillsOnlyDefaultValue(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	var gotOptions parserOptions
	var gotArgs []string

	command := newRootCommand(&stdout, &stderr,
		func(_ io.Writer, options parserOptions, args []string) error {
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

func TestRootCommandMultiKillsOnlyExplicitValue(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	var gotOptions parserOptions

	command := newRootCommand(&stdout, &stderr,
		func(_ io.Writer, options parserOptions, _ []string) error {
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

func TestRootCommandRejectsTooSmallMultiKillMinimum(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	command := newRootCommand(&stdout, &stderr,
		func(_ io.Writer, _ parserOptions, _ []string) error {
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
		func(_ io.Writer, options parserOptions, _ []string) error {
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
		func(_ io.Writer, _ parserOptions, _ []string) error {
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
		func(_ io.Writer, options parserOptions, _ []string) error {
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
