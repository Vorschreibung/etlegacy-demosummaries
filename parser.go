package main

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"sort"
	"strconv"
	"strings"
)

type parserOptions struct {
	multiKillsOnly bool
}

type killOutput struct {
	serverTime int
	line       string
}

type multiKillWindow struct {
	outputs []killOutput
}

type parser struct {
	out io.Writer

	options parserOptions

	serverCommandSequence int
	levelStartTime        int
	bigConfig             string

	configStrings [maxConfigStrings]string
	players       [maxClients]playerInfo

	baselines        [maxGentities]entityState
	parseEntities    [maxParseEntities]entityState
	parseEntitiesNum int
	snapshots        [packetBackup]snapshotState

	// Temp event entities stay around for a few snapshots. Track which entity
	// numbers have already been emitted so obituaries are printed once.
	activeTempEntities map[int]struct{}
	pendingKills       map[int]multiKillWindow

	printedMultiKillWindow bool
}

func newParser(out io.Writer, options parserOptions) *parser {
	p := &parser{
		out:                out,
		options:            options,
		activeTempEntities: make(map[int]struct{}),
		pendingKills:       make(map[int]multiKillWindow),
	}
	p.resetState()

	return p
}

func (p *parser) resetState() {
	p.serverCommandSequence = 0
	p.levelStartTime = 0
	p.bigConfig = ""
	p.configStrings = [maxConfigStrings]string{}
	p.players = [maxClients]playerInfo{}
	p.baselines = [maxGentities]entityState{}
	p.parseEntities = [maxParseEntities]entityState{}
	p.parseEntitiesNum = 0
	p.snapshots = [packetBackup]snapshotState{}
	p.activeTempEntities = make(map[int]struct{})
	p.pendingKills = make(map[int]multiKillWindow)
	p.printedMultiKillWindow = false
}

func (p *parser) parseFile(path string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()

	for {
		var sequence int32
		if err := binary.Read(file, binary.LittleEndian, &sequence); err != nil {
			if errors.Is(err, io.EOF) {
				p.flushAllMultiKillWindows()
				return nil
			}
			if errors.Is(err, io.ErrUnexpectedEOF) {
				return fmt.Errorf("truncated demo header: %w", err)
			}
			return err
		}

		var size int32
		if err := binary.Read(file, binary.LittleEndian, &size); err != nil {
			if errors.Is(err, io.ErrUnexpectedEOF) {
				return fmt.Errorf("truncated demo packet header: %w", err)
			}
			return err
		}

		if size == -1 {
			p.flushAllMultiKillWindows()
			return nil
		}
		if size < 0 || size > maxMsgLen {
			return fmt.Errorf("invalid packet size %d", size)
		}

		packet := make([]byte, size)
		if _, err := io.ReadFull(file, packet); err != nil {
			return fmt.Errorf("truncated demo packet: %w", err)
		}

		if err := p.parsePacket(int(sequence), packet); err != nil {
			return fmt.Errorf("packet %d: %w", sequence, err)
		}
	}
}

func (p *parser) parsePacket(sequence int, data []byte) error {
	msg := newMsgReader(data)

	if _, err := msg.readLong(); err != nil {
		return fmt.Errorf("missing reliable acknowledge: %w", err)
	}

	for {
		if msg.readcount > msg.cursize {
			return errors.New("read past end of server message")
		}

		cmd, err := msg.readByte()
		if err != nil {
			return fmt.Errorf("read server opcode: %w", err)
		}

		switch cmd {
		case svcEOF:
			return nil
		case svcNop:
			continue
		case svcServerCommand:
			if err := p.parseServerCommand(msg); err != nil {
				return err
			}
		case svcGamestate:
			if err := p.parseGamestate(msg); err != nil {
				return err
			}
		case svcSnapshot:
			if err := p.parseSnapshot(sequence, msg); err != nil {
				return err
			}
		case svcDownload:
			return errors.New("svc_download is not supported")
		default:
			return fmt.Errorf("illegible server message %d", cmd)
		}
	}
}

func (p *parser) parseServerCommand(msg *msgReader) error {
	sequence, err := msg.readLong()
	if err != nil {
		return fmt.Errorf("read server command sequence: %w", err)
	}

	command := msg.readString()
	if p.serverCommandSequence >= sequence {
		return nil
	}

	p.serverCommandSequence = sequence
	p.handleServerCommand(command)

	return nil
}

func (p *parser) parseGamestate(msg *msgReader) error {
	var empty entityState

	p.flushAllMultiKillWindows()
	p.resetState()

	sequence, err := msg.readLong()
	if err != nil {
		return fmt.Errorf("read gamestate command sequence: %w", err)
	}
	p.serverCommandSequence = sequence

	for {
		cmd, err := msg.readByte()
		if err != nil {
			return fmt.Errorf("read gamestate opcode: %w", err)
		}
		if cmd == svcEOF {
			break
		}

		switch cmd {
		case svcConfigstring:
			index, err := msg.readShort()
			if err != nil {
				return fmt.Errorf("read configstring index: %w", err)
			}
			if index < 0 || index >= maxConfigStrings {
				return fmt.Errorf("configstring index out of range: %d", index)
			}
			p.setConfigString(index, msg.readBigString())
		case svcBaseline:
			number, err := msg.readBits(gentityNumBits)
			if err != nil {
				return fmt.Errorf("read baseline entity number: %w", err)
			}
			if number < 0 || int(number) >= maxGentities {
				return fmt.Errorf("baseline entity out of range: %d", number)
			}

			state, err := readDeltaEntity(msg, &empty, int(number))
			if err != nil {
				return fmt.Errorf("read baseline entity %d: %w", number, err)
			}
			p.baselines[number] = state
		default:
			return fmt.Errorf("bad gamestate opcode %d", cmd)
		}
	}

	if _, err := msg.readLong(); err != nil {
		return fmt.Errorf("read gamestate client num: %w", err)
	}
	if _, err := msg.readLong(); err != nil {
		return fmt.Errorf("read gamestate checksum feed: %w", err)
	}

	return nil
}

func (p *parser) parseSnapshot(sequence int, msg *msgReader) error {
	var snap snapshotState
	var old *snapshotState

	serverTime, err := msg.readLong()
	if err != nil {
		return fmt.Errorf("read snapshot server time: %w", err)
	}
	snap.ServerTime = serverTime
	snap.MessageNum = sequence

	deltaByte, err := msg.readByte()
	if err != nil {
		return fmt.Errorf("read snapshot delta number: %w", err)
	}
	if deltaByte == 0 {
		snap.DeltaNum = -1
		snap.Valid = true
	} else {
		snap.DeltaNum = snap.MessageNum - deltaByte
		old = &p.snapshots[snap.DeltaNum&packetMask]
		if old.Valid && old.MessageNum == snap.DeltaNum &&
			p.parseEntitiesNum-old.ParseEntitiesNum <= maxParseBacklog {
			snap.Valid = true
		}
	}

	if _, err := msg.readByte(); err != nil {
		return fmt.Errorf("read snapshot flags: %w", err)
	}

	areaMaskLen, err := msg.readByte()
	if err != nil {
		return fmt.Errorf("read areamask length: %w", err)
	}
	if areaMaskLen < 0 {
		return fmt.Errorf("invalid areamask length %d", areaMaskLen)
	}
	if err := msg.skipBytes(areaMaskLen); err != nil {
		return fmt.Errorf("read areamask: %w", err)
	}

	if old != nil {
		snap.PlayerState, err = readDeltaPlayerState(msg, &old.PlayerState)
	} else {
		snap.PlayerState, err = readDeltaPlayerState(msg, nil)
	}
	if err != nil {
		return fmt.Errorf("read delta playerstate: %w", err)
	}

	if err := p.parsePacketEntities(msg, old, &snap); err != nil {
		return err
	}

	if !snap.Valid {
		return nil
	}

	p.snapshots[snap.MessageNum&packetMask] = snap
	p.emitSnapshotKills(&snap)

	return nil
}

func (p *parser) parsePacketEntities(msg *msgReader, oldFrame *snapshotState, newFrame *snapshotState) error {
	var oldState *entityState

	oldIndex := 0
	oldNum := maxGentities

	newFrame.ParseEntitiesNum = p.parseEntitiesNum
	newFrame.NumEntities = 0

	if oldFrame != nil && oldIndex < oldFrame.NumEntities {
		oldState = &p.parseEntities[(oldFrame.ParseEntitiesNum+oldIndex)&(maxParseEntities-1)]
		oldNum = oldState.Number
	}

	for {
		newNumBits, err := msg.readBits(gentityNumBits)
		if err != nil {
			return fmt.Errorf("read packet entity number: %w", err)
		}
		newNum := int(newNumBits)
		if newNum >= removedEntityNum {
			break
		}

		for oldNum < newNum {
			p.copyEntity(newFrame, oldState)
			oldIndex++
			if oldFrame == nil || oldIndex >= oldFrame.NumEntities {
				oldNum = maxGentities
				oldState = nil
			} else {
				oldState = &p.parseEntities[(oldFrame.ParseEntitiesNum+oldIndex)&(maxParseEntities-1)]
				oldNum = oldState.Number
			}
		}

		if oldNum == newNum {
			if err := p.deltaEntity(msg, newFrame, newNum, oldState); err != nil {
				return err
			}
			oldIndex++
			if oldFrame == nil || oldIndex >= oldFrame.NumEntities {
				oldNum = maxGentities
				oldState = nil
			} else {
				oldState = &p.parseEntities[(oldFrame.ParseEntitiesNum+oldIndex)&(maxParseEntities-1)]
				oldNum = oldState.Number
			}
			continue
		}

		if oldNum > newNum {
			if err := p.deltaEntity(msg, newFrame, newNum, &p.baselines[newNum]); err != nil {
				return err
			}
		}
	}

	for oldNum != maxGentities {
		p.copyEntity(newFrame, oldState)
		oldIndex++
		if oldFrame == nil || oldIndex >= oldFrame.NumEntities {
			oldNum = maxGentities
			oldState = nil
		} else {
			oldState = &p.parseEntities[(oldFrame.ParseEntitiesNum+oldIndex)&(maxParseEntities-1)]
			oldNum = oldState.Number
		}
	}

	return nil
}

func (p *parser) copyEntity(frame *snapshotState, source *entityState) {
	if source == nil {
		return
	}

	slot := p.parseEntitiesNum & (maxParseEntities - 1)
	p.parseEntities[slot] = *source
	p.parseEntitiesNum++
	frame.NumEntities++
}

func (p *parser) deltaEntity(msg *msgReader, frame *snapshotState, number int, old *entityState) error {
	if old == nil {
		return fmt.Errorf("missing delta source for entity %d", number)
	}

	state, err := readDeltaEntity(msg, old, number)
	if err != nil {
		return fmt.Errorf("read delta entity %d: %w", number, err)
	}
	if state.Number == removedEntityNum {
		return nil
	}

	slot := p.parseEntitiesNum & (maxParseEntities - 1)
	p.parseEntities[slot] = state
	p.parseEntitiesNum++
	frame.NumEntities++

	return nil
}

func readDeltaEntity(msg *msgReader, from *entityState, number int) (entityState, error) {
	to := *from

	if number < 0 || number >= maxGentities {
		return entityState{}, fmt.Errorf("bad delta entity number %d", number)
	}

	remove, err := msg.readBits(1)
	if err != nil {
		return entityState{}, err
	}
	if remove != 0 {
		return entityState{Number: removedEntityNum}, nil
	}

	noDelta, err := msg.readBits(1)
	if err != nil {
		return entityState{}, err
	}
	if noDelta == 0 {
		to.Number = number
		return to, nil
	}

	count, err := msg.readByte()
	if err != nil {
		return entityState{}, err
	}
	if count < 0 || count > entityFieldCount {
		return entityState{}, fmt.Errorf("invalid entityState field count %d", count)
	}

	to.Number = number

	for i := 0; i < count; i++ {
		changed, err := msg.readBits(1)
		if err != nil {
			return entityState{}, err
		}
		if changed == 0 {
			continue
		}

		bits := entityFieldBits[i]
		if bits == 0 {
			isNonZero, err := msg.readBits(1)
			if err != nil {
				return entityState{}, err
			}
			if isNonZero == 0 {
				to.Fields[i] = int32(mathFloat32bits(0))
				continue
			}

			isFullFloat, err := msg.readBits(1)
			if err != nil {
				return entityState{}, err
			}
			if isFullFloat == 0 {
				trunc, err := msg.readBits(floatIntBits)
				if err != nil {
					return entityState{}, err
				}
				to.Fields[i] = floatBitsFromInt(trunc - floatIntBias)
			} else {
				value, err := msg.readBits(32)
				if err != nil {
					return entityState{}, err
				}
				to.Fields[i] = value
			}
			continue
		}

		isNonZero, err := msg.readBits(1)
		if err != nil {
			return entityState{}, err
		}
		if isNonZero == 0 {
			to.Fields[i] = 0
			continue
		}

		value, err := msg.readBits(bits)
		if err != nil {
			return entityState{}, err
		}
		to.Fields[i] = value
	}

	return to, nil
}

func readDeltaPlayerState(msg *msgReader, from *playerState) (playerState, error) {
	var empty playerState
	to := empty

	if from != nil {
		to = *from
	}

	count, err := msg.readByte()
	if err != nil {
		return playerState{}, err
	}
	if count < 0 || count > playerStateFieldCount {
		return playerState{}, fmt.Errorf("invalid playerState field count %d", count)
	}

	for i := 0; i < count; i++ {
		changed, err := msg.readBits(1)
		if err != nil {
			return playerState{}, err
		}
		if changed == 0 {
			continue
		}

		bits := playerStateFieldBits[i]
		if bits == 0 {
			isFullFloat, err := msg.readBits(1)
			if err != nil {
				return playerState{}, err
			}
			if isFullFloat == 0 {
				trunc, err := msg.readBits(floatIntBits)
				if err != nil {
					return playerState{}, err
				}
				to.Fields[i] = floatBitsFromInt(trunc - floatIntBias)
			} else {
				value, err := msg.readBits(32)
				if err != nil {
					return playerState{}, err
				}
				to.Fields[i] = value
			}
			continue
		}

		value, err := msg.readBits(bits)
		if err != nil {
			return playerState{}, err
		}
		to.Fields[i] = value
	}

	changedArrays, err := msg.readBits(1)
	if err != nil {
		return playerState{}, err
	}
	if changedArrays != 0 {
		statsChanged, err := msg.readBits(1)
		if err != nil {
			return playerState{}, err
		}
		if statsChanged != 0 {
			mask, err := msg.readShort()
			if err != nil {
				return playerState{}, err
			}
			for i := 0; i < maxStats; i++ {
				if mask&(1<<i) == 0 {
					continue
				}
				value, err := msg.readShort()
				if err != nil {
					return playerState{}, err
				}
				to.Stats[i] = int32(value)
			}
		}

		persistChanged, err := msg.readBits(1)
		if err != nil {
			return playerState{}, err
		}
		if persistChanged != 0 {
			mask, err := msg.readShort()
			if err != nil {
				return playerState{}, err
			}
			for i := 0; i < maxPersistant; i++ {
				if mask&(1<<i) == 0 {
					continue
				}
				value, err := msg.readShort()
				if err != nil {
					return playerState{}, err
				}
				to.Persistant[i] = int32(value)
			}
		}

		holdableChanged, err := msg.readBits(1)
		if err != nil {
			return playerState{}, err
		}
		if holdableChanged != 0 {
			mask, err := msg.readShort()
			if err != nil {
				return playerState{}, err
			}
			for i := 0; i < maxHoldable; i++ {
				if mask&(1<<i) == 0 {
					continue
				}
				value, err := msg.readShort()
				if err != nil {
					return playerState{}, err
				}
				to.Holdable[i] = int32(value)
			}
		}

		powerupChanged, err := msg.readBits(1)
		if err != nil {
			return playerState{}, err
		}
		if powerupChanged != 0 {
			mask, err := msg.readShort()
			if err != nil {
				return playerState{}, err
			}
			for i := 0; i < maxPowerups; i++ {
				if mask&(1<<i) == 0 {
					continue
				}
				value, err := msg.readLong()
				if err != nil {
					return playerState{}, err
				}
				to.Powerups[i] = int32(value)
			}
		}
	}

	ammoChanged, err := msg.readBits(1)
	if err != nil {
		return playerState{}, err
	}
	if ammoChanged != 0 {
		for group := 0; group < maxAmmoGroups; group++ {
			groupChanged, err := msg.readBits(1)
			if err != nil {
				return playerState{}, err
			}
			if groupChanged == 0 {
				continue
			}

			mask, err := msg.readShort()
			if err != nil {
				return playerState{}, err
			}
			for i := 0; i < ammoPerGroup; i++ {
				if mask&(1<<i) == 0 {
					continue
				}
				value, err := msg.readShort()
				if err != nil {
					return playerState{}, err
				}
				to.Ammo[i+(group*ammoPerGroup)] = int32(value)
			}
		}
	}

	for group := 0; group < maxAmmoGroups; group++ {
		groupChanged, err := msg.readBits(1)
		if err != nil {
			return playerState{}, err
		}
		if groupChanged == 0 {
			continue
		}

		mask, err := msg.readShort()
		if err != nil {
			return playerState{}, err
		}
		for i := 0; i < ammoPerGroup; i++ {
			if mask&(1<<i) == 0 {
				continue
			}
			value, err := msg.readShort()
			if err != nil {
				return playerState{}, err
			}
			to.AmmoClip[i+(group*ammoPerGroup)] = int32(value)
		}
	}

	return to, nil
}

func (p *parser) emitSnapshotKills(snapshot *snapshotState) {
	presentTemps := make(map[int]struct{})

	for i := 0; i < snapshot.NumEntities; i++ {
		state := &p.parseEntities[(snapshot.ParseEntitiesNum+i)&(maxParseEntities-1)]
		eType := int(state.Fields[fieldEntityType])
		if eType < etEvents {
			continue
		}

		presentTemps[state.Number] = struct{}{}
		if _, alreadySeen := p.activeTempEntities[state.Number]; alreadySeen {
			continue
		}

		p.activeTempEntities[state.Number] = struct{}{}

		if eType-etEvents == evObituary {
			p.emitKill(snapshot.ServerTime, state)
		}
	}

	for number := range p.activeTempEntities {
		if _, stillPresent := presentTemps[number]; !stillPresent {
			delete(p.activeTempEntities, number)
		}
	}
}

func (p *parser) emitKill(serverTime int, state *entityState) {
	target := int(state.Fields[fieldOtherEntityNum])
	if target < 0 || target >= maxClients {
		return
	}

	attacker := int(state.Fields[fieldOtherEntityNum2])
	timestamp := formatMatchTimestamp(serverTime - p.levelStartTime)
	if attacker == entityNumWorld || attacker < 0 || attacker >= maxClients {
		return
	}

	relation, ok := p.killRelation(attacker, target)
	if !ok {
		return
	}
	if p.options.multiKillsOnly && relation == "Self" {
		return
	}

	p.writeKill(attacker, killOutput{
		serverTime: serverTime,
		line: fmt.Sprintf("%s ; %s ; %s ; %s",
			timestamp,
			p.playerName(attacker),
			p.playerName(target),
			relation,
		),
	})
}

func (p *parser) killRelation(attacker, target int) (string, bool) {
	if attacker == target {
		return "Self", true
	}

	attackerTeam := p.players[attacker].Team
	targetTeam := p.players[target].Team

	if (attackerTeam == teamAxis || attackerTeam == teamAllies) &&
		(targetTeam == teamAxis || targetTeam == teamAllies) {
		if attackerTeam == targetTeam {
			return "Teammate", true
		}
		return "Enemy", true
	}

	return "", false
}

func (p *parser) writeKill(attacker int, output killOutput) {
	if !p.options.multiKillsOnly {
		fmt.Fprintln(p.out, output.line)
		return
	}

	p.flushExpiredMultiKillWindows(output.serverTime)

	window := p.pendingKills[attacker]
	if len(window.outputs) == 0 {
		p.pendingKills[attacker] = multiKillWindow{
			outputs: []killOutput{output},
		}
		return
	}

	lastOutput := window.outputs[len(window.outputs)-1]
	if output.serverTime-lastOutput.serverTime > 3000 {
		p.pendingKills[attacker] = multiKillWindow{
			outputs: []killOutput{output},
		}
		return
	}

	window.outputs = append(window.outputs, output)
	p.pendingKills[attacker] = window
}

func (p *parser) flushExpiredMultiKillWindows(currentTime int) {
	type windowEntry struct {
		attacker int
		window   multiKillWindow
	}

	expired := make([]windowEntry, 0, len(p.pendingKills))
	for attacker, window := range p.pendingKills {
		if len(window.outputs) == 0 {
			delete(p.pendingKills, attacker)
			continue
		}
		lastOutput := window.outputs[len(window.outputs)-1]
		if currentTime-lastOutput.serverTime > 3000 {
			expired = append(expired, windowEntry{
				attacker: attacker,
				window:   window,
			})
		}
	}

	sort.Slice(expired, func(i, j int) bool {
		left := expired[i].window.outputs[0]
		right := expired[j].window.outputs[0]
		if left.serverTime != right.serverTime {
			return left.serverTime < right.serverTime
		}
		return expired[i].attacker < expired[j].attacker
	})

	for _, entry := range expired {
		p.emitMultiKillWindow(entry.window)
		delete(p.pendingKills, entry.attacker)
	}
}

func (p *parser) flushAllMultiKillWindows() {
	type windowEntry struct {
		attacker int
		window   multiKillWindow
	}

	windows := make([]windowEntry, 0, len(p.pendingKills))
	for attacker, window := range p.pendingKills {
		if len(window.outputs) == 0 {
			continue
		}
		windows = append(windows, windowEntry{
			attacker: attacker,
			window:   window,
		})
	}

	sort.Slice(windows, func(i, j int) bool {
		left := windows[i].window.outputs[0]
		right := windows[j].window.outputs[0]
		if left.serverTime != right.serverTime {
			return left.serverTime < right.serverTime
		}
		return windows[i].attacker < windows[j].attacker
	})

	for _, entry := range windows {
		p.emitMultiKillWindow(entry.window)
		delete(p.pendingKills, entry.attacker)
	}
}

func (p *parser) emitMultiKillWindow(window multiKillWindow) {
	if len(window.outputs) < 2 {
		return
	}

	if p.printedMultiKillWindow {
		fmt.Fprintln(p.out, "---")
	}

	for _, output := range window.outputs {
		fmt.Fprintln(p.out, output.line)
	}

	p.printedMultiKillWindow = true
}

func (p *parser) handleServerCommand(command string) {
	tokens := tokenizeCommand(command)
	if len(tokens) == 0 {
		return
	}

	switch tokens[0] {
	case "bcs0":
		if len(tokens) >= 3 {
			p.bigConfig = fmt.Sprintf("cs %s \"%s", tokens[1], tokens[2])
		}
	case "bcs1":
		if len(tokens) >= 3 && p.bigConfig != "" {
			p.bigConfig += tokens[2]
		}
	case "bcs2":
		if len(tokens) >= 3 && p.bigConfig != "" {
			complete := p.bigConfig + tokens[2] + `"`
			p.bigConfig = ""
			p.handleServerCommand(complete)
		}
	case "cs":
		if len(tokens) >= 3 {
			index, err := strconv.Atoi(tokens[1])
			if err == nil && index >= 0 && index < maxConfigStrings {
				p.setConfigString(index, tokens[2])
			}
		}
	case "map_restart":
		p.flushAllMultiKillWindows()
		p.activeTempEntities = make(map[int]struct{})
		p.pendingKills = make(map[int]multiKillWindow)
	}
}

func (p *parser) setConfigString(index int, value string) {
	p.configStrings[index] = value

	switch {
	case index == csLevelStartTime:
		startTime, err := strconv.Atoi(value)
		if err == nil {
			p.levelStartTime = startTime
		}
	case index >= csPlayers && index < csPlayers+maxClients:
		p.players[index-csPlayers] = parsePlayerInfo(value)
	}
}

func parsePlayerInfo(config string) playerInfo {
	if config == "" {
		return playerInfo{}
	}

	return playerInfo{
		Name: cleanName(infoValue(config, "n")),
		Team: atoiDefault(infoValue(config, "t"), teamFree),
	}
}

func (p *parser) playerName(clientNum int) string {
	if clientNum >= 0 && clientNum < maxClients {
		if name := p.players[clientNum].Name; name != "" {
			return name
		}
	}

	return fmt.Sprintf("Client#%d", clientNum)
}

func infoValue(info, key string) string {
	if info == "" {
		return ""
	}

	i := 0
	if info[0] == '\\' {
		i = 1
	}

	for i < len(info) {
		keyStart := i
		for i < len(info) && info[i] != '\\' {
			i++
		}
		currentKey := info[keyStart:i]
		if i < len(info) {
			i++
		}

		valueStart := i
		for i < len(info) && info[i] != '\\' {
			i++
		}
		currentValue := info[valueStart:i]
		if i < len(info) {
			i++
		}

		if currentKey == key {
			return currentValue
		}
	}

	return ""
}

func cleanName(name string) string {
	if name == "" {
		return ""
	}

	var builder strings.Builder
	builder.Grow(len(name))

	for i := 0; i < len(name); i++ {
		if isColorCode(name, i) {
			i++
			continue
		}

		if name[i] >= 0x20 && name[i] <= 0x7e {
			builder.WriteByte(name[i])
		}
	}

	return builder.String()
}

func isColorCode(s string, index int) bool {
	if index+1 >= len(s) || s[index] != '^' {
		return false
	}
	if s[index+1] == '^' {
		return false
	}

	return s[index+1] > 0x20 && s[index+1] < 0x7f
}

func tokenizeCommand(command string) []string {
	tokens := make([]string, 0, 4)

	for i := 0; i < len(command); {
		for i < len(command) && command[i] <= ' ' {
			i++
		}
		if i >= len(command) {
			break
		}

		if command[i] == '"' {
			i++
			start := i
			for i < len(command) && command[i] != '"' {
				i++
			}
			tokens = append(tokens, command[start:i])
			if i < len(command) {
				i++
			}
			continue
		}

		start := i
		for i < len(command) && command[i] > ' ' {
			i++
		}
		tokens = append(tokens, command[start:i])
	}

	return tokens
}

func atoiDefault(value string, fallback int) int {
	if value == "" {
		return fallback
	}

	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}

	return parsed
}

func formatMatchTimestamp(milliseconds int) string {
	if milliseconds < 0 {
		milliseconds = 0
	}

	minutes := milliseconds / 60000
	seconds := (milliseconds / 1000) % 60
	centiseconds := (milliseconds % 1000) / 10

	return fmt.Sprintf("%02d:%02d.%02d", minutes, seconds, centiseconds)
}

func mathFloat32bits(value float32) uint32 {
	return math.Float32bits(value)
}
