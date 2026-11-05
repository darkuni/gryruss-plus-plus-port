// Package video turns the board's 256x256 frame into the pictures the player
// sees: the original upright (portrait) screen or the widescreen layout that
// moves the heads-up display into side bars.
package video

import (
	"image"
	"image/color"
)

// Upright picture (the monitor is turned 90 degrees in the cabinet).
const (
	ScreenW = 224
	ScreenH = 256
)

// Widescreen layout: the 224x216 play area sits in the middle of a 384x216
// (16:9) canvas; the HUD strips the cabinet shows at the top and bottom of
// the tube are moved into the two 80 pixel side bars.
const (
	LandW    = 384
	LandH    = 216
	SideW    = 80
	FieldTop = 24 // first play-area row of the upright picture
	RightX   = SideW + ScreenW
)

type Rect struct{ X, Y, W, H int }

// HUD pieces of the upright picture.
var (
	HudP1     = Rect{0, 0, 72, 16}    // 1-UP and score
	HudHigh   = Rect{72, 0, 80, 16}   // HI-SCORE
	HudP2     = Rect{152, 0, 72, 16}  // 2-UP and score
	HudStage  = Rect{144, 16, 80, 8}  // STAGE n
	HudLives  = Rect{0, 240, 80, 16}  // reserve ships
	HudCredit = Rect{144, 248, 80, 8} // CREDIT nn
)

// Upright converts the board frame (palette indices) into the upright
// picture.
func Upright(dst *image.RGBA, frame []byte, pal *[32]color.RGBA) {
	for y := 16; y < 240; y++ {
		row := frame[y*256 : y*256+256]
		px := 239 - y
		for x := 0; x < 256; x++ {
			c := pal[row[x]&31]
			o := x*dst.Stride + px*4
			dst.Pix[o], dst.Pix[o+1], dst.Pix[o+2], dst.Pix[o+3] = c.R, c.G, c.B, 255
		}
	}
}

// Blit copies src rectangle r to (dx, dy) in dst.
func Blit(dst, src *image.RGBA, r Rect, dx, dy int) {
	for y := 0; y < r.H; y++ {
		so := src.PixOffset(r.X, r.Y+y)
		do := dst.PixOffset(dx, dy+y)
		copy(dst.Pix[do:do+r.W*4], src.Pix[so:so+r.W*4])
	}
}

// Fill paints a rectangle.
func Fill(dst *image.RGBA, r Rect, c color.RGBA) {
	for y := r.Y; y < r.Y+r.H; y++ {
		o := dst.PixOffset(r.X, y)
		for x := 0; x < r.W; x++ {
			dst.Pix[o+4*x], dst.Pix[o+4*x+1], dst.Pix[o+4*x+2], dst.Pix[o+4*x+3] = c.R, c.G, c.B, c.A
		}
	}
}

var (
	black   = color.RGBA{0, 0, 0, 255}
	divider = color.RGBA{24, 36, 92, 255}
)

// clipBlit copies src rectangle r to (dx, dy), keeping it inside the
// bar x0 .. x1-1.
func clipBlit(dst, src *image.RGBA, r Rect, dx, dy, x0, x1 int) {
	if dx < x0 {
		r.X += x0 - dx
		r.W -= x0 - dx
		dx = x0
	}
	if dx+r.W > x1 {
		r.W = x1 - dx
	}
	if r.W > 0 {
		Blit(dst, src, r, dx, dy)
	}
}

// Landscape builds the widescreen picture. up is the upright picture, hud
// the upright picture without sprites, from which the read-outs are taken
// (so stars and ships flying through the top and bottom rows of the tube
// don't get copied into the side bars).
func Landscape(dst, up, hud *image.RGBA) {
	Fill(dst, Rect{0, 0, LandW, LandH}, black)
	Blit(dst, up, Rect{0, FieldTop, ScreenW, LandH}, SideW, 0)
	// a 2 pixel black gutter each side of the play area keeps the ship,
	// which flies right up to the edge of the tube, off the dividers
	const gap = 2
	// left bar: player 1, the stage, reserve ships
	l0, l1 := 0, SideW-1-gap
	clipBlit(dst, hud, HudP1, 4, 6, l0, l1)
	clipBlit(dst, hud, HudStage, 0, 28, l0, l1)
	clipBlit(dst, hud, HudLives, 0, LandH-20, l0, l1)
	// right bar: high score, player 2, credits
	r0, r1 := RightX+gap+1, LandW
	clipBlit(dst, hud, HudHigh, RightX+gap, 6, r0, r1)
	clipBlit(dst, hud, HudP2, RightX+gap+3, 28, r0, r1)
	clipBlit(dst, hud, HudCredit, RightX-1, LandH-12, r0, r1)
	// dividers last, so nothing can draw over them
	for y := 0; y < LandH; y++ {
		dst.SetRGBA(SideW-1-gap, y, divider)
		dst.SetRGBA(RightX+gap, y, divider)
	}
}
