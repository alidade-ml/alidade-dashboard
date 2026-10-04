package api

// A dashboard started before the engine's first submit finds the state DB once
// the engine creates it, without a restart.
//
// Contract:
//   * With no file at the path, state-backed endpoints degrade as before
//     (empty list, 503 on a detail) and /api/health stays "ok" but names the
//     state DB as unavailable.
//   * Once the file exists, the next request after the retry interval reads it.
//   * Within the interval a missing file is not looked for again.

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func retryEvery(t *testing.T, d time.Duration) {
	t.Helper()
	old := stateRetryInterval
	stateRetryInterval = d
	t.Cleanup(func() { stateRetryInterval = old })
}

// beforeTheFirstSubmit is a handler as cmd/main.go builds it on a fresh NUC.
func beforeTheFirstSubmit(t *testing.T) (*Handler, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "state.db")
	h, err := NewHandlerAt(fakeAim(t, nil), path, nil)
	if err == nil {
		t.Fatal("the state DB opened before it existed")
	}
	return h, path
}

// theEngineSubmits creates the DB at path the way a first submit leaves it.
func theEngineSubmits(t *testing.T, path string) {
	t.Helper()
	built := makeStateDBWith(t, func(db *sql.DB) {
		submit(t, db, "s001", "v1", "s001-v1", "2026-10-02T22:07:00+00:00", nil)
	})
	if err := os.Rename(built, path); err != nil {
		t.Fatal(err)
	}
}

func health(t *testing.T, h *Handler) map[string]string {
	t.Helper()
	rr := httptest.NewRecorder()
	h.HandleHealth(rr, httptest.NewRequest("GET", "/api/health", nil))
	var out map[string]string
	if err := json.NewDecoder(rr.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return out
}

// --- Unhappy paths ---

func TestBeforeTheFirstSubmitTheDetailIs503(t *testing.T) {
	retryEvery(t, 0)
	h, _ := beforeTheFirstSubmit(t)
	rr, _ := getDetail(t, h, "s001")
	if rr.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", rr.Code)
	}
}

func TestHealthNamesAMissingStateDB(t *testing.T) {
	retryEvery(t, 0)
	h, _ := beforeTheFirstSubmit(t)
	got := health(t, h)
	if got["status"] != "ok" {
		t.Errorf("status = %q; a fresh NUC has no state DB yet and is healthy", got["status"])
	}
	if !strings.HasPrefix(got["state_db"], "unavailable") {
		t.Errorf("state_db = %q, want it named unavailable", got["state_db"])
	}
}

func TestAMissingFileIsNotLookedForAgainWithinTheInterval(t *testing.T) {
	retryEvery(t, time.Hour)
	h, path := beforeTheFirstSubmit(t)
	if got := callExperiments(t, h); len(got) != 0 {
		t.Fatalf("listed %d experiments with no state DB", len(got))
	}
	theEngineSubmits(t, path)
	if got := callExperiments(t, h); len(got) != 0 {
		t.Errorf("re-opened the state DB inside the retry interval")
	}
}

// --- Happy paths ---

func TestTheFirstSubmitIsListedWithoutARestart(t *testing.T) {
	retryEvery(t, 0)
	h, path := beforeTheFirstSubmit(t)
	if got := callExperiments(t, h); len(got) != 0 {
		t.Fatalf("listed %d experiments with no state DB", len(got))
	}

	theEngineSubmits(t, path)

	got := callExperiments(t, h)
	if len(got) != 1 || got[0].Name != "s001" {
		t.Fatalf("experiments = %+v, want s001 listed once the engine wrote it", got)
	}
	if rr, _ := getDetail(t, h, "s001"); rr.Code != http.StatusOK {
		t.Errorf("detail status = %d after the first submit, want 200", rr.Code)
	}
	if s := health(t, h)["state_db"]; s != "ok" {
		t.Errorf("state_db = %q after the first submit, want ok", s)
	}
}

func TestTheFirstSubmitsSpendIsShownWithoutARestart(t *testing.T) {
	retryEvery(t, 0)
	h, path := beforeTheFirstSubmit(t)
	if spend := callCost(t, h, "window=all"); len(spend.Experiments) != 0 {
		t.Fatalf("cost lists %d experiments with no state DB", len(spend.Experiments))
	}

	theEngineSubmits(t, path)

	if spend := callCost(t, h, "window=all"); len(spend.Experiments) != 1 {
		t.Errorf("cost lists %d experiments after the first submit, want 1", len(spend.Experiments))
	}
}
