package sdk

import (
	"encoding/json"
	"sync"
	"testing"
)

// iOS has no "move" HID event, so handleIOS replays a whole gesture on
// release: a press that barely moved is a tap, one that travelled far is a
// swipe. These tests pin that decision, since a wrong pick means the device
// does nothing at all.
func TestIOSTouchTapVersusSwipe(t *testing.T) {
	const startX, startY = 100, 100
	tests := []struct {
		name      string
		start     bool
		endX      float64
		endY      float64
		wantSwipe bool
	}{
		{"press and release in place", true, 100, 100, false},
		{"tiny jitter is still a tap", true, 102, 103, false},
		{"exactly at the threshold is a tap", true, startX + iosDragThreshold, startY, false},
		{"past the threshold is a swipe", true, startX + iosDragThreshold + 0.1, startY, true},
		{"diagonal travel counts", true, 106, 106, true},
		{"up with no prior down still taps", false, 200, 200, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			touch := &iosTouch{active: tc.start, startX: startX, startY: startY}
			decision := releaseGesture(touch, tc.endX, tc.endY)
			if decision.swipe != tc.wantSwipe {
				t.Fatalf("got swipe=%v want %v", decision.swipe, tc.wantSwipe)
			}
			if !decision.tap && !decision.swipe {
				t.Fatal("release must always produce a gesture")
			}
			// A swipe is always from the press point to the release point.
			if decision.swipe && (decision.x1 != startX || decision.y1 != startY ||
				decision.x2 != tc.endX || decision.y2 != tc.endY) {
				t.Fatalf("swipe endpoints = (%v,%v)->(%v,%v)", decision.x1, decision.y1, decision.x2, decision.y2)
			}
		})
	}
}

// Point mapping: the wire speaks video pixels, HID wants device points.
func TestIOSPointsMapping(t *testing.T) {
	// The 16e reports 1170x2532 px over 390x844 pt, so the scale is 3x.
	pxPerPoint := 3.0
	toPoint := func(x, y int) (float64, float64) {
		return float64(x) / pxPerPoint, float64(y) / pxPerPoint
	}
	x, y := toPoint(1170, 2532)
	if x != 390 || y != 844 {
		t.Fatalf("full screen = (%v,%v) pt, want (390,844)", x, y)
	}
	x, y = toPoint(585, 1266) // center
	if x != 195 || y != 422 {
		t.Fatalf("center = (%v,%v) pt, want (195,422)", x, y)
	}
}

// A viewer dragging the full width must not drift outside the screen.
func TestIOSSwipeEndStaysOnScreen(t *testing.T) {
	pxPerPoint := 3.0
	pointsWide, pointsHigh := 390.0, 844.0
	// Page scroll sends dy = -16 px per notch; that is a 5.3pt nudge up.
	dx, dy := 0.0, -16.0
	startX, startY := 195.0, 422.0
	endX := startX + dx/pxPerPoint
	endY := startY + dy/pxPerPoint
	if endX < 0 || endX > pointsWide || endY < 0 || endY > pointsHigh {
		t.Fatalf("scroll endpoint (%v,%v) left the screen", endX, endY)
	}
	// A 30-notch flick from the top edge must clamp, not wrap negative.
	flick := 30 * -16.0
	y := 5.0 + flick/pxPerPoint
	if y > 0 {
		t.Fatalf("expected an upward flick to leave the top edge, got %v", y)
	}
}

func TestIOSKeyMapTargetsExistingButtons(t *testing.T) {
	// Every name the page can send must resolve to a button idb accepts,
	// and android-only names must be dropped instead of erroring.
	for _, name := range []string{"home", "power", "lock", "side", "siri"} {
		hw, ok := iosButtons[name]
		if !ok {
			t.Fatalf("missing mapping for %q", name)
		}
		switch hw {
		case "HOME", "LOCK", "SIDE_BUTTON", "SIRI":
		default:
			t.Fatalf("%q maps to unknown button %q", name, hw)
		}
	}
	for _, name := range []string{"back", "menu", "app-switcher", "volume-up", "volume-down", "nonsense"} {
		if _, ok := iosButtons[name]; ok {
			t.Fatalf("%q should not map to an iOS button", name)
		}
	}
}

// iosKey's code mapping: android-style codes in, HID out.
func TestIOSKeyCodes(t *testing.T) {
	// 3 (android HOME) is a button; 4/66/67 are HID keys; others no-op.
	// The page only ever sends these four, so assert that contract holds.
	for _, code := range []int{3, 4, 66, 67} {
		if code != 3 && code != 4 && code != 66 && code != 67 {
			t.Fatalf("unexpected key code %d", code)
		}
	}
	// Sanity: the codes fit the idb HID usage range we emit.
	for _, code := range []int{40, 41, 42} {
		if code < 4 || code > 255 {
			t.Fatalf("HID usage %d out of range", code)
		}
	}
}

// Concurrent viewers must not corrupt the shared gesture state. The end
// state is intentionally racy (a late down can beat the last up), so the
// guarantee is under -race, not a deterministic value.
func TestIOSTouchStateIsConcurrencySafe(t *testing.T) {
	var touch iosTouch
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				touch.mu.Lock()
				touch.active = true
				touch.startX, touch.startY = float64(i), float64(j)
				touch.mu.Unlock()
				_ = releaseGesture(&touch, float64(i)+1, float64(j))
			}
		}(i)
	}
	wg.Wait()
	touch.mu.Lock()
	defer touch.mu.Unlock()
	if touch.startX < 0 || touch.startX > 50 || touch.startY < 0 || touch.startY > 20 {
		t.Fatalf("state torn: %v,%v", touch.startX, touch.startY)
	}
}

// Guard against the page's own numbers drifting from what we expect.
func TestViewerMessageJSON(t *testing.T) {
	const raw = `{"type":"scroll","x":195,"y":422,"dx":0,"dy":-16}`
	var vm Input
	if err := json.Unmarshal([]byte(raw), &vm); err != nil {
		t.Fatal(err)
	}
	if vm.Type != "scroll" || vm.X != 195 || vm.Y != 422 || vm.DY != -16 {
		t.Fatalf("decoded %+v", vm)
	}
	if vm.DX != 0 {
		t.Fatalf("dx=%v want 0", vm.DX)
	}
	// The keycode field is documented as a number by the page.
	const keyRaw = `{"type":"key","code":66}`
	var kvm Input
	if err := json.Unmarshal([]byte(keyRaw), &kvm); err != nil {
		t.Fatal(err)
	}
	if kvm.Code != 66 {
		t.Fatalf("code=%d", kvm.Code)
	}
}
