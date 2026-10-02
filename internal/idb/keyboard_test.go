package idb

import (
	"testing"

	gen "github.com/Adelodunpeter25/sim-go/internal/idb/gen"
)

// keySeq flattens events into (keycode, direction) pairs for assertions.
func keySeq(t *testing.T, events []*gen.HIDEvent) [][2]int {
	t.Helper()
	var out [][2]int
	for _, ev := range events {
		press := ev.GetPress()
		if press == nil {
			t.Fatalf("event %v is not a press", ev)
		}
		key := press.GetAction().GetKey()
		if key == nil {
			t.Fatalf("press action is not a key: %v", press.GetAction())
		}
		out = append(out, [2]int{int(key.GetKeycode()), int(press.GetDirection())})
	}
	return out
}

func TestTextEventsLetters(t *testing.T) {
	events, err := TextEvents("a")
	if err != nil {
		t.Fatalf("a: %v", err)
	}
	got := keySeq(t, events)
	want := [][2]int{{4, int(gen.HIDEvent_DOWN)}, {4, int(gen.HIDEvent_UP)}}
	if len(got) != len(want) {
		t.Fatalf("a: got %v want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("a: got %v want %v", got, want)
		}
	}
}

func TestTextEventsShifted(t *testing.T) {
	events, err := TextEvents("A")
	if err != nil {
		t.Fatalf("A: %v", err)
	}
	got := keySeq(t, events)
	want := [][2]int{
		{HIDKeyShiftLeft, int(gen.HIDEvent_DOWN)},
		{4, int(gen.HIDEvent_DOWN)},
		{4, int(gen.HIDEvent_UP)},
		{HIDKeyShiftLeft, int(gen.HIDEvent_UP)},
	}
	if len(got) != len(want) {
		t.Fatalf("A: got %v want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("A: got %v want %v", got, want)
		}
	}
}

func TestTextEventsDigitsAndPunctuation(t *testing.T) {
	// 1 -> 0x1E (30), 0 -> 0x27 (39), space -> 0x2C (44), \n -> 0x28 (40).
	events, err := TextEvents("10 \n")
	if err != nil {
		t.Fatalf("digits: %v", err)
	}
	var codes []int
	for _, ev := range events {
		press := ev.GetPress()
		if press.GetDirection() != gen.HIDEvent_DOWN {
			continue
		}
		codes = append(codes, int(press.GetAction().GetKey().GetKeycode()))
	}
	want := []int{30, 39, 44, 40}
	if len(codes) != len(want) {
		t.Fatalf("codes=%v want %v", codes, want)
	}
	for i := range want {
		if codes[i] != want[i] {
			t.Fatalf("codes=%v want %v", codes, want)
		}
	}
}

func TestTextEventsShiftedPunctuation(t *testing.T) {
	// "?" is shift + "/" (56); "(" is shift + "8" (38).
	events, err := TextEvents("?(")
	if err != nil {
		t.Fatalf("shifted punct: %v", err)
	}
	var codes []int
	for _, ev := range events {
		press := ev.GetPress()
		if press.GetDirection() != gen.HIDEvent_DOWN {
			continue
		}
		codes = append(codes, int(press.GetAction().GetKey().GetKeycode()))
	}
	want := []int{HIDKeyShiftLeft, 56, HIDKeyShiftLeft, 38}
	if len(codes) != len(want) {
		t.Fatalf("codes=%v want %v", codes, want)
	}
	for i := range want {
		if codes[i] != want[i] {
			t.Fatalf("codes=%v want %v", codes, want)
		}
	}
}

func TestTextEventsUnsupported(t *testing.T) {
	events, err := TextEvents("é")
	if err == nil {
		t.Fatal("expected an error for an unmapped rune")
	}
	if len(events) != 0 {
		t.Fatalf("expected no events, got %d", len(events))
	}
}

func TestTextEventsPartialKeepsSupported(t *testing.T) {
	events, err := TextEvents("aé")
	if err == nil {
		t.Fatal("expected an error mentioning the skipped rune")
	}
	if len(events) == 0 {
		t.Fatal("supported characters should still produce events")
	}
}
