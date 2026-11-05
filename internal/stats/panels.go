package stats

import (
	"fmt"
	"image"
	"image/color"

	"gyruss/internal/video"
)

var (
	colLabel = color.RGBA{73, 182, 182, 255}
	colValue = color.RGBA{255, 255, 255, 255}
	colHot   = color.RGBA{255, 255, 73, 255}
	colDim   = color.RGBA{110, 110, 140, 255}
	colDark  = color.RGBA{40, 40, 60, 255}
)

// planet colours for the journey map
var planetCol = [6]color.RGBA{
	{60, 120, 255, 255}, // Neptune
	{80, 220, 200, 255}, // Uranus
	{230, 190, 70, 255}, // Saturn
	{210, 140, 60, 255}, // Jupiter
	{220, 70, 50, 255},  // Mars
	{70, 150, 255, 255}, // Earth
}

func num(n int) string {
	s := fmt.Sprint(n)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}

func disc(dst *image.RGBA, cx, cy, r int, c color.RGBA, ring bool) {
	for y := -r; y <= r; y++ {
		for x := -r; x <= r; x++ {
			d := x*x + y*y
			if d > r*r {
				continue
			}
			if ring && d < (r-1)*(r-1) {
				continue
			}
			dst.SetRGBA(cx+x, cy+y, c)
		}
	}
}

// DrawPanels draws the Gyruss+ read-outs in the side bars of the 384x216
// widescreen picture. They follow the player whose turn it is.
func DrawPanels(dst *image.RGBA, t *Tracker) {
	cur := t.Cur
	if !t.Active {
		cur = 0
	}
	game, life := &t.GameP[cur], &t.LifeP[cur]

	// ---- left: this game, then all time --------------------------------
	x, y := 6, 42
	if t.Active && t.Players > 1 {
		DrawText(dst, x, y, fmt.Sprintf("PLAYER %d STATS", cur+1), colHot, 1)
	}
	y += 9
	DrawText(dst, x, y, "KILLS", colLabel, 1)
	DrawText(dst, x, y+7, num(game.Kills), colValue, 2)
	y += 21
	small := func(label, value string, c color.RGBA) {
		DrawText(dst, x, y, label, colDim, 1)
		DrawText(dst, x+TextWidth(label+" ", 1), y, value, c, 1)
		y += 7
	}
	small("SATELLITES", num(game.Satellites), colValue)
	small("HIT RATE", fmt.Sprintf("%d%%", game.HitRate()), colValue)
	small("BEST RUN", num(game.BestKillRun), colValue)
	small("CHANCE", fmt.Sprintf("%d/%d", game.BestChance, ChanceEnemies), colValue)
	small("PERFECT", num(game.Perfect), colValue)
	y += 3
	DrawText(dst, x, y, "ALL TIME", colHot, 1)
	y += 8
	small("GAMES", num(life.Games), colValue)
	small("BEST", num(life.BestScore), colHot)
	small("KILLS", num(life.Kills), colValue)
	small("BEST STAGE", num(life.BestStage), colValue)
	small("PLANETS", num(life.Planets), colValue)
	small("EARTHS", num(life.Earths), colHot)
	small("BEST CHANCE", fmt.Sprintf("%d/%d", life.BestChance, ChanceEnemies), colValue)

	// ---- right: the journey to Earth -------------------------------------
	rx := video.RightX + 6
	ry := 52
	stage := t.Stage
	if !t.Active {
		stage = 1
	}
	planet, warps := Place(stage)
	loop := (stage - 1) / 24
	switch {
	case warps == 0:
		DrawText(dst, rx, ry, Planets[planet], planetCol[planet], 1)
		DrawText(dst, rx, ry+7, "CHANCE STAGE", colHot, 1)
		DrawText(dst, rx, ry+14, fmt.Sprintf("HITS %d/%d", t.ChanceNow, ChanceEnemies), colValue, 1)
	default:
		w := "WARPS"
		if warps == 1 {
			w = "WARP"
		}
		DrawText(dst, rx, ry, fmt.Sprintf("%d %s TO", warps, w), colLabel, 1)
		DrawText(dst, rx, ry+7, Planets[planet], planetCol[planet], 1)
	}
	ry += 22
	if loop > 0 {
		DrawText(dst, rx, ry-4, fmt.Sprintf("TRIP %d", loop+1), colDim, 1)
		ry += 4
	}
	// Earth at the top, Neptune at the bottom, the ship climbing
	for i := 5; i >= 0; i-- {
		py := ry + (5-i)*17 + 6
		c := planetCol[i]
		reached := i < planet || (i == planet && warps == 0)
		lc := colDim
		switch {
		case i == planet:
			lc = colHot
		case reached:
			lc = colValue
		}
		if !reached && i != planet {
			disc(dst, rx+5, py, 4, colDark, false)
			disc(dst, rx+5, py, 4, c, true)
		} else {
			disc(dst, rx+5, py, 4, c, false)
		}
		DrawText(dst, rx+14, py-2, Planets[i], lc, 1)
		if i == planet && warps > 0 {
			// pips for the warps still to go, under the name
			for k := 0; k < warps; k++ {
				disc(dst, rx+15+k*6, py+7, 1, colHot, false)
			}
		}
	}
}

// DrawToast shows a short message at the bottom centre of a picture.
func DrawToast(dst *image.RGBA, s string) {
	b := dst.Bounds()
	w := TextWidth(s, 1)
	x := (b.Dx() - w) / 2
	y := b.Dy() - 30
	for yy := y - 3; yy < y+8; yy++ {
		for xx := x - 4; xx < x+w+4; xx++ {
			if image.Pt(xx, yy).In(b) {
				dst.SetRGBA(xx, yy, color.RGBA{0, 0, 0, 255})
			}
		}
	}
	DrawText(dst, x, y, s, colHot, 1)
}
