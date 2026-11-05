package drums

import (
	"embed"
	"fmt"
)

// The Roland drum samples from Retrocade's Gyruss driver (Retrocade's
// WAV.PAK, Gyruss/Audio01-06), included with permission. Decoded to plain
// 22050 Hz 16-bit mono WAV files.
//
//go:embed kit/*.wav
var kitFS embed.FS

// Builtin returns the drum kit that ships inside the program.
func Builtin() *Kit {
	k := &Kit{Samples: map[int][]float32{}, Source: "built in"}
	for n := 1; n <= 7; n++ {
		d, err := kitFS.ReadFile(fmt.Sprintf("kit/Audio%02d.wav", n))
		if err != nil {
			continue
		}
		pcm, rate, err := decodeWav(d)
		if err != nil {
			continue
		}
		k.Rate = float64(rate)
		k.Samples[n] = pcm
	}
	if len(k.Samples) == 0 {
		return nil
	}
	return k
}
