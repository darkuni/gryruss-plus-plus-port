package board

import "image/color"

// gfx holds the decoded tile and sprite pixels and colour lookups.
type gfx struct {
	chars   [512][8][8]byte     // 2bpp pens
	sprites [2][256][16][8]byte // 4bpp pens, two banks
	charLUT [64]byte            // color*4+pen -> palette index (16..31)
	sprLUT  [256]byte           // color*16+pen -> palette index (0..15)
	Palette [32]color.RGBA
}

func bit(rom []byte, o int) byte { return rom[o>>3] >> (7 - uint(o&7)) & 1 }

func decodeGfx(tiles, spr, prom []byte) *gfx {
	g := &gfx{}
	xo := [8]int{0, 1, 2, 3, 64, 65, 66, 67}
	for c := 0; c < 512; c++ {
		for y := 0; y < 8; y++ {
			for x := 0; x < 8; x++ {
				o := c*128 + y*8 + xo[x]
				g.chars[c][y][x] = bit(tiles, o+4)<<1 | bit(tiles, o)
			}
		}
	}
	planes := [4]int{0x4000*8 + 4, 0x4000 * 8, 4, 0}
	for bank := 0; bank < 2; bank++ {
		for c := 0; c < 256; c++ {
			for y := 0; y < 16; y++ {
				yo := y * 8
				if y >= 8 {
					yo = 256 + (y-8)*8
				}
				for x := 0; x < 8; x++ {
					o := bank*0x10*8 + c*512 + yo + xo[x]
					var p byte
					for _, pl := range planes {
						p = p<<1 | bit(spr, o+pl)
					}
					g.sprites[bank][c][y][x] = p
				}
			}
		}
	}
	// palette: 3-3-2 resistor network (1k / 470 / 220 with 470 pull-down)
	rg := [3]float64{33.23, 70.71, 151.06}
	bl := [2]float64{78.74, 168.22}
	for i := 0; i < 32; i++ {
		v := prom[i]
		r := float64(v&1)*rg[0] + float64(v>>1&1)*rg[1] + float64(v>>2&1)*rg[2]
		gg := float64(v>>3&1)*rg[0] + float64(v>>4&1)*rg[1] + float64(v>>5&1)*rg[2]
		b := float64(v>>6&1)*bl[0] + float64(v>>7&1)*bl[1]
		g.Palette[i] = color.RGBA{uint8(r + 0.5), uint8(gg + 0.5), uint8(b + 0.5), 255}
	}
	for i := 0; i < 256; i++ {
		g.sprLUT[i] = prom[0x20+i] & 0x0f
	}
	for i := 0; i < 64; i++ {
		g.charLUT[i] = prom[0x120+i]&0x0f | 0x10
	}
	return g
}

// Palette returns the 32 colours of the board.
func (b *Board) Palette() [32]color.RGBA { return b.gfx().Palette }

func (b *Board) gfx() *gfx {
	if b.g == nil {
		b.g = decodeGfx(b.roms.Tiles, b.roms.Sprites, b.roms.PROMs)
	}
	return b.g
}

// renderLine draws one scanline of the 256x256 picture: tiles, sprites,
// then the tiles flagged as being in front of sprites.
func (b *Board) renderLine(y int) {
	g := b.gfx()
	line := b.Frame[y*256 : y*256+256]
	var front [256]bool
	row := y >> 3
	for tx := 0; tx < 32; tx++ {
		idx := row*32 + tx
		attr := b.Color[idx]
		code := int(attr&0x20)<<3 | int(b.VRAM[idx])
		col := int(attr&15) * 4
		ty := y & 7
		if attr&0x80 != 0 {
			ty = 7 - ty
		}
		pix := &g.chars[code][ty]
		fx := attr&0x40 != 0
		fr := attr&0x10 != 0
		for px := 0; px < 8; px++ {
			sx := px
			if fx {
				sx = 7 - px
			}
			line[tx*8+px] = g.charLUT[col+int(pix[sx])]
			front[tx*8+px] = fr
		}
	}
	spr := b.SubRAM[0x40:0x100]
	for offs := 0xbc; offs >= 0; offs -= 4 {
		sy := 241 - int(spr[offs+3])
		r := y - sy
		if r < 0 || r >= 16 {
			continue
		}
		a1, a2 := spr[offs+1], spr[offs+2]
		bank := int(a1 & 1)
		code := int(a2&0x20)<<2 | int(a1>>1)
		col := int(a2&15) * 16
		if a2&0x80 != 0 {
			r = 15 - r
		}
		fx := a2&0x40 == 0
		pix := &g.sprites[bank][code][r]
		x0 := int(spr[offs])
		for px := 0; px < 8; px++ {
			x := x0 + px
			if x > 255 {
				break
			}
			sx := px
			if fx {
				sx = 7 - px
			}
			p := pix[sx]
			if p == 0 || front[x] {
				continue
			}
			line[x] = g.sprLUT[col+int(p)]
		}
	}
}
