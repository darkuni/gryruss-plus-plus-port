package board

import "sync"

// SampleRate is the audio output rate.
const SampleRate = 44100

const ayTickHz = 3579545.0 / 2 / 8

// sound mixes the five AY-3-8910s, their RC filters and the drum channel
// into stereo the way the board's discrete mixer does.
type sound struct {
	mu     sync.Mutex
	out    []float32 // interleaved L,R
	acc    float64
	sumL   float64
	sumR   float64
	n      int
	lp     [6]float64
	hpXL   float64
	hpYL   float64
	hpXR   float64
	hpYR   float64
	voices []voice
	pos    float64
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
	right := (ch[0][0]+ch[0][1]+ch[0][2])/2.2 + avg(ch[2])/1.1 + avg(ch[3])/1.1
	right /= 3/2.2 + 2/1.1
	right += kd / 4.7 / (3/2.2 + 2/1.1) * 0.75
	left := (ch[1][0]+ch[1][1]+ch[1][2])/2.2 + avg(ch[4])/1.1 + drum/4.7
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
		// DC removal (coupling capacitor)
		yl := l - s.hpXL + 0.995*s.hpYL
		s.hpXL, s.hpYL = l, yl
		yr := r - s.hpXR + 0.995*s.hpYR
		s.hpXR, s.hpYR = r, yr
		s.mu.Lock()
		s.out = append(s.out, clip(yl*1.8), clip(yr*1.8))
		s.mu.Unlock()
	}
}

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

// TriggerDrum starts the kit sample for a drum command. The board's
// 8039 took the same command numbers: N plays drum sound N.
func (b *Board) TriggerDrum(id int) {
	if b.kit == nil {
		return
	}
	pcm, ok := b.kit.Samples[id]
	if !ok {
		return
	}
	s := &b.snd
	// the same drum re-triggered cuts itself off, different drums overlap
	k := 0
	for _, v := range s.voices {
		if v.id != id {
			s.voices[k] = v
			k++
		}
	}
	s.voices = append(s.voices[:k], voice{id: id, pcm: pcm})
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
