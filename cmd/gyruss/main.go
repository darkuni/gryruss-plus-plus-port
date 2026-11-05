// Gyruss - Konami's 1983 arcade game, running natively in Go in a widescreen
// layout, with the Roland drum kit that Retrocade's Gyruss driver used.
//
// The game needs the original ROM set (gyruss.zip) for its program, graphics
// and sound; none of Konami's data is bundled with this program. The Roland
// drum kit from Retrocade's Gyruss driver is built in (with permission); a
// "drums" folder of Audio01.wav..Audio07.wav next to the program replaces it,
// and a WAV.PAK can be given on the command line.
//
// Controls: arrows move, Z / Space / Left Ctrl fire, 5 coin, 1 / 2 start,
// L widescreen / original upright, P Gyruss+ panels, D drums (Roland /
// original), - / = Roland drum level, S effects (Retrocade style / arcade
// chip), B bass (cabinet / full range), W stereo (blended / full
// separation), F11 full screen, Esc quit.
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/audio"
	"github.com/hajimehoshi/ebiten/v2/ebitenutil"
	"github.com/hajimehoshi/ebiten/v2/inpututil"

	"gyruss/internal/assets"
	"gyruss/internal/board"
	"gyruss/internal/drums"
	"gyruss/internal/stats"
	"gyruss/internal/video"
)

type app struct {
	b      *board.Board
	up     *image.RGBA
	land   *image.RGBA
	screen *ebiten.Image
	coin   int
	view   int // 0 widescreen, 1 original upright
	plus   bool
	roland bool
	kit    *board.Kit
	drumLv int // Roland kit level in percent of the default mix
	retro  bool
	wide   bool
	cab    bool
	pad    padTracker
	ring   *ring
	tr     *stats.Tracker
	frames int
	toast  string
	toastT int
	hsNext time.Time
}

// ring is a small stereo sample FIFO between the emulation and the audio device.
type ring struct {
	mu  sync.Mutex
	buf []float32
}

func (r *ring) write(s []float32) {
	r.mu.Lock()
	r.buf = append(r.buf, s...)
	if over := len(r.buf) - board.SampleRate/2; over > 0 { // keep latency under 250 ms
		r.buf = r.buf[over&^1:]
	}
	r.mu.Unlock()
}

// Read streams the FIFO as 16-bit stereo little-endian samples.
func (r *ring) Read(out []byte) (int, error) {
	n := len(out) / 4
	r.mu.Lock()
	for i := 0; i < n; i++ {
		var lv, rv float32
		if len(r.buf) >= 2 {
			lv, rv = r.buf[0], r.buf[1]
			r.buf = r.buf[2:]
		}
		l, rr := int16(lv*30000), int16(rv*30000)
		out[4*i], out[4*i+1] = byte(l), byte(uint16(l)>>8)
		out[4*i+2], out[4*i+3] = byte(rr), byte(uint16(rr)>>8)
	}
	r.mu.Unlock()
	return n * 4, nil
}

func (a *app) say(s string) { a.toast, a.toastT = s, 120 }

func (a *app) setDrums(roland bool) {
	if roland && a.kit == nil {
		a.say("NO DRUM KIT")
		roland = false
	}
	a.roland = roland
	if roland {
		a.b.SetKit(a.kit)
	} else {
		a.b.SetKit(nil)
	}
}

func (a *app) applyWidth() {
	if a.wide {
		board.Width = 1
	} else {
		board.Width = 0.6
	}
}

// kitGain is the Roland kit's gain at 100%. It was matched against a
// recording of Retrocade's Gyruss driver: the hi-hat band (5-10 kHz)
// against the melody band (0.6-3 kHz) comes out at the same ratio.
const kitGain = 0.8

func (a *app) setDrumLevel(lv int) {
	if lv < 0 {
		lv = 0
	}
	if lv > 200 {
		lv = 200
	}
	a.drumLv = lv
	if a.kit != nil {
		a.kit.Gain = kitGain * float64(lv) / 100
	}
}

func (a *app) Update() error {
	if inpututil.IsKeyJustPressed(ebiten.KeyEscape) {
		return ebiten.Termination
	}
	pd := a.pad.poll()
	if pd.quit {
		return ebiten.Termination
	}
	if inpututil.IsKeyJustPressed(ebiten.KeyL) || pd.view {
		a.view = (a.view + 1) % 2
		a.saveSettings()
	}
	if inpututil.IsKeyJustPressed(ebiten.KeyP) || pd.plus {
		a.plus = !a.plus
		if a.plus {
			a.say("GYRUSS+ ON")
		} else {
			a.say("GYRUSS+ OFF")
		}
		a.saveSettings()
	}
	if inpututil.IsKeyJustPressed(ebiten.KeyD) || pd.drums {
		a.setDrums(!a.roland)
		if a.roland {
			a.say("DRUMS: ROLAND KIT")
		} else if a.kit != nil {
			a.say("DRUMS: ORIGINAL 8039")
		}
		a.saveSettings()
	}
	step := 0
	if inpututil.IsKeyJustPressed(ebiten.KeyMinus) || inpututil.IsKeyJustPressed(ebiten.KeyNumpadSubtract) {
		step = -10
	}
	if inpututil.IsKeyJustPressed(ebiten.KeyEqual) || inpututil.IsKeyJustPressed(ebiten.KeyNumpadAdd) {
		step = 10
	}
	if step != 0 {
		a.setDrumLevel(a.drumLv + step)
		a.say(fmt.Sprintf("ROLAND DRUM LEVEL %d%%", a.drumLv))
		a.saveSettings()
	}
	if inpututil.IsKeyJustPressed(ebiten.KeyS) {
		a.retro = !a.retro
		a.b.SetRetroSFX(a.retro)
		if a.retro {
			a.say("EFFECTS: RETROCADE STYLE")
		} else {
			a.say("EFFECTS: ARCADE CHIP")
		}
		a.saveSettings()
	}
	if inpututil.IsKeyJustPressed(ebiten.KeyW) {
		a.wide = !a.wide
		a.applyWidth()
		if a.wide {
			a.say("STEREO: FULL SEPARATION")
		} else {
			a.say("STEREO: BLENDED")
		}
		a.saveSettings()
	}
	if inpututil.IsKeyJustPressed(ebiten.KeyB) {
		a.cab = !a.cab
		a.b.Cabinet = a.cab
		if a.cab {
			a.say("BASS: CABINET")
		} else {
			a.say("BASS: FULL RANGE")
		}
		a.saveSettings()
	}
	if inpututil.IsKeyJustPressed(ebiten.KeyF11) {
		ebiten.SetFullscreen(!ebiten.IsFullscreen())
	}
	down := ebiten.IsKeyPressed
	joy := byte(0xff)
	if down(ebiten.KeyLeft) || pd.left {
		joy &^= board.JoyLeft
	}
	if down(ebiten.KeyRight) || pd.right {
		joy &^= board.JoyRight
	}
	if down(ebiten.KeyUp) || pd.up {
		joy &^= board.JoyUp
	}
	if down(ebiten.KeyDown) || pd.down {
		joy &^= board.JoyDown
	}
	if down(ebiten.KeyZ) || down(ebiten.KeySpace) || down(ebiten.KeyControlLeft) || pd.fire {
		joy &^= board.JoyFire
	}
	sys := byte(0xff)
	if down(ebiten.Key1) || pd.start1 {
		sys &^= board.Start1
	}
	if down(ebiten.Key2) || pd.start2 {
		sys &^= board.Start2
	}
	if inpututil.IsKeyJustPressed(ebiten.Key5) || pd.coin {
		a.coin = 4
	}
	if a.coin > 0 {
		sys &^= board.Coin1
		a.coin--
	}
	if down(ebiten.Key9) {
		sys &^= board.Service
	}
	a.b.System, a.b.P1, a.b.P2 = sys, joy, joy

	a.b.RunFrame()
	a.ring.write(a.b.DrainSamples())
	a.tr.Frame(a.b, joy)
	a.frames++
	if a.toastT > 0 {
		a.toastT--
	}
	if a.frames%600 == 0 {
		a.saveStats()
	}
	return nil
}

func (a *app) Draw(screen *ebiten.Image) {
	pal := a.b.Palette()
	video.Upright(a.up, a.b.Frame[:], &pal)
	img := a.up
	if a.view == 0 {
		video.Landscape(a.land, a.up)
		if a.plus {
			stats.DrawPanels(a.land, a.tr)
		}
		img = a.land
	}
	if a.toastT > 0 {
		stats.DrawToast(img, a.toast)
	}
	w, h := img.Bounds().Dx(), img.Bounds().Dy()
	if a.screen == nil || a.screen.Bounds().Dx() != w || a.screen.Bounds().Dy() != h {
		a.screen = ebiten.NewImage(w, h)
	}
	a.screen.WritePixels(img.Pix)
	sw, sh := screen.Bounds().Dx(), screen.Bounds().Dy()
	k := float64(sw) / float64(w)
	if ky := float64(sh) / float64(h); ky < k {
		k = ky
	}
	if k >= 1 {
		k = float64(int(k))
	}
	op := &ebiten.DrawImageOptions{}
	op.GeoM.Scale(k, k)
	op.GeoM.Translate((float64(sw)-k*float64(w))/2, (float64(sh)-k*float64(h))/2)
	screen.DrawImage(a.screen, op)
}

func (a *app) Layout(outW, outH int) (int, int) {
	// work in device pixels so the picture can be scaled by whole numbers
	s := ebiten.Monitor().DeviceScaleFactor()
	return int(float64(outW) * s), int(float64(outH) * s)
}

// ---- settings ----------------------------------------------------------------

type settings struct {
	View     int
	Plus     bool
	Roland   bool
	Drums    *int
	RetroSFX bool // zero-period tones silent, as Retrocade played them
	Cabinet  bool // bass roll-off like an arcade speaker
	Wide     bool // hard left / right split instead of the blended stereo
}

func configFile(name string) string {
	d, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(d, "gyruss", name)
}

func (a *app) saveSettings() {
	f := configFile("settings.json")
	if f == "" {
		return
	}
	d, _ := json.Marshal(settings{View: a.view, Plus: a.plus, Roland: a.roland, Drums: &a.drumLv, RetroSFX: a.retro, Cabinet: a.cab, Wide: a.wide})
	_ = os.MkdirAll(filepath.Dir(f), 0o755)
	_ = os.WriteFile(f, d, 0o644)
}

func loadSettings() settings {
	s := settings{View: 0, Plus: true, Roland: true}
	if f := configFile("settings.json"); f != "" {
		if d, err := os.ReadFile(f); err == nil {
			_ = json.Unmarshal(d, &s)
		}
	}
	if s.View < 0 || s.View > 1 {
		s.View = 0
	}
	return s
}

func (a *app) saveStats() {
	if f := configFile("stats.json"); f != "" && a.tr.Dirty {
		_ = os.MkdirAll(filepath.Dir(f), 0o755)
		_ = a.tr.Save(f)
	}
}

// ---- ROM and drum kit lookup ---------------------------------------------------

func searchDirs() []string {
	dirs := []string{"."}
	if exe, err := os.Executable(); err == nil {
		d := filepath.Dir(exe)
		dirs = append(dirs, d, filepath.Join(d, "roms"), filepath.Join(d, "..", "Resources"), filepath.Join(d, "..", "..", ".."))
	}
	if home, err := os.UserHomeDir(); err == nil {
		dirs = append(dirs, home)
	}
	return dirs
}

func findROMs() []string {
	var given []string
	for _, a := range os.Args[1:] {
		if len(a) > 0 && a[0] != '-' && filepath.Ext(a) != ".pak" && filepath.Ext(a) != ".PAK" {
			given = append(given, a)
		}
	}
	if len(given) > 0 {
		return given
	}
	var out []string
	seen := map[string]bool{}
	for _, d := range searchDirs() {
		for _, n := range []string{"gyruss.zip", "gyrussce.zip", "gyrussb.zip", "venus.zip"} {
			c, _ := filepath.Abs(filepath.Join(d, n))
			if seen[c] {
				continue
			}
			if _, err := os.Stat(c); err == nil {
				seen[c] = true
				out = append(out, c)
			}
		}
	}
	return out
}

func findKit() *board.Kit {
	try := func(k *drums.Kit, err error) *board.Kit {
		if err != nil || k == nil {
			return nil
		}
		return &board.Kit{Samples: k.Samples, Rate: k.Rate, Gain: 1}
	}
	for _, a := range os.Args[1:] {
		if e := filepath.Ext(a); e == ".pak" || e == ".PAK" {
			if k := try(drums.FromPak(a)); k != nil {
				return k
			}
		}
	}
	// a drums folder of AudioNN.wav files lets players swap in their own kit
	for _, d := range searchDirs() {
		for _, p := range []string{"drums", "gyruss_drums"} {
			if k := try(drums.FromWavDir(filepath.Join(d, p))); k != nil {
				return k
			}
		}
	}
	return try(drums.Builtin(), nil)
}

// errApp shows a problem in the window, because the Windows build has no
// console to print to.
type errApp struct{ msg string }

func (e *errApp) Update() error { return nil }
func (e *errApp) Draw(s *ebiten.Image) {
	ebitenutil.DebugPrintAt(s, e.msg, 6, 6)
}
func (e *errApp) Layout(_, _ int) (int, int) { return 320, 240 }

func showError(msg string) {
	log.Print(msg)
	ebiten.SetWindowTitle("Gyruss - ROM problem")
	ebiten.SetWindowSize(320*3, 240*3)
	_ = ebiten.RunGame(&errApp{msg})
	os.Exit(1)
}

func romProblem(err error, paths []string) string {
	var me *assets.MissingError
	switch {
	case len(paths) == 0:
		return "ROM set not found.\n\nPut gyruss.zip next to\ngyruss.exe, or drag the zip\nonto the exe."
	case errors.As(err, &me):
		m := "Found:\n"
		for _, p := range paths {
			m += "  " + filepath.Base(p) + "\n"
		}
		m += "\nbut these files are missing:\n"
		for i, n := range me.Missing {
			m += " " + n
			if i%4 == 3 {
				m += "\n"
			}
		}
		return m
	}
	return fmt.Sprintf("Could not load the ROMs:\n%v", err)
}

func main() {
	paths := findROMs()
	roms, err := assets.Load(paths...)
	if err != nil {
		showError(romProblem(err, paths))
	}
	b := board.New(roms)
	st := loadSettings()
	for _, arg := range os.Args[1:] {
		switch arg {
		case "-classic", "--classic":
			// the plain original game: upright picture, no read-outs, 8039
			// drums, effects and bass as the board makes them
			st.View, st.Plus, st.Roland, st.RetroSFX, st.Cabinet, st.Wide = 1, false, false, false, false, true
		case "-plus", "--plus":
			st.View, st.Plus = 0, true
		}
	}
	a := &app{
		b:    b,
		up:   image.NewRGBA(image.Rect(0, 0, video.ScreenW, video.ScreenH)),
		land: image.NewRGBA(image.Rect(0, 0, video.LandW, video.LandH)),
		ring: &ring{},
		view: st.View,
		plus: st.Plus,
		kit:  findKit(),
		tr:   stats.New(),
	}
	b.OnDrum = func(cmd byte) { b.TriggerDrum(int(cmd)) }
	lv := 100
	if st.Drums != nil {
		lv = *st.Drums
	}
	a.setDrumLevel(lv)
	a.setDrums(st.Roland)
	a.retro, a.cab, a.wide = st.RetroSFX, st.Cabinet, st.Wide
	a.applyWidth()
	b.SetRetroSFX(a.retro)
	b.Cabinet = a.cab
	a.toastT = 0
	if f := configFile("stats.json"); f != "" {
		a.tr.Load(f)
	}
	a.tr.Attach(b)
	ctx := audio.NewContext(board.SampleRate)
	player, err := ctx.NewPlayer(a.ring)
	if err != nil {
		log.Fatal(err)
	}
	player.SetBufferSize(70 * time.Millisecond)
	player.Play()
	ebiten.SetWindowTitle("Gyruss")
	ebiten.SetWindowSize(video.LandW*3, video.LandH*3)
	ebiten.SetWindowResizingMode(ebiten.WindowResizingModeEnabled)
	ebiten.SetTPS(60)
	err = ebiten.RunGame(a)
	a.saveStats()
	a.saveSettings()
	if err != nil && !errors.Is(err, ebiten.Termination) {
		log.Fatal(err)
	}
}
