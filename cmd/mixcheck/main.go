package main

import (
	"fmt"
	"math"

	"gyruss/internal/assets"
	"gyruss/internal/board"
	"gyruss/internal/drums"
)

func run(mode int, gain float64) []float32 {
	r, _ := assets.Load("/home/claude/roms")
	b := board.New(r)
	k, _ := drums.FromPak("/mnt/user-data/uploads/_Gyruss++_/wav/WAV.PAK")
	switch mode {
	case 0: // kit
		b.SetKit(&board.Kit{Samples: k.Samples, Rate: k.Rate, Gain: gain})
	case 1: // original
	case 2: // no drums at all
		b.DrumMute = true
	}
	b.OnDrum = func(v byte) { b.TriggerDrum(int(v)) }
	var out []float32
	for f := 0; f < 3600; f++ {
		b.System, b.P1 = 0xff, 0xff
		if f >= 300 && f < 305 {
			b.System &^= board.Coin1
		}
		if f >= 330 && f < 335 {
			b.System &^= board.Start1
		}
		if f > 400 {
			b.MainRAM[0x600] = 3
			if (f/120)%3 != 2 {
				b.P1 &^= board.JoyLeft
			}
			if f%6 < 3 {
				b.P1 &^= board.JoyFire
			}
		}
		b.RunFrame()
		out = append(out, b.DrainSamples()...)
	}
	return out
}

func rms(a, ref []float32) (float64, float64) {
	var s, p float64
	n := len(a)
	if ref != nil && len(ref) < n {
		n = len(ref)
	}
	for i := 0; i < n; i++ {
		d := float64(a[i])
		if ref != nil {
			d -= float64(ref[i])
		}
		s += d * d
		if math.Abs(d) > p {
			p = math.Abs(d)
		}
	}
	return math.Sqrt(s / float64(n)), p
}

func main() {
	none := run(2, 0)
	orig := run(1, 0)
	kit := run(0, 0.45)
	m, mp := rms(none, nil)
	o, op := rms(orig, none)
	k, kp := rms(kit, none)
	fmt.Printf("music rms %.4f peak %.3f\noriginal drums rms %.4f peak %.3f\nkit drums rms %.4f peak %.3f\n", m, mp, o, op, k, kp)
}
