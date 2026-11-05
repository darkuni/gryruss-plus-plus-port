// Package z80 is a Zilog Z80 interpreter.

package z80

type Bus interface {
	Read(addr uint16) uint8
	Write(addr uint16, v uint8)
	In(port uint16) uint8
	Out(port uint16, v uint8)
}

type CPU struct {
	A, F, B, C, D, E, H, L         uint8
	A_, F_, B_, C_, D_, E_, H_, L_ uint8
	IX, IY, SP, PC                 uint16
	I, R                           uint8
	IFF1, IFF2                     bool
	IM                             uint8
	Halted                         bool
	eiPending                      bool
	Bus                            Bus
	Cycles                         uint64
	Q                              uint8
}

const (
	fC = 0x01
	fN = 0x02
	fP = 0x04
	fX = 0x08
	fH = 0x10
	fY = 0x20
	fZ = 0x40
	fS = 0x80
)

var parity [256]bool
var szp [256]uint8

func init() {
	for i := 0; i < 256; i++ {
		n := 0
		for b := 0; b < 8; b++ {
			if i>>b&1 == 1 {
				n++
			}
		}
		parity[i] = n%2 == 0
		f := uint8(i) & (fS | fX | fY)
		if i == 0 {
			f |= fZ
		}
		if parity[i] {
			f |= fP
		}
		szp[i] = f
	}
}

func (c *CPU) Reset() {
	*c = CPU{Bus: c.Bus}
	c.SP = 0xffff
	c.A, c.F = 0xff, 0xff
}

func (c *CPU) rd(a uint16) uint8       { return c.Bus.Read(a) }
func (c *CPU) wr(a uint16, v uint8)    { c.Bus.Write(a, v) }
func (c *CPU) rd16(a uint16) uint16    { return uint16(c.rd(a)) | uint16(c.rd(a+1))<<8 }
func (c *CPU) wr16(a uint16, v uint16) { c.wr(a, uint8(v)); c.wr(a+1, uint8(v>>8)) }
func (c *CPU) fetch() uint8 {
	v := c.rd(c.PC)
	c.PC++
	c.R = c.R&0x80 | (c.R+1)&0x7f
	return v
}
func (c *CPU) fetch16() uint16 {
	l := c.fetch2()
	h := c.fetch2()
	return uint16(l) | uint16(h)<<8
}
func (c *CPU) fetch2() uint8 { v := c.rd(c.PC); c.PC++; return v } // operand fetch (no R inc)
func (c *CPU) push(v uint16) {
	c.SP--
	c.wr(c.SP, uint8(v>>8))
	c.SP--
	c.wr(c.SP, uint8(v))
}
func (c *CPU) pop() uint16 {
	l := c.rd(c.SP)
	c.SP++
	h := c.rd(c.SP)
	c.SP++
	return uint16(l) | uint16(h)<<8
}

func (c *CPU) BC() uint16     { return uint16(c.B)<<8 | uint16(c.C) }
func (c *CPU) DE() uint16     { return uint16(c.D)<<8 | uint16(c.E) }
func (c *CPU) HL() uint16     { return uint16(c.H)<<8 | uint16(c.L) }
func (c *CPU) AF() uint16     { return uint16(c.A)<<8 | uint16(c.F) }
func (c *CPU) setBC(v uint16) { c.B, c.C = uint8(v>>8), uint8(v) }
func (c *CPU) setDE(v uint16) { c.D, c.E = uint8(v>>8), uint8(v) }
func (c *CPU) setHL(v uint16) { c.H, c.L = uint8(v>>8), uint8(v) }
func (c *CPU) setAF(v uint16) { c.A, c.F = uint8(v>>8), uint8(v) }

// index register mode: 0 = HL, 1 = IX, 2 = IY
func (c *CPU) hlx(m int) uint16 {
	switch m {
	case 1:
		return c.IX
	case 2:
		return c.IY
	}
	return c.HL()
}
func (c *CPU) sethlx(m int, v uint16) {
	switch m {
	case 1:
		c.IX = v
	case 2:
		c.IY = v
	default:
		c.setHL(v)
	}
}

// register by index 0..7 (B C D E H L (HL) A), honoring IXH/IXL for m!=0
func (c *CPU) getr(r int, m int) uint8 {
	switch r {
	case 0:
		return c.B
	case 1:
		return c.C
	case 2:
		return c.D
	case 3:
		return c.E
	case 4:
		switch m {
		case 1:
			return uint8(c.IX >> 8)
		case 2:
			return uint8(c.IY >> 8)
		}
		return c.H
	case 5:
		switch m {
		case 1:
			return uint8(c.IX)
		case 2:
			return uint8(c.IY)
		}
		return c.L
	case 7:
		return c.A
	}
	panic("getr")
}
func (c *CPU) setr(r int, m int, v uint8) {
	switch r {
	case 0:
		c.B = v
	case 1:
		c.C = v
	case 2:
		c.D = v
	case 3:
		c.E = v
	case 4:
		switch m {
		case 1:
			c.IX = c.IX&0xff | uint16(v)<<8
		case 2:
			c.IY = c.IY&0xff | uint16(v)<<8
		default:
			c.H = v
		}
	case 5:
		switch m {
		case 1:
			c.IX = c.IX&0xff00 | uint16(v)
		case 2:
			c.IY = c.IY&0xff00 | uint16(v)
		default:
			c.L = v
		}
	case 7:
		c.A = v
	default:
		panic("setr")
	}
}

func (c *CPU) cond(cc int) bool {
	switch cc {
	case 0:
		return c.F&fZ == 0
	case 1:
		return c.F&fZ != 0
	case 2:
		return c.F&fC == 0
	case 3:
		return c.F&fC != 0
	case 4:
		return c.F&fP == 0
	case 5:
		return c.F&fP != 0
	case 6:
		return c.F&fS == 0
	}
	return c.F&fS != 0
}

func (c *CPU) add8(a, b uint8, carry uint8) uint8 {
	r := uint16(a) + uint16(b) + uint16(carry)
	res := uint8(r)
	f := res & (fS | fX | fY)
	if res == 0 {
		f |= fZ
	}
	if (a&0xf)+(b&0xf)+carry > 0xf {
		f |= fH
	}
	if (a^b)&0x80 == 0 && (a^res)&0x80 != 0 {
		f |= fP
	}
	if r > 0xff {
		f |= fC
	}
	c.F = f
	return res
}
func (c *CPU) sub8(a, b uint8, carry uint8) uint8 {
	r := int(a) - int(b) - int(carry)
	res := uint8(r)
	f := res&(fS|fX|fY) | fN
	if res == 0 {
		f |= fZ
	}
	if int(a&0xf)-int(b&0xf)-int(carry) < 0 {
		f |= fH
	}
	if (a^b)&0x80 != 0 && (a^res)&0x80 != 0 {
		f |= fP
	}
	if r < 0 {
		f |= fC
	}
	c.F = f
	return res
}
func (c *CPU) alu(op int, v uint8) {
	switch op {
	case 0:
		c.A = c.add8(c.A, v, 0)
	case 1:
		c.A = c.add8(c.A, v, c.F&fC)
	case 2:
		c.A = c.sub8(c.A, v, 0)
	case 3:
		c.A = c.sub8(c.A, v, c.F&fC)
	case 4:
		c.A &= v
		c.F = szp[c.A] | fH
	case 5:
		c.A ^= v
		c.F = szp[c.A]
	case 6:
		c.A |= v
		c.F = szp[c.A]
	case 7:
		c.sub8(c.A, v, 0)
		c.F = c.F&^(fX|fY) | v&(fX|fY)
	}
}
func (c *CPU) inc8(v uint8) uint8 {
	r := v + 1
	f := c.F&fC | r&(fS|fX|fY)
	if r == 0 {
		f |= fZ
	}
	if v&0xf == 0xf {
		f |= fH
	}
	if v == 0x7f {
		f |= fP
	}
	c.F = f
	return r
}
func (c *CPU) dec8(v uint8) uint8 {
	r := v - 1
	f := c.F&fC | r&(fS|fX|fY) | fN
	if r == 0 {
		f |= fZ
	}
	if v&0xf == 0 {
		f |= fH
	}
	if v == 0x80 {
		f |= fP
	}
	c.F = f
	return r
}
func (c *CPU) add16(a, b uint16) uint16 {
	r := uint32(a) + uint32(b)
	f := c.F & (fS | fZ | fP)
	f |= uint8(r>>8) & (fX | fY)
	if (a&0xfff)+(b&0xfff) > 0xfff {
		f |= fH
	}
	if r > 0xffff {
		f |= fC
	}
	c.F = f
	return uint16(r)
}
func (c *CPU) adc16(a, b uint16) uint16 {
	cy := uint32(c.F & fC)
	r := uint32(a) + uint32(b) + cy
	res := uint16(r)
	f := uint8(res>>8) & (fS | fX | fY)
	if res == 0 {
		f |= fZ
	}
	if (a&0xfff)+(b&0xfff)+uint16(cy) > 0xfff {
		f |= fH
	}
	if (a^b)&0x8000 == 0 && (a^res)&0x8000 != 0 {
		f |= fP
	}
	if r > 0xffff {
		f |= fC
	}
	c.F = f
	return res
}
func (c *CPU) sbc16(a, b uint16) uint16 {
	cy := int32(c.F & fC)
	r := int32(a) - int32(b) - cy
	res := uint16(r)
	f := uint8(res>>8)&(fS|fX|fY) | fN
	if res == 0 {
		f |= fZ
	}
	if int32(a&0xfff)-int32(b&0xfff)-cy < 0 {
		f |= fH
	}
	if (a^b)&0x8000 != 0 && (a^res)&0x8000 != 0 {
		f |= fP
	}
	if r < 0 {
		f |= fC
	}
	c.F = f
	return res
}

func (c *CPU) rp(p int, m int) uint16 {
	switch p {
	case 0:
		return c.BC()
	case 1:
		return c.DE()
	case 2:
		return c.hlx(m)
	}
	return c.SP
}
func (c *CPU) setrp(p int, m int, v uint16) {
	switch p {
	case 0:
		c.setBC(v)
	case 1:
		c.setDE(v)
	case 2:
		c.sethlx(m, v)
	default:
		c.SP = v
	}
}

func (c *CPU) rot(op int, v uint8) uint8 {
	var r uint8
	var cy uint8
	switch op {
	case 0: // RLC
		cy = v >> 7
		r = v<<1 | cy
	case 1: // RRC
		cy = v & 1
		r = v>>1 | cy<<7
	case 2: // RL
		cy = v >> 7
		r = v<<1 | c.F&fC
	case 3: // RR
		cy = v & 1
		r = v>>1 | (c.F&fC)<<7
	case 4: // SLA
		cy = v >> 7
		r = v << 1
	case 5: // SRA
		cy = v & 1
		r = v>>1 | v&0x80
	case 6: // SLL
		cy = v >> 7
		r = v<<1 | 1
	case 7: // SRL
		cy = v & 1
		r = v >> 1
	}
	c.F = szp[r] | cy
	return r
}

// Interrupt requests
func (c *CPU) NMI() {
	c.Halted = false
	c.IFF2 = c.IFF1
	c.IFF1 = false
	c.push(c.PC)
	c.PC = 0x66
	c.Cycles += 11
}

func (c *CPU) IRQ(data uint8) bool {
	if !c.IFF1 || c.eiPending {
		return false
	}
	c.Halted = false
	c.IFF1, c.IFF2 = false, false
	switch c.IM {
	case 2:
		c.push(c.PC)
		c.PC = c.rd16(uint16(c.I)<<8 | uint16(data))
		c.Cycles += 19
	default:
		c.push(c.PC)
		c.PC = 0x38
		c.Cycles += 13
	}
	return true
}

// Step executes one instruction and returns its T-state count.
func (c *CPU) Step() int {
	start := c.Cycles
	if c.eiPending {
		c.eiPending = false
	}
	if c.Halted {
		c.R = c.R&0x80 | (c.R+1)&0x7f
		c.Cycles += 4
		return 4
	}
	m := 0
	op := c.fetch()
	for op == 0xdd || op == 0xfd {
		if op == 0xdd {
			m = 1
		} else {
			m = 2
		}
		c.Cycles += 4
		op = c.fetch()
	}
	switch op {
	case 0xcb:
		c.execCB(m)
	case 0xed:
		c.execED()
	default:
		c.exec(op, m)
	}
	return int(c.Cycles - start)
}

func (c *CPU) disp(m int) uint16 {
	if m == 0 {
		return c.HL()
	}
	d := int8(c.fetch2())
	c.Cycles += 5
	return c.hlx(m) + uint16(int16(d))
}

var cyc = [256]uint8{}

func (c *CPU) exec(op uint8, m int) {
	x := int(op >> 6)
	y := int(op >> 3 & 7)
	z := int(op & 7)
	p := y >> 1
	q := y & 1
	t := 4
	switch x {
	case 0:
		switch z {
		case 0:
			switch y {
			case 0:
			case 1:
				c.A, c.A_ = c.A_, c.A
				c.F, c.F_ = c.F_, c.F
			case 2:
				d := int8(c.fetch2())
				c.B--
				t = 8
				if c.B != 0 {
					c.PC += uint16(int16(d))
					t = 13
				}
			case 3:
				d := int8(c.fetch2())
				c.PC += uint16(int16(d))
				t = 12
			default:
				d := int8(c.fetch2())
				t = 7
				if c.cond(y - 4) {
					c.PC += uint16(int16(d))
					t = 12
				}
			}
		case 1:
			if q == 0 {
				v := c.fetch16()
				c.setrp(p, m, v)
				t = 10
			} else {
				c.sethlx(m, c.add16(c.hlx(m), c.rp(p, m)))
				t = 11
			}
		case 2:
			switch y {
			case 0:
				c.wr(c.BC(), c.A)
				t = 7
			case 1:
				c.A = c.rd(c.BC())
				t = 7
			case 2:
				c.wr(c.DE(), c.A)
				t = 7
			case 3:
				c.A = c.rd(c.DE())
				t = 7
			case 4:
				a := c.fetch16()
				c.wr16(a, c.hlx(m))
				t = 16
			case 5:
				a := c.fetch16()
				c.sethlx(m, c.rd16(a))
				t = 16
			case 6:
				a := c.fetch16()
				c.wr(a, c.A)
				t = 13
			case 7:
				a := c.fetch16()
				c.A = c.rd(a)
				t = 13
			}
		case 3:
			v := c.rp(p, m)
			if q == 0 {
				v++
			} else {
				v--
			}
			c.setrp(p, m, v)
			t = 6
		case 4:
			if y == 6 {
				a := c.disp(m)
				c.wr(a, c.inc8(c.rd(a)))
				t = 11
			} else {
				c.setr(y, m, c.inc8(c.getr(y, m)))
			}
		case 5:
			if y == 6 {
				a := c.disp(m)
				c.wr(a, c.dec8(c.rd(a)))
				t = 11
			} else {
				c.setr(y, m, c.dec8(c.getr(y, m)))
			}
		case 6:
			if y == 6 {
				a := c.disp(m)
				c.wr(a, c.fetch2())
				t = 10
			} else {
				c.setr(y, m, c.fetch2())
				t = 7
			}
		case 7:
			switch y {
			case 0: // RLCA
				cy := c.A >> 7
				c.A = c.A<<1 | cy
				c.F = c.F&(fS|fZ|fP) | c.A&(fX|fY) | cy
			case 1:
				cy := c.A & 1
				c.A = c.A>>1 | cy<<7
				c.F = c.F&(fS|fZ|fP) | c.A&(fX|fY) | cy
			case 2:
				cy := c.A >> 7
				c.A = c.A<<1 | c.F&fC
				c.F = c.F&(fS|fZ|fP) | c.A&(fX|fY) | cy
			case 3:
				cy := c.A & 1
				c.A = c.A>>1 | (c.F&fC)<<7
				c.F = c.F&(fS|fZ|fP) | c.A&(fX|fY) | cy
			case 4: // DAA
				a := c.A
				var adj uint8
				cy := c.F & fC
				if c.F&fH != 0 || a&0xf > 9 {
					adj |= 6
				}
				if cy != 0 || a > 0x99 {
					adj |= 0x60
					cy = fC
				}
				var f uint8
				if c.F&fN != 0 {
					if c.F&fH != 0 && a&0xf < 6 {
						f |= fH
					}
					c.A = a - adj
				} else {
					if a&0xf > 9 {
						f |= fH
					}
					c.A = a + adj
				}
				c.F = szp[c.A] | f | cy | c.F&fN
			case 5:
				c.A = ^c.A
				c.F = c.F&(fS|fZ|fP|fC) | fH | fN | c.A&(fX|fY)
			case 6:
				c.F = c.F&(fS|fZ|fP) | fC | c.A&(fX|fY)
			case 7:
				f := c.F & (fS | fZ | fP)
				if c.F&fC != 0 {
					f |= fH
				} else {
					f |= fC
				}
				c.F = f | c.A&(fX|fY)
			}
		}
	case 1:
		if op == 0x76 {
			c.Halted = true
			c.PC--
			break
		}
		if y == 6 {
			a := c.disp(m)
			c.wr(a, c.getr(z, 0))
			t = 7
		} else if z == 6 {
			a := c.disp(m)
			c.setr(y, 0, c.rd(a))
			t = 7
		} else {
			c.setr(y, m, c.getr(z, m))
		}
	case 2:
		if z == 6 {
			a := c.disp(m)
			c.alu(y, c.rd(a))
			t = 7
		} else {
			c.alu(y, c.getr(z, m))
		}
	case 3:
		switch z {
		case 0:
			t = 5
			if c.cond(y) {
				c.PC = c.pop()
				t = 11
			}
		case 1:
			if q == 0 {
				v := c.pop()
				switch p {
				case 0:
					c.setBC(v)
				case 1:
					c.setDE(v)
				case 2:
					c.sethlx(m, v)
				case 3:
					c.setAF(v)
				}
				t = 10
			} else {
				switch p {
				case 0:
					c.PC = c.pop()
					t = 10
				case 1:
					c.B, c.B_ = c.B_, c.B
					c.C, c.C_ = c.C_, c.C
					c.D, c.D_ = c.D_, c.D
					c.E, c.E_ = c.E_, c.E
					c.H, c.H_ = c.H_, c.H
					c.L, c.L_ = c.L_, c.L
				case 2:
					c.PC = c.hlx(m)
				case 3:
					c.SP = c.hlx(m)
					t = 6
				}
			}
		case 2:
			a := c.fetch16()
			if c.cond(y) {
				c.PC = a
			}
			t = 10
		case 3:
			switch y {
			case 0:
				c.PC = c.fetch16()
				t = 10
			case 2:
				port := uint16(c.fetch2()) | uint16(c.A)<<8
				c.Bus.Out(port, c.A)
				t = 11
			case 3:
				port := uint16(c.fetch2()) | uint16(c.A)<<8
				c.A = c.Bus.In(port)
				t = 11
			case 4:
				v := c.rd16(c.SP)
				c.wr16(c.SP, c.hlx(m))
				c.sethlx(m, v)
				t = 19
			case 5:
				d, h := c.DE(), c.HL()
				c.setDE(h)
				c.setHL(d)
			case 6:
				c.IFF1, c.IFF2 = false, false
			case 7:
				c.IFF1, c.IFF2 = true, true
				c.eiPending = true
			}
		case 4:
			a := c.fetch16()
			t = 10
			if c.cond(y) {
				c.push(c.PC)
				c.PC = a
				t = 17
			}
		case 5:
			if q == 0 {
				var v uint16
				switch p {
				case 0:
					v = c.BC()
				case 1:
					v = c.DE()
				case 2:
					v = c.hlx(m)
				case 3:
					v = c.AF()
				}
				c.push(v)
				t = 11
			} else if p == 0 {
				a := c.fetch16()
				c.push(c.PC)
				c.PC = a
				t = 17
			}
		case 6:
			c.alu(y, c.fetch2())
			t = 7
		case 7:
			c.push(c.PC)
			c.PC = uint16(y) * 8
			t = 11
		}
	}
	c.Cycles += uint64(t)
}

func (c *CPU) execCB(m int) {
	var a uint16
	if m != 0 {
		d := int8(c.fetch2())
		a = c.hlx(m) + uint16(int16(d))
	}
	op := c.fetch2()
	if m == 0 {
		c.R = c.R&0x80 | (c.R+1)&0x7f
	}
	x := int(op >> 6)
	y := int(op >> 3 & 7)
	z := int(op & 7)
	if m == 0 && z != 6 {
		v := c.getr(z, 0)
		switch x {
		case 0:
			c.setr(z, 0, c.rot(y, v))
		case 1:
			c.bit(y, v, v)
		case 2:
			c.setr(z, 0, v&^(1<<y))
		case 3:
			c.setr(z, 0, v|1<<y)
		}
		c.Cycles += 8
		return
	}
	if m == 0 {
		a = c.HL()
	}
	v := c.rd(a)
	switch x {
	case 0:
		r := c.rot(y, v)
		c.wr(a, r)
		if m != 0 && z != 6 {
			c.setr(z, 0, r)
		}
		c.Cycles += 15
	case 1:
		if m == 0 {
			c.bit(y, v, v)
		} else {
			c.bit(y, v, uint8(a>>8))
		}
		c.Cycles += 12
	case 2:
		r := v &^ (1 << y)
		c.wr(a, r)
		if m != 0 && z != 6 {
			c.setr(z, 0, r)
		}
		c.Cycles += 15
	case 3:
		r := v | 1<<y
		c.wr(a, r)
		if m != 0 && z != 6 {
			c.setr(z, 0, r)
		}
		c.Cycles += 15
	}
	if m != 0 {
		c.Cycles += 8
	}
}

func (c *CPU) bit(n int, v uint8, xy uint8) {
	f := c.F&fC | fH | xy&(fX|fY)
	if v&(1<<n) == 0 {
		f |= fZ | fP
	}
	if n == 7 && v&0x80 != 0 {
		f |= fS
	}
	c.F = f
}

func (c *CPU) execED() {
	op := c.fetch()
	x := int(op >> 6)
	y := int(op >> 3 & 7)
	z := int(op & 7)
	p := y >> 1
	q := y & 1
	t := 8
	switch x {
	case 1:
		switch z {
		case 0:
			v := c.Bus.In(c.BC())
			c.F = c.F&fC | szp[v]
			if y != 6 {
				c.setr(y, 0, v)
			}
			t = 12
		case 1:
			var v uint8
			if y != 6 {
				v = c.getr(y, 0)
			}
			c.Bus.Out(c.BC(), v)
			t = 12
		case 2:
			if q == 0 {
				c.setHL(c.sbc16(c.HL(), c.rp(p, 0)))
			} else {
				c.setHL(c.adc16(c.HL(), c.rp(p, 0)))
			}
			t = 15
		case 3:
			a := c.fetch16()
			if q == 0 {
				c.wr16(a, c.rp(p, 0))
			} else {
				c.setrp(p, 0, c.rd16(a))
			}
			t = 20
		case 4:
			c.A = c.sub8(0, c.A, 0)
		case 5:
			c.PC = c.pop()
			c.IFF1 = c.IFF2
			t = 14
		case 6:
			switch y & 3 {
			case 0, 1:
				c.IM = 0
			case 2:
				c.IM = 1
			case 3:
				c.IM = 2
			}
		case 7:
			switch y {
			case 0:
				c.I = c.A
				t = 9
			case 1:
				c.R = c.A
				t = 9
			case 2:
				c.A = c.I
				c.F = c.F&fC | szp[c.A]&^fP
				if c.IFF2 {
					c.F |= fP
				}
				t = 9
			case 3:
				c.A = c.R
				c.F = c.F&fC | szp[c.A]&^fP
				if c.IFF2 {
					c.F |= fP
				}
				t = 9
			case 4: // RRD
				v := c.rd(c.HL())
				n := c.A & 0xf
				c.A = c.A&0xf0 | v&0xf
				c.wr(c.HL(), v>>4|n<<4)
				c.F = c.F&fC | szp[c.A]
				t = 18
			case 5: // RLD
				v := c.rd(c.HL())
				n := c.A & 0xf
				c.A = c.A&0xf0 | v>>4
				c.wr(c.HL(), v<<4|n)
				c.F = c.F&fC | szp[c.A]
				t = 18
			}
		}
	case 2:
		if z <= 3 && y >= 4 {
			t = c.blockOp(y, z)
		}
	}
	c.Cycles += uint64(t)
}

func (c *CPU) blockOp(y, z int) int {
	inc := y&1 == 0 // 4,6: increment ; 5,7: decrement
	repeat := y >= 6
	var d uint16 = 1
	if !inc {
		d = 0xffff
	}
	switch z {
	case 0: // LDI/LDD/LDIR/LDDR
		v := c.rd(c.HL())
		c.wr(c.DE(), v)
		c.setHL(c.HL() + d)
		c.setDE(c.DE() + d)
		c.setBC(c.BC() - 1)
		n := v + c.A
		f := c.F&(fS|fZ|fC) | n&fX | (n<<4)&fY
		if c.BC() != 0 {
			f |= fP
		}
		c.F = f
		if repeat && c.BC() != 0 {
			c.PC -= 2
			return 21
		}
		return 16
	case 1: // CPI/CPD
		v := c.rd(c.HL())
		cf := c.F & fC
		r := c.A - v
		f := cf | fN | r&fS
		if r == 0 {
			f |= fZ
		}
		if c.A&0xf < v&0xf {
			f |= fH
		}
		n := r
		if f&fH != 0 {
			n--
		}
		f |= n&fX | (n<<4)&fY
		c.setHL(c.HL() + d)
		c.setBC(c.BC() - 1)
		if c.BC() != 0 {
			f |= fP
		}
		c.F = f
		if repeat && c.BC() != 0 && r != 0 {
			c.PC -= 2
			return 21
		}
		return 16
	case 2: // INI/IND
		v := c.Bus.In(c.BC())
		c.B--
		c.wr(c.HL(), v)
		c.setHL(c.HL() + d)
		f := c.B&(fS|fX|fY) | 0
		if c.B == 0 {
			f |= fZ
		}
		f |= fN & (v >> 6 & 2)
		c.F = f | c.F&fC
		if repeat && c.B != 0 {
			c.PC -= 2
			return 21
		}
		return 16
	default: // OUTI/OUTD
		v := c.rd(c.HL())
		c.B--
		c.Bus.Out(c.BC(), v)
		c.setHL(c.HL() + d)
		f := c.B&(fS|fX|fY) | 0
		if c.B == 0 {
			f |= fZ
		}
		f |= fN & (v >> 6 & 2)
		c.F = f | c.F&fC
		if repeat && c.B != 0 {
			c.PC -= 2
			return 21
		}
		return 16
	}
}
