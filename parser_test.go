package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestEmitKillMultiKillsOnly(t *testing.T) {
	var out bytes.Buffer

	parser := newParser(&out, parserOptions{multiKillsOnly: true})
	parser.levelStartTime = 1000
	parser.players[1] = playerInfo{Name: "Killer", Team: teamAxis}
	parser.players[2] = playerInfo{Name: "VictimA", Team: teamAllies}
	parser.players[3] = playerInfo{Name: "VictimB", Team: teamAllies}
	parser.players[4] = playerInfo{Name: "VictimC", Team: teamAllies}

	parser.emitKill(2000, &entityState{
		Fields: [entityFieldCount]int32{
			fieldOtherEntityNum:  2,
			fieldOtherEntityNum2: 1,
		},
	})
	if out.Len() != 0 {
		t.Fatalf("unexpected output after first kill: %q", out.String())
	}

	parser.emitKill(4800, &entityState{
		Fields: [entityFieldCount]int32{
			fieldOtherEntityNum:  3,
			fieldOtherEntityNum2: 1,
		},
	})

	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 multikill lines, got %d: %q", len(lines), out.String())
	}
	if lines[0] != "00:01.00 ; Killer ; VictimA ; Enemy" {
		t.Fatalf("unexpected first multikill line: %q", lines[0])
	}
	if lines[1] != "00:03.80 ; Killer ; VictimB ; Enemy" {
		t.Fatalf("unexpected second multikill line: %q", lines[1])
	}

	out.Reset()
	parser.emitKill(9000, &entityState{
		Fields: [entityFieldCount]int32{
			fieldOtherEntityNum:  4,
			fieldOtherEntityNum2: 1,
		},
	})
	if out.Len() != 0 {
		t.Fatalf("unexpected output for non-multikill: %q", out.String())
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
