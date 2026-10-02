package idb

import "context"

// AndroidKey maps the Android-style keycodes the viewer wire and the Driver
// contract speak onto iOS: HOME via the button, the rest via the HID
// keyboard. handled=false means the code has no iOS equivalent.
func (s *Session) AndroidKey(ctx context.Context, code int) (handled bool, err error) {
	switch code {
	case 3: // HOME
		return true, s.Button(ctx, "HOME")
	case 4: // the page sends this for Escape
		return true, s.SendEvents(ctx, KeyEvents(HIDKeyEscape)...)
	case 66: // ENTER
		return true, s.SendEvents(ctx, KeyEvents(HIDKeyReturn)...)
	case 67: // DEL
		return true, s.SendEvents(ctx, KeyEvents(HIDKeyBackspace)...)
	}
	return false, nil
}
