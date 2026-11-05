// Package drums loads the replacement drum kit: the Roland drum samples
// from Retrocade's sound pack (WAV.PAK), or plain WAV files.
//
// Retrocade's Gyruss driver replaced the 8039 drum CPU's 8-bit samples with
// recordings named Gyruss/Audio01..Audio06; drum command N plays AudioN.
package drums

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Kit is a set of drum samples keyed by drum command, mono -1..1.
type Kit struct {
	Samples map[int][]float32
	Rate    float64
	Source  string
}

// entry is one sample inside a Retrocade .PAK file.
type entry struct {
	Game, Name string
	Rate       int
	PCM        []int16
}

// readPak decodes every sample in a Retrocade sound pack.
//
// Layout: a 9 byte header, then per entry: name and game as length-prefixed
// strings, five little-endian dwords (id, 0, compressed size, size, 0) and a
// zlib stream. Inside the stream is a 24 byte header (flags, bits, stereo,
// pad, rate, length, 0, count, 0) and the samples, stored as two byte planes
// (low bytes, then high bytes) of a third-order delta biased by 0x80.
func readPak(data []byte) ([]entry, error) {
	if len(data) < 9 {
		return nil, errors.New("pak too short")
	}
	var out []entry
	pos := 9
	str := func() (string, error) {
		if pos >= len(data) {
			return "", io.ErrUnexpectedEOF
		}
		n := int(data[pos])
		if pos+1+n > len(data) {
			return "", io.ErrUnexpectedEOF
		}
		s := string(data[pos+1 : pos+1+n])
		pos += 1 + n
		return s, nil
	}
	for pos < len(data)-4 {
		name, err := str()
		if err != nil {
			return out, err
		}
		game, err := str()
		if err != nil {
			return out, err
		}
		if pos+20 > len(data) {
			return out, io.ErrUnexpectedEOF
		}
		clen := int(binary.LittleEndian.Uint32(data[pos+8:]))
		pos += 20
		if pos+clen > len(data) {
			return out, io.ErrUnexpectedEOF
		}
		zr, err := zlib.NewReader(bytes.NewReader(data[pos : pos+clen]))
		if err != nil {
			return out, err
		}
		raw, err := io.ReadAll(zr)
		pos += clen
		if err != nil {
			return out, err
		}
		if len(raw) < 24 {
			continue
		}
		rate := int(binary.LittleEndian.Uint32(raw[4:]))
		dlen := int(binary.LittleEndian.Uint32(raw[8:]))
		if raw[1] != 16 || raw[2] != 0 || 24+dlen > len(raw) {
			continue // only 16-bit mono is used
		}
		p := raw[24 : 24+dlen]
		n := len(p) / 2
		pcm := make([]int16, n)
		var a, b, c int32
		for i := 0; i < n; i++ {
			w := int32(uint16(p[i]) | uint16(p[n+i])<<8)
			a += w - 0x80
			b += a
			c += b
			pcm[i] = int16(uint16(c))
		}
		out = append(out, entry{game, name, rate, pcm})
	}
	return out, nil
}

// FromPak loads the Gyruss drum samples out of a Retrocade WAV.PAK.
func FromPak(path string) (*Kit, error) {
	d, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	ents, err := readPak(d)
	k := &Kit{Samples: map[int][]float32{}, Source: path}
	for _, e := range ents {
		if !strings.EqualFold(e.Game, "Gyruss") {
			continue
		}
		var n int
		if _, err := fmt.Sscanf(strings.ToLower(e.Name), "audio%d", &n); err != nil {
			continue
		}
		k.Rate = float64(e.Rate)
		k.Samples[n] = toFloat(e.PCM)
	}
	if len(k.Samples) == 0 {
		if err == nil {
			err = errors.New("no Gyruss samples in " + filepath.Base(path))
		}
		return nil, err
	}
	return k, nil
}

func toFloat(p []int16) []float32 {
	f := make([]float32, len(p))
	for i, v := range p {
		f[i] = float32(v) / 32768
	}
	return f
}

// FromWavDir loads AudioNN.wav files (16-bit or 8-bit PCM mono) from a folder.
func FromWavDir(dir string) (*Kit, error) {
	k := &Kit{Samples: map[int][]float32{}, Source: dir}
	for n := 0; n < 8; n++ {
		d, err := os.ReadFile(filepath.Join(dir, fmt.Sprintf("Audio%02d.wav", n)))
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
		return nil, errors.New("no AudioNN.wav files in " + dir)
	}
	return k, nil
}

func decodeWav(d []byte) ([]float32, int, error) {
	if len(d) < 12 || string(d[:4]) != "RIFF" || string(d[8:12]) != "WAVE" {
		return nil, 0, errors.New("not a wav file")
	}
	var ch, bits int
	var rate int
	pos := 12
	for pos+8 <= len(d) {
		id := string(d[pos : pos+4])
		sz := int(binary.LittleEndian.Uint32(d[pos+4:]))
		body := pos + 8
		if body+sz > len(d) {
			sz = len(d) - body
		}
		switch id {
		case "fmt ":
			ch = int(binary.LittleEndian.Uint16(d[body+2:]))
			rate = int(binary.LittleEndian.Uint32(d[body+4:]))
			bits = int(binary.LittleEndian.Uint16(d[body+14:]))
		case "data":
			if ch < 1 {
				return nil, 0, errors.New("wav without format")
			}
			var out []float32
			s := d[body : body+sz]
			switch bits {
			case 16:
				for i := 0; i+2*ch <= len(s); i += 2 * ch {
					out = append(out, float32(int16(binary.LittleEndian.Uint16(s[i:])))/32768)
				}
			case 8:
				for i := 0; i+ch <= len(s); i += ch {
					out = append(out, (float32(s[i])-128)/128)
				}
			default:
				return nil, 0, errors.New("unsupported wav bit depth")
			}
			return out, rate, nil
		}
		pos = body + sz + sz&1
	}
	return nil, 0, errors.New("wav without data")
}
