package streamws

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Adelodunpeter25/sim-go/sdk"
	"github.com/gorilla/websocket"
)

func TestHandlerRejectsBeforeUpgrade(t *testing.T) {
	srv := httptest.NewServer(Handler(sdk.New()))
	defer srv.Close()
	cases := []struct {
		query string
		want  int
	}{
		{"platform=windows&id=x", http.StatusBadRequest},
		{"id=D6FFA457-63C1-4621-90D0-37719DED598F", http.StatusBadRequest},
		{"platform=android&id=emulator-does-not-exist", http.StatusNotFound},
		{"platform=ios&id=not-a-simulator", http.StatusNotFound},
	}
	for _, tc := range cases {
		resp, err := http.Get(srv.URL + "/?" + tc.query)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != tc.want {
			t.Fatalf("%s: status %d want %d", tc.query, resp.StatusCode, tc.want)
		}
		if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
			t.Fatalf("%s: content-type %q", tc.query, ct)
		}
	}
}

func TestPlainGETToValidDeviceIsNotAnUpgrade(t *testing.T) {
	// A non-websocket request for a bad device must still be a clean JSON
	// error, never a hijacked or hung connection.
	srv := httptest.NewServer(Handler(sdk.New()))
	defer srv.Close()
	_, resp, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(srv.URL, "http")+"/?platform=ios&id=nope", nil)
	if err == nil {
		t.Fatal("dial should fail for an unknown device")
	}
	if resp == nil || resp.StatusCode != http.StatusNotFound {
		t.Fatalf("resp=%v err=%v", resp, err)
	}
}

func TestTaggedCopiesAndPrefixes(t *testing.T) {
	src := []byte{9, 8, 7}
	out := tagged(TagKeyFrame, src)
	if len(out) != 4 || out[0] != 2 || out[1] != 9 {
		t.Fatalf("out=%v", out)
	}
	out[1] = 0
	if src[0] != 9 {
		t.Fatal("tagged must not alias the shared packet data")
	}
	if TagDescription != 1 || TagKeyFrame != 2 || TagDelta != 3 {
		t.Fatal("wire tags changed: the page depends on 1/2/3")
	}
}
