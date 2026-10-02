package idb

import (
	"context"
	"fmt"
	"time"

	gen "github.com/Adelodunpeter25/sim-go/internal/idb/gen"
)

// Tap injects down+up at device points (use Session.Points to map from video
// pixels). Proven live against the 16e sim: companion logs hid succeeded.
func (s *Session) Tap(ctx context.Context, x, y float64) error {
	down := &gen.HIDEvent{
		Event: &gen.HIDEvent_Press{Press: &gen.HIDEvent_HIDPress{
			Action: &gen.HIDEvent_HIDPressAction{Action: &gen.HIDEvent_HIDPressAction_Touch{
				Touch: &gen.HIDEvent_HIDTouch{Point: &gen.Point{X: x, Y: y}},
			}},
			Direction: gen.HIDEvent_DOWN,
		}},
	}
	up := &gen.HIDEvent{
		Event: &gen.HIDEvent_Press{Press: &gen.HIDEvent_HIDPress{
			Action: &gen.HIDEvent_HIDPressAction{Action: &gen.HIDEvent_HIDPressAction_Touch{
				Touch: &gen.HIDEvent_HIDTouch{Point: &gen.Point{X: x, Y: y}},
			}},
			Direction: gen.HIDEvent_UP,
		}},
	}
	return s.press(ctx, down, up)
}

// Swipe injects one swipe in device points.
func (s *Session) Swipe(ctx context.Context, x1, y1, x2, y2, seconds float64) error {
	if seconds <= 0 {
		seconds = 0.3
	}
	stream, err := s.client.Hid(ctx)
	if err != nil {
		return fmt.Errorf("idb hid: %w", err)
	}
	err = stream.Send(&gen.HIDEvent{
		Event: &gen.HIDEvent_Swipe{Swipe: &gen.HIDEvent_HIDSwipe{
			Start:    &gen.Point{X: x1, Y: y1},
			End:      &gen.Point{X: x2, Y: y2},
			Duration: seconds,
		}},
	})
	if err != nil {
		return fmt.Errorf("idb hid send: %w", err)
	}
	_, err = stream.CloseAndRecv()
	if err != nil {
		return fmt.Errorf("idb hid: %w", err)
	}
	return nil
}

// Button injects a hardware button (HOME, LOCK, SIDE_BUTTON, SIRI).
func (s *Session) Button(ctx context.Context, name string) error {
	var b gen.HIDEvent_HIDButtonType
	switch name {
	case "HOME":
		b = gen.HIDEvent_HOME
	case "LOCK":
		b = gen.HIDEvent_LOCK
	case "SIDE_BUTTON":
		b = gen.HIDEvent_SIDE_BUTTON
	case "SIRI":
		b = gen.HIDEvent_SIRI
	default:
		return fmt.Errorf("unknown idb button %q (want HOME|LOCK|SIDE_BUTTON|SIRI)", name)
	}
	mk := func(d gen.HIDEvent_HIDDirection) *gen.HIDEvent {
		return &gen.HIDEvent{
			Event: &gen.HIDEvent_Press{Press: &gen.HIDEvent_HIDPress{
				Action: &gen.HIDEvent_HIDPressAction{Action: &gen.HIDEvent_HIDPressAction_Button{
					Button: &gen.HIDEvent_HIDButton{Button: b},
				}},
				Direction: d,
			}},
		}
	}
	return s.press(ctx, mk(gen.HIDEvent_DOWN), mk(gen.HIDEvent_UP))
}

func (s *Session) press(ctx context.Context, down, up *gen.HIDEvent) error {
	stream, err := s.client.Hid(ctx)
	if err != nil {
		return fmt.Errorf("idb hid: %w", err)
	}
	if err := stream.Send(down); err != nil {
		return fmt.Errorf("idb hid send: %w", err)
	}
	time.Sleep(60 * time.Millisecond)
	if err := stream.Send(up); err != nil {
		return fmt.Errorf("idb hid send: %w", err)
	}
	if _, err := stream.CloseAndRecv(); err != nil {
		return fmt.Errorf("idb hid: %w", err)
	}
	return nil
}

// SendEvents streams HID events over one connection and waits for the
// companion to acknowledge the whole sequence (mirrors fb-idb's
// send_events, so a text run goes out in order without per-event round trips).
func (s *Session) SendEvents(ctx context.Context, events ...*gen.HIDEvent) error {
	if len(events) == 0 {
		return nil
	}
	stream, err := s.client.Hid(ctx)
	if err != nil {
		return fmt.Errorf("idb hid: %w", err)
	}
	for _, ev := range events {
		if err := stream.Send(ev); err != nil {
			return fmt.Errorf("idb hid send: %w", err)
		}
	}
	if _, err := stream.CloseAndRecv(); err != nil {
		return fmt.Errorf("idb hid: %w", err)
	}
	return nil
}

// Text types a string through HID keys.
func (s *Session) Text(ctx context.Context, text string) error {
	events, err := TextEvents(text)
	if err != nil && len(events) == 0 {
		return err
	}
	if sendErr := s.SendEvents(ctx, events...); sendErr != nil {
		return sendErr
	}
	return err
}
