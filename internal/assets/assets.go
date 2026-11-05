// Package assets loads the Gyruss ROM set (MAME "gyruss" layout) from a zip
// file or a folder. Nothing of Konami's is bundled with the program.
package assets

import (
	"archive/zip"
	"fmt"
	"hash/crc32"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// ROMs holds every region the board needs, already laid out at its
// address / region offset.
type ROMs struct {
	Main    []byte // 0x8000, 0000-5fff used
	Sub     []byte // 0x2000, mapped at e000-ffff (Konami-1 encrypted)
	Audio   []byte // 0x6000, 0000-3fff used
	Drum    []byte // 0x1000, i8039 program
	Sprites []byte // 0x8000
	Tiles   []byte // 0x2000
	PROMs   []byte // 0x220: palette, sprite lookup, char lookup
}

type part struct {
	name   string
	region *[]byte
	offset int
	size   int
	crc    uint32
	alt    []string // names used by other dumps of the same chip
}

// MissingError lists the ROM files that could not be found.
type MissingError struct{ Missing []string }

func (e *MissingError) Error() string {
	return "missing ROM files: " + strings.Join(e.Missing, ", ")
}

func (r *ROMs) parts() []part {
	return []part{
		{"gyrussk.1", &r.Main, 0x0000, 0x2000, 0xc673b43d, []string{"gya-1.11j"}},
		{"gyrussk.2", &r.Main, 0x2000, 0x2000, 0xa4ec03e4, []string{"2.bin"}},
		{"gyrussk.3", &r.Main, 0x4000, 0x2000, 0x27454a98, []string{"3.bin"}},
		{"gyrussk.9", &r.Sub, 0x0000, 0x2000, 0x822bf27e, []string{"gy-5.19e", "9.bin"}},
		{"gyrussk.1a", &r.Audio, 0x0000, 0x2000, 0xf4ae1c17, []string{"gy-11.7a", "11.bin"}},
		{"gyrussk.2a", &r.Audio, 0x2000, 0x2000, 0xba498115, []string{"gy-12.8a", "12.bin"}},
		{"gyrussk.3a", &r.Drum, 0x0000, 0x1000, 0x3f9b5dea, []string{"gy-13.11h", "13.bin"}},
		{"gyrussk.6", &r.Sprites, 0x0000, 0x2000, 0xc949db10, []string{"gy-10.9d", "6.bin"}},
		{"gyrussk.5", &r.Sprites, 0x2000, 0x2000, 0x4f22411a, []string{"gy-9.8d", "5.bin"}},
		{"gyrussk.8", &r.Sprites, 0x4000, 0x2000, 0x47cd1fbc, []string{"gy-8.7d", "8.bin"}},
		{"gyrussk.7", &r.Sprites, 0x6000, 0x2000, 0x8e8d388c, []string{"gy-7.6d", "7.bin"}},
		{"gyrussk.4", &r.Tiles, 0x0000, 0x2000, 0x27d8329b, []string{"gy-6.1g", "4.bin"}},
		{"gyrussk.pr3", &r.PROMs, 0x0000, 0x0020, 0x98782db3, nil},
		{"gyrussk.pr1", &r.PROMs, 0x0020, 0x0100, 0x7ed057de, nil},
		{"gyrussk.pr2", &r.PROMs, 0x0120, 0x0100, 0xde823a81, nil},
	}
}

// Load reads the ROM set from one or more zip files or folders. Files are
// matched by CRC first (so renamed dumps work) and by name second.
func Load(paths ...string) (*ROMs, error) {
	files := map[string][]byte{}
	for _, p := range paths {
		st, err := os.Stat(p)
		if err != nil {
			continue
		}
		if st.IsDir() {
			ents, _ := os.ReadDir(p)
			for _, e := range ents {
				if e.IsDir() {
					continue
				}
				if d, err := os.ReadFile(filepath.Join(p, e.Name())); err == nil && len(d) <= 0x10000 {
					files[strings.ToLower(e.Name())] = d
				}
			}
			continue
		}
		zr, err := zip.OpenReader(p)
		if err != nil {
			continue
		}
		for _, f := range zr.File {
			if f.UncompressedSize64 > 0x10000 {
				continue
			}
			rc, err := f.Open()
			if err != nil {
				continue
			}
			d, err := io.ReadAll(rc)
			rc.Close()
			if err == nil {
				files[strings.ToLower(filepath.Base(f.Name))] = d
			}
		}
		zr.Close()
	}
	byCRC := map[uint32][]byte{}
	for _, d := range files {
		byCRC[crc32.ChecksumIEEE(d)] = d
	}
	r := &ROMs{
		Main: make([]byte, 0x8000), Sub: make([]byte, 0x2000), Audio: make([]byte, 0x6000),
		Drum: make([]byte, 0x1000), Sprites: make([]byte, 0x8000), Tiles: make([]byte, 0x2000),
		PROMs: make([]byte, 0x220),
	}
	var missing []string
	for _, p := range r.parts() {
		d, ok := byCRC[p.crc]
		if !ok {
			for _, n := range append([]string{p.name}, p.alt...) {
				if x, ok2 := files[n]; ok2 && len(x) == p.size {
					d, ok = x, true
					break
				}
			}
		}
		if !ok || len(d) != p.size {
			missing = append(missing, p.name)
			continue
		}
		copy((*p.region)[p.offset:], d)
	}
	if len(missing) > 0 {
		return nil, &MissingError{missing}
	}
	return r, nil
}

// Describe is a short human-readable summary used in error messages.
func Describe() string {
	var r ROMs
	var n []string
	for _, p := range r.parts() {
		n = append(n, p.name)
	}
	return fmt.Sprint(strings.Join(n, " "))
}
