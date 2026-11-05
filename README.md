# Gyruss+ - widescreen native port, with the Roland drums

Konami's 1983 Gyruss as a native Go + Ebitengine program, re-laid-out for 16:9, with the Roland drum kit that
Retrocade's Gyruss driver used in place of the board's 8-bit drum samples.

## How it works (honest version)

This is **not** a routine-by-routine rewrite. It is a native Go port of the whole board: the main Z80
(game program), the Konami-1 6809 (sprite processor, with its opcode decryption), the sound Z80 with its five
AY-3-8910s and switchable RC filters, the 8039 drum CPU and its DAC, and the tile / sprite video drawn line by
line. The original ROMs run unmodified on top of it. The ROMs are never included and are loaded at runtime.

## Files you need (not included)

* **gyruss.zip** - your own copy of the MAME `gyruss` set (the `gyrussce`, `gyrussb` and `venus` sets work too;
  files are matched by CRC). Put it next to the program, in a `roms` folder, the working directory or your home
  folder, or drag it onto the exe. If anything is missing a window lists what.
* **WAV.PAK** (optional) - Retrocade's sound sample pack. Put it next to the program or in a `wav` folder. The
  port decodes its `Gyruss/Audio01-06` samples (Retrocade's own lossless format: zlib, then split byte planes
  of a third-order delta). A folder called `drums` holding `Audio01.wav` ... `Audio06.wav` works as well.
  Without it the board's original 8039 drums play.

## The drums

The sound Z80 sends drum commands 1-7 to the 8039, which plays 8-bit samples through a DAC on the left
channel. With the Roland kit loaded, command N plays Retrocade's `AudioN` instead (the same numbering
Retrocade's driver used), the 8039's DAC is muted and the kit is mixed to the centre. **D** (or a click of the
right stick) switches between the Roland kit and the original 8039 drums at any time; the choice is remembered.

Like Retrocade's driver (read out of RETRO.EXE), the kit plays on one drum channel: a new hit cuts off the
drum that was sounding, and command 0 stops it. The level was matched against a recording of Retrocade
(hi-hat band against melody band). **-** and **=** turn the kit down / up in 10% steps (0-200%, remembered).

## Sound effects and bass

Two of the game's effects - the swoop when a wave flies in from the centre and the shot - play a channel of the
second sound chip with a tone period of 0 and the envelope generator on. The real chip treats period 0 as 1,
so the envelope comes out as a buzz: on the shot a 1.8 second falling drone, on the wave entrance a pulsing
low buzz. Retrocade played those channels silent, which is the sound in its videos. **S** switches between
**arcade chip** (default) and **Retrocade style** (experimental). **B** switches the bass between **full range**
(default) and **cabinet** (experimental, a roll-off below ~110 Hz like an arcade amp and speaker).

## Controls

Arrows move, Z / Space / Left Ctrl fire, 5 coin, 1 / 2 start, L widescreen / original upright,
P Gyruss+ panels on / off, D drums (Roland / original), - / = Roland drum level, S effects style, B bass, F11 full screen, Esc quit, 9 service.

**Gamepad:** D-pad / left stick move, A or B fire, Select = coin, Start = 1 player, X or Y = 2 players,
right bumper = change view, left bumper = Gyruss+ panels, right-stick click = drums, hold Select + Start for a
second to quit.

## Classic or Plus

The game itself is never changed - Plus only adds read-outs in the widescreen side panels. For the plain
original: **P** switches the panels off, **L** gives the original upright picture, **D** the original drums, or
start with `-classic` (upright, no panels, 8039 drums, arcade chip effects, full range bass). `-plus` goes back to widescreen with panels.

Widescreen puts the 224x216 play area in the middle of a 384x216 canvas and moves the HUD (scores, stage,
reserve ships, credits) into the side bars. Plus shows:

* **Left (this game, following whose turn it is)** - kills, satellites shot, hit rate (hits per shot fired),
  best run of kills without dying, best chance stage (hits out of the 40 targets), perfect chance-stage groups.
* **All time** - games, best score, kills, best stage, planets reached, Earths reached, best chance stage.
* **Right: the journey** - Neptune to Earth, with the warps still to go, the planet you are on and the chance
  stage's live hit count when you arrive.

Everything is read from the game: the score routine (its score table index tells hits from bonuses), the
sound commands (a shot) and the player's RAM block (stage, reserve ships, satellites). Two-player games keep
separate numbers per player. All-time totals are saved in your user config folder (`gyruss/stats.json`).

## Releases

Publishing a GitHub release runs `.github/workflows/release.yml`, which builds `gyruss.exe` (Windows),
`Gyruss-linux.tar.gz` (Linux x86_64, Steam Deck / SteamOS desktop mode) and `Gyruss-mac.zip` (universal macOS app).

## Build

```
go build -ldflags "-H=windowsgui -s -w" -o gyruss.exe ./cmd/gyruss     # Windows (pure Go)
go build -o gyruss ./cmd/gyruss                                          # Linux needs X11 / ALSA dev libs
go run ./cmd/dev -rom gyruss.zip -pak wav/WAV.PAK -n 3000 -coin 300 -play -shots 1000   # headless test
```

## Known simplifications

* The AY-3-8910 output stage and the board's resistor mixer are modelled, not circuit-simulated.
* The CPUs are interleaved four times per scanline, close to the original board's timing.
