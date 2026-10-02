package idb

// Keyboard: HID keys carry USB HID usage IDs (verified against fb-idb's
// keycode tables: 'a'=4, Return=0x28, Tab=0x2B, LeftShift=0xE1=225), not
// macOS virtual keycodes. Ported from idb/common/hid.py so `text` behaves
// exactly like `idb ui text`.

import (
	"fmt"

	gen "github.com/Adelodunpeter25/sim-go/internal/idb/gen"
)

// HID usage codes worth naming.
const (
	HIDKeyBackspace = 42
	HIDKeyTab       = 43
	HIDKeyReturn    = 40
	HIDKeyEscape    = 41
	HIDKeySpace     = 44
	HIDKeyShiftLeft = 225
)

// KeyEvents returns a down+up pair for a HID usage code.
func KeyEvents(keycode int) []*gen.HIDEvent {
	return []*gen.HIDEvent{keyEvent(keycode, gen.HIDEvent_DOWN), keyEvent(keycode, gen.HIDEvent_UP)}
}

// ShiftedKeyEvents presses left shift around the key, the way shifted
// characters are typed.
func ShiftedKeyEvents(keycode int) []*gen.HIDEvent {
	return []*gen.HIDEvent{
		keyEvent(HIDKeyShiftLeft, gen.HIDEvent_DOWN),
		keyEvent(keycode, gen.HIDEvent_DOWN),
		keyEvent(keycode, gen.HIDEvent_UP),
		keyEvent(HIDKeyShiftLeft, gen.HIDEvent_UP),
	}
}

func keyEvent(keycode int, dir gen.HIDEvent_HIDDirection) *gen.HIDEvent {
	return &gen.HIDEvent{
		Event: &gen.HIDEvent_Press{Press: &gen.HIDEvent_HIDPress{
			Action: &gen.HIDEvent_HIDPressAction{Action: &gen.HIDEvent_HIDPressAction_Key{
				Key: &gen.HIDEvent_HIDKey{Keycode: uint64(keycode)},
			}},
			Direction: dir,
		}},
	}
}

// TextEvents maps text to HID key events, the same table fb-idb uses.
// Unsupported runes are skipped but reported: callers can send the rest and
// surface the gap instead of silently dropping input.
func TextEvents(text string) ([]*gen.HIDEvent, error) {
	var out []*gen.HIDEvent
	var skipped []rune
	for _, r := range text {
		evs, ok := textKey(r)
		if !ok {
			skipped = append(skipped, r)
			continue
		}
		out = append(out, evs...)
	}
	if len(skipped) > 0 {
		return out, fmt.Errorf("no HID keycode for %q", string(skipped))
	}
	return out, nil
}

func textKey(r rune) ([]*gen.HIDEvent, bool) {
	switch {
	case r >= 'a' && r <= 'z':
		return KeyEvents(4 + int(r-'a')), true
	case r >= 'A' && r <= 'Z':
		return ShiftedKeyEvents(4 + int(r-'A')), true
	case r >= '1' && r <= '9':
		return KeyEvents(29 + int(r-'0')), true
	case r == '0':
		return KeyEvents(39), true
	}
	shifted := map[rune]int{
		'!': 30, '@': 31, '#': 32, '$': 33, '%': 34, '^': 35, '&': 36,
		'*': 37, '(': 38, ')': 39, '_': 45, '+': 46, '{': 47, '}': 48,
		':': 51, '"': 52, '|': 49, '<': 54, '>': 55, '?': 56, '~': 53,
	}
	if code, ok := shifted[r]; ok {
		return ShiftedKeyEvents(code), true
	}
	switch r {
	case ';':
		return KeyEvents(51), true
	case '=':
		return KeyEvents(46), true
	case ',':
		return KeyEvents(54), true
	case '-':
		return KeyEvents(45), true
	case '.':
		return KeyEvents(55), true
	case '/':
		return KeyEvents(56), true
	case '`':
		return KeyEvents(53), true
	case '[':
		return KeyEvents(47), true
	case '\\':
		return KeyEvents(49), true
	case ']':
		return KeyEvents(48), true
	case '\'':
		return KeyEvents(52), true
	case ' ':
		return KeyEvents(HIDKeySpace), true
	case '\n', '\r':
		return KeyEvents(HIDKeyReturn), true
	case '\t':
		return KeyEvents(HIDKeyTab), true
	}
	return nil, false
}
