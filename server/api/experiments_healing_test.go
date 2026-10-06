package api

// Tests for healing_attempts on GET /api/experiments.
//
// Contract, from what the home page has to say rather than from the handler:
//
//   * Each row carries the healer sessions of the submit it represents, the
//     newest, as `alidade list` does. An older version's healing is not the
//     current run's, and a sum across versions matches no other surface.
//   * One experiment's count never lands on another's row.
//   * A run that never healed reports 0 explicitly. The key is always present,
//     so a consumer can tell "did not heal" from a dashboard that predates it.

import (
	"database/sql"
	"encoding/json"
	"net/http/httptest"
	"testing"
)

func setHealing(t *testing.T, db *sql.DB, submitID string, n int) {
	t.Helper()
	// insertSubmit does not list this column, so it is set directly.
	if _, err := db.Exec(`UPDATE submits SET healing_attempts = ? WHERE submit_id = ?`, n, submitID); err != nil {
		t.Fatal(err)
	}
}

func healingByName(t *testing.T, seed func(*sql.DB)) map[string]int {
	t.Helper()
	got := callExperiments(t, makeHandlerWithState(t, makeStateDBWith(t, seed)))
	out := map[string]int{}
	for _, e := range got {
		out[e.Name] = e.HealingAttempts
	}
	return out
}

func healingSubmit(t *testing.T, db *sql.DB, name, version, startedAt string) {
	t.Helper()
	insertSubmit(t, db, map[string]any{
		"experiment_name": name,
		"version":         version,
		"submit_id":       name + "-" + version,
		"started_at":      startedAt,
		"current_state":   "COMPLETED",
		"outcome":         "success",
	})
}

func TestHandleExperiments_AnOlderVersionsHealingIsNotTheNewestRows(t *testing.T) {
	got := healingByName(t, func(db *sql.DB) {
		healingSubmit(t, db, "exp", "v1", "2026-08-01T00:00:00+00:00")
		healingSubmit(t, db, "exp", "v2", "2026-08-02T00:00:00+00:00")
		setHealing(t, db, "exp-v1", 2)
	})
	if got["exp"] != 0 {
		t.Errorf("healing_attempts = %d, want 0: v2 is the row and it never healed", got["exp"])
	}
}

func TestHandleExperiments_HealingIsNotSummedAcrossVersions(t *testing.T) {
	got := healingByName(t, func(db *sql.DB) {
		healingSubmit(t, db, "exp", "v1", "2026-08-01T00:00:00+00:00")
		healingSubmit(t, db, "exp", "v2", "2026-08-02T00:00:00+00:00")
		setHealing(t, db, "exp-v1", 2)
		setHealing(t, db, "exp-v2", 1)
	})
	if got["exp"] != 1 {
		t.Errorf("healing_attempts = %d, want 1, the newest submit's own count", got["exp"])
	}
}

func TestHandleExperiments_HealingStaysOnItsOwnExperiment(t *testing.T) {
	got := healingByName(t, func(db *sql.DB) {
		healingSubmit(t, db, "healed", "v1", "2026-08-01T00:00:00+00:00")
		healingSubmit(t, db, "clean", "v1", "2026-08-02T00:00:00+00:00")
		setHealing(t, db, "healed-v1", 3)
	})
	if got["healed"] != 3 || got["clean"] != 0 {
		t.Errorf("healing_attempts = %v, want healed=3 clean=0", got)
	}
}

func TestHandleExperiments_ANeverHealedRunSendsAnExplicitZero(t *testing.T) {
	path := makeStateDBWith(t, func(db *sql.DB) {
		healingSubmit(t, db, "clean", "v1", "2026-08-01T00:00:00+00:00")
	})
	rr := httptest.NewRecorder()
	makeHandlerWithState(t, path).HandleExperiments(rr, httptest.NewRequest("GET", "/api/experiments", nil))
	var raw []map[string]any
	if err := json.NewDecoder(rr.Body).Decode(&raw); err != nil {
		t.Fatal(err)
	}
	v, ok := raw[0]["healing_attempts"]
	if !ok || v != float64(0) {
		t.Errorf("healing_attempts = %v (present=%v), want an explicit 0", v, ok)
	}
}
