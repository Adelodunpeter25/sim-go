package streamws

import (
	"net/url"
	"testing"
)

func TestQualityFromQuery(t *testing.T) {
	q := QualityFromQuery(url.Values{"max_size": {"720"}, "max_fps": {"30"}, "bitrate": {"2500000"}, "scale": {"0.5"}})
	if q.MaxSize != 720 || q.MaxFPS != 30 || q.BitRate != 2500000 || q.Scale != 0.5 {
		t.Fatalf("got %+v", q)
	}
	bad := QualityFromQuery(url.Values{"max_size": {"9"}, "max_fps": {"x"}, "bitrate": {"-1"}, "scale": {"4"}})
	if bad != (QualityFromQuery(url.Values{})) {
		t.Fatalf("out-of-range values must be ignored, got %+v", bad)
	}
}
