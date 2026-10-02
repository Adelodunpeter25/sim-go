package android

import "testing"

func TestParseAndroidAppearance(t *testing.T) {
	for in, want := range map[string]string{
		"Night mode: yes\n": "dark", "Night mode: no": "light",
		"Night mode: auto": "auto", "Night mode: custom_bedtime": "custom_bedtime",
	} {
		if got, err := parseAndroidAppearance(in); err != nil || got != want {
			t.Errorf("%q: got %q, %v", in, got, err)
		}
	}
	if _, err := parseAndroidAppearance("Error"); err == nil {
		t.Error("want error")
	}
}
