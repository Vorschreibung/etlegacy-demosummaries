package main

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func expectedKillLine(timestamp string, headshot bool, attacker, weapon, target, relation string) string {
	return timestamp + " ; " + obituaryKillLabel(headshot) + " ; " +
		attacker + " ; " + weapon + " ; " + target + " ; " + relation
}

func TestObituaryKillLabelPadding(t *testing.T) {
	if got := obituaryKillLabel(false); got != "Kill        " {
		t.Fatalf("unexpected padded kill label: %q", got)
	}
	if got := obituaryKillLabel(true); got != "HeadshotKill" {
		t.Fatalf("unexpected headshot kill label: %q", got)
	}
}

func TestWarnWhenDemoPredatesObituaryHeadshotSupport(t *testing.T) {
	var out bytes.Buffer
	var warn bytes.Buffer

	parser := newParserWithWarning(&out, &warn, parserOptions{})
	parser.demoPath = "old.dm_84"
	parser.setConfigString(csServerInfo, `\mod_version\v2.83.2-172-gdeadbeef`)

	got := warn.String()
	if !strings.Contains(got, "old.dm_84") {
		t.Fatalf("expected warning to mention demo path, got %q", got)
	}
	if !strings.Contains(got, "v2.83.2-172-gdeadbeef") {
		t.Fatalf("expected warning to mention demo version, got %q", got)
	}
	if !strings.Contains(got, "HeadshotKill output is unavailable") {
		t.Fatalf("expected warning to explain missing headshot output, got %q", got)
	}
}

func TestNoWarningWhenDemoSupportsObituaryHeadshots(t *testing.T) {
	var out bytes.Buffer
	var warn bytes.Buffer

	parser := newParserWithWarning(&out, &warn, parserOptions{})
	parser.demoPath = "new.dm_84"
	parser.setConfigString(csServerInfo, `\mod_version\v2.83.2-173-g076d72559`)

	if warn.Len() != 0 {
		t.Fatalf("unexpected warning for supported demo: %q", warn.String())
	}
}

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
			fieldWeapon:          weaponMP40,
		},
	})
	if out.Len() != 0 {
		t.Fatalf("unexpected output after first kill: %q", out.String())
	}

	parser.emitKill(3000, &entityState{
		Fields: [entityFieldCount]int32{
			fieldOtherEntityNum:  1,
			fieldOtherEntityNum2: 1,
			fieldWeapon:          weaponMP40,
		},
	})
	if out.Len() != 0 {
		t.Fatalf("unexpected output after self-kill in multikill mode: %q", out.String())
	}

	parser.emitKill(4800, &entityState{
		Fields: [entityFieldCount]int32{
			fieldOtherEntityNum:  3,
			fieldOtherEntityNum2: 1,
			fieldWeapon:          weaponMP40,
		},
	})
	if out.Len() != 0 {
		t.Fatalf("unexpected output before window close: %q", out.String())
	}

	parser.emitKill(9000, &entityState{
		Fields: [entityFieldCount]int32{
			fieldOtherEntityNum:  4,
			fieldOtherEntityNum2: 1,
			fieldWeapon:          weaponMP40,
		},
	})
	parser.emitKill(11300, &entityState{
		Fields: [entityFieldCount]int32{
			fieldOtherEntityNum:  5,
			fieldOtherEntityNum2: 1,
			fieldWeapon:          weaponMP40,
		},
	})
	parser.flushAllMultiKillWindows()

	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 5 {
		t.Fatalf("expected 5 lines including separator, got %d: %q", len(lines), out.String())
	}
	if lines[0] != expectedKillLine("00:01.00", false, "Killer", "MP40", "VictimA", "Enemy") {
		t.Fatalf("unexpected first multikill line: %q", lines[0])
	}
	if lines[1] != expectedKillLine("00:03.80", false, "Killer", "MP40", "VictimB", "Enemy") {
		t.Fatalf("unexpected second multikill line: %q", lines[1])
	}
	if lines[2] != "---" {
		t.Fatalf("missing window separator: %q", lines[2])
	}
	if lines[3] != expectedKillLine("00:08.00", false, "Killer", "MP40", "VictimC", "Enemy") {
		t.Fatalf("unexpected third multikill line: %q", lines[3])
	}
	if lines[4] != expectedKillLine("00:10.30", false, "Killer", "MP40", "VictimD", "Enemy") {
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
			fieldWeapon:          weaponMP40,
		},
	})
	parser.emitKill(4800, &entityState{
		Fields: [entityFieldCount]int32{
			fieldOtherEntityNum:  3,
			fieldOtherEntityNum2: 1,
			fieldWeapon:          weaponMP40,
		},
	})
	parser.emitKill(9000, &entityState{
		Fields: [entityFieldCount]int32{
			fieldOtherEntityNum:  4,
			fieldOtherEntityNum2: 1,
			fieldWeapon:          weaponMP40,
		},
	})
	parser.emitKill(11200, &entityState{
		Fields: [entityFieldCount]int32{
			fieldOtherEntityNum:  5,
			fieldOtherEntityNum2: 1,
			fieldWeapon:          weaponMP40,
		},
	})
	parser.emitKill(13200, &entityState{
		Fields: [entityFieldCount]int32{
			fieldOtherEntityNum:  6,
			fieldOtherEntityNum2: 1,
			fieldWeapon:          weaponMP40,
		},
	})
	parser.flushAllMultiKillWindows()

	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 3 {
		t.Fatalf("expected only the 3-kill window to print, got %d lines: %q", len(lines), out.String())
	}
	if lines[0] != expectedKillLine("00:08.00", false, "Killer", "MP40", "VictimC", "Enemy") {
		t.Fatalf("unexpected first printed line: %q", lines[0])
	}
	if lines[1] != expectedKillLine("00:10.20", false, "Killer", "MP40", "VictimD", "Enemy") {
		t.Fatalf("unexpected second printed line: %q", lines[1])
	}
	if lines[2] != expectedKillLine("00:12.20", false, "Killer", "MP40", "VictimE", "Enemy") {
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
			fieldWeapon:          weaponMP40,
		},
	})

	if got := strings.TrimSpace(out.String()); got != expectedKillLine("00:02.50", false, "Solo", "MP40", "Solo", "Self") {
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
			fieldWeapon:          weaponMP40,
		},
	})
	parser.emitKill(2000, &entityState{
		Fields: [entityFieldCount]int32{
			fieldOtherEntityNum:  4,
			fieldOtherEntityNum2: 3,
			fieldWeapon:          weaponMP40,
		},
	})

	if got := strings.TrimSpace(out.String()); got != expectedKillLine("00:01.00", false, "Killer", "MP40", "VictimA", "Enemy") {
		t.Fatalf("unexpected filtered output: %q", got)
	}
}

func TestEmitKillMultiKillHeadshotsOnly(t *testing.T) {
	var out bytes.Buffer

	parser := newParser(&out, parserOptions{multiKillHeadshotMin: 2})
	parser.levelStartTime = 1000
	parser.players[1] = playerInfo{Name: "Killer", Team: teamAxis}
	parser.players[2] = playerInfo{Name: "VictimA", Team: teamAllies}
	parser.players[3] = playerInfo{Name: "VictimB", Team: teamAllies}
	parser.players[4] = playerInfo{Name: "VictimC", Team: teamAllies}

	parser.emitKill(2000, &entityState{
		Fields: [entityFieldCount]int32{
			fieldOtherEntityNum:  2,
			fieldOtherEntityNum2: 1,
			fieldLoopSound:       1,
			fieldWeapon:          weaponMP40,
		},
	})
	parser.emitKill(3000, &entityState{
		Fields: [entityFieldCount]int32{
			fieldOtherEntityNum:  3,
			fieldOtherEntityNum2: 1,
			fieldWeapon:          weaponMP40,
		},
	})
	parser.emitKill(4800, &entityState{
		Fields: [entityFieldCount]int32{
			fieldOtherEntityNum:  4,
			fieldOtherEntityNum2: 1,
			fieldLoopSound:       1,
			fieldWeapon:          weaponMP40,
		},
	})
	parser.flushAllMultiKillWindows()

	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected only the headshot window to print, got %d lines: %q", len(lines), out.String())
	}
	if lines[0] != expectedKillLine("00:01.00", true, "Killer", "MP40", "VictimA", "Enemy") {
		t.Fatalf("unexpected first headshot line: %q", lines[0])
	}
	if lines[1] != expectedKillLine("00:03.80", true, "Killer", "MP40", "VictimC", "Enemy") {
		t.Fatalf("unexpected second headshot line: %q", lines[1])
	}
}

func TestParseFileIgnoresTruncatedTailPacket(t *testing.T) {
	var out bytes.Buffer
	parser := newParser(&out, parserOptions{})

	path := filepath.Join(t.TempDir(), "truncated.dm_84")

	var demo bytes.Buffer
	if err := binary.Write(&demo, binary.LittleEndian, int32(1)); err != nil {
		t.Fatalf("write sequence: %v", err)
	}
	if err := binary.Write(&demo, binary.LittleEndian, int32(4)); err != nil {
		t.Fatalf("write packet size: %v", err)
	}
	demo.Write([]byte{0x00, 0x01})

	if err := os.WriteFile(path, demo.Bytes(), 0o644); err != nil {
		t.Fatalf("write demo: %v", err)
	}
	if err := parser.parseFile(path); err != nil {
		t.Fatalf("parseFile returned unexpected error: %v", err)
	}
}
