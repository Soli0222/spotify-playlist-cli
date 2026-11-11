package login

import (
	"context"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/soli0222/spotify-playlist-cli/internal/apperr"
)

func TestParsePasted(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"http://127.0.0.1:8765/callback?code=ABC123&state=st", "ABC123"},
		{"  http://127.0.0.1:8765/callback?code=ABC123&state=st\n", "ABC123"},
		{"http://127.0.0.1:8765/callback?code=ABC123", "ABC123"},
		{"code=ABC123&state=st", "ABC123"},
		{"?code=ABC123&state=st", "ABC123"},
		{"ABC123", "ABC123"},
		{"AQD-x_y.z\n", "AQD-x_y.z"},
	}
	for _, tc := range cases {
		got, err := ParsePasted(tc.in, "st")
		if err != nil || got != tc.want {
			t.Errorf("ParsePasted(%q) = %q, %v; want %q", tc.in, got, err, tc.want)
		}
	}
}

func TestParsePastedErrors(t *testing.T) {
	cases := map[string]apperr.Kind{
		"": apperr.KindInvalidArgument,
		"http://127.0.0.1:8765/callback?code=ABC&state=other":         apperr.KindAuthFailed,
		"http://127.0.0.1:8765/callback?error=access_denied&state=st": apperr.KindAuthFailed,
		"http://127.0.0.1:8765/callback?state=st":                     apperr.KindInvalidArgument,
		"some random text": apperr.KindInvalidArgument,
	}
	for in, want := range cases {
		_, err := ParsePasted(in, "st")
		if e := apperr.As(err); e == nil || e.Kind != want {
			t.Errorf("ParsePasted(%q) err = %v, want kind %s", in, err, want)
		}
	}
}

func TestReadPasted(t *testing.T) {
	got, err := ReadPasted(strings.NewReader("http://127.0.0.1:8765/callback?code=XYZ&state=st\nextra"), "st")
	if err != nil || got != "XYZ" {
		t.Fatalf("got %q err %v", got, err)
	}
	// 改行なしで EOF でも受け付ける。
	got, err = ReadPasted(strings.NewReader("XYZ"), "st")
	if err != nil || got != "XYZ" {
		t.Fatalf("got %q err %v", got, err)
	}
	_, err = ReadPasted(strings.NewReader(""), "st")
	if e := apperr.As(err); e == nil || e.Kind != apperr.KindInvalidArgument {
		t.Fatalf("err = %v", err)
	}
}

func TestServeCallback(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	base := "http://" + ln.Addr().String()
	ready := make(chan struct{})
	go func() {
		<-ready
		resp, err := http.Get(base + "/callback?code=ABC&state=st")
		if err != nil {
			t.Error(err)
			return
		}
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode != 200 || !strings.Contains(string(body), "Authorization complete") {
			t.Errorf("status %d body %s", resp.StatusCode, body)
		}
	}()
	code, err := serveCallback(context.Background(), ln, "/callback", "st", 5*time.Second, func() { close(ready) })
	if err != nil || code != "ABC" {
		t.Fatalf("code=%q err=%v", code, err)
	}
}

func TestServeCallbackStateMismatch(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	base := "http://" + ln.Addr().String()
	go func() {
		resp, err := http.Get(base + "/callback?code=ABC")
		if err == nil {
			_ = resp.Body.Close()
		}
	}()
	_, err = serveCallback(context.Background(), ln, "/callback", "st", 5*time.Second, nil)
	if e := apperr.As(err); e == nil || e.Kind != apperr.KindAuthFailed {
		t.Fatalf("err = %v", err)
	}
}

func TestServeCallbackTimeout(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	_, err = serveCallback(context.Background(), ln, "/callback", "st", 50*time.Millisecond, nil)
	if e := apperr.As(err); e == nil || e.Kind != apperr.KindAuthFailed {
		t.Fatalf("err = %v", err)
	}
}

func TestNewState(t *testing.T) {
	a, _ := NewState()
	b, _ := NewState()
	if len(a) != 32 || a == b {
		t.Fatalf("a=%q b=%q", a, b)
	}
}
