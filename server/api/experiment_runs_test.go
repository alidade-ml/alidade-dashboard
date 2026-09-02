package api

// Tests for GET /api/experiments/{name}/runs.
//
// The endpoint answers "what belongs on this experiment's page", which is
// a union of the models the experiment produced and the models it
// evaluated. Those are different sets: an eval-only submit produces
// nothing, and a submit can evaluate a model that lives in a different
// experiment entirely. Returning either set alone drops rows.
//
// Bookkeeping runs (engine cost runs, the eval runs themselves) are not
// rows. Everything else is returned carrying its kind so the client can
// decide what belongs in a training chart.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func getExperimentRuns(t *testing.T, h *Handler, experiment string) []RunDetail {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/experiments/"+experiment+"/runs", nil)
	w := httptest.NewRecorder()
	h.HandleExperimentRuns(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var got []RunDetail
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return got
}

func runByHash(details []RunDetail, hash string) *RunDetail {
	for i := range details {
		if details[i].Hash == hash {
			return &details[i]
		}
	}
	return nil
}

func hashes(details []RunDetail) []string {
	out := make([]string, 0, len(details))
	for _, d := range details {
		out = append(out, d.Hash)
	}
	return out
}

// ---------- bookkeeping runs are not rows ----------------------------

func TestExperimentRunsExcludesEvalRuns(t *testing.T) {
	// An eval run filed under the experiment carries no training
	// metrics. Rendering it as a row puts an empty line in the stats
	// table and an empty series in the chart legend.
	aim := fakeAim(t, []fakeRun{
		{experiment: "latent-bert", hash: "train-1", name: "LatentBERT",
			tags: map[string]any{"alidade.version": "v1"}},
		{experiment: "latent-bert", hash: "eval-1", name: "glue-eval",
			tags: map[string]any{
				"alidade.kind":           "eval",
				"alidade.task_set":       "glue",
				"alidade.model_run_hash": "train-1",
			}},
	})
	got := getExperimentRuns(t, makeHandlerWithAim(t, aim), "latent-bert")

	if runByHash(got, "eval-1") != nil {
		t.Errorf("eval run rendered as a row; hashes = %v", hashes(got))
	}
	if runByHash(got, "train-1") == nil {
		t.Errorf("training run missing; hashes = %v", hashes(got))
	}
}

func TestExperimentRunsExcludesMetadataRuns(t *testing.T) {
	aim := fakeAim(t, []fakeRun{
		{experiment: "exp", hash: "train-1", tags: map[string]any{"alidade.version": "v1"}},
		{experiment: "exp", hash: "cost-1", tags: map[string]any{"alidade.kind": "metadata"}},
	})
	got := getExperimentRuns(t, makeHandlerWithAim(t, aim), "exp")

	if runByHash(got, "cost-1") != nil {
		t.Errorf("metadata run rendered as a row; hashes = %v", hashes(got))
	}
}

// ---------- kind is surfaced, not inferred ---------------------------

func TestExperimentRunsSurfacesKindSoUnknownKindsAreNotTraining(t *testing.T) {
	// The bug this guards: deciding what to omit by enumerating the
	// kinds to hide means every kind invented later falls through to the
	// training path by default. Passing kind through moves the decision
	// to the client, where a new kind is visibly not training rather
	// than silently counted as one.
	aim := fakeAim(t, []fakeRun{
		{experiment: "exp", hash: "train-1", tags: map[string]any{"alidade.version": "v1"}},
		{experiment: "exp", hash: "import-1", name: "roberta-base",
			tags: map[string]any{"alidade.kind": "external_checkpoint"}},
	})
	got := getExperimentRuns(t, makeHandlerWithAim(t, aim), "exp")

	imported := runByHash(got, "import-1")
	if imported == nil {
		t.Fatalf("imported model missing; hashes = %v", hashes(got))
	}
	if imported.Kind != "external_checkpoint" {
		t.Errorf("Kind = %q, want %q", imported.Kind, "external_checkpoint")
	}
	if training := runByHash(got, "train-1"); training == nil || training.Kind != "" {
		t.Errorf("untagged training run should carry an empty Kind, got %+v", training)
	}
}

// ---------- the union ------------------------------------------------

func TestExperimentRunsIncludesAModelEvaluatedFromAnotherExperiment(t *testing.T) {
	// The case a fallback cannot serve: this experiment produced nothing
	// and scored a model that lives elsewhere. Its page is empty unless
	// the evaluated model is resolved across the experiment boundary.
	aim := fakeAim(t, []fakeRun{
		{experiment: "latent-bert", hash: "model-1", name: "LatentBERT",
			tags: map[string]any{"alidade.version": "v1"}},
		{experiment: "glue-sweep", hash: "eval-1",
			tags: map[string]any{
				"alidade.kind":           "eval",
				"alidade.task_set":       "glue",
				"alidade.model_run_hash": "model-1",
			}},
	})
	got := getExperimentRuns(t, makeHandlerWithAim(t, aim), "glue-sweep")

	model := runByHash(got, "model-1")
	if model == nil {
		t.Fatalf("evaluated model missing; hashes = %v", hashes(got))
	}
	if !model.Evaluated {
		t.Errorf("Evaluated = false, want true — the row arrived by evaluation, not production")
	}
	if model.ExperimentName != "latent-bert" {
		t.Errorf("ExperimentName = %q, want %q — the row must say where the model actually lives",
			model.ExperimentName, "latent-bert")
	}
}

func TestExperimentRunsUnionsProducedAndEvaluatedRatherThanFallingBack(t *testing.T) {
	// The union bug: a submit that trains one model and also evaluates a
	// foreign one has both on its page. An implementation that falls
	// back from produced to evaluated (only consulting evals when there
	// are no runs) silently drops one of them.
	aim := fakeAim(t, []fakeRun{
		{experiment: "mixed", hash: "own-model", name: "our-model",
			tags: map[string]any{"alidade.version": "v1"}},
		{experiment: "mixed", hash: "eval-own",
			tags: map[string]any{
				"alidade.kind": "eval", "alidade.task_set": "glue",
				"alidade.model_run_hash": "own-model",
			}},
		{experiment: "mixed", hash: "eval-foreign",
			tags: map[string]any{
				"alidade.kind": "eval", "alidade.task_set": "glue",
				"alidade.model_run_hash": "foreign-model",
			}},
		{experiment: "hf-imports", hash: "foreign-model", name: "t5-base",
			tags: map[string]any{"alidade.kind": "external_checkpoint"}},
	})
	got := getExperimentRuns(t, makeHandlerWithAim(t, aim), "mixed")

	if runByHash(got, "own-model") == nil {
		t.Errorf("locally trained model dropped; hashes = %v", hashes(got))
	}
	if runByHash(got, "foreign-model") == nil {
		t.Errorf("externally evaluated model dropped; hashes = %v", hashes(got))
	}
}

func TestExperimentRunsDoesNotDuplicateAModelItProducedAndEvaluated(t *testing.T) {
	// Evaluating your own model is the common case; it must not appear
	// twice, once per half of the union.
	aim := fakeAim(t, []fakeRun{
		{experiment: "exp", hash: "model-1", name: "our-model",
			tags: map[string]any{"alidade.version": "v1"}},
		{experiment: "exp", hash: "eval-1",
			tags: map[string]any{
				"alidade.kind": "eval", "alidade.task_set": "glue",
				"alidade.model_run_hash": "model-1",
			}},
	})
	got := getExperimentRuns(t, makeHandlerWithAim(t, aim), "exp")

	count := 0
	for _, d := range got {
		if d.Hash == "model-1" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("model-1 appears %d times, want 1; hashes = %v", count, hashes(got))
	}
	if d := runByHash(got, "model-1"); d != nil && d.Evaluated {
		t.Errorf("a model the experiment produced should not be marked Evaluated")
	}
}

func TestExperimentRunsTwoTaskSetsOnOneModelYieldOneRow(t *testing.T) {
	// Scoring one model across GLUE and MMLU is two eval runs pointing
	// at the same model. The model is still one row.
	aim := fakeAim(t, []fakeRun{
		{experiment: "bench", hash: "eval-glue",
			tags: map[string]any{
				"alidade.kind": "eval", "alidade.task_set": "glue",
				"alidade.model_run_hash": "model-1",
			}},
		{experiment: "bench", hash: "eval-mmlu",
			tags: map[string]any{
				"alidade.kind": "eval", "alidade.task_set": "mmlu",
				"alidade.model_run_hash": "model-1",
			}},
		{experiment: "elsewhere", hash: "model-1", name: "roberta-base",
			tags: map[string]any{"alidade.kind": "external_checkpoint"}},
	})
	got := getExperimentRuns(t, makeHandlerWithAim(t, aim), "bench")

	if len(got) != 1 {
		t.Fatalf("got %d rows, want 1; hashes = %v", len(got), hashes(got))
	}
	if got[0].Hash != "model-1" {
		t.Errorf("hash = %q, want model-1", got[0].Hash)
	}
}

func TestExperimentRunsSkipsAnEvaluatedModelMissingFromAim(t *testing.T) {
	// A deleted or not-yet-synced model must not become an empty row.
	// The eval results still exist and stay reachable on the Eval tab.
	aim := fakeAim(t, []fakeRun{
		{experiment: "bench", hash: "eval-1",
			tags: map[string]any{
				"alidade.kind": "eval", "alidade.task_set": "glue",
				"alidade.model_run_hash": "vanished",
			}},
	})
	got := getExperimentRuns(t, makeHandlerWithAim(t, aim), "bench")

	if len(got) != 0 {
		t.Errorf("got %d rows, want 0; hashes = %v", len(got), hashes(got))
	}
}

func TestExperimentRunsIgnoresAnEvalWithNoModel(t *testing.T) {
	// An unlinked eval (no provenance resolved) carries no model hash.
	// It must not produce a phantom row keyed on the empty string.
	aim := fakeAim(t, []fakeRun{
		{experiment: "bench", hash: "eval-1",
			tags: map[string]any{"alidade.kind": "eval", "alidade.task_set": "glue"}},
	})
	got := getExperimentRuns(t, makeHandlerWithAim(t, aim), "bench")

	if len(got) != 0 {
		t.Errorf("got %d rows, want 0; hashes = %v", len(got), hashes(got))
	}
}

// ---------- naming ----------------------------------------------------

func TestEvaluatedModelIsNotLabelledWithTheRequestingExperiment(t *testing.T) {
	// runDisplayName falls back to the experiment name for a placeholder
	// Aim name. Applied to a model borrowed from another experiment that
	// attributes it to the wrong experiment outright.
	aim := fakeAim(t, []fakeRun{
		{experiment: "glue-sweep", hash: "eval-1",
			tags: map[string]any{
				"alidade.kind": "eval", "alidade.task_set": "glue",
				"alidade.model_run_hash": "model-1",
			}},
		{experiment: "latent-bert", hash: "model-1", name: "Run: model-1",
			tags: map[string]any{"alidade.kind": "external_checkpoint"}},
	})
	got := getExperimentRuns(t, makeHandlerWithAim(t, aim), "glue-sweep")

	model := runByHash(got, "model-1")
	if model == nil {
		t.Fatalf("evaluated model missing; hashes = %v", hashes(got))
	}
	if model.Name == "glue-sweep" {
		t.Errorf("model labelled with the requesting experiment %q", model.Name)
	}
	if model.Name != "latent-bert" {
		t.Errorf("Name = %q, want the model's own experiment %q", model.Name, "latent-bert")
	}
}

func TestRunLabelFallsBackThroughNameThenExperimentThenHash(t *testing.T) {
	cases := []struct {
		label         string
		ar            AimRun
		ownExperiment string
		want          string
	}{
		{"real name wins", AimRun{RunID: "abcdef0123456789", Name: "LatentBERT"}, "exp", "LatentBERT"},
		{"aim placeholder is not a name", AimRun{RunID: "abcdef0123456789", Name: "Run: abcdef01"}, "exp", "exp"},
		{"empty name falls to experiment", AimRun{RunID: "abcdef0123456789"}, "exp", "exp"},
		{"whitespace name falls to experiment", AimRun{RunID: "abcdef0123456789", Name: "   "}, "exp", "exp"},
		{"no experiment either falls to short hash", AimRun{RunID: "abcdef0123456789"}, "", "abcdef012345"},
		{"short hash is not truncated", AimRun{RunID: "abc"}, "", "abc"},
	}
	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			if got := runLabel(tc.ar, tc.ownExperiment); got != tc.want {
				t.Errorf("runLabel = %q, want %q", got, tc.want)
			}
		})
	}
}
