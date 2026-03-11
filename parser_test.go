package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestEmitKillMultiKillWindowsSeparated(t *testing.T) {
	var out bytes.Buffer

	parser := newParser(&out, parserOptions{multiKillMin: 2})
	parser.levelStartTime = 1000
	parser.players[1] = playerInfo{Name: "Killer", Team: teamAxis}
	parser.players[2] = playerInfo{Name: "VictimA", Team: teamAllies}
	parser.players[3] = playerInfo{Name: "VictimB", Team: teamAllies}
	parser.players[4] = playerInfo{Name: "VictimC", Team: teamAllies}
	parser.players[5] = playerInfo{Name: "VictimD", Team: teamAllies}

	parser.emitKill(2000, &entityState{
		Fields: [entityFieldCount]int32{
			fieldOtherEntityNum:  2,
			fieldOtherEntityNum2: 1,
		},
	})
	if out.Len() != 0 {
		t.Fatalf("unexpected output after first kill: %q", out.String())
	}

	parser.emitKill(3000, &entityState{
		Fields: [entityFieldCount]int32{
			fieldOtherEntityNum:  1,
			fieldOtherEntityNum2: 1,
		},
	})
	if out.Len() != 0 {
		t.Fatalf("unexpected output after self-kill in multikill mode: %q", out.String())
	}

	parser.emitKill(4800, &entityState{
		Fields: [entityFieldCount]int32{
			fieldOtherEntityNum:  3,
			fieldOtherEntityNum2: 1,
		},
	})
	if out.Len() != 0 {
		t.Fatalf("unexpected output before window close: %q", out.String())
	}

	parser.emitKill(9000, &entityState{
		Fields: [entityFieldCount]int32{
			fieldOtherEntityNum:  4,
			fieldOtherEntityNum2: 1,
		},
	})
	parser.emitKill(11300, &entityState{
		Fields: [entityFieldCount]int32{
			fieldOtherEntityNum:  5,
			fieldOtherEntityNum2: 1,
		},
	})
	parser.flushAllMultiKillWindows()

	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 5 {
		t.Fatalf("expected 5 lines including separator, got %d: %q", len(lines), out.String())
	}
	if lines[0] != "00:01.00 ; Killer ; VictimA ; Enemy" {
		t.Fatalf("unexpected first multikill line: %q", lines[0])
	}
	if lines[1] != "00:03.80 ; Killer ; VictimB ; Enemy" {
		t.Fatalf("unexpected second multikill line: %q", lines[1])
	}
	if lines[2] != "---" {
		t.Fatalf("missing window separator: %q", lines[2])
	}
	if lines[3] != "00:08.00 ; Killer ; VictimC ; Enemy" {
		t.Fatalf("unexpected third multikill line: %q", lines[3])
	}
	if lines[4] != "00:10.30 ; Killer ; VictimD ; Enemy" {
		t.Fatalf("unexpected fourth multikill line: %q", lines[4])
	}
}

func TestEmitKillMultiKillMinimumFiltersShortWindows(t *testing.T) {
	var out bytes.Buffer

	parser := newParser(&out, parserOptions{multiKillMin: 3})
	parser.levelStartTime = 1000
	parser.players[1] = playerInfo{Name: "Killer", Team: teamAxis}
	parser.players[2] = playerInfo{Name: "VictimA", Team: teamAllies}
	parser.players[3] = playerInfo{Name: "VictimB", Team: teamAllies}
	parser.players[4] = playerInfo{Name: "VictimC", Team: teamAllies}
	parser.players[5] = playerInfo{Name: "VictimD", Team: teamAllies}
	parser.players[6] = playerInfo{Name: "VictimE", Team: teamAllies}

	parser.emitKill(2000, &entityState{
		Fields: [entityFieldCount]int32{
			fieldOtherEntityNum:  2,
			fieldOtherEntityNum2: 1,
		},
	})
	parser.emitKill(4800, &entityState{
		Fields: [entityFieldCount]int32{
			fieldOtherEntityNum:  3,
			fieldOtherEntityNum2: 1,
		},
	})
	parser.emitKill(9000, &entityState{
		Fields: [entityFieldCount]int32{
			fieldOtherEntityNum:  4,
			fieldOtherEntityNum2: 1,
		},
	})
	parser.emitKill(11200, &entityState{
		Fields: [entityFieldCount]int32{
			fieldOtherEntityNum:  5,
			fieldOtherEntityNum2: 1,
		},
	})
	parser.emitKill(13200, &entityState{
		Fields: [entityFieldCount]int32{
			fieldOtherEntityNum:  6,
			fieldOtherEntityNum2: 1,
		},
	})
	parser.flushAllMultiKillWindows()

	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 3 {
		t.Fatalf("expected only the 3-kill window to print, got %d lines: %q", len(lines), out.String())
	}
	if lines[0] != "00:08.00 ; Killer ; VictimC ; Enemy" {
		t.Fatalf("unexpected first printed line: %q", lines[0])
	}
	if lines[1] != "00:10.20 ; Killer ; VictimD ; Enemy" {
		t.Fatalf("unexpected second printed line: %q", lines[1])
	}
	if lines[2] != "00:12.20 ; Killer ; VictimE ; Enemy" {
		t.Fatalf("unexpected third printed line: %q", lines[2])
	}
}

func TestEmitKillSelfMultiKillClassification(t *testing.T) {
	var out bytes.Buffer

	parser := newParser(&out, parserOptions{})
	parser.players[5] = playerInfo{Name: "Solo", Team: teamAxis}

	parser.emitKill(2500, &entityState{
		Fields: [entityFieldCount]int32{
			fieldOtherEntityNum:  5,
			fieldOtherEntityNum2: 5,
		},
	})

	if got := strings.TrimSpace(out.String()); got != "00:02.50 ; Solo ; Solo ; Self" {
		t.Fatalf("unexpected self-kill output: %q", got)
	}
}

func TestEmitKillFiltersByAttackerName(t *testing.T) {
	var out bytes.Buffer

	parser := newParser(&out, parserOptions{killsOnlyFrom: "Killer"})
	parser.players[1] = playerInfo{Name: "Killer", Team: teamAxis}
	parser.players[2] = playerInfo{Name: "VictimA", Team: teamAllies}
	parser.players[3] = playerInfo{Name: "Other", Team: teamAxis}
	parser.players[4] = playerInfo{Name: "VictimB", Team: teamAllies}

	parser.emitKill(1000, &entityState{
		Fields: [entityFieldCount]int32{
			fieldOtherEntityNum:  2,
			fieldOtherEntityNum2: 1,
		},
	})
	parser.emitKill(2000, &entityState{
		Fields: [entityFieldCount]int32{
			fieldOtherEntityNum:  4,
			fieldOtherEntityNum2: 3,
		},
	})

	if got := strings.TrimSpace(out.String()); got != "00:01.00 ; Killer ; VictimA ; Enemy" {
		t.Fatalf("unexpected filtered output: %q", got)
	}
}
