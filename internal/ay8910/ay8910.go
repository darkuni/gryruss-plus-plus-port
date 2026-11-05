// Package ay8910 is a General Instrument AY-3-8910 programmable sound
// generator: three square-wave tone channels, a noise generator, the
// envelope generator and the two 8-bit I/O ports.
package ay8910

// Volume curve of the real chip (4-bit level, roughly 3 dB per step),
// normalised to 1.0 at full level.
var level = [16]float64{
	0, 0.0137, 0.0205, 0.0291, 0.0423, 0.0618, 0.0847, 0.1369,
	0.1691, 0.2647, 0.3527, 0.4499, 0.5704, 0.6873, 0.8482, 1.0,
}

// Chip is one AY-3-8910.
type Chip struct {
	Reg  [16]byte
	addr byte

	PortARead  func() byte
	PortBWrite func(v byte)

	tone    [3]uint16 // counters
	out     [3]bool
	noiseC  uint16
	lfsr    uint32
	noise   bool
	envC    uint32
	envPos  int // 0..31
	envHold bool
	envAtt  bool
	envAlt  bool
	envOn   bool
}

// New returns a chip in its power-on state.
func New() *Chip {
	c := &Chip{lfsr: 1}
	c.Reg[7] = 0xff
	return c
}

// Address latches the register number.
func (c *Chip) Address(v byte) { c.addr = v & 0x0f }

// Read returns the latched register (port A goes through PortARead).
func (c *Chip) Read() byte {
	if c.addr == 14 && c.Reg[7]&0x40 == 0 && c.PortARead != nil {
		return c.PortARead()
	}
	return c.Reg[c.addr]
}

// Write stores into the latched register.
func (c *Chip) Write(v byte) {
	r := c.addr
	c.Reg[r] = v
	switch r {
	case 13:
		c.envStart()
	case 15:
		if c.PortBWrite != nil {
			c.PortBWrite(v)
		}
	}
}

func (c *Chip) envStart() {
	s := c.Reg[13] & 15
	c.envPos = 0
	c.envHold = false
	c.envAtt = s&4 != 0
	c.envOn = true
	c.envC = 0
}

// Tick advances the chip by 8 input clocks.
func (c *Chip) Tick() {
	// tone: the output toggles every period ticks (f = clock / 16P)
	for i := 0; i < 3; i++ {
		p := uint16(c.Reg[2*i]) | uint16(c.Reg[2*i+1]&15)<<8
		if p == 0 {
			p = 1
		}
		c.tone[i]++
		if c.tone[i] >= p {
			c.tone[i] = 0
			c.out[i] = !c.out[i]
		}
	}
	// noise: the LFSR shifts every 2*period ticks (f = clock / 16NP)
	np := uint16(c.Reg[6] & 31)
	if np == 0 {
		np = 1
	}
	c.noiseC++
	if c.noiseC >= np*2 {
		c.noiseC = 0
		bit := (c.lfsr ^ (c.lfsr >> 3)) & 1
		c.lfsr = (c.lfsr >> 1) | bit<<16
		c.noise = c.lfsr&1 != 0
	}
	// envelope: 16 steps per cycle, one step every 2*period ticks
	if c.envOn {
		ep := uint32(c.Reg[11]) | uint32(c.Reg[12])<<8
		if ep == 0 {
			ep = 1
		}
		c.envC++
		if c.envC >= ep*2 {
			c.envC = 0
			if !c.envHold {
				c.envPos++
				if c.envPos >= 16 {
					s := c.Reg[13] & 15
					cont := s&8 != 0
					hold := s&1 != 0
					alt := s&2 != 0
					if !cont {
						c.envHold = true
						c.envAtt = false
						c.envPos = 15
					} else if hold {
						c.envHold = true
						if alt {
							c.envAtt = !c.envAtt
						}
						c.envPos = 15
					} else {
						c.envPos = 0
						if alt {
							c.envAtt = !c.envAtt
						}
					}
				}
			}
		}
	}
}

func (c *Chip) envLevel() int {
	if !c.envOn {
		return 0
	}
	s := c.Reg[13] & 15
	if c.envHold && s&8 == 0 {
		return 0 // one-shot shapes end at 0
	}
	p := c.envPos & 15
	if c.envAtt {
		return p
	}
	return 15 - p
}

// Output returns the three channel levels (0..1).
func (c *Chip) Output() (a, b, ch float64) {
	var v [3]float64
	mix := c.Reg[7]
	for i := 0; i < 3; i++ {
		toneOff := mix>>uint(i)&1 != 0
		noiseOff := mix>>uint(i+3)&1 != 0
		on := (c.out[i] || toneOff) && (c.noise || noiseOff)
		if !on {
			continue
		}
		vol := c.Reg[8+i]
		if vol&0x10 != 0 {
			v[i] = level[c.envLevel()]
		} else {
			v[i] = level[vol&15]
		}
	}
	return v[0], v[1], v[2]
}
