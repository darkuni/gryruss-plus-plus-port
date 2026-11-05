// Package i8039 is an Intel 8039 (MCS-48 family, external program memory)
// interpreter. On the Gyruss sound board it plays the drum samples through
// an 8-bit DAC on port 1.
package i8039

// Bus connects the CPU to the board.
type Bus interface {
	ReadExt(a byte) byte     // MOVX A,@Ri
	WriteExt(a byte, v byte) // MOVX @Ri,A
	WritePort(p int, v byte) // OUTL P1/P2 and ANL/ORL Pn
	ReadPort(p int) byte     // IN A,P1/P2
	ReadT(n int) bool        // T0 / T1 test inputs
}

// CPU is the register file.
type CPU struct {
	ROM  []byte // program, 4 KB
	RAM  [128]byte
	A    byte
	PC   uint16
	PSW  byte // CY AC F0 BS 1 SP2..0
	F1   bool
	MB   bool // memory bank select (A11 for JMP/CALL)
	P1   byte
	P2   byte
	BusL byte

	Bus Bus

	IntLine      bool // external /INT asserted
	intEn        bool
	tcntEn       bool
	inIRQ        bool
	timer        byte
	tf           bool
	timerOn      bool
	cntOn        bool
	prescal      int
	timerPending bool

	Cycles uint64
}

const (
	pCY = 0x80
	pAC = 0x40
	pF0 = 0x20
	pBS = 0x10
)

// Reset puts the CPU in its power-on state.
func (c *CPU) Reset() {
	c.PC = 0
	c.PSW = 0x08
	c.A = 0
	c.F1 = false
	c.MB = false
	c.intEn, c.tcntEn, c.inIRQ = false, false, false
	c.timerOn, c.cntOn, c.tf = false, false, false
	c.P1, c.P2 = 0xff, 0xff
}

func (c *CPU) rom(a uint16) byte { return c.ROM[int(a)&(len(c.ROM)-1)] }

func (c *CPU) fetch() byte {
	v := c.rom(c.PC)
	c.PC = c.PC&0x800 | (c.PC+1)&0x7ff
	return v
}

func (c *CPU) reg(n byte) *byte {
	base := byte(0)
	if c.PSW&pBS != 0 {
		base = 24
	}
	return &c.RAM[(base+n)&127]
}

func (c *CPU) ind(n byte) *byte { return &c.RAM[*c.reg(n)&127] }

func (c *CPU) push() {
	sp := c.PSW & 7
	a := 8 + 2*sp
	c.RAM[a] = byte(c.PC)
	c.RAM[a+1] = byte(c.PC>>8)&0x0f | c.PSW&0xf0
	c.PSW = c.PSW&0xf8 | (sp+1)&7
}

func (c *CPU) pop(restorePSW bool) {
	sp := (c.PSW - 1) & 7
	a := 8 + 2*sp
	c.PC = uint16(c.RAM[a]) | uint16(c.RAM[a+1]&0x0f)<<8
	if restorePSW {
		c.PSW = c.RAM[a+1]&0xf0 | 0x08 | sp
	} else {
		c.PSW = c.PSW&0xf8 | sp
	}
}

func (c *CPU) add(v byte, carry bool) {
	ci := byte(0)
	if carry && c.PSW&pCY != 0 {
		ci = 1
	}
	r := uint16(c.A) + uint16(v) + uint16(ci)
	h := c.A&15 + v&15 + ci
	c.PSW &^= pCY | pAC
	if r > 0xff {
		c.PSW |= pCY
	}
	if h > 15 {
		c.PSW |= pAC
	}
	c.A = byte(r)
}

func (c *CPU) jmpPage(cond bool) {
	d := c.fetch()
	if cond {
		c.PC = (c.PC-1)&0xf00 | uint16(d)
	}
}

func (c *CPU) longAddr(op byte) uint16 {
	d := c.fetch()
	a := uint16(op&0xe0)<<3 | uint16(d)
	if c.MB && !c.inIRQ {
		a |= 0x800
	}
	return a
}

func (c *CPU) tickTimer(cy int) {
	if !c.timerOn {
		return
	}
	c.prescal += cy
	for c.prescal >= 32 {
		c.prescal -= 32
		c.timer++
		if c.timer == 0 {
			c.tf = true
			if c.tcntEn {
				c.timerPending = true
			}
		}
	}
}

// Step executes one instruction (or takes an interrupt); returns machine cycles.
func (c *CPU) Step() int {
	if !c.inIRQ {
		if c.IntLine && c.intEn {
			c.inIRQ = true
			c.push()
			c.PC = 3
			c.Cycles += 2
			return 2
		}
		if c.timerPending && c.tcntEn {
			c.timerPending = false
			c.inIRQ = true
			c.push()
			c.PC = 7
			c.Cycles += 2
			return 2
		}
	}
	cy := c.exec(c.fetch())
	c.tickTimer(cy)
	c.Cycles += uint64(cy)
	return cy
}

func (c *CPU) exec(op byte) int {
	switch {
	case op&0x1f == 0x04: // JMP
		c.PC = c.longAddr(op)
		return 2
	case op&0x1f == 0x14: // CALL
		a := c.longAddr(op)
		c.push()
		c.PC = a
		return 2
	case op&0x1f == 0x12: // JBb
		c.jmpPage(c.A>>(op>>5)&1 != 0)
		return 2
	case op >= 0x18 && op <= 0x1f:
		*c.reg(op & 7)++
		return 1
	case op >= 0x28 && op <= 0x2f:
		r := c.reg(op & 7)
		c.A, *r = *r, c.A
		return 1
	case op >= 0x48 && op <= 0x4f:
		c.A |= *c.reg(op & 7)
		return 1
	case op >= 0x58 && op <= 0x5f:
		c.A &= *c.reg(op & 7)
		return 1
	case op >= 0x68 && op <= 0x6f:
		c.add(*c.reg(op & 7), false)
		return 1
	case op >= 0x78 && op <= 0x7f:
		c.add(*c.reg(op & 7), true)
		return 1
	case op >= 0xa8 && op <= 0xaf:
		*c.reg(op & 7) = c.A
		return 1
	case op >= 0xb8 && op <= 0xbf:
		*c.reg(op & 7) = c.fetch()
		return 2
	case op >= 0xc8 && op <= 0xcf:
		*c.reg(op & 7)--
		return 1
	case op >= 0xd8 && op <= 0xdf:
		c.A ^= *c.reg(op & 7)
		return 1
	case op >= 0xe8 && op <= 0xef:
		r := c.reg(op & 7)
		*r--
		c.jmpPage(*r != 0)
		return 2
	case op >= 0xf8:
		c.A = *c.reg(op & 7)
		return 1
	}
	i := op & 1
	switch op {
	case 0x00:
		return 1
	case 0x02: // OUTL BUS,A
		c.BusL = c.A
		return 2
	case 0x03:
		c.add(c.fetch(), false)
		return 2
	case 0x05:
		c.intEn = true
		return 1
	case 0x07:
		c.A--
		return 1
	case 0x08: // INS A,BUS
		c.A = c.Bus.ReadExt(0)
		return 2
	case 0x09:
		c.A = c.Bus.ReadPort(1) & c.P1
		return 2
	case 0x0a:
		c.A = c.Bus.ReadPort(2) & c.P2
		return 2
	case 0x0c, 0x0d, 0x0e, 0x0f: // MOVD A,Pp (8243 not fitted)
		c.A = 0x0f
		return 2
	case 0x10, 0x11:
		*c.ind(i)++
		return 1
	case 0x13:
		c.add(c.fetch(), true)
		return 2
	case 0x15:
		c.intEn = false
		return 1
	case 0x16:
		t := c.tf
		c.tf = false
		c.jmpPage(t)
		return 2
	case 0x17:
		c.A++
		return 1
	case 0x20, 0x21:
		p := c.ind(i)
		c.A, *p = *p, c.A
		return 1
	case 0x23:
		c.A = c.fetch()
		return 2
	case 0x25:
		c.tcntEn = true
		return 1
	case 0x26:
		c.jmpPage(!c.Bus.ReadT(0))
		return 2
	case 0x27:
		c.A = 0
		return 1
	case 0x30, 0x31:
		p := c.ind(i)
		c.A, *p = c.A&0xf0|*p&0x0f, *p&0xf0|c.A&0x0f
		return 1
	case 0x35:
		c.tcntEn = false
		c.timerPending = false
		return 1
	case 0x36:
		c.jmpPage(c.Bus.ReadT(0))
		return 2
	case 0x37:
		c.A = ^c.A
		return 1
	case 0x39:
		c.P1 = c.A
		c.Bus.WritePort(1, c.P1)
		return 2
	case 0x3a:
		c.P2 = c.A
		c.Bus.WritePort(2, c.P2)
		return 2
	case 0x3c, 0x3d, 0x3e, 0x3f:
		return 2
	case 0x40, 0x41:
		c.A |= *c.ind(i)
		return 1
	case 0x42:
		c.A = c.timer
		return 1
	case 0x43:
		c.A |= c.fetch()
		return 2
	case 0x45:
		c.cntOn, c.timerOn = true, false
		return 1
	case 0x46:
		c.jmpPage(!c.Bus.ReadT(1))
		return 2
	case 0x47:
		c.A = c.A<<4 | c.A>>4
		return 1
	case 0x50, 0x51:
		c.A &= *c.ind(i)
		return 1
	case 0x53:
		c.A &= c.fetch()
		return 2
	case 0x55:
		c.timerOn, c.cntOn = true, false
		c.prescal = 0
		return 1
	case 0x56:
		c.jmpPage(c.Bus.ReadT(1))
		return 2
	case 0x57: // DA A
		if c.A&15 > 9 || c.PSW&pAC != 0 {
			if c.A > 0xf9 {
				c.PSW |= pCY
			}
			c.A += 6
		}
		if c.A>>4 > 9 || c.PSW&pCY != 0 {
			c.A += 0x60
			c.PSW |= pCY
		}
		return 1
	case 0x60, 0x61:
		c.add(*c.ind(i), false)
		return 1
	case 0x62:
		c.timer = c.A
		return 1
	case 0x65:
		c.timerOn, c.cntOn = false, false
		return 1
	case 0x67: // RRC
		cy := c.PSW & pCY
		if c.A&1 != 0 {
			c.PSW |= pCY
		} else {
			c.PSW &^= pCY
		}
		c.A = c.A>>1 | cy
		return 1
	case 0x70, 0x71:
		c.add(*c.ind(i), true)
		return 1
	case 0x75:
		return 1
	case 0x76:
		c.jmpPage(c.F1)
		return 2
	case 0x77:
		c.A = c.A>>1 | c.A<<7
		return 1
	case 0x80, 0x81:
		c.A = c.Bus.ReadExt(*c.reg(i))
		return 2
	case 0x83:
		c.pop(false)
		return 2
	case 0x85:
		c.PSW &^= pF0
		return 1
	case 0x86:
		c.jmpPage(c.IntLine)
		return 2
	case 0x88:
		c.BusL |= c.fetch()
		return 2
	case 0x89:
		c.P1 |= c.fetch()
		c.Bus.WritePort(1, c.P1)
		return 2
	case 0x8a:
		c.P2 |= c.fetch()
		c.Bus.WritePort(2, c.P2)
		return 2
	case 0x8c, 0x8d, 0x8e, 0x8f:
		return 2
	case 0x90, 0x91:
		c.Bus.WriteExt(*c.reg(i), c.A)
		return 2
	case 0x93:
		c.pop(true)
		c.inIRQ = false
		return 2
	case 0x95:
		c.PSW ^= pF0
		return 1
	case 0x96:
		c.jmpPage(c.A != 0)
		return 2
	case 0x97:
		c.PSW &^= pCY
		return 1
	case 0x98:
		c.BusL &= c.fetch()
		return 2
	case 0x99:
		c.P1 &= c.fetch()
		c.Bus.WritePort(1, c.P1)
		return 2
	case 0x9a:
		c.P2 &= c.fetch()
		c.Bus.WritePort(2, c.P2)
		return 2
	case 0x9c, 0x9d, 0x9e, 0x9f:
		return 2
	case 0xa0, 0xa1:
		*c.ind(i) = c.A
		return 1
	case 0xa3:
		c.A = c.rom(c.PC&0xf00 | uint16(c.A))
		return 2
	case 0xa5:
		c.F1 = false
		return 1
	case 0xa7:
		c.PSW ^= pCY
		return 1
	case 0xb0, 0xb1:
		*c.ind(i) = c.fetch()
		return 2
	case 0xb3:
		c.PC = c.PC&0xf00 | uint16(c.rom(c.PC&0xf00|uint16(c.A)))
		return 2
	case 0xb5:
		c.F1 = !c.F1
		return 1
	case 0xb6:
		c.jmpPage(c.PSW&pF0 != 0)
		return 2
	case 0xc5:
		c.PSW &^= pBS
		return 1
	case 0xc6:
		c.jmpPage(c.A == 0)
		return 2
	case 0xc7:
		c.A = c.PSW | 0x08
		return 1
	case 0xd0, 0xd1:
		c.A ^= *c.ind(i)
		return 1
	case 0xd3:
		c.A ^= c.fetch()
		return 2
	case 0xd5:
		c.PSW |= pBS
		return 1
	case 0xd7:
		c.PSW = c.A | 0x08
		return 1
	case 0xe3:
		c.A = c.rom(0x300 | uint16(c.A))
		return 2
	case 0xe5:
		c.MB = false
		return 1
	case 0xe6:
		c.jmpPage(c.PSW&pCY == 0)
		return 2
	case 0xe7:
		c.A = c.A<<1 | c.A>>7
		return 1
	case 0xf0, 0xf1:
		c.A = *c.ind(i)
		return 1
	case 0xf5:
		c.MB = true
		return 1
	case 0xf6:
		c.jmpPage(c.PSW&pCY != 0)
		return 2
	case 0xf7: // RLC
		cy := c.PSW & pCY >> 7
		if c.A&0x80 != 0 {
			c.PSW |= pCY
		} else {
			c.PSW &^= pCY
		}
		c.A = c.A<<1 | cy
		return 1
	}
	return 1 // undefined opcodes behave as NOP
}
