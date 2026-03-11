package main

import (
	"fmt"
	"io"
	"math"
)

const (
	hmax         = 256
	nytSymbol    = hmax
	internalNode = hmax + 1
)

type huffHead struct {
	node *huffNode
}

type huffNode struct {
	left   *huffNode
	right  *huffNode
	parent *huffNode

	next *huffNode
	prev *huffNode

	head *huffHead

	weight int
	symbol int
}

type huffTree struct {
	blocNode int
	nextHead int

	tree  *huffNode
	lhead *huffNode
	ltail *huffNode

	loc       [hmax + 1]*huffNode
	nodeList  [768]huffNode
	headList  [768]huffHead
	freeHeads []*huffHead
}

var messageHuffmanRoot = buildMessageHuffmanTree()

// msgReader mirrors MSG_ReadBits for in-band server messages.
type msgReader struct {
	data      []byte
	cursize   int
	bit       int
	readcount int
}

func newMsgReader(data []byte) *msgReader {
	return &msgReader{
		data:    data,
		cursize: len(data),
	}
}

func buildMessageHuffmanTree() *huffNode {
	tree := newHuffTree()

	for symbol, count := range msgHData {
		for i := 0; i < count; i++ {
			tree.addRef(byte(symbol))
		}
	}

	return tree.tree
}

func newHuffTree() *huffTree {
	tree := &huffTree{}
	root := tree.newNode()
	root.symbol = nytSymbol
	tree.tree = root
	tree.lhead = root
	tree.ltail = root
	tree.loc[nytSymbol] = root

	return tree
}

func (h *huffTree) newNode() *huffNode {
	node := &h.nodeList[h.blocNode]
	h.blocNode++
	*node = huffNode{}
	return node
}

func (h *huffTree) getHead() *huffHead {
	if n := len(h.freeHeads); n > 0 {
		head := h.freeHeads[n-1]
		h.freeHeads = h.freeHeads[:n-1]
		head.node = nil
		return head
	}

	head := &h.headList[h.nextHead]
	h.nextHead++
	head.node = nil

	return head
}

func (h *huffTree) freeHead(head *huffHead) {
	if head == nil {
		return
	}

	head.node = nil
	h.freeHeads = append(h.freeHeads, head)
}

func (h *huffTree) swap(node1, node2 *huffNode) {
	parent1 := node1.parent
	parent2 := node2.parent

	if parent1 != nil {
		if parent1.left == node1 {
			parent1.left = node2
		} else {
			parent1.right = node2
		}
	} else {
		h.tree = node2
	}

	if parent2 != nil {
		if parent2.left == node2 {
			parent2.left = node1
		} else {
			parent2.right = node1
		}
	} else {
		h.tree = node1
	}

	node1.parent = parent2
	node2.parent = parent1
}

func swapList(node1, node2 *huffNode) {
	next1 := node1.next

	node1.next = node2.next
	node2.next = next1

	prev1 := node1.prev
	node1.prev = node2.prev
	node2.prev = prev1

	if node1.next == node1 {
		node1.next = node2
	}
	if node2.next == node2 {
		node2.next = node1
	}
	if node1.next != nil {
		node1.next.prev = node1
	}
	if node2.next != nil {
		node2.next.prev = node2
	}
	if node1.prev != nil {
		node1.prev.next = node1
	}
	if node2.prev != nil {
		node2.prev.next = node2
	}
}

func (h *huffTree) increment(node *huffNode) {
	if node == nil {
		return
	}

	if node.next != nil && node.next.weight == node.weight {
		leader := node.head.node
		if leader != node.parent {
			h.swap(leader, node)
		}
		swapList(leader, node)
	}

	if node.prev != nil && node.prev.weight == node.weight {
		node.head.node = node.prev
	} else {
		node.head.node = nil
		h.freeHead(node.head)
	}

	node.weight++

	if node.next != nil && node.next.weight == node.weight {
		node.head = node.next.head
	} else {
		node.head = h.getHead()
		node.head.node = node
	}

	if node.parent != nil {
		h.increment(node.parent)
		if node.prev == node.parent {
			swapList(node, node.parent)
			if node.head.node == node {
				node.head.node = node.parent
			}
		}
	}
}

func (h *huffTree) addRef(ch byte) {
	if h.loc[ch] == nil {
		leaf := h.newNode()
		internal := h.newNode()
		oldNYTParent := h.lhead.parent

		internal.symbol = internalNode
		internal.weight = 1
		internal.next = h.lhead.next
		if h.lhead.next != nil {
			h.lhead.next.prev = internal
			if h.lhead.next.weight == 1 {
				internal.head = h.lhead.next.head
			} else {
				internal.head = h.getHead()
				internal.head.node = internal
			}
		} else {
			internal.head = h.getHead()
			internal.head.node = internal
		}
		h.lhead.next = internal
		internal.prev = h.lhead

		leaf.symbol = int(ch)
		leaf.weight = 1
		leaf.next = h.lhead.next
		if h.lhead.next != nil {
			h.lhead.next.prev = leaf
			if h.lhead.next.weight == 1 {
				leaf.head = h.lhead.next.head
			} else {
				leaf.head = h.getHead()
				leaf.head.node = internal
			}
		} else {
			leaf.head = h.getHead()
			leaf.head.node = leaf
		}
		h.lhead.next = leaf
		leaf.prev = h.lhead

		if oldNYTParent != nil {
			if oldNYTParent.left == h.lhead {
				oldNYTParent.left = internal
			} else {
				oldNYTParent.right = internal
			}
		} else {
			h.tree = internal
		}

		internal.right = leaf
		internal.left = h.lhead
		internal.parent = oldNYTParent

		h.lhead.parent = internal
		leaf.parent = internal

		h.loc[ch] = leaf
		h.increment(internal.parent)

		return
	}

	h.increment(h.loc[ch])
}

func (r *msgReader) getBit() (int, error) {
	maxBits := r.cursize << 3
	if r.bit >= maxBits {
		r.readcount = r.cursize + 1
		return 0, io.ErrUnexpectedEOF
	}

	value := int((r.data[r.bit>>3] >> (r.bit & 7)) & 0x1)
	r.bit++

	return value, nil
}

func (r *msgReader) readHuffByte() (int, error) {
	maxBits := r.cursize << 3
	node := messageHuffmanRoot

	for node != nil && node.symbol == internalNode {
		if r.bit >= maxBits {
			r.readcount = r.cursize + 1
			return 0, io.ErrUnexpectedEOF
		}

		bit := int((r.data[r.bit>>3] >> (r.bit & 7)) & 0x1)
		r.bit++
		if bit != 0 {
			node = node.right
		} else {
			node = node.left
		}
	}

	if node == nil {
		return 0, fmt.Errorf("illegal huffman tree")
	}

	return node.symbol, nil
}

func (r *msgReader) readBits(bits int) (int32, error) {
	if bits == 0 {
		return 0, nil
	}

	signExtend := false
	if bits < 0 {
		bits = -bits
		signExtend = true
	}

	origBits := bits
	lowBits := bits & 7
	var value uint32

	if lowBits != 0 {
		if r.bit+lowBits > r.cursize<<3 {
			r.readcount = r.cursize + 1
			return 0, io.ErrUnexpectedEOF
		}

		for i := 0; i < lowBits; i++ {
			bit, err := r.getBit()
			if err != nil {
				return 0, err
			}
			value |= uint32(bit) << uint(i)
		}
		bits -= lowBits
	}

	if bits != 0 {
		for i := 0; i < bits; i += 8 {
			ch, err := r.readHuffByte()
			if err != nil {
				return 0, err
			}
			value |= uint32(ch) << uint(i+lowBits)
		}
	}

	r.readcount = (r.bit >> 3) + 1

	if signExtend && origBits > 0 && origBits < 32 {
		signMask := uint32(1) << uint(origBits-1)
		if value&signMask != 0 {
			value |= ^((uint32(1) << uint(origBits)) - 1)
		}
	}

	return int32(value), nil
}

func (r *msgReader) readByte() (int, error) {
	value, err := r.readBits(8)
	return int(value), err
}

func (r *msgReader) readShort() (int, error) {
	value, err := r.readBits(16)
	return int(value), err
}

func (r *msgReader) readLong() (int, error) {
	value, err := r.readBits(32)
	return int(value), err
}

func (r *msgReader) readString() string {
	return r.readCString(maxStringChars)
}

func (r *msgReader) readBigString() string {
	return r.readCString(bigInfoString)
}

func (r *msgReader) readCString(limit int) string {
	buf := make([]byte, 0, limit)

	for {
		c, err := r.readByte()
		if err != nil || c == 0 {
			break
		}
		if len(buf) >= limit-1 {
			continue
		}
		buf = append(buf, byte(c))
	}

	return string(buf)
}

func (r *msgReader) skipBytes(count int) error {
	for i := 0; i < count; i++ {
		if _, err := r.readByte(); err != nil {
			return err
		}
	}

	return nil
}

func floatBitsFromInt(value int32) int32 {
	return int32(math.Float32bits(float32(value)))
}

var msgHData = [256]int{
	250315, 41193, 6292, 7106, 3730, 3750, 6110, 23283, 33317, 6950, 7838, 9714, 9257, 17259, 3949, 1778,
	8288, 1604, 1590, 1663, 1100, 1213, 1238, 1134, 1749, 1059, 1246, 1149, 1273, 4486, 2805, 3472,
	21819, 1159, 1670, 1066, 1043, 1012, 1053, 1070, 1726, 888, 1180, 850, 960, 780, 1752, 3296,
	10630, 4514, 5881, 2685, 4650, 3837, 2093, 1867, 2584, 1949, 1972, 940, 1134, 1788, 1670, 1206,
	5719, 6128, 7222, 6654, 3710, 3795, 1492, 1524, 2215, 1140, 1355, 971, 2180, 1248, 1328, 1195,
	1770, 1078, 1264, 1266, 1168, 965, 1155, 1186, 1347, 1228, 1529, 1600, 2617, 2048, 2546, 3275,
	2410, 3585, 2504, 2800, 2675, 6146, 3663, 2840, 14253, 3164, 2221, 1687, 3208, 2739, 3512, 4796,
	4091, 3515, 5288, 4016, 7937, 6031, 5360, 3924, 4892, 3743, 4566, 4807, 5852, 6400, 6225, 8291,
	23243, 7838, 7073, 8935, 5437, 4483, 3641, 5256, 5312, 5328, 5370, 3492, 2458, 1694, 1821, 2121,
	1916, 1149, 1516, 1367, 1236, 1029, 1258, 1104, 1245, 1006, 1149, 1025, 1241, 952, 1287, 997,
	1713, 1009, 1187, 879, 1099, 929, 1078, 951, 1656, 930, 1153, 1030, 1262, 1062, 1214, 1060,
	1621, 930, 1106, 912, 1034, 892, 1158, 990, 1175, 850, 1121, 903, 1087, 920, 1144, 1056,
	3462, 2240, 4397, 12136, 7758, 1345, 1307, 3278, 1950, 886, 1023, 1112, 1077, 1042, 1061, 1071,
	1484, 1001, 1096, 915, 1052, 995, 1070, 876, 1111, 851, 1059, 805, 1112, 923, 1103, 817,
	1899, 1872, 976, 841, 1127, 956, 1159, 950, 7791, 954, 1289, 933, 1127, 3207, 1020, 927,
	1355, 768, 1040, 745, 952, 805, 1073, 740, 1013, 805, 1008, 796, 996, 1057, 11457, 13504,
}
