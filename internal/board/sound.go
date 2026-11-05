package board

import (
	"math"
	"sync"
)

// SampleRate is the audio output rate.
const SampleRate = 44100

const ayTickHz = 3579545.0 / 2 / 8

// sound mixes the five AY-3-8910s, their RC filters and the drum channel
// into stereo the way the board's discrete mixer does.
type sound struct {
	mu       sync.Mutex
	out      []float32 // interleaved L,R
	acc      float64
	sumL     float64
	sumR     float64
	n        int
	lp       [6]float64
	hpL, hpR biquad
	lpL, lpR float64
	voices   []voice
	pos      float64
}

type voice struct {
	id  int
	pcm []float32
	pos float64
}

// Kit is a set of replacement drum samples (22050 Hz mono, -1..1), indexed
// the way the drum commands select them.
type Kit struct {
	Samples map[int][]float32
	Rate    float64
	Gain    float64
}

func (s *sound) init() {}

// RC filter on chips 1 and 2: R = AY output resistance + 1k, C switched in
// by port B bits (bit 0: 0.047uF, bit 1: 0.22uF).
// Width is the stereo width: 1 keeps the board's hard left / right split,
// 0 is mono. 0.6 matches the channel blend of the MAME reference.
var Width = 0.6

// OutGain is the master output level.
var OutGain = 3.8

// FXGain scales the two effect chips (1 and 2) against the music chips.
// Measured on the game's opening (same events in both) against a MAME
// recording: with the effect chips at 0.35 both sides of the mix land
// within 10% of MAME and the music / effect balance matches.
var FXGain = 0.35

// MuteChip silences one AY (1-5) for listening tests; 0 = none.
var MuteChip int

const filtR = 356.0 + 1000.0

var filtAlpha [4]float64

func init() {
	dt := 1 / ayTickHz
	for i := 1; i < 4; i++ {
		c := 0.0
		if i&1 != 0 {
			c += 0.047e-6
		}
		if i&2 != 0 {
			c += 0.22e-6
		}
		filtAlpha[i] = dt / (filtR*c + dt)
	}
}

func (s *sound) tick(b *Board) {
	var ch [5][3]float64
	for i, c := range b.AY {
		c.Tick()
		ch[i][0], ch[i][1], ch[i][2] = c.Output()
		if MuteChip == i+1 {
			ch[i] = [3]float64{}
		}
	}
	// switchable RC filters on chips 1 (right) and 2 (left)
	for chip := 0; chip < 2; chip++ {
		sel := b.Filter[chip]
		for k := 0; k < 3; k++ {
			f := sel >> uint(2*k) & 3
			i := chip*3 + k
			if f == 0 {
				s.lp[i] = ch[chip][k]
			} else {
				s.lp[i] += filtAlpha[f] * (ch[chip][k] - s.lp[i])
			}
			ch[chip][k] = s.lp[i]
		}
	}
	avg := func(c [3]float64) float64 { return (c[0] + c[1] + c[2]) / 3 }
	// resistor mixer: 2.2k per voice of chips 1/2, 1.1k for each of the
	// summed chips 3/4/5, 4.7k for the DAC
	var drum float64
	if !b.DrumMute {
		drum = (float64(b.DAC) - 128) / 128 * 2 // +-2V around the 2V midpoint
	}
	var kd float64 // replacement kit: mixed to the centre, not hard left
	if len(s.voices) > 0 {
		step := b.kit.Rate / ayTickHz
		k := 0
		for _, v := range s.voices {
			i := int(v.pos)
			if i < len(v.pcm) {
				kd += float64(v.pcm[i]) * 2 * b.kit.Gain
				v.pos += step
				s.voices[k] = v
				k++
			}
		}
		s.voices = s.voices[:k]
	}
	right := FXGain*(ch[0][0]+ch[0][1]+ch[0][2])/2.2 + avg(ch[2])/1.1 + avg(ch[3])/1.1
	right /= 3/2.2 + 2/1.1
	right += kd / 4.7 / (3/2.2 + 2/1.1) * 0.75
	left := FXGain*(ch[1][0]+ch[1][1]+ch[1][2])/2.2 + avg(ch[4])/1.1 + drum/4.7
	left /= 3/2.2 + 1/1.1 + 1/4.7
	left += kd / 4.7 / (3/2.2 + 1/1.1 + 1/4.7) * 0.75

	s.sumL += left
	s.sumR += right
	s.n++
	s.acc += SampleRate
	if s.acc >= ayTickHz {
		s.acc -= ayTickHz
		l := s.sumL / float64(s.n)
		r := s.sumR / float64(s.n)
		s.sumL, s.sumR, s.n = 0, 0, 0
		// the mixer's 0.1uF output capacitor rolls off the top (~10 kHz)
		s.lpL += mixLP * (l - s.lpL)
		s.lpR += mixLP * (r - s.lpR)
		// coupling capacitor, and with Cabinet on the bass roll-off of an
		// arcade amp and speaker
		hp := &hpFlat
		if b.Cabinet {
			hp = &hpCab
		}
		yl := s.hpL.run(hp, s.lpL)
		yr := s.hpR.run(hp, s.lpR)
		if Width < 1 {
			mid := (yl + yr) / 2
			yl, yr = mid+(yl-mid)*Width, mid+(yr-mid)*Width
		}
		s.mu.Lock()
		s.out = append(s.out, clip(yl*OutGain), clip(yr*OutGain))
		s.mu.Unlock()
	}
}

// biquad is a second-order filter section (direct form I).
type biquad struct{ x1, x2, y1, y2 float64 }

type coefs struct{ b0, b1, b2, a1, a2 float64 }

func (q *biquad) run(c *coefs, x float64) float64 {
	y := c.b0*x + c.b1*q.x1 + c.b2*q.x2 - c.a1*q.y1 - c.a2*q.y2
	q.x2, q.x1 = q.x1, x
	q.y2, q.y1 = q.y1, y
	return y
}

// highpass returns Butterworth high-pass coefficients for fc at SampleRate.
func highpass(fc float64) coefs {
	w := 2 * math.Pi * fc / SampleRate
	cw, sw := math.Cos(w), math.Sin(w)
	alpha := sw / math.Sqrt2
	a0 := 1 + alpha
	return coefs{
		b0: (1 + cw) / 2 / a0, b1: -(1 + cw) / a0, b2: (1 + cw) / 2 / a0,
		a1: -2 * cw / a0, a2: (1 - alpha) / a0,
	}
}

var (
	hpFlat = highpass(20)
	hpCab  = highpass(CabinetHz)
	mixLP  = 1 - math.Exp(-2*math.Pi*10600/SampleRate)
)

// CabinetHz is the bass roll-off used with Board.Cabinet.
const CabinetHz = 110

func clip(v float64) float32 {
	if v > 1 {
		return 1
	}
	if v < -1 {
		return -1
	}
	return float32(v)
}

// SetKit installs replacement drum samples. With a kit installed the 8039's
// own DAC is muted and drum commands play the kit instead.
func (b *Board) SetKit(k *Kit) {
	b.kit = k
	b.DrumMute = k != nil
}

// TriggerDrum plays the kit sample for a drum command, the way Retrocade's
// Gyruss driver did: one drum channel, command N plays AudioN and cuts off
// whatever drum was sounding; command 0 just stops the drum.
func (b *Board) TriggerDrum(id int) {
	if b.kit == nil {
		return
	}
	s := &b.snd
	s.voices = s.voices[:0]
	if pcm, ok := b.kit.Samples[id&7]; ok && id&7 != 0 {
		s.voices = append(s.voices, voice{id: id & 7, pcm: pcm})
	}
}

// DrainSamples returns the stereo samples produced since the last call.
func (b *Board) DrainSamples() []float32 {
	s := &b.snd
	s.mu.Lock()
	out := s.out
	s.out = nil
	s.mu.Unlock()
	return out
}
