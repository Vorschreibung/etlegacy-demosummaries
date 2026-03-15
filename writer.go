package main

import (
	"encoding/binary"
	"fmt"
	"io"
	"math"
)

var messageHuffmanCodes = buildMessageHuffmanCodes(messageHuffmanRoot)

type msgWriter struct {
	data []byte
	bit  int
}

type demoFileWriter struct {
	writer       io.Writer
	nextSequence int
}

func newMsgWriter(maxSize int) *msgWriter {
	return &msgWriter{
		data: make([]byte, maxSize),
	}
}

func buildMessageHuffmanCodes(root *huffNode) [256][]byte {
	var codes [256][]byte
	var walk func(*huffNode, []byte)

	walk = func(node *huffNode, path []byte) {
		if node == nil {
			return
		}
		if node.symbol == internalNode {
			walk(node.left, append(path, 0))
			walk(node.right, append(path, 1))
			return
		}
		if node.symbol >= 0 && node.symbol < len(codes) {
			codes[node.symbol] = append([]byte(nil), path...)
		}
	}

	walk(root, nil)

	return codes
}

func newDemoFileWriter(writer io.Writer) *demoFileWriter {
	return &demoFileWriter{writer: writer}
}

func (w *msgWriter) writeBit(value byte) error {
	if w.bit >= len(w.data)<<3 {
		return fmt.Errorf("message exceeds %d bytes", len(w.data))
	}

	byteIndex := w.bit >> 3
	bitIndex := uint(w.bit & 7)
	if bitIndex == 0 {
		w.data[byteIndex] = 0
	}
	if value != 0 {
		w.data[byteIndex] |= 1 << bitIndex
	}
	w.bit++

	return nil
}

func (w *msgWriter) writeHuffByte(value byte) error {
	code := messageHuffmanCodes[value]
	if len(code) == 0 {
		return fmt.Errorf("missing huffman code for byte %d", value)
	}

	for _, bit := range code {
		if err := w.writeBit(bit); err != nil {
			return err
		}
	}

	return nil
}

func (w *msgWriter) writeBits(value int32, bits int) error {
	if bits == 0 || bits < -31 || bits > 32 {
		return fmt.Errorf("bad bit width %d", bits)
	}
	if bits < 0 {
		bits = -bits
	}

	unsigned := uint32(value)
	if bits < 32 {
		unsigned &= (uint32(1) << uint(bits)) - 1
	}

	lowBits := bits & 7
	for i := 0; i < lowBits; i++ {
		if err := w.writeBit(byte(unsigned & 0x1)); err != nil {
			return err
		}
		unsigned >>= 1
	}

	for remaining := bits - lowBits; remaining > 0; remaining -= 8 {
		if err := w.writeHuffByte(byte(unsigned & 0xff)); err != nil {
			return err
		}
		unsigned >>= 8
	}

	return nil
}

func (w *msgWriter) writeByte(value int) error {
	return w.writeBits(int32(value), 8)
}

func (w *msgWriter) writeShort(value int) error {
	return w.writeBits(int32(value), 16)
}

func (w *msgWriter) writeLong(value int) error {
	return w.writeBits(int32(value), 32)
}

func (w *msgWriter) writeData(data []byte) error {
	for _, value := range data {
		if err := w.writeByte(int(value)); err != nil {
			return err
		}
	}

	return nil
}

func (w *msgWriter) writeString(value string) error {
	return w.writeCString(value)
}

func (w *msgWriter) writeBigString(value string) error {
	return w.writeCString(value)
}

func (w *msgWriter) writeCString(value string) error {
	if err := w.writeData([]byte(value)); err != nil {
		return err
	}

	return w.writeByte(0)
}

func (w *msgWriter) bytes() []byte {
	if w.bit == 0 {
		return nil
	}

	return w.data[:(w.bit>>3)+1]
}

func writeDeltaEntity(msg *msgWriter, from *entityState, to *entityState, force bool) error {
	if to == nil {
		if from == nil {
			return nil
		}
		if err := msg.writeBits(int32(from.Number), gentityNumBits); err != nil {
			return err
		}
		return msg.writeBits(1, 1)
	}

	if to.Number < 0 || to.Number >= maxGentities {
		return fmt.Errorf("bad entity number %d", to.Number)
	}

	lastChanged := 0
	for i := range to.Fields {
		if from.Fields[i] != to.Fields[i] {
			lastChanged = i + 1
		}
	}

	if lastChanged == 0 {
		if !force {
			return nil
		}
		if err := msg.writeBits(int32(to.Number), gentityNumBits); err != nil {
			return err
		}
		if err := msg.writeBits(0, 1); err != nil {
			return err
		}
		return msg.writeBits(0, 1)
	}

	if err := msg.writeBits(int32(to.Number), gentityNumBits); err != nil {
		return err
	}
	if err := msg.writeBits(0, 1); err != nil {
		return err
	}
	if err := msg.writeBits(1, 1); err != nil {
		return err
	}
	if err := msg.writeByte(lastChanged); err != nil {
		return err
	}

	for i := 0; i < lastChanged; i++ {
		if from.Fields[i] == to.Fields[i] {
			if err := msg.writeBits(0, 1); err != nil {
				return err
			}
			continue
		}

		if err := msg.writeBits(1, 1); err != nil {
			return err
		}

		bits := entityFieldBits[i]
		if bits == 0 {
			fullFloat := math.Float32frombits(uint32(to.Fields[i]))
			trunc := int(fullFloat)
			if fullFloat == 0 {
				if err := msg.writeBits(0, 1); err != nil {
					return err
				}
				continue
			}

			if err := msg.writeBits(1, 1); err != nil {
				return err
			}
			if float32(trunc) == fullFloat &&
				trunc+floatIntBias >= 0 &&
				trunc+floatIntBias < (1<<floatIntBits) {
				if err := msg.writeBits(0, 1); err != nil {
					return err
				}
				if err := msg.writeBits(int32(trunc+floatIntBias), floatIntBits); err != nil {
					return err
				}
				continue
			}

			if err := msg.writeBits(1, 1); err != nil {
				return err
			}
			if err := msg.writeBits(to.Fields[i], 32); err != nil {
				return err
			}
			continue
		}

		if to.Fields[i] == 0 {
			if err := msg.writeBits(0, 1); err != nil {
				return err
			}
			continue
		}

		if err := msg.writeBits(1, 1); err != nil {
			return err
		}
		if err := msg.writeBits(to.Fields[i], bits); err != nil {
			return err
		}
	}

	return nil
}

func writeDeltaPlayerState(msg *msgWriter, from *playerState, to *playerState) error {
	var empty playerState
	if from == nil {
		from = &empty
	}

	lastChanged := 0
	for i := range to.Fields {
		if from.Fields[i] != to.Fields[i] {
			lastChanged = i + 1
		}
	}

	if err := msg.writeByte(lastChanged); err != nil {
		return err
	}

	for i := 0; i < lastChanged; i++ {
		if from.Fields[i] == to.Fields[i] {
			if err := msg.writeBits(0, 1); err != nil {
				return err
			}
			continue
		}

		if err := msg.writeBits(1, 1); err != nil {
			return err
		}

		bits := playerStateFieldBits[i]
		if bits == 0 {
			fullFloat := math.Float32frombits(uint32(to.Fields[i]))
			trunc := int(fullFloat)
			if float32(trunc) == fullFloat &&
				trunc+floatIntBias >= 0 &&
				trunc+floatIntBias < (1<<floatIntBits) {
				if err := msg.writeBits(0, 1); err != nil {
					return err
				}
				if err := msg.writeBits(int32(trunc+floatIntBias), floatIntBits); err != nil {
					return err
				}
				continue
			}

			if err := msg.writeBits(1, 1); err != nil {
				return err
			}
			if err := msg.writeBits(to.Fields[i], 32); err != nil {
				return err
			}
			continue
		}

		if err := msg.writeBits(to.Fields[i], bits); err != nil {
			return err
		}
	}

	statsBits := 0
	for i := range to.Stats {
		if to.Stats[i] != from.Stats[i] {
			statsBits |= 1 << i
		}
	}
	persistBits := 0
	for i := range to.Persistant {
		if to.Persistant[i] != from.Persistant[i] {
			persistBits |= 1 << i
		}
	}
	holdableBits := 0
	for i := range to.Holdable {
		if to.Holdable[i] != from.Holdable[i] {
			holdableBits |= 1 << i
		}
	}
	powerupBits := 0
	for i := range to.Powerups {
		if to.Powerups[i] != from.Powerups[i] {
			powerupBits |= 1 << i
		}
	}

	if statsBits != 0 || persistBits != 0 || holdableBits != 0 || powerupBits != 0 {
		if err := msg.writeBits(1, 1); err != nil {
			return err
		}
		if err := writeDeltaInt16Array(msg, statsBits, to.Stats[:]); err != nil {
			return err
		}
		if err := writeDeltaInt16Array(msg, persistBits, to.Persistant[:]); err != nil {
			return err
		}
		if err := writeDeltaInt16Array(msg, holdableBits, to.Holdable[:]); err != nil {
			return err
		}
		if err := writeDeltaInt32Array(msg, powerupBits, to.Powerups[:]); err != nil {
			return err
		}
	} else if err := msg.writeBits(0, 1); err != nil {
		return err
	}

	ammoBits := [maxAmmoGroups]int{}
	for group := 0; group < maxAmmoGroups; group++ {
		for i := 0; i < ammoPerGroup; i++ {
			index := i + (group * ammoPerGroup)
			if to.Ammo[index] != from.Ammo[index] {
				ammoBits[group] |= 1 << i
			}
		}
	}

	if ammoBits[0] != 0 || ammoBits[1] != 0 || ammoBits[2] != 0 || ammoBits[3] != 0 {
		if err := msg.writeBits(1, 1); err != nil {
			return err
		}
		for group := 0; group < maxAmmoGroups; group++ {
			if err := writeDeltaGroupedInt16(msg, ammoBits[group], to.Ammo[group*ammoPerGroup:(group+1)*ammoPerGroup]); err != nil {
				return err
			}
		}
	} else if err := msg.writeBits(0, 1); err != nil {
		return err
	}

	for group := 0; group < maxAmmoGroups; group++ {
		mask := 0
		for i := 0; i < ammoPerGroup; i++ {
			index := i + (group * ammoPerGroup)
			if to.AmmoClip[index] != from.AmmoClip[index] {
				mask |= 1 << i
			}
		}
		if err := writeDeltaGroupedInt16(msg, mask, to.AmmoClip[group*ammoPerGroup:(group+1)*ammoPerGroup]); err != nil {
			return err
		}
	}

	return nil
}

func writeDeltaInt16Array(msg *msgWriter, mask int, values []int32) error {
	if mask == 0 {
		return msg.writeBits(0, 1)
	}
	if err := msg.writeBits(1, 1); err != nil {
		return err
	}
	if err := msg.writeShort(mask); err != nil {
		return err
	}
	for i, value := range values {
		if mask&(1<<i) == 0 {
			continue
		}
		if err := msg.writeShort(int(value)); err != nil {
			return err
		}
	}
	return nil
}

func writeDeltaInt32Array(msg *msgWriter, mask int, values []int32) error {
	if mask == 0 {
		return msg.writeBits(0, 1)
	}
	if err := msg.writeBits(1, 1); err != nil {
		return err
	}
	if err := msg.writeShort(mask); err != nil {
		return err
	}
	for i, value := range values {
		if mask&(1<<i) == 0 {
			continue
		}
		if err := msg.writeLong(int(value)); err != nil {
			return err
		}
	}
	return nil
}

func writeDeltaGroupedInt16(msg *msgWriter, mask int, values []int32) error {
	if mask == 0 {
		return msg.writeBits(0, 1)
	}
	if err := msg.writeBits(1, 1); err != nil {
		return err
	}
	if err := msg.writeShort(mask); err != nil {
		return err
	}
	for i, value := range values {
		if mask&(1<<i) == 0 {
			continue
		}
		if err := msg.writeShort(int(value)); err != nil {
			return err
		}
	}
	return nil
}

func encodeGamestate(p *parser) ([]byte, error) {
	msg := newMsgWriter(maxMsgLen)
	if err := msg.writeLong(0); err != nil {
		return nil, err
	}
	if err := msg.writeByte(svcGamestate); err != nil {
		return nil, err
	}
	if err := msg.writeLong(p.serverCommandSequence); err != nil {
		return nil, err
	}

	for index, value := range p.configStrings {
		if value == "" {
			continue
		}
		if err := msg.writeByte(svcConfigstring); err != nil {
			return nil, err
		}
		if err := msg.writeShort(index); err != nil {
			return nil, err
		}
		if err := msg.writeBigString(value); err != nil {
			return nil, err
		}
	}

	var empty entityState
	for index := range p.baselines {
		baseline := &p.baselines[index]
		if baseline.Number == 0 {
			continue
		}
		if err := msg.writeByte(svcBaseline); err != nil {
			return nil, err
		}
		if err := writeDeltaEntity(msg, &empty, baseline, true); err != nil {
			return nil, err
		}
	}

	if err := msg.writeByte(svcEOF); err != nil {
		return nil, err
	}
	if err := msg.writeLong(p.clientNum); err != nil {
		return nil, err
	}
	if err := msg.writeLong(p.checksumFeed); err != nil {
		return nil, err
	}
	if err := msg.writeByte(svcEOF); err != nil {
		return nil, err
	}

	return msg.bytes(), nil
}

func encodeSnapshot(p *parser, snap *snapshotState) ([]byte, error) {
	msg := newMsgWriter(maxMsgLen)
	if err := msg.writeLong(0); err != nil {
		return nil, err
	}
	if err := msg.writeByte(svcSnapshot); err != nil {
		return nil, err
	}
	if err := msg.writeLong(snap.ServerTime); err != nil {
		return nil, err
	}
	if err := msg.writeByte(0); err != nil {
		return nil, err
	}
	if err := msg.writeByte(snap.SnapFlags); err != nil {
		return nil, err
	}
	if err := msg.writeByte(len(snap.AreaMask)); err != nil {
		return nil, err
	}
	if err := msg.writeData(snap.AreaMask); err != nil {
		return nil, err
	}
	if err := writeDeltaPlayerState(msg, nil, &snap.PlayerState); err != nil {
		return nil, err
	}

	for i := 0; i < snap.NumEntities; i++ {
		state := &p.parseEntities[(snap.ParseEntitiesNum+i)&(maxParseEntities-1)]
		if err := writeDeltaEntity(msg, &p.baselines[state.Number], state, true); err != nil {
			return nil, err
		}
	}
	if err := msg.writeBits(removedEntityNum, gentityNumBits); err != nil {
		return nil, err
	}
	if err := msg.writeByte(svcEOF); err != nil {
		return nil, err
	}

	return msg.bytes(), nil
}

func (w *demoFileWriter) writeGamestate(p *parser) error {
	payload, err := encodeGamestate(p)
	if err != nil {
		return err
	}
	if err := writeDemoPacket(w.writer, w.nextSequence, payload); err != nil {
		return err
	}
	w.nextSequence++
	return nil
}

func (w *demoFileWriter) writeSnapshot(p *parser, snap *snapshotState) error {
	payload, err := encodeSnapshot(p, snap)
	if err != nil {
		return err
	}
	if err := writeDemoPacket(w.writer, w.nextSequence, payload); err != nil {
		return err
	}
	w.nextSequence++
	return nil
}

func (w *demoFileWriter) writeEndMarker() error {
	return writeDemoEndMarker(w.writer)
}

func writeDemoPacket(writer io.Writer, sequence int, payload []byte) error {
	if err := binary.Write(writer, binary.LittleEndian, int32(sequence)); err != nil {
		return err
	}
	if err := binary.Write(writer, binary.LittleEndian, int32(len(payload))); err != nil {
		return err
	}
	_, err := writer.Write(payload)
	return err
}

func writeDemoEndMarker(writer io.Writer) error {
	if err := binary.Write(writer, binary.LittleEndian, int32(-1)); err != nil {
		return err
	}
	return binary.Write(writer, binary.LittleEndian, int32(-1))
}
