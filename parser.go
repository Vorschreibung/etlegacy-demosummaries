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
	multiKillMin         int
	multiKillHeadshotMin int
	killsOnlyFrom        string
}

func (o parserOptions) multiKillThreshold() int {
	if o.multiKillHeadshotMin > 0 {
		return o.multiKillHeadshotMin
	}
	return o.multiKillMin
}

func (o parserOptions) multiKillEnabled() bool {
	return o.multiKillThreshold() > 0
}

func (o parserOptions) multiKillHeadshotsOnly() bool {
	return o.multiKillHeadshotMin > 0
}

type killOutput struct {
	serverTime   int
	matchTimeMs  int
	attackerNum  int
	attackerName string
	line         string
}

const (
	killLabel         = "Kill"
	paddedKillLabel   = "Kill        "
	headshotKillLabel = "HeadshotKill"
)

type multiKillWindow struct {
	outputs []killOutput
}

type windowEntry struct {
	attacker int
	window   *multiKillWindow
}

type parser struct {
	out  io.Writer
	warn io.Writer

	options parserOptions

	demoPath                           string
	warnedAboutObituaryHeadshotSupport bool

	serverCommandSequence int
	clientNum             int
	checksumFeed          int
	levelStartTime        int
	bigConfig             string

	configStrings [maxConfigStrings]string
	players       [maxClients]playerInfo

	baselines        [maxGentities]entityState
	parseEntities    [maxParseEntities]entityState
	parseEntitiesNum int
	snapshots        [packetBackup]snapshotState

	// Temp event entities stay around for a few snapshots. Track them in fixed
	// arrays keyed by entity number to avoid per-snapshot map churn.
	activeTempEntities         [maxGentities]bool
	activeTempEntityNumbers    []int
	presentTempEntityStamp     [maxGentities]uint32
	presentTempEntityGen       uint32
	pendingKills               [maxClients]multiKillWindow
	pendingKillActive          [maxClients]bool
	multiKillWindowSortScratch []windowEntry

	onSnapshot        func(*parser, *snapshotState) error
	onMultiKillWindow func(multiKillWindow)

	printedMultiKillWindow bool
}

func newParser(out io.Writer, options parserOptions) *parser {
	return newParserWithWarning(out, io.Discard, options)
}

func newParserWithWarning(out io.Writer, warn io.Writer, options parserOptions) *parser {
	p := &parser{
		out:     out,
		warn:    warn,
		options: options,
	}
	p.resetState()

	return p
}

func (p *parser) resetState() {
	p.serverCommandSequence = 0
	p.clientNum = 0
	p.checksumFeed = 0
	p.levelStartTime = 0
	p.bigConfig = ""
	p.configStrings = [maxConfigStrings]string{}
	p.players = [maxClients]playerInfo{}
	p.baselines = [maxGentities]entityState{}
	p.parseEntities = [maxParseEntities]entityState{}
	p.parseEntitiesNum = 0
	p.snapshots = [packetBackup]snapshotState{}
	p.activeTempEntities = [maxGentities]bool{}
	p.activeTempEntityNumbers = p.activeTempEntityNumbers[:0]
	p.presentTempEntityStamp = [maxGentities]uint32{}
	p.presentTempEntityGen = 0
	p.pendingKills = [maxClients]multiKillWindow{}
	p.pendingKillActive = [maxClients]bool{}
	p.multiKillWindowSortScratch = p.multiKillWindowSortScratch[:0]
	p.printedMultiKillWindow = false
}

func (p *parser) parseFile(path string) error {
	p.demoPath = path

	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()

	var header [8]byte
	packet := make([]byte, maxMsgLen)

	for {
		if _, err := io.ReadFull(file, header[:]); err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
				p.flushAllMultiKillWindows()
				return nil
			}
			return err
		}

		sequence := int32(binary.LittleEndian.Uint32(header[:4]))
		size := int32(binary.LittleEndian.Uint32(header[4:]))

		if size == -1 {
			p.flushAllMultiKillWindows()
			return nil
		}
		if size < 0 || size > maxMsgLen {
			return fmt.Errorf("invalid packet size %d", size)
		}

		packetData := packet[:size]
		if _, err := io.ReadFull(file, packetData); err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
				p.flushAllMultiKillWindows()
				return nil
			}
			return err
		}

		if err := p.parsePacket(int(sequence), packetData); err != nil {
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

	clientNum, err := msg.readLong()
	if err != nil {
		return fmt.Errorf("read gamestate client num: %w", err)
	}
	checksumFeed, err := msg.readLong()
	if err != nil {
		return fmt.Errorf("read gamestate checksum feed: %w", err)
	}
	p.clientNum = clientNum
	p.checksumFeed = checksumFeed

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

	snapFlags, err := msg.readByte()
	if err != nil {
		return fmt.Errorf("read snapshot flags: %w", err)
	}
	snap.SnapFlags = snapFlags

	areaMaskLen, err := msg.readByte()
	if err != nil {
		return fmt.Errorf("read areamask length: %w", err)
	}
	if areaMaskLen < 0 {
		return fmt.Errorf("invalid areamask length %d", areaMaskLen)
	}
	snap.AreaMask = make([]byte, areaMaskLen)
	for i := range snap.AreaMask {
		value, err := msg.readByte()
		if err != nil {
			return fmt.Errorf("read areamask: %w", err)
		}
		snap.AreaMask[i] = byte(value)
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
	if p.onSnapshot != nil {
		if err := p.onSnapshot(p, &snap); err != nil {
			return err
		}
	}
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
	p.presentTempEntityGen++
	if p.presentTempEntityGen == 0 {
		p.presentTempEntityStamp = [maxGentities]uint32{}
		p.presentTempEntityGen = 1
	}
	presentGen := p.presentTempEntityGen

	for i := 0; i < snapshot.NumEntities; i++ {
		state := &p.parseEntities[(snapshot.ParseEntitiesNum+i)&(maxParseEntities-1)]
		eType := int(state.Fields[fieldEntityType])
		if eType < etEvents {
			continue
		}

		number := state.Number
		p.presentTempEntityStamp[number] = presentGen
		if p.activeTempEntities[number] {
			continue
		}

		p.activeTempEntities[number] = true
		p.activeTempEntityNumbers = append(p.activeTempEntityNumbers, number)

		if eType-etEvents == evObituary {
			p.emitKill(snapshot.ServerTime, state)
		}
	}

	active := p.activeTempEntityNumbers[:0]
	for _, number := range p.activeTempEntityNumbers {
		if p.presentTempEntityStamp[number] == presentGen {
			active = append(active, number)
			continue
		}

		p.activeTempEntities[number] = false
	}
	p.activeTempEntityNumbers = active
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
	if p.options.multiKillEnabled() {
		p.flushExpiredMultiKillWindows(serverTime)
	}

	relation, ok := p.killRelation(attacker, target)
	if !ok {
		return
	}
	headshot := state.Fields[fieldLoopSound] != 0
	if p.options.multiKillEnabled() && relation == "Self" {
		return
	}
	if p.options.multiKillHeadshotsOnly() && !headshot {
		return
	}

	attackerName := p.playerName(attacker)
	if p.options.killsOnlyFrom != "" && attackerName != p.options.killsOnlyFrom {
		return
	}

	weaponName := obituaryWeaponName(int(state.Fields[fieldWeapon]))

	p.writeKill(attacker, killOutput{
		serverTime:   serverTime,
		matchTimeMs:  serverTime - p.levelStartTime,
		attackerNum:  attacker,
		attackerName: attackerName,
		line: fmt.Sprintf("%s ; %s ; %s ; %s ; %s ; %s",
			timestamp,
			obituaryKillLabel(headshot),
			attackerName,
			weaponName,
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
	if !p.options.multiKillEnabled() {
		fmt.Fprintln(p.out, output.line)
		return
	}

	window := &p.pendingKills[attacker]
	if !p.pendingKillActive[attacker] || len(window.outputs) == 0 {
		p.pendingKillActive[attacker] = true
		window.outputs = append(window.outputs[:0], output)
		return
	}

	lastOutput := window.outputs[len(window.outputs)-1]
	if output.serverTime-lastOutput.serverTime > 3000 {
		window.outputs = append(window.outputs[:0], output)
		return
	}

	window.outputs = append(window.outputs, output)
}

func (p *parser) flushExpiredMultiKillWindows(currentTime int) {
	expired := p.multiKillWindowSortScratch[:0]
	for attacker := 0; attacker < maxClients; attacker++ {
		if !p.pendingKillActive[attacker] {
			continue
		}

		window := &p.pendingKills[attacker]
		if len(window.outputs) == 0 {
			p.pendingKillActive[attacker] = false
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
		p.emitMultiKillWindow(*entry.window)
		p.pendingKillActive[entry.attacker] = false
		entry.window.outputs = entry.window.outputs[:0]
	}

	p.multiKillWindowSortScratch = expired[:0]
}

func (p *parser) flushAllMultiKillWindows() {
	windows := p.multiKillWindowSortScratch[:0]
	for attacker := 0; attacker < maxClients; attacker++ {
		if !p.pendingKillActive[attacker] {
			continue
		}

		window := &p.pendingKills[attacker]
		if len(window.outputs) == 0 {
			p.pendingKillActive[attacker] = false
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
		p.emitMultiKillWindow(*entry.window)
		p.pendingKillActive[entry.attacker] = false
		entry.window.outputs = entry.window.outputs[:0]
	}

	p.multiKillWindowSortScratch = windows[:0]
}

func (p *parser) emitMultiKillWindow(window multiKillWindow) {
	if len(window.outputs) < p.options.multiKillThreshold() {
		return
	}

	if p.onMultiKillWindow != nil {
		p.onMultiKillWindow(window)
	}

	if p.printedMultiKillWindow {
		fmt.Fprintln(p.out, "---")
	}

	for _, output := range window.outputs {
		fmt.Fprintln(p.out, output.line)
	}

	p.printedMultiKillWindow = true
}

func obituaryWeaponName(weapon int) string {
	if weapon < 0 || weapon >= len(weaponNames) {
		return "UNKNOWN"
	}

	return weaponNames[weapon]
}

func obituaryKillLabel(headshot bool) string {
	if headshot {
		return headshotKillLabel
	}

	return paddedKillLabel
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
		p.activeTempEntities = [maxGentities]bool{}
		p.activeTempEntityNumbers = p.activeTempEntityNumbers[:0]
		p.pendingKillActive = [maxClients]bool{}
	}
}

func (p *parser) setConfigString(index int, value string) {
	p.configStrings[index] = value

	switch {
	case index == csServerInfo || index == csSystemInfo || index == csVersionInfo:
		p.maybeWarnAboutObituaryHeadshotSupport()
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

type demoVersion struct {
	raw            string
	major          int
	minor          int
	patch          int
	commitCount    int
	hasCommitCount bool
}

const obituaryHeadshotSupportCommitCount = 173

func (p *parser) maybeWarnAboutObituaryHeadshotSupport() {
	if p.warn == nil || p.warnedAboutObituaryHeadshotSupport {
		return
	}

	version, ok := parseDemoVersionFromConfigStrings(
		p.configStrings[csServerInfo],
		p.configStrings[csSystemInfo],
	)
	if !ok || !version.predatesObituaryHeadshotSupport() {
		return
	}

	// Exact obituary headshot flags were added in v2.83.2-173-g076d72559.
	fmt.Fprintf(
		p.warn,
		"WARNING: %s predates obituary headshot support (%s); exact HeadshotKill output is unavailable for this demo\n",
		p.demoPath,
		version.raw,
	)
	p.warnedAboutObituaryHeadshotSupport = true
}

func parseDemoVersionFromConfigStrings(serverInfo, systemInfo string) (demoVersion, bool) {
	if version := infoValue(serverInfo, "mod_version"); version != "" {
		return parseDemoVersionString(version)
	}

	pakNames := infoValue(systemInfo, "sv_referencedPakNames")
	if pakNames == "" {
		return demoVersion{}, false
	}

	slash := strings.IndexByte(pakNames, '/')
	if slash < 0 || slash+1 >= len(pakNames) {
		return demoVersion{}, false
	}

	return parseDemoVersionString(pakNames[slash+1:])
}

func parseDemoVersionString(raw string) (demoVersion, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return demoVersion{}, false
	}

	start := 0
	for start < len(raw) && (raw[start] < '0' || raw[start] > '9') {
		start++
	}
	if start == len(raw) {
		return demoVersion{}, false
	}

	version := demoVersion{raw: raw}
	remainder := raw[start:]
	rest, ok := parseVersionComponent(remainder, &version.major)
	if !ok || !strings.HasPrefix(rest, ".") {
		return demoVersion{}, false
	}
	rest, ok = parseVersionComponent(rest[1:], &version.minor)
	if !ok || !strings.HasPrefix(rest, ".") {
		return demoVersion{}, false
	}
	rest, ok = parseVersionComponent(rest[1:], &version.patch)
	if !ok {
		return demoVersion{}, false
	}

	if strings.HasPrefix(rest, "-") {
		if parsedRest, parsed := parseVersionComponent(rest[1:], &version.commitCount); parsed {
			version.hasCommitCount = true
			rest = parsedRest
		}
	}

	return version, true
}

func parseVersionComponent(input string, out *int) (string, bool) {
	end := 0
	for end < len(input) && input[end] >= '0' && input[end] <= '9' {
		end++
	}
	if end == 0 {
		return input, false
	}

	value, err := strconv.Atoi(input[:end])
	if err != nil {
		return input, false
	}

	*out = value
	return input[end:], true
}

func (v demoVersion) predatesObituaryHeadshotSupport() bool {
	switch {
	case v.major < 2:
		return true
	case v.major > 2:
		return false
	case v.minor < 83:
		return true
	case v.minor > 83:
		return false
	case v.patch < 2:
		return true
	case v.patch > 2:
		return false
	case !v.hasCommitCount:
		return true
	default:
		return v.commitCount < obituaryHeadshotSupportCommitCount
	}
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
