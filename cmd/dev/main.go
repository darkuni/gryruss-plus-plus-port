// Command dev runs the board headless for testing: screenshots, drum
// command log, audio dump.
package main

import (
	"encoding/binary"
	"flag"
	"fmt"
	"image"
	"image/png"
	"os"
	"strconv"
	"strings"

	"gyruss/internal/assets"
	"gyruss/internal/board"
	"gyruss/internal/drums"
	"gyruss/internal/stats"
	"gyruss/internal/video"
)

var tr = stats.New()

func main() {
	rom := flag.String("rom", "gyruss.zip", "rom zip or folder")
	pak := flag.String("pak", "wav/WAV.PAK", "Retrocade WAV.PAK with the drum kit")
	frames := flag.Int("n", 600, "frames")
	shots := flag.String("shots", "", "comma separated frame numbers to screenshot")
	coin := flag.Int("coin", -1, "frame to insert a coin and start")
	wav := flag.String("wav", "", "write audio to this wav file")
	play := flag.Bool("play", false, "random play after start")
	two := flag.Bool("two", false, "start a 2 player game")
	flag.Bool("inf", false, "infinite lives")
	flag.IntVar(&board.MuteChip, "mutechip", 0, "mute one AY chip 1-5")
	cab := flag.Bool("cab", false, "cabinet bass roll-off")
	retro := flag.Bool("retro", false, "Retrocade-style zero-period tones")
	flag.Float64Var(&board.FXGain, "fx", 0.35, "effect chip gain")
	flag.Float64Var(&board.Width, "width", 0.6, "stereo width")
	nod := flag.Bool("nodrum", false, "mute all drums")
	gain := flag.Float64("gain", 0.8, "kit gain")
	flag.Parse()
	r, err := assets.Load(*rom)
	if err != nil {
		panic(err)
	}
	b := board.New(r)
	dr := map[byte]int{}
	if k, err := drums.FromPak(*pak); err == nil {
		b.SetKit(&board.Kit{Samples: k.Samples, Rate: k.Rate, Gain: *gain})
		fmt.Println("kit", len(k.Samples), k.Rate)
	}
	b.OnDrum = func(v byte) { dr[v]++; b.TriggerDrum(int(v)) }
	tr.Attach(b)
	b.Cabinet = *cab
	b.SetRetroSFX(*retro)
	if *nod {
		b.SetKit(nil)
		b.DrumMute = true
	}
	inf := flag.Lookup("inf").Value.String() == "true"
	want := map[int]bool{}
	for _, s := range strings.Split(*shots, ",") {
		if n, err := strconv.Atoi(s); err == nil {
			want[n] = true
		}
	}
	var audio []float32
	seed := uint32(1)
	for f := 0; f < *frames; f++ {
		b.System, b.P1 = 0xff, 0xff
		if *coin >= 0 {
			switch {
			case f >= *coin && f < *coin+5:
				b.System &^= board.Coin1
			case *two && f >= *coin+15 && f < *coin+20:
				b.System &^= board.Coin1
			case f >= *coin+30 && f < *coin+35:
				if *two {
					b.System &^= board.Start2
				} else {
					b.System &^= board.Start1
				}
			}
			if *play && f > *coin+60 && !inf {
				seed = seed*1103515245 + 12345
				if (f/20)%2 == 0 {
					b.P1 &^= board.JoyLeft
				} else {
					b.P1 &^= board.JoyRight
				}
				if seed>>16&1 == 0 {
					b.P1 &^= board.JoyFire
				}
			}
		}
		if inf && f > *coin+100 {
			b.MainRAM[0x600] = 3
		}
		if *play && inf && f > *coin+60 {
			if (f/120)%3 != 2 {
				b.P1 &^= board.JoyLeft
			}
			if f%6 < 3 {
				b.P1 &^= board.JoyFire
			}
		}
		b.RunFrame()
		tr.Frame(b, b.P1)
		audio = append(audio, b.DrainSamples()...)
		if want[f] {
			shot(b, fmt.Sprintf("/tmp/shot_%05d.png", f))
		}
	}
	fmt.Println("main PC", fmt.Sprintf("%04x", b.Main.PC), "sub PC", fmt.Sprintf("%04x", b.Sub.PC), "aud PC", fmt.Sprintf("%04x", b.Audio.PC), "drum PC", fmt.Sprintf("%03x", b.Drum.PC))
	fmt.Println("drum commands:", dr)
	if *wav != "" {
		writeWav(*wav, audio)
	}
}

func shot(b *board.Board, fn string) {
	pal := b.Palette()
	up := image.NewRGBA(image.Rect(0, 0, video.ScreenW, video.ScreenH))
	video.Upright(up, b.Frame[:], &pal)
	land := image.NewRGBA(image.Rect(0, 0, video.LandW, video.LandH))
	hud := image.NewRGBA(image.Rect(0, 0, video.ScreenW, video.ScreenH))
	video.Upright(hud, b.Tiles[:], &pal)
	video.Landscape(land, up, hud)
	stats.DrawPanels(land, tr)
	lf, _ := os.Create(fn[:len(fn)-4] + "_land.png")
	png.Encode(lf, land)
	lf.Close()
	// rotate 90 degrees clockwise, visible lines 16..239
	img := image.NewRGBA(image.Rect(0, 0, 224, 256))
	for y := 16; y < 240; y++ {
		for x := 0; x < 256; x++ {
			c := pal[b.Frame[y*256+x]&31]
			img.Set(239-y, x, c)
		}
	}
	f, _ := os.Create(fn)
	png.Encode(f, img)
	f.Close()
}

func writeWav(fn string, s []float32) {
	f, _ := os.Create(fn)
	defer f.Close()
	n := len(s)
	hdr := []any{[4]byte{'R', 'I', 'F', 'F'}, uint32(36 + n*2), [4]byte{'W', 'A', 'V', 'E'}, [4]byte{'f', 'm', 't', ' '}, uint32(16), uint16(1), uint16(2), uint32(board.SampleRate), uint32(board.SampleRate * 4), uint16(4), uint16(16), [4]byte{'d', 'a', 't', 'a'}, uint32(n * 2)}
	for _, h := range hdr {
		binary.Write(f, binary.LittleEndian, h)
	}
	buf := make([]int16, n)
	for i, v := range s {
		buf[i] = int16(v * 32000)
	}
	binary.Write(f, binary.LittleEndian, buf)
}
