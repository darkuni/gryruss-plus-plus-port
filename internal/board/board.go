// Package board is the Gyruss arcade board in Go: the main Z80 (game
// program), the Konami-1 6809 (sprite processor), the sound Z80 with its five
// AY-3-8910s, the 8039 drum CPU and the tile / sprite video. The original
// ROMs run unmodified on it.
package board

import (
	"gyruss/internal/assets"
	"gyruss/internal/ay8910"
	"gyruss/internal/i8039"
	"gyruss/internal/m6809"
	"gyruss/internal/z80"
)

// Input bits (active low) of the SYSTEM and player ports.
const (
	Coin1   = 0x01
	Coin2   = 0x02
	Service = 0x04
	Start1  = 0x08
	Start2  = 0x10

	JoyLeft  = 0x01
	JoyRight = 0x02
	JoyUp    = 0x04
	JoyDown  = 0x08
	JoyFire  = 0x10
)

// Video timing (pixel clock 6.144 MHz, 396 x 256).
const (
	Lines        = 256
	VisTop       = 16
	VisBottom    = 240 // first line of vblank
	MainPerLine  = 3072000.0 / (6144000.0 / 396)
	SubPerLine   = 1536000.0 / (6144000.0 / 396)
	AudPerLine   = 3579545.0 / (6144000.0 / 396)
	DrumPerLine  = 8000000.0 / 15 / (6144000.0 / 396)
	AYTickPerLin = 3579545.0 / 2 / 8 / (6144000.0 / 396)
	FrameHz      = 6144000.0 / 396 / 256
)

// Board is the whole machine.
type Board struct {
	roms *assets.ROMs

	Main  z80.CPU
	Sub   m6809.CPU
	Audio z80.CPU
	Drum  i8039.CPU
	AY    [5]*ay8910.Chip

	MainRAM [0x1000]byte // 9000-9fff
	Shared  [0x800]byte  // main a000-a7ff, sub 6000-67ff
	Color   [0x400]byte  // 8000-83ff
	VRAM    [0x400]byte  // 8400-87ff
	SubRAM  [0x800]byte  // 4000-47ff (sprite RAM at 4040-40ff)
	AudRAM  [0x400]byte  // 6000-63ff

	nmiMask, nmiLine bool
	subMask          bool
	Flip             bool
	audIRQ           bool
	SoundLatch       byte
	DrumLatch        byte
	Filter           [2]byte // AY1 / AY2 port B (RC filter selects)
	DAC              byte

	System, P1, P2   byte
	DSW1, DSW2, DSW3 byte
	line             int
	mainAcc, subAcc  float64
	audAcc, drumAcc  float64
	ayAcc            float64
	audCycles        uint64

	// Frame is the 256x256 indexed-colour picture (palette index 0..31),
	// rebuilt line by line as the beam moves, like the real hardware.
	Frame [256 * 256]byte

	snd sound
	g   *gfx
	kit *Kit

	// OnDrum, if set, is called whenever the sound CPU sends a drum command
	// to the 8039 (the value written to the drum latch).
	OnDrum func(cmd byte)
	// DrumMute silences the original 8039 DAC output (replacement samples
	// are mixed in instead).
	DrumMute bool

	// OnSound, if set, sees every sound command the game sends.
	OnSound func(cmd byte)

	// MainWrite, if set, sees every main CPU write to RAM.
	MainWrite func(a uint16, v byte)

	// MainHook, if set, is called before every main CPU instruction.
	MainHook func(pc uint16)

	FrameCount uint64
}

// New builds a board around a loaded ROM set.
func New(r *assets.ROMs) *Board {
	b := &Board{roms: r, System: 0xff, P1: 0xff, P2: 0xff, DSW1: 0xff, DSW2: 0x3b, DSW3: 0xfe}
	b.Main.Bus = &mainBus{b}
	b.Sub.Bus = &subBus{b}
	b.Sub.Decrypt = konami1
	b.Audio.Bus = &audBus{b}
	b.Drum.ROM = r.Drum
	b.Drum.Bus = &drumBus{b}
	for i := range b.AY {
		b.AY[i] = ay8910.New()
	}
	b.AY[0].PortBWrite = func(v byte) { b.Filter[0] = v }
	b.AY[1].PortBWrite = func(v byte) { b.Filter[1] = v }
	b.AY[2].PortARead = func() byte {
		return ayTimer[(b.Audio.Cycles/1024)%10]
	}
	b.snd.init()
	b.Reset()
	return b
}

var ayTimer = [10]byte{0x00, 0x01, 0x02, 0x03, 0x04, 0x09, 0x0a, 0x0b, 0x0a, 0x0d}

// konami1 undoes the opcode scrambling of the Konami-1 CPU.
func konami1(a uint16, v byte) byte {
	switch a & 0xa {
	case 0x0:
		return v ^ 0x22
	case 0x2:
		return v ^ 0x82
	case 0x8:
		return v ^ 0x28
	default:
		return v ^ 0x88
	}
}

// Reset is the power-on reset of all four CPUs.
func (b *Board) Reset() {
	b.Main.Reset()
	b.Audio.Reset()
	b.Sub.Reset()
	b.Drum.Reset()
	b.nmiMask, b.nmiLine, b.subMask, b.audIRQ = false, false, false, false
	b.line = 0
}

// ---- main CPU ------------------------------------------------------------

type mainBus struct{ b *Board }

func (m *mainBus) Read(a uint16) byte {
	b := m.b
	switch {
	case a < 0x8000:
		return b.roms.Main[a]
	case a < 0x8400:
		return b.Color[a&0x3ff]
	case a < 0x8800:
		return b.VRAM[a&0x3ff]
	case a >= 0x9000 && a < 0xa000:
		return b.MainRAM[a&0xfff]
	case a >= 0xa000 && a < 0xa800:
		return b.Shared[a&0x7ff]
	case a == 0xc000:
		return b.DSW2
	case a == 0xc080:
		return b.System
	case a == 0xc0a0:
		return b.P1
	case a == 0xc0c0:
		return b.P2
	case a == 0xc0e0:
		return b.DSW1
	case a == 0xc100:
		return b.DSW3
	}
	return 0xff
}

func (m *mainBus) Write(a uint16, v byte) {
	b := m.b
	if b.MainWrite != nil {
		b.MainWrite(a, v)
	}
	switch {
	case a >= 0x8000 && a < 0x8400:
		b.Color[a&0x3ff] = v
	case a >= 0x8400 && a < 0x8800:
		b.VRAM[a&0x3ff] = v
	case a >= 0x9000 && a < 0xa000:
		b.MainRAM[a&0xfff] = v
	case a >= 0xa000 && a < 0xa800:
		b.Shared[a&0x7ff] = v
	case a == 0xc080:
		b.audIRQ = true
	case a == 0xc100:
		b.SoundLatch = v
		if b.OnSound != nil {
			b.OnSound(v)
		}
	case a >= 0xc180 && a <= 0xc187:
		bit := v&1 != 0
		switch a & 7 {
		case 0:
			b.nmiMask = bit
			if !bit {
				b.nmiLine = false
			}
		case 5:
			b.Flip = bit
		}
	}
}

func (m *mainBus) In(p uint16) byte     { return 0xff }
func (m *mainBus) Out(p uint16, v byte) {}

// ---- sub CPU (Konami-1) ---------------------------------------------------

type subBus struct{ b *Board }

func (s *subBus) Read(a uint16) byte {
	b := s.b
	switch {
	case a == 0x0000:
		return byte(b.line)
	case a >= 0x4000 && a < 0x4800:
		return b.SubRAM[a&0x7ff]
	case a >= 0x6000 && a < 0x6800:
		return b.Shared[a&0x7ff]
	case a >= 0xe000:
		return b.roms.Sub[a&0x1fff]
	}
	return 0
}

func (s *subBus) Write(a uint16, v byte) {
	b := s.b
	switch {
	case a == 0x2000:
		b.subMask = v&1 != 0
		if !b.subMask {
			b.Sub.IRQ = false
		}
	case a >= 0x4000 && a < 0x4800:
		b.SubRAM[a&0x7ff] = v
	case a >= 0x6000 && a < 0x6800:
		b.Shared[a&0x7ff] = v
	}
}

// ---- sound CPU -----------------------------------------------------------

type audBus struct{ b *Board }

func (s *audBus) Read(a uint16) byte {
	b := s.b
	switch {
	case a < 0x6000:
		return b.roms.Audio[a]
	case a < 0x6400:
		return b.AudRAM[a&0x3ff]
	case a == 0x8000:
		return b.SoundLatch
	}
	return 0xff
}

func (s *audBus) Write(a uint16, v byte) {
	if a >= 0x6000 && a < 0x6400 {
		s.b.AudRAM[a&0x3ff] = v
	}
}

func (s *audBus) In(p uint16) byte {
	b := s.b
	p &= 0xff
	if p < 0x14 && p&3 == 1 {
		return b.AY[p>>2].Read()
	}
	return 0xff
}

func (s *audBus) Out(p uint16, v byte) {
	b := s.b
	p &= 0xff
	switch {
	case p < 0x14 && p&3 == 0:
		b.AY[p>>2].Address(v)
	case p < 0x14 && p&3 == 2:
		b.AY[p>>2].Write(v)
	case p == 0x14:
		b.Drum.IntLine = true
	case p == 0x18:
		b.DrumLatch = v
		if b.OnDrum != nil {
			b.OnDrum(v)
		}
	}
}

// ---- drum CPU (8039) -------------------------------------------------------

type drumBus struct{ b *Board }

func (d *drumBus) ReadExt(a byte) byte     { return d.b.DrumLatch }
func (d *drumBus) WriteExt(a byte, v byte) {}
func (d *drumBus) ReadPort(p int) byte     { return 0xff }
func (d *drumBus) ReadT(n int) bool        { return false }
func (d *drumBus) WritePort(p int, v byte) {
	switch p {
	case 1:
		d.b.DAC = v
	case 2:
		d.b.Drum.IntLine = false
	}
}

// ---- scheduling ------------------------------------------------------------

// RunFrame runs the machine for one video frame (256 lines).
func (b *Board) RunFrame() {
	for i := 0; i < Lines; i++ {
		b.runLine()
	}
	b.FrameCount++
}

func (b *Board) runLine() {
	y := b.line
	if y >= VisTop && y < VisBottom {
		b.renderLine(y)
	}
	if y == VisBottom {
		if b.nmiMask && !b.nmiLine {
			b.nmiLine = true
			b.Main.NMI()
		}
		if b.subMask {
			b.Sub.IRQ = true
		}
	}
	// interleave the CPUs in small slices so shared RAM and latches see
	// each other's writes in a timely way
	const slices = 4
	for s := 0; s < slices; s++ {
		b.mainAcc += MainPerLine / slices
		for b.mainAcc > 0 {
			if b.MainHook != nil {
				b.MainHook(b.Main.PC)
			}
			b.mainAcc -= float64(b.Main.Step())
		}
		b.subAcc += SubPerLine / slices
		for b.subAcc > 0 {
			b.subAcc -= float64(b.Sub.Step())
		}
		b.audAcc += AudPerLine / slices
		for b.audAcc > 0 {
			if b.audIRQ && b.Audio.IRQ(0xff) {
				b.audIRQ = false
			}
			b.audAcc -= float64(b.Audio.Step())
		}
		b.drumAcc += DrumPerLine / slices
		for b.drumAcc > 0 {
			b.drumAcc -= float64(b.Drum.Step())
		}
		b.ayAcc += AYTickPerLin / slices
		for b.ayAcc >= 1 {
			b.ayAcc--
			b.snd.tick(b)
		}
	}
	b.line = (b.line + 1) % Lines
}
