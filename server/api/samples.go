package api

// Sample discovery for the Examples tab.
//
// ``astrolabe_callbacks.log_samples`` writes one Aim run per batch of
// qualitative outputs, tagged with astrolabe.kind="sample", the
// researcher's sample_set label, and the model run the samples came
// from. This file answers "which batches exist for this run?" — the
// payloads themselves are fetched separately, so a tab can render its
// structure before pulling megabytes of images.
//
// Deliberately mirrors HandleRunEvals: eval and sample runs are the
// same discovery shape (kind + model_run_hash), and a reader who knows
// one should recognise the other.

import (
	"net/http"
	"sort"
	"sync"
)

// Contract literals. These are copied from astrolabe's contract.py and
// checked by nothing today — see EXAMPLES-1.05. A rename engine-side
// makes this endpoint return an empty list, which is indistinguishable
// from "nobody logged any samples".
const (
	// SampleKind is astrolabe.kind on a run written by log_samples.
	SampleKind = "sample"
	// SampleSeqPrefix begins every sample sequence name:
	// sample/<set>/input and sample/<set>/output.
	SampleSeqPrefix = "sample/"
)

// SampleManifestEntry is one batch: one log_samples call.
//
// No payload here on purpose. The manifest is cheap and the payloads
// are not, and a tab wants to know what exists before deciding what to
// fetch.
type SampleManifestEntry struct {
	AimRunHash   string  `json:"aim_run_hash"`
	SampleSet    string  `json:"sample_set"`
	ModelRunHash string  `json:"model_run_hash"`
	CreationTime float64 `json:"creation_time"`
}

// HandleRunSamples returns the sample batches logged against a run.
//
// GET /api/runs/{model_run_hash}/samples
// → [{ aim_run_hash, sample_set, model_run_hash, creation_time }, ...]
//
// Discovery is by tag: astrolabe.kind == "sample" AND
// astrolabe.model_run_hash == <hash>. Multiple batches sharing a
// sample_set collapse to the newest by creation_time.
func (h *Handler) HandleRunSamples(w http.ResponseWriter, r *http.Request) {
	modelRunHash := extractPathParam(r.URL.Path, "/api/runs/", "/samples")
	if modelRunHash == "" {
		http.Error(w, "missing run hash", http.StatusBadRequest)
		return
	}

	experiments, err := h.aim.ListExperiments()
	if err != nil {
		// 502 rather than an empty list. An empty list reads as "this
		// run has no samples", which is a plausible and wrong answer —
		// exactly the silent failure this tab exists to remove.
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}

	// No pre-filter on experiment name. log_samples files a batch under
	// the submitting experiment when one is in scope, but under local-aim
	// mode the sync sidecar re-stamps synced runs with the *training*
	// experiment name — so filtering on "sample/<set>" would silently
	// drop precisely the runs this exists to find. The tag is the source
	// of truth. Same reasoning as HandleRunEvals, same trap.
	type candidate struct {
		hash         string
		creationTime float64
	}
	var candidates []candidate
	for _, exp := range experiments {
		if exp.RunCount == 0 || exp.Archived {
			continue
		}
		expRuns, err := h.aim.ListExperimentRuns(exp.ID)
		if err != nil {
			continue
		}
		for _, ar := range expRuns.Runs {
			if ar.Archived {
				continue
			}
			candidates = append(candidates, candidate{
				hash:         ar.RunID,
				creationTime: ar.CreationTime,
			})
		}
	}

	// One GetRunInfo per candidate is unavoidable — the tags live in
	// params — so fan out. Same pattern as eval discovery, and the same
	// cost: this walks every run in the project. Fine at hundreds,
	// wrong at thousands, and now the second endpoint paying it.
	type indexed struct {
		e  SampleManifestEntry
		ok bool
	}
	results := make(chan indexed, len(candidates))
	var wg sync.WaitGroup
	for _, c := range candidates {
		wg.Add(1)
		go func(c candidate) {
			defer wg.Done()
			info, err := h.aim.GetRunInfo(c.hash)
			if err != nil {
				results <- indexed{ok: false}
				return
			}
			tags := AstrolabeTagsFromParams(info.Params)
			if tags.Kind != SampleKind || tags.ModelRunHash != modelRunHash {
				results <- indexed{ok: false}
				return
			}
			results <- indexed{
				e: SampleManifestEntry{
					AimRunHash:   c.hash,
					SampleSet:    tags.SampleSet,
					ModelRunHash: tags.ModelRunHash,
					CreationTime: c.creationTime,
				},
				ok: true,
			}
		}(c)
	}
	go func() {
		wg.Wait()
		close(results)
	}()

	// Newest wins per sample_set. Re-running a sampling script mints a
	// new Aim run with the same tags; two batches under one label render
	// as two identically-titled blocks a reader cannot tell apart.
	//
	// Unlike an eval there is no score that supersedes, so this is a
	// judgement rather than an obvious call: if showing every batch turns
	// out to matter, the fix is an ?all=true parameter, not a different
	// default.
	newestBySet := map[string]SampleManifestEntry{}
	for res := range results {
		if !res.ok {
			continue
		}
		// A batch with no sample_set cannot be labelled, and an unlabelled
		// block is worse than an absent one.
		if res.e.SampleSet == "" {
			continue
		}
		if existing, found := newestBySet[res.e.SampleSet]; !found ||
			res.e.CreationTime > existing.CreationTime {
			newestBySet[res.e.SampleSet] = res.e
		}
	}

	// Non-nil so the JSON is [] and not null: the client maps over this,
	// and "no samples" is the common case for every run predating the
	// feature.
	out := make([]SampleManifestEntry, 0, len(newestBySet))
	for _, e := range newestBySet {
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreationTime != out[j].CreationTime {
			return out[i].CreationTime > out[j].CreationTime
		}
		return out[i].SampleSet < out[j].SampleSet
	})

	writeJSON(w, out)
}
