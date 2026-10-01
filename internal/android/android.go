// Package android drives Android emulators via `adb` + `emulator`.
//
// Works on mac and linux. Slimming disables slim.AndroidPackages with
// `pm disable-user` (avdslim-style, cf. reference/simfleet src/android.ts).
package android

// Driver talks to the Android SDK toolchain.
type Driver struct{}

func (Driver) Name() string { return "android" }
