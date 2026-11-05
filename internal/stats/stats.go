// Package stats keeps the Gyruss+ statistics and draws them in the side
// panels. It only watches the game (its RAM, its sound commands and its
// score routine); it never changes how the game plays.
package stats

import (
	"encoding/json"
	"os"

	"gyruss/internal/board"
)

// Places on the way to Earth. Stages 1 and 2 warp towards Neptune, stage 3
// arrives there and is a chance (bonus) stage; every following planet is
// three warps further on (stages 7, 11, 15, 19 and 23).
var Planets = [6]string{"NEPTUNE", "URANUS", "SATURN", "JUPITER", "MARS", "EARTH"}

// Game RAM the tracker reads (main CPU).
const (
	ramLives     = 0x600 // current player's reserve ships
	ramStage     = 0x60b // current player's stage - 1
	ramShipState = 0x601 // 0 while the ship is not in play
	ramSatellite = 0x610 // satellites shot in the current group
	ramCurPlayer = 0x662
	ramScore     = 0x663 // player 1, player 2 at +3
	ramPlaying   = 0x669 // 0 attract / demo, 1 one player, ff two players
	scoreRoutine = 0x0685
	sndShot      = 0x03
)

// score table index (the score routine's A) -> points
var points = [16]int{0, 50, 100, 200, 300, 400, 500, 600, 700, 800, 900, 1000, 1500, 2000, 2500, 3000}

// Counts are one set of totals (this game, or all games) for one player.
type Counts struct {
	Games       int
	Kills       int
	Shots       int
	Hits        int
	Satellites  int
	Deaths      int
	ChanceHits  int
	BestChance  int // most hits in one chance stage
	BestStage   int
	Planets     int // planets reached
	Earths      int // times Earth was reached
	BestKillRun int
	Perfect     int // chance stage groups shot down completely
	BestScore   int
}

// Tracker follows the game.
type Tracker struct {
	GameP, LifeP [2]Counts
	Players      int
	Cur          int
	Active       bool
	Stage        int // 1-based, current player
	ChanceNow    int // hits in the running chance stage
	Dirty        bool

	b        *board.Board
	lastSat  byte
	lastLive byte
	lastStg  byte
	run      int
	tally    bool
}

func New() *Tracker { return &Tracker{Players: 1} }

// InChance reports whether the stage is a chance (bonus) stage: the one
// that arrives at a planet.
func InChance(stage int) bool { return stage > 0 && stage%4 == 3 }

// ChanceEnemies is how many targets a chance stage sends (4 groups of 10).
const ChanceEnemies = 40

// Place returns the planet the player is at or heading for and the warps
// left before arriving there (0 = at the planet).
func Place(stage int) (planet, warps int) {
	if stage < 1 {
		stage = 1
	}
	return (stage / 4) % 6, 3 - stage%4
}

func (t *Tracker) both(f func(c *Counts)) {
	p := t.Cur
	f(&t.GameP[p])
	f(&t.LifeP[p])
	t.Dirty = true
}

// Attach hooks the tracker to the board.
func (t *Tracker) Attach(b *board.Board) {
	t.b = b
	b.OnSound = func(cmd byte) {
		if t.Active && cmd == sndShot {
			t.both(func(c *Counts) { c.Shots++ })
		}
	}
	b.MainHook = func(pc uint16) {
		if pc != scoreRoutine || !t.Active {
			return
		}
		idx := int(b.Main.A)
		if idx == 11 && InChance(t.Stage) {
			t.both(func(c *Counts) { c.Perfect++ })
		}
		if idx < 1 || idx > 10 || t.tally {
			return // bonuses, not hits
		}
		t.run++
		chance := InChance(t.Stage)
		t.both(func(c *Counts) {
			c.Kills++
			c.Hits++
			if t.run > c.BestKillRun {
				c.BestKillRun = t.run
			}
		})
		if chance {
			t.ChanceNow++
			n := t.ChanceNow
			t.both(func(c *Counts) {
				c.ChanceHits++
				if n > c.BestChance {
					c.BestChance = n
				}
			})
		}
	}
}

// Frame is called once per emulated frame.
func (t *Tracker) Frame(b *board.Board, joy byte) {
	ram := b.MainRAM[:]
	playing := ram[ramPlaying] != 0
	if playing && !t.Active {
		// a new game
		t.Active = true
		t.Players = 1
		if ram[ramPlaying] == 0xff {
			t.Players = 2
		}
		t.GameP = [2]Counts{}
		for p := 0; p < t.Players; p++ {
			t.GameP[p].Games = 1
			t.LifeP[p].Games++
		}
		t.Cur = 0
		t.Stage = 1
		t.lastSat, t.lastLive, t.lastStg = ram[ramSatellite], ram[ramLives], ram[ramStage]
		t.ChanceNow, t.run = 0, 0
		t.Dirty = true
	}
	if !playing {
		t.Active = false
		return
	}
	cur := int(ram[ramCurPlayer] & 1)
	if t.Players == 1 {
		cur = 0
	}
	if cur != t.Cur {
		t.Cur = cur
		t.Stage = int(ram[ramStage]) + 1
		t.lastSat, t.lastLive, t.lastStg = ram[ramSatellite], ram[ramLives], ram[ramStage]
	}
	// the chance stage's bonus is paid out in 300 point steps after the
	// stage: those are not hits
	t.tally = ram[ramShipState] == 0 && InChance(t.Stage)
	if s := ram[ramStage]; s != t.lastStg {
		if s == t.lastStg+1 {
			st := int(s) + 1
			t.Stage = st
			t.ChanceNow = 0
			if InChance(st) {
				pl, _ := Place(st)
				t.both(func(c *Counts) {
					c.Planets++
					if pl == 5 {
						c.Earths++
					}
				})
			}
		} else if s == 0 {
			t.Stage = 1
		}
		t.lastStg = s
	}
	st := t.Stage
	sc := score(ram, t.Cur)
	t.both(func(c *Counts) {
		if st > c.BestStage {
			c.BestStage = st
		}
		if sc > c.BestScore {
			c.BestScore = sc
		}
	})
	if s := ram[ramSatellite]; s != t.lastSat {
		if s == t.lastSat+1 {
			t.both(func(c *Counts) { c.Satellites++ })
		}
		t.lastSat = s
	}
	if l := ram[ramLives]; l != t.lastLive {
		if l+1 == t.lastLive {
			t.both(func(c *Counts) { c.Deaths++ })
			t.run = 0
		}
		t.lastLive = l
	}
}

// score reads a player's BCD score (three bytes, low first).
func score(ram []byte, p int) int {
	a := ramScore + 3*p
	n := 0
	for i := 2; i >= 0; i-- {
		v := int(ram[a+i])
		n = n*100 + (v>>4)*10 + v&15
	}
	return n
}

// HitRate is hits per shot in percent.
func (c *Counts) HitRate() int {
	if c.Shots == 0 {
		return 0
	}
	r := c.Hits * 100 / c.Shots
	if r > 100 {
		r = 100
	}
	return r
}

type saved struct {
	Life [2]Counts
}

func (t *Tracker) Save(f string) error {
	d, err := json.MarshalIndent(saved{t.LifeP}, "", " ")
	if err != nil {
		return err
	}
	t.Dirty = false
	return os.WriteFile(f, d, 0o644)
}

func (t *Tracker) Load(f string) {
	d, err := os.ReadFile(f)
	if err != nil {
		return
	}
	var s saved
	if json.Unmarshal(d, &s) == nil {
		t.LifeP = s.Life
	}
}
