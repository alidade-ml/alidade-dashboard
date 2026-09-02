package api

// Sample discovery for the Examples tab.
//
// ``alidade_callbacks.log_samples`` writes one Aim run per batch of
// qualitative outputs, tagged with alidade.kind="sample", the
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
	neturl "net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Contract literals. These are copied from alidade's contract.py and
// checked by nothing today. A rename engine-side makes this endpoint
// return an empty list, which is indistinguishable from "nobody logged
// any samples".
const (
	// SampleKind is alidade.kind on a run written by log_samples.
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
// Discovery is by tag: alidade.kind == "sample" AND
// alidade.model_run_hash == <hash>. Multiple batches sharing a
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
// Known gap, accepted: a batch logged with no submit in scope lands
// under "sample/<set>" and is not found here. That belongs to the
// producer, and is not a reason to restore the scan.
func (h *Handler) HandleRunSamples(w http.ResponseWriter, r *http.Request) {
	modelRunHash := extractPathParam(r.URL.Path, "/api/runs/", "/samples")
	if modelRunHash == "" {
		http.Error(w, "missing run hash", http.StatusBadRequest)
		return
	}

	// Confirm the run exists before answering about it. The query below
	// cannot tell "this hash has no sample batches" from "this hash is
	// not a run at all", and returning [] for a typo is the ambiguity
	// this removes. One extra request, and the same one the hash-shaped
	// include path already makes.
	if _, err := h.aim.GetRunInfo(modelRunHash); err != nil {
		if errors.Is(err, ErrRunNotFound) {
			http.Error(w, "run not found", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}

	// One query, cross-experiment on purpose. Discovery was once
	// narrowed to the model run's own experiment, because a project-wide
	// walk was the only alternative — which made a batch logged with no
	// submit in scope invisible. Asking by tag has no reason to care
	// which experiment a batch is in, so that gap closes.
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
		tags := AlidadeTagsFromParams(run.Params)
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
	InputURL   *string `json:"input_url,omitempty"`
	OutputText *string `json:"output_text,omitempty"`
	OutputURL  *string `json:"output_url,omitempty"`
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
// Text only. Images are not served yet; this refuses them rather than
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
		Pairs:      joinByStep(aimRunHash, sampleSet, inputs, outputs),
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
func joinByStep(aimRunHash, sampleSet string, inputs, outputs *ObjectSequence) []SamplePair {
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
				url := blobURL(aimRunHash, sampleSet, RoleInput, step)
				p.InputURL = &url
			} else {
				text := rec.Text
				p.InputText = &text
			}
		}
		if rec, ok := outputs.At(step); ok {
			if rec.BlobURI != "" {
				url := blobURL(aimRunHash, sampleSet, RoleOutput, step)
				p.OutputURL = &url
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

// Roles a sample sequence can hold. The sequence name is
// sample/<set>/<role>, so these are path segments, not free text.
const (
	RoleInput  = "input"
	RoleOutput = "output"
)

// blobURL is the address of one image, and it is the same address every time.
//
// Aim's own blob token cannot be used here. It is Fernet-encrypted, and Fernet
// embeds a random IV, so the same image yields a different token on every
// batch response — the URL changes, the browser cache key changes, and nothing
// is ever a hit. This tuple names the image by what it IS, so a re-render, a
// refetch, a refresh and a second viewer all ask for the same URL.
func blobURL(aimRunHash, sampleSet, role string, step int64) string {
	q := neturl.Values{
		"run":  {aimRunHash},
		"set":  {sampleSet},
		"role": {role},
		"step": {strconv.FormatInt(step, 10)},
	}
	return "/api/samples/blob?" + q.Encode()
}

// blobRef identifies one image by content rather than by token.
type blobRef struct {
	run, set, role string
}

// blobURIEntry is one sequence's step-to-token map.
//
// Tokens are cached rather than re-resolved because resolving costs a sequence
// fetch — measured at 44ms against 1.6ms for the blob itself, so a 24-image
// grid is 1102ms unresolved against 83ms cached. They are safe to hold: Aim
// decrypts them with no TTL, so a token stays valid for the life of the repo's
// encryption key.
type blobURIEntry struct {
	uris    map[int64]string
	formats map[int64]string
	fetched time.Time
}

// BlobURITTL bounds how long a step-to-token map is reused.
//
// Not about token expiry, which does not happen. A sampling run can gain steps
// while someone is looking at it, and a cached map would not know. Short enough
// that new steps appear promptly, long enough that one grid resolves once.
const BlobURITTL = 60 * time.Second

type blobURICache struct {
	mu      sync.Mutex
	ttl     time.Duration
	entries map[blobRef]blobURIEntry
}

// resolve returns the current Aim token and format for one step.
//
// A miss on a step within a live entry re-resolves rather than 404ing: the
// alternative is a broken image for up to the TTL on a batch that just grew.
func (h *Handler) resolve(ref blobRef, step int64) (uri, format string, err error) {
	h.blobURIs.mu.Lock()
	entry, ok := h.blobURIs.entries[ref]
	ttl := h.blobURIs.ttl
	h.blobURIs.mu.Unlock()

	fresh := ok && ttl > 0 && time.Since(entry.fetched) < ttl
	if fresh {
		if u, hit := entry.uris[step]; hit {
			return u, entry.formats[step], nil
		}
	}

	seq, err := h.sequenceEitherWay(ref.run, ref.set, ref.role)
	if err != nil {
		return "", "", err
	}
	next := blobURIEntry{
		uris:    make(map[int64]string, len(seq.Records)),
		formats: make(map[int64]string, len(seq.Records)),
		fetched: time.Now(),
	}
	for i, rec := range seq.Records {
		if rec.BlobURI == "" || i >= len(seq.Steps) {
			continue
		}
		next.uris[seq.Steps[i]] = rec.BlobURI
		next.formats[seq.Steps[i]] = rec.Format
	}

	h.blobURIs.mu.Lock()
	if h.blobURIs.entries == nil {
		h.blobURIs.entries = map[blobRef]blobURIEntry{}
	}
	h.blobURIs.entries[ref] = next
	h.blobURIs.mu.Unlock()

	u, ok := next.uris[step]
	if !ok {
		return "", "", errNoSuchStep
	}
	return u, next.formats[step], nil
}

var errNoSuchStep = errors.New("no image at that step")

// HandleSampleBlob streams the bytes for one image.
//
// GET /api/samples/blob?run=<hash>&set=<set>&role=input|output&step=<n>
//
// The hub resolves this tuple to Aim's current blob token itself. It still
// never mints or decodes a token — it just stops using one as a cache key,
// which is what makes the immutable header below mean anything.
func (h *Handler) HandleSampleBlob(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	ref := blobRef{run: q.Get("run"), set: q.Get("set"), role: q.Get("role")}
	if ref.run == "" || ref.set == "" {
		http.Error(w, "missing run or set", http.StatusBadRequest)
		return
	}
	if !sampleHashPattern.MatchString(ref.run) {
		http.Error(w, "invalid run hash", http.StatusBadRequest)
		return
	}
	if strings.Contains(ref.set, "/") {
		http.Error(w, "invalid set", http.StatusBadRequest)
		return
	}
	if ref.role != RoleInput && ref.role != RoleOutput {
		// Closed set: the role is a path segment in the sequence name, and
		// anything else addresses a sequence that does not exist.
		http.Error(w, "role must be input or output", http.StatusBadRequest)
		return
	}
	step, err := strconv.ParseInt(q.Get("step"), 10, 64)
	if err != nil {
		http.Error(w, "step must be an integer", http.StatusBadRequest)
		return
	}

	uri, format, err := h.resolve(ref, step)
	if errors.Is(err, errNoSuchStep) {
		http.Error(w, "no image at that step", http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}

	if format == "" {
		format = "png"
	}
	contentType, ok := imageContentTypes[format]
	if !ok {
		// Refused rather than sniffed. The record states the format, so a
		// value outside this set means Aim wrote something this package has
		// never seen, and DetectContentType would paper over it.
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
