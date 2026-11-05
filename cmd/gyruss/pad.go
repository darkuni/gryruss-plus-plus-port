package main

import "github.com/hajimehoshi/ebiten/v2"

// Gamepad (standard layout: Xbox / PlayStation / Switch Pro / Steam Deck).
//
//	D-pad or left stick   move
//	A or B                fire
//	Select (Back)         insert coin (on release)
//	Start                 1 player start
//	X or Y                2 player start
//	Right bumper          change view
//	Left bumper           Gyruss+ panels on / off
//	Right stick click     drums: Roland kit / original
//	Select + Start held   quit (about one second)
type padState struct {
	left, right, up, down bool
	fire                  bool
	start1, start2        bool
	coin                  bool // one-frame pulse
	view                  bool // one-frame pulse
	plus                  bool // one-frame pulse
	drums                 bool // one-frame pulse
	quit                  bool
}

func (t *padTracker) poll() padState {
	var s padState
	var sel, start, view, lb, rs bool
	ids := ebiten.AppendGamepadIDs(nil)
	t.connected = false
	for _, id := range ids {
		if !ebiten.IsStandardGamepadLayoutAvailable(id) {
			continue
		}
		t.connected = true
		b := func(btn ebiten.StandardGamepadButton) bool { return ebiten.IsStandardGamepadButtonPressed(id, btn) }
		ax := func(a ebiten.StandardGamepadAxis) float64 { return ebiten.StandardGamepadAxisValue(id, a) }
		hx := ax(ebiten.StandardGamepadAxisLeftStickHorizontal)
		hy := ax(ebiten.StandardGamepadAxisLeftStickVertical)
		s.left = s.left || b(ebiten.StandardGamepadButtonLeftLeft) || hx < -0.5
		s.right = s.right || b(ebiten.StandardGamepadButtonLeftRight) || hx > 0.5
		s.up = s.up || b(ebiten.StandardGamepadButtonLeftTop) || hy < -0.5
		s.down = s.down || b(ebiten.StandardGamepadButtonLeftBottom) || hy > 0.5
		s.fire = s.fire || b(ebiten.StandardGamepadButtonRightBottom) || b(ebiten.StandardGamepadButtonRightRight)
		sel = sel || b(ebiten.StandardGamepadButtonCenterLeft)
		start = start || b(ebiten.StandardGamepadButtonCenterRight)
		view = view || b(ebiten.StandardGamepadButtonFrontTopRight)
		lb = lb || b(ebiten.StandardGamepadButtonFrontTopLeft)
		rs = rs || b(ebiten.StandardGamepadButtonRightStick)
		if b(ebiten.StandardGamepadButtonRightLeft) || b(ebiten.StandardGamepadButtonRightTop) {
			s.start2 = true
		}
	}
	s.start1, s.coin, s.quit = t.buttons(sel, start)
	if sel && start {
		s.start2 = false
	}
	s.view = view && !t.prevView
	t.prevView = view
	s.plus = lb && !t.prevLB
	t.prevLB = lb
	s.drums = rs && !t.prevRS
	t.prevRS = rs
	return s
}
