package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// woff2Magic is the first four bytes of every WOFF2 file.
var woff2Magic = []byte("wOF2")

func staticFixture(t *testing.T) (string, http.Handler) {
	t.Helper()
	dir := t.TempDir()
	assets := filepath.Join(dir, "assets")
	if err := os.MkdirAll(assets, 0o755); err != nil {
		t.Fatal(err)
	}
	// A believable font: the magic number plus filler, so a handler that
	// returns the wrong body cannot pass by accident.
	font := append(append([]byte{}, woff2Magic...), bytes.Repeat([]byte{0x42}, 64)...)
	if err := os.WriteFile(filepath.Join(assets, "real-latin-500.woff2"), font, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("<!doctype html><title>spa</title>"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir, spaFallback(dir, http.FileServer(http.Dir(dir)))
}

// A missing asset is served as the SPA shell with status 200, never a 404.
// This is why nothing downstream may treat a 200 as proof a font arrived: the
// browser receives HTML where it asked for a font, rejects it, and falls back
// to the system stack without surfacing an error anywhere.
func TestMissingAssetIsServedAsTheSpaShell(t *testing.T) {
	_, h := staticFixture(t)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/assets/absent.woff2", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("a missing asset returned %d; the fallback's behaviour changed and the "+
			"byte-level font checks may no longer be necessary", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Fatalf("missing asset served as %q, want text/html", ct)
	}
	if bytes.HasPrefix(rec.Body.Bytes(), woff2Magic) {
		t.Fatal("a missing font came back with font bytes")
	}
}

func TestFontIsServedAsFontBytes(t *testing.T) {
	_, h := staticFixture(t)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/assets/real-latin-500.woff2", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	if !bytes.HasPrefix(rec.Body.Bytes(), woff2Magic) {
		t.Fatalf("body did not start with the WOFF2 magic number; got %q",
			rec.Body.Bytes()[:min(16, rec.Body.Len())])
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "font/woff2") {
		t.Fatalf("Content-Type %q, want font/woff2", ct)
	}
}

// Deep links must still reach the SPA — the fallback's actual purpose.
func TestDeepLinkReachesTheSpa(t *testing.T) {
	_, h := staticFixture(t)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/experiment/thesis-vit", nil))

	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "<!doctype html>") {
		t.Fatalf("deep link got %d %q", rec.Code, rec.Body.String())
	}
}
