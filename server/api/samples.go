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
	"errors"
	"net/http"
	"regexp"
	"sort"
	"strings"
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
//
// Scoped to the model run's own Aim experiment. log_samples files a
// batch under the submitting experiment:
//
//	experiment=identity.get(TAG_EXPERIMENT) or f"sample/{sample_set}"
//
// and ambient_identity() reads the AIM_RUN_TAGS the engine sets at
// setup — so a run's sample batches are its siblings. The first cut
// walked every run in the project, copied from HandleRunEvals, where
// leaving the experiment IS required: a model evaluated by one
// experiment can have been produced by another. Samples do not have
// that property, so this was project-wide work to find something next
// door.
//
// The trade is recorded in EXAMPLES-1.01b: a batch logged with no
// submit in scope lands under "sample/<set>" and is not found here.
// That is a known gap belonging to the producer, not a reason to
// restore the scan.
func (h *Handler) HandleRunSamples(w http.ResponseWriter, r *http.Request) {
	modelRunHash := extractPathParam(r.URL.Path, "/api/runs/", "/samples")
	if modelRunHash == "" {
		http.Error(w, "missing run hash", http.StatusBadRequest)
		return
	}

	// Which experiment is the model run in? One call, and it also
	// establishes the run exists.
	modelInfo, err := h.aim.GetRunInfo(modelRunHash)
	if errors.Is(err, ErrRunNotFound) {
		// A hash Aim does not know is the caller's mistake, and a
		// different fact from "no samples" or "Aim is down". The old
		// scan could not tell them apart: it returned [] for a typo.
		http.Error(w, "run not found", http.StatusNotFound)
		return
	}
	if err != nil {
		// 502 rather than an empty list. An empty list reads as "this
		// run has no samples", which is a plausible and wrong answer —
		// exactly the silent failure this tab exists to remove.
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	// No guard on an empty experiment ID. Aim always places a run in an
	// experiment, so an empty one means a malformed info response, and
	// letting that fall through to a 502 is the honest answer — an early
	// return of [] would report "no samples" for a broken dependency.
	expRuns, err := h.aim.ListExperimentRuns(modelInfo.Props.Experiment.ID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}

	type candidate struct {
		hash         string
		creationTime float64
	}
	var candidates []candidate
	for _, ar := range expRuns.Runs {
		if ar.Archived || ar.RunID == modelRunHash {
			continue
		}
		candidates = append(candidates, candidate{
			hash:         ar.RunID,
			creationTime: ar.CreationTime,
		})
	}

	// One GetRunInfo per candidate is unavoidable — the tags live in
	// params — so fan out. The count is now the experiment's runs
	// rather than the project's.
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

// --- Batch payloads, read from Aim ---

// SamplePair is one step of a batch: what went in, what came out.
//
// Every field but Step is a pointer because absent and empty are
// different facts. A set logged without inputs (unconditional
// generation) has no InputText; a model that returned the empty string
// has one, and it is "". Collapsing those loses the only signal that
// says which happened.
//
// Kind on the batch describes the OUTPUT. A prompt-to-image batch is
// kind "image" with InputText set — read the per-pair fields, never
// infer the input's type from the batch's kind.
type SamplePair struct {
	Step       int64   `json:"step"`
	InputText  *string `json:"input_text,omitempty"`
	InputURI   *string `json:"input_uri,omitempty"`
	OutputText *string `json:"output_text,omitempty"`
	OutputURI  *string `json:"output_uri,omitempty"`
}

// SampleBatch is one log_samples call, read back.
type SampleBatch struct {
	AimRunHash string       `json:"aim_run_hash"`
	SampleSet  string       `json:"sample_set"`
	Kind       string       `json:"kind"`
	Pairs      []SamplePair `json:"pairs"`
}

// sampleHashPattern guards the run hash before it reaches a URL path.
var sampleHashPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// HandleSampleBatch returns one sample batch.
//
// GET /api/samples/{aim_run_hash}?set=<sample_set>
// → { aim_run_hash, sample_set, kind, pairs: [...] }
//
// Reads the two sequences the producer wrote — sample/<set>/input and
// sample/<set>/output — and joins them by STEP. They need not share a
// step set: an absent input is unconditional generation, and a
// partially drained buffer can leave one shorter.
//
// Text only. Images are EXAMPLES-1.03; this refuses them rather than
// half-rendering a batch whose payloads it cannot serve.
func (h *Handler) HandleSampleBatch(w http.ResponseWriter, r *http.Request) {
	aimRunHash := extractPathParam(r.URL.Path, "/api/samples/", "")
	if aimRunHash == "" {
		http.Error(w, "missing run hash", http.StatusBadRequest)
		return
	}
	if !sampleHashPattern.MatchString(aimRunHash) {
		http.Error(w, "invalid run hash", http.StatusBadRequest)
		return
	}
	sampleSet := r.URL.Query().Get("set")
	if sampleSet == "" {
		http.Error(w, "missing set", http.StatusBadRequest)
		return
	}
	if strings.Contains(sampleSet, "/") {
		// The set is a path segment in the sequence name. A slash would
		// silently address a different sequence.
		http.Error(w, "invalid set", http.StatusBadRequest)
		return
	}

	inputs, err := h.aim.GetTextSequence(aimRunHash, SampleSeqPrefix+sampleSet+"/input")
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	outputs, err := h.aim.GetTextSequence(aimRunHash, SampleSeqPrefix+sampleSet+"/output")
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}

	// An image batch decodes with empty Text and a populated BlobURI.
	// Serving it would render every output as an empty string, which
	// looks like a model that produced nothing.
	for _, rec := range append(append([]ObjectRecord{}, inputs.Records...), outputs.Records...) {
		if rec.BlobURI != "" {
			http.Error(w, "this batch contains images; image payloads are not served yet",
				http.StatusNotImplemented)
			return
		}
	}

	batch := SampleBatch{
		AimRunHash: aimRunHash,
		SampleSet:  sampleSet,
		Kind:       "text",
		Pairs:      joinByStep(inputs, outputs),
	}
	writeJSON(w, batch)
}

// joinByStep pairs the two sequences on their step, not their position.
//
// The output sequence drives the ordering: log_samples always tracks an
// output and only sometimes an input, so an output with no input is
// unconditional generation while an input with no output is a broken
// write. The latter is kept rather than dropped — a visible half-pair
// is a better bug report than a silently shorter batch.
func joinByStep(inputs, outputs *ObjectSequence) []SamplePair {
	seen := map[int64]bool{}
	pairs := make([]SamplePair, 0, len(outputs.Steps))

	add := func(step int64) {
		if seen[step] {
			return
		}
		seen[step] = true
		p := SamplePair{Step: step}
		if rec, ok := inputs.At(step); ok {
			text := rec.Text
			p.InputText = &text
		}
		if rec, ok := outputs.At(step); ok {
			text := rec.Text
			p.OutputText = &text
		}
		pairs = append(pairs, p)
	}
	for _, step := range outputs.Steps {
		add(step)
	}
	for _, step := range inputs.Steps {
		add(step)
	}
	sort.Slice(pairs, func(i, j int) bool { return pairs[i].Step < pairs[j].Step })
	return pairs
}
