package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSplitMultikillCreatesParseableClip(t *testing.T) {
	tempDir := t.TempDir()
	demoPath := filepath.Join(tempDir, "demo.dm_84")
	writeSyntheticDemo(t, demoPath, 1, []syntheticSnapshot{
		{serverTime: 1000},
		{serverTime: 5000, entities: []entityState{makeObituaryEntity(200, 1, 2)}},
		{serverTime: 7000, entities: []entityState{makeObituaryEntity(201, 1, 3)}},
	})

	originalDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("get working directory: %v", err)
	}
	if err := os.Chdir(tempDir); err != nil {
		t.Fatalf("change working directory: %v", err)
	}
	defer func() {
		if err := os.Chdir(originalDir); err != nil {
			t.Fatalf("restore working directory: %v", err)
		}
	}()

	var stdout bytes.Buffer
	var stderr bytes.Buffer

	command := newRootCommand(&stdout, &stderr, runParser)
	command.SetArgs([]string{
		"split-multikill",
		"--before", "0",
		"--after", "0",
		demoPath,
	})

	if err := command.Execute(); err != nil {
		t.Fatalf("execute split-multikill: %v", err)
	}
	if stderr.Len() != 0 {
		t.Fatalf("unexpected stderr: %q", stderr.String())
	}

	clipPath := filepath.Join(tempDir, "demo_00_00_04_killer_2kills.dm_84")
	if _, err := os.Stat(clipPath); err != nil {
		t.Fatalf("expected clip %s: %v\nstdout: %s", clipPath, err, stdout.String())
	}
	if !strings.Contains(stdout.String(), clipPath) {
		t.Fatalf("expected stdout to mention %s, got %q", clipPath, stdout.String())
	}

	var clipOut bytes.Buffer
	parser := newParser(&clipOut, parserOptions{})
	if err := parser.parseFile(clipPath); err != nil {
		t.Fatalf("parse clip %s: %v", clipPath, err)
	}

	lines := strings.Split(strings.TrimSpace(clipOut.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 kill lines in clip, got %d: %q", len(lines), clipOut.String())
	}
	if lines[0] != expectedKillLine("00:04.00", false, "Killer", "MP40", "VictimA", "Enemy") {
		t.Fatalf("unexpected first clip line: %q", lines[0])
	}
	if lines[1] != expectedKillLine("00:06.00", false, "Killer", "MP40", "VictimB", "Enemy") {
		t.Fatalf("unexpected second clip line: %q", lines[1])
	}
}

func TestSplitMultikillsFromMeOnlyCreatesRecorderClips(t *testing.T) {
	tempDir := t.TempDir()
	demoPath := filepath.Join(tempDir, "demo.dm_84")
	writeSyntheticDemo(t, demoPath, 1, []syntheticSnapshot{
		{serverTime: 1000},
		{serverTime: 5000, entities: []entityState{makeObituaryEntity(200, 1, 2)}},
		{serverTime: 7000, entities: []entityState{makeObituaryEntity(201, 1, 3)}},
		{serverTime: 12000, entities: []entityState{makeObituaryEntity(202, 4, 5)}},
		{serverTime: 14000, entities: []entityState{makeObituaryEntity(203, 4, 6)}},
	})

	originalDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("get working directory: %v", err)
	}
	if err := os.Chdir(tempDir); err != nil {
		t.Fatalf("change working directory: %v", err)
	}
	defer func() {
		if err := os.Chdir(originalDir); err != nil {
			t.Fatalf("restore working directory: %v", err)
		}
	}()

	var stdout bytes.Buffer
	var stderr bytes.Buffer

	command := newRootCommand(&stdout, &stderr, runParser)
	command.SetArgs([]string{
		"split-multikills",
		"--from-me",
		"--before", "0",
		"--after", "0",
		demoPath,
	})

	if err := command.Execute(); err != nil {
		t.Fatalf("execute split-multikills --from-me: %v", err)
	}
	if stderr.Len() != 0 {
		t.Fatalf("unexpected stderr: %q", stderr.String())
	}

	recorderClip := filepath.Join(tempDir, "demo_00_00_04_killer_2kills.dm_84")
	if _, err := os.Stat(recorderClip); err != nil {
		t.Fatalf("expected recorder clip %s: %v\nstdout: %s", recorderClip, err, stdout.String())
	}

	otherClip := filepath.Join(tempDir, "demo_00_00_11_other_2kills.dm_84")
	if _, err := os.Stat(otherClip); !os.IsNotExist(err) {
		t.Fatalf("expected no non-recorder clip at %s, stat err: %v", otherClip, err)
	}
	if strings.Contains(stdout.String(), otherClip) {
		t.Fatalf("stdout should not mention non-recorder clip: %q", stdout.String())
	}
}

type syntheticSnapshot struct {
	serverTime int
	entities   []entityState
}

func writeSyntheticDemo(t *testing.T, path string, recorderClientNum int, snapshots []syntheticSnapshot) {
	t.Helper()

	parser := newParser(io.Discard, parserOptions{})
	parser.serverCommandSequence = 1
	parser.clientNum = recorderClientNum
	parser.checksumFeed = 0
	parser.configStrings[csLevelStartTime] = "1000"
	parser.configStrings[csPlayers+1] = `\n\Killer\t\1`
	parser.configStrings[csPlayers+2] = `\n\VictimA\t\2`
	parser.configStrings[csPlayers+3] = `\n\VictimB\t\2`
	parser.configStrings[csPlayers+4] = `\n\Other\t\1`
	parser.configStrings[csPlayers+5] = `\n\VictimC\t\2`
	parser.configStrings[csPlayers+6] = `\n\VictimD\t\2`

	file, err := os.Create(path)
	if err != nil {
		t.Fatalf("create %s: %v", path, err)
	}

	writer := newDemoFileWriter(file)
	if err := writer.writeGamestate(parser); err != nil {
		_ = file.Close()
		t.Fatalf("write gamestate: %v", err)
	}

	writeSnapshot := func(serverTime int, entities []entityState) {
		snapshot := snapshotState{
			Valid:      true,
			ServerTime: serverTime,
		}
		snapshot.ParseEntitiesNum = parser.parseEntitiesNum
		for _, entity := range entities {
			parser.parseEntities[parser.parseEntitiesNum&(maxParseEntities-1)] = entity
			parser.parseEntitiesNum++
			snapshot.NumEntities++
		}

		if err := writer.writeSnapshot(parser, &snapshot); err != nil {
			_ = file.Close()
			t.Fatalf("write snapshot at %d: %v", serverTime, err)
		}
	}

	for _, snapshot := range snapshots {
		writeSnapshot(snapshot.serverTime, snapshot.entities)
	}

	if err := writer.writeEndMarker(); err != nil {
		_ = file.Close()
		t.Fatalf("write end marker: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close %s: %v", path, err)
	}
}

func makeObituaryEntity(number int, attacker int, target int) entityState {
	var state entityState

	state.Number = number
	state.Fields[fieldEntityType] = etEvents + evObituary
	state.Fields[fieldOtherEntityNum] = int32(target)
	state.Fields[fieldOtherEntityNum2] = int32(attacker)
	state.Fields[fieldWeapon] = weaponMP40

	return state
}
