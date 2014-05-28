package gen

import (
	"bytes"
	"crypto/aes"
	"fmt"
	"math/rand"

	"github.com/tjim/smpcc/runtime/base"
	"github.com/tjim/smpcc/runtime/ot"
)

const (
	KEY_SIZE = aes.BlockSize
)

// type ConcurrentId [KEY_SIZE / 2]byte
type ConcurrentId int64

/* YaoRState implements the GenVM interface */
type YaoRState struct {
	io           base.Genio
	concurrentId ConcurrentId
	gateId       uint16
}

var (
	AESCount  uint     = 0
	ALL_ZEROS base.Key = make([]byte, KEY_SIZE)
)

func NewYaoRState(io base.Genio, id ConcurrentId) YaoRState {
	return YaoRState{io, id, 0}
}

func slot(keys []base.Key) int {
	result := 0
	for i := 0; i < len(keys); i++ {
		key := keys[i]
		result *= 2
		result += int(key[0] % 2)
	}
	return result
}

func encrypt(keys []base.Key, result []byte) []byte {
	for i := 0; i < len(keys); i++ {
		result = base.Encrypt(keys[i], result)
	}
	return result
}

func encrypt_nonoptimized(keys []base.Key, result []byte) []byte {
	for i := 0; i < len(keys); i++ {
		result = base.Encrypt(keys[i], result)
		AESCount++
	}
	return result
}

func encrypt_slot_nonoptimized(t base.GarbledTable, plaintext []byte, keys []base.Key) {
	// fmt.Println("Non-optimized encrypt slot")
	t[slot(keys)] = encrypt_nonoptimized(keys, plaintext)
}

func (gax YaoRState) encrypt_slot(t base.GarbledTable, plaintext []byte, keys ...base.Key) {
	if len(keys) != 2 {
		// log.Println("Non optimized encrypt_slot")
		encrypt_slot_nonoptimized(t, plaintext, keys)
		return
		// panic("shouldn't be here")
	}
	// log.Println("Optimized encrypt slot")
	t[slot(keys)] = encrypt(keys, plaintext)
}

var key0 base.Key    // The XOR random constant
var const0 base.Wire // A wire for a constant 0 bit with unbounded fanout
var const1 base.Wire // A wire for a constant 1 bit with unbounded fanout

func init_key0() {
	if key0 != nil {
		return
	}
	key0 = make([]byte, KEY_SIZE)
	base.GenKey(key0) // least significant bit is random...
	key0[0] |= 1      // ...force it to 1
}

func init_constants(io base.Genio) {
	if const0 == nil {
		const0 = genWire()
		const1 = genWire()
		io.SendK(const0[0])
		io.SendK(const1[1])
	}
}

func reset() {
	key0 = nil
	const0 = nil
	const1 = nil
}

// Generates two keys of size KEY_SIZE and returns the pair
func genWire() base.Wire {
	init_key0()
	k0 := make([]byte, KEY_SIZE)
	base.GenKey(k0)
	k1 := base.XorKey(k0, key0)
	return []base.Key{k0, k1}
}

func (g *YaoRState) genWireRR(inKey0, inKey1 base.Key, gateVal byte) base.Wire {
	init_key0()
	var k0, k1 base.Key
	if gateVal == 0 {
		k0 = encrypt([]base.Key{inKey0, inKey1}, ALL_ZEROS)
		k1 = base.XorKey(k0, key0)
	} else if gateVal == 1 {
		k1 = encrypt([]base.Key{inKey0, inKey1}, ALL_ZEROS)
		k0 = base.XorKey(k1, key0)
	} else {
		panic("Invalid gateVal")
	}
	return []base.Key{k0, k1}
}

// Generates an array of wires. A wire is a pair of keys.
func genWires(size int) []base.Wire {
	if size <= 0 {
		panic("genWires with request <= 0")
	}
	res := make([]base.Wire, size)
	for i := 0; i < size; i++ {
		res[i] = genWire()
	}
	return res
}

/* http://www.llvm.org/docs/LangRef.html */

// Gates built directly using encrypt_slot

func (y YaoRState) And(a, b []base.Wire) []base.Wire {
	if len(a) != len(b) {
		panic("Wire mismatch in gen.And()")
	}
	result := make([]base.Wire, len(a))

	var w base.Wire

	for i := 0; i < len(a); i++ {
		t := make([]base.Ciphertext, 3)

		ii := a[i][0][0] % 2
		jj := b[i][0][0] % 2
		r := ii & jj
		w = y.genWireRR(a[i][ii], b[i][jj], r)
		result[i] = w
		for counter := 1; counter < 4; counter++ {
			aa := byte(counter % 2)
			bb := byte(counter / 2)
			ii := aa ^ (a[i][0][0] % 2)
			jj := bb ^ (b[i][0][0] % 2)

			t[counter-1] = encrypt([]base.Key{a[i][ii], b[i][jj]}, w[ii&jj])
		}

		y.io.SendT(t)
	}
	return result
}

func (y YaoRState) Or(a, b []base.Wire) []base.Wire {
	if len(a) != len(b) {
		panic("Wire mismatch in gen.And()")
	}
	result := make([]base.Wire, len(a))

	var w base.Wire

	for i := 0; i < len(a); i++ {
		t := make([]base.Ciphertext, 3)
		// fmt.Printf("==== %d, %d \n", len(a), len(a[i]))
		ii := a[i][0][0] % 2
		jj := b[i][0][0] % 2
		r := ii | jj
		w = y.genWireRR(a[i][ii], b[i][jj], r)
		result[i] = w
		for counter := 1; counter < 4; counter++ {
			aa := byte(counter % 2)
			bb := byte(counter / 2)
			ii := aa ^ (a[i][0][0] % 2)
			jj := bb ^ (b[i][0][0] % 2)

			t[counter-1] = encrypt([]base.Key{a[i][ii], b[i][jj]}, w[ii|jj])
		}

		y.io.SendT(t)
	}
	return result
}

func (y YaoRState) Xor(a, b []base.Wire) []base.Wire {
	if len(a) != len(b) {
		panic("Xor(): mismatch")
	}
	result := make([]base.Wire, len(a))
	for i := 0; i < len(a); i++ {
		k0 := base.XorKey(a[i][0], b[i][0])
		k1 := base.XorKey(a[i][0], b[i][1])
		result[i] = []base.Key{k0, k1}
	}
	return result
}

func (y YaoRState) True() []base.Wire {
	init_constants(y.io)
	return []base.Wire{const1}
}

func (y YaoRState) False() []base.Wire {
	init_constants(y.io)
	return []base.Wire{const0}
}

// Other gates and helper functions

/* Reveal to party 0 = gen */
func (y YaoRState) RevealTo0(a []base.Wire) []bool {
	result := make([]bool, len(a))
	for i := 0; i < len(a); i++ {
		bit := resolveKey(a[i], y.io.RecvK2())
		if bit == 0 {
			result[i] = false
		} else {
			result[i] = true
		}
	}
	return result
}

/* Reveal to party 1 = eval */
func (y YaoRState) RevealTo1(a []base.Wire) {
	for i := 0; i < len(a); i++ {
		t := make([]base.Ciphertext, 2)
		w := genWire()
		w[0][0] = 0
		w[1][0] = 1
		y.encrypt_slot(t, w[0], a[i][0])
		y.encrypt_slot(t, w[1], a[i][1])
		y.io.SendT(t)
	}
}

func (y YaoRState) ShareTo0(bits int) []base.Wire {
	a := make([]base.Wire, bits)
	for i := 0; i < len(a); i++ {
		w := genWire()
		a[i] = w
		y.io.Send(ot.Message(w[0]), ot.Message(w[1]))
	}
	return a
}

func (y YaoRState) ShareTo1(a uint64, bits int) []base.Wire {
	if bits > 64 {
		panic("BT: bits > 64")
	}
	result := make([]base.Wire, bits)
	for i := 0; i < bits; i++ {
		w := genWire()
		result[i] = w
		if (a>>uint(i))%2 == 0 {
			y.io.SendK(w[0])
		} else {
			y.io.SendK(w[1])
		}
	}
	return result
}

// Random generates random bits.
func (y YaoRState) Random(bits int) []base.Wire {
	if bits < 1 {
		panic("Random: bits < 1")
	}
	result := make([]base.Wire, bits)
	for i, _ := range result {
		w := genWire()
		result[i] = w
		switch rand.Intn(2) {
		case 0:
			y.io.Send(ot.Message(w[0]), ot.Message(w[1]))
		default:
			y.io.Send(ot.Message(w[1]), ot.Message(w[0]))
		}
	}
	return result
}

func resolveKey(w base.Wire, k base.Key) int {
	if bytes.Equal(k, w[0]) {
		return 0
	} else if bytes.Equal(k, w[1]) {
		return 1
	} else {
		panic(fmt.Sprintf("resolveKey(): key and wire mismatch\nKey: %v\nWire: %v\n", k, w))
	}
	panic("unreachable")
}