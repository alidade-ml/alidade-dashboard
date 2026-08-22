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

	// Confirm the run exists before answering about it. The query below
	// cannot tell "this hash has no sample batches" from "this hash is
	// not a run at all", and returning [] for a typo is exactly the
	// ambiguity EXAMPLES-1.01b removed. One extra request, and the same
	// one the hash-shaped include path already makes.
	if _, err := h.aim.GetRunInfo(modelRunHash); err != nil {
		if errors.Is(err, ErrRunNotFound) {
			http.Error(w, "run not found", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}

	// One query. Cross-experiment on purpose, and that is a change from
	// EXAMPLES-1.01b: that slice narrowed discovery to the model run's
	// own experiment because a project-wide walk was the only
	// alternative, and it recorded the resulting gap — a batch logged
	// with no submit in scope files under "sample/<set>" and became
	// invisible. Asking by tag has no reason to care which experiment a
	// batch is in, so the gap closes.
	runs, err := h.aim.SearchRuns(QueryByTags(map[string]string{
		TagKind:         SampleKind,
		TagModelRunHash: modelRunHash,
	}))
	if err != nil {
		// 502 rather than an empty list. An empty list reads as "this
		// run has no samples", which is a plausible and wrong answer —
		// exactly the silent failure this tab exists to remove.
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}

	entries := make([]SampleManifestEntry, 0, len(runs))
	for _, run := range runs {
		if run.Archived {
			continue
		}
		tags := AstrolabeTagsFromParams(run.Params)
		// Re-check what the query asked for — see HandleRunEvals.
		if tags.Kind != SampleKind || tags.ModelRunHash != modelRunHash {
			continue
		}
		entries = append(entries, SampleManifestEntry{
			AimRunHash:   run.Hash,
			SampleSet:    tags.SampleSet,
			ModelRunHash: tags.ModelRunHash,
			CreationTime: run.CreationTime,
		})
	}

	// Newest wins per sample_set. Re-running a sampling script mints a
	// new Aim run with the same tags; two batches under one label render
	// as two identically-titled blocks a reader cannot tell apart.
	//
	// Unlike an eval there is no score that supersedes, so this is a
	// judgement rather than an obvious call: if showing every batch turns
	// out to matter, the fix is an ?all=true parameter, not a different
	// default.
	newestBySet := map[string]SampleManifestEntry{}
	for _, e := range entries {
		// A batch with no sample_set cannot be labelled, and an unlabelled
		// block is worse than an absent one.
		if e.SampleSet == "" {
			continue
		}
		if existing, found := newestBySet[e.SampleSet]; !found ||
			e.CreationTime > existing.CreationTime {
			newestBySet[e.SampleSet] = e
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

	// Each half is fetched twice — once as text, once as images — because
	// nothing on the run says which it is, and the two halves can differ:
	// a prompt-to-image batch has a text input and an image output. Asking
	// the wrong route returns an empty sequence rather than an error, so
	// the populated one wins.
	inputs, err := h.sequenceEitherWay(aimRunHash, sampleSet, "input")
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	outputs, err := h.sequenceEitherWay(aimRunHash, sampleSet, "output")
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}

	// kind describes the OUTPUT, not the input. A prompt-to-image batch
	// is "image" with input_text set — the asymmetry that has already
	// caused bugs on both sides of this contract.
	kind := "text"
	for _, rec := range outputs.Records {
		if rec.BlobURI != "" {
			kind = "image"
			break
		}
	}

	batch := SampleBatch{
		AimRunHash: aimRunHash,
		SampleSet:  sampleSet,
		Kind:       kind,
		Pairs:      joinByStep(inputs, outputs),
	}
	writeJSON(w, batch)
}

// sequenceEitherWay returns whichever of the text and image sequences
// for this role actually has records.
//
// A run carries no marker saying which payload a set used, and asking
// the wrong route is not an error — it returns an empty sequence. So
// both are asked and the populated one is returned. If both are empty
// the set was never logged, which is a valid answer.
func (h *Handler) sequenceEitherWay(aimRunHash, sampleSet, role string) (*ObjectSequence, error) {
	name := SampleSeqPrefix + sampleSet + "/" + role
	text, err := h.aim.GetTextSequence(aimRunHash, name)
	if err != nil {
		return nil, err
	}
	if len(text.Records) > 0 {
		return text, nil
	}
	return h.aim.GetImageSequence(aimRunHash, name)
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
			if rec.BlobURI != "" {
				uri := rec.BlobURI
				p.InputURI = &uri
			} else {
				text := rec.Text
				p.InputText = &text
			}
		}
		if rec, ok := outputs.At(step); ok {
			if rec.BlobURI != "" {
				uri := rec.BlobURI
				p.OutputURI = &uri
			} else {
				text := rec.Text
				p.OutputText = &text
			}
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

// HandleSampleBlob streams the bytes for one image.
//
// GET /api/samples/blob?uri=<aim-blob-uri>
//
// The uri arrives from a batch response and goes back to Aim verbatim.
// It is an opaque Fernet-encrypted token: this handler does not decode
// it, does not validate its shape, and must never construct one.
func (h *Handler) HandleSampleBlob(w http.ResponseWriter, r *http.Request) {
	uri := r.URL.Query().Get("uri")
	if uri == "" {
		http.Error(w, "missing uri", http.StatusBadRequest)
		return
	}
	format := r.URL.Query().Get("format")
	if format == "" {
		format = "png"
	}
	contentType, ok := imageContentTypes[format]
	if !ok {
		// Refused rather than sniffed. The sequence record states the
		// format, so a value outside this set means the batch and the
		// request disagree, and http.DetectContentType would paper over
		// that with application/octet-stream.
		http.Error(w, "unsupported image format "+format, http.StatusBadRequest)
		return
	}

	blobs, err := h.aim.GetBlobs([]string{uri})
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	// Keyed by uri, never by position — see GetBlobs.
	data, ok := blobs[uri]
	if !ok || len(data) == 0 {
		// Aim knows the route but not this uri. A 404 renders as a
		// broken image, which is honest; an empty 200 renders as a
		// blank one, which is not.
		http.Error(w, "blob not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	_, _ = w.Write(data)
}

// imageContentTypes are the formats aim.Image produces. Deliberately a
// closed set: an unknown format is a disagreement between the batch and
// the request, not something to guess at.
var imageContentTypes = map[string]string{
	"png":  "image/png",
	"jpg":  "image/jpeg",
	"jpeg": "image/jpeg",
	"gif":  "image/gif",
	"webp": "image/webp",
}
