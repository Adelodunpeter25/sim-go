package ios

import "testing"

func TestParseIOSAppearance(t *testing.T) {
	for in, want := range map[string]string{"dark\n": "dark", " Light ": "light"} {
		if got, err := parseIOSAppearance(in); err != nil || got != want {
			t.Errorf("%q: got %q, %v", in, got, err)
		}
	}
	if _, err := parseIOSAppearance("unsupported"); err == nil {
		t.Error("want error")
	}
}
