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
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
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

// --- Batch payloads, read from astrolabe's export ---

// SampleExportFormatVersion is the manifest layout this build understands.
// astrolabe writes it into every manifest; the two ship on separate cadences,
// so a mismatch is refused rather than parsed on a best effort. See
// docs/samples-export.md in astrolabe.
const SampleExportFormatVersion = 1

// ManifestName is the file astrolabe writes per exported batch.
const ManifestName = "manifest.json"

// SamplePair is one step of a batch: what went in, what came out.
//
// Every field but Step is a pointer because absent and empty are different
// facts. A set logged without inputs (unconditional generation) has no
// InputText; a model that returned the empty string has one, and it is "".
// Collapsing those loses the only signal that says which happened.
//
// kind on the batch describes the OUTPUT. A prompt-to-image batch is
// kind "image" with InputText set — read the per-pair fields, never infer
// the input's type from the batch's kind.
type SamplePair struct {
	Step       int     `json:"step"`
	InputText  *string `json:"input_text,omitempty"`
	InputFile  *string `json:"input_file,omitempty"`
	OutputText *string `json:"output_text,omitempty"`
	OutputFile *string `json:"output_file,omitempty"`
}

// SampleBatch is one exported directory: one log_samples call.
type SampleBatch struct {
	FormatVersion int          `json:"format_version"`
	AimRunHash    string       `json:"aim_run_hash"`
	SampleSet     string       `json:"sample_set"`
	ModelRunHash  string       `json:"model_run_hash"`
	Kind          string       `json:"kind"`
	ExportedAt    string       `json:"exported_at"`
	Pairs         []SamplePair `json:"pairs"`
}

// errBatchNotExported separates "astrolabe has not exported this yet" from
// "the export is broken". Discovery lists what exists in Aim; the export can
// lag it, and a tab that renders an empty grid for a batch that simply has not
// been written yet reads as data loss.
var errBatchNotExported = errors.New("batch not exported")

// sampleHashPattern guards the run hash before it becomes a filesystem path.
// The hash arrives in a URL.
var sampleHashPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// readManifest loads one exported batch off disk.
//
// Returns errBatchNotExported when the directory or manifest is absent; any
// other error means the export is present and unusable, which is a different
// answer for the caller to give.
func readManifest(samplesDir, aimRunHash string) (*SampleBatch, error) {
	if !sampleHashPattern.MatchString(aimRunHash) {
		return nil, fmt.Errorf("invalid run hash %q", aimRunHash)
	}
	path := filepath.Join(samplesDir, aimRunHash, ManifestName)
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, errBatchNotExported
	}
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	var batch SampleBatch
	if err := json.Unmarshal(data, &batch); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	if batch.FormatVersion != SampleExportFormatVersion {
		return nil, fmt.Errorf(
			"%s is export format version %d; this dashboard reads version %d — upgrade the dashboard or re-run `astrolabe samples export --all --force`",
			path, batch.FormatVersion, SampleExportFormatVersion)
	}
	// The client maps over this, so [] and not null.
	if batch.Pairs == nil {
		batch.Pairs = []SamplePair{}
	}
	return &batch, nil
}

// HandleSampleBatch returns one exported batch.
//
// GET /api/samples/{aim_run_hash}
// → { format_version, aim_run_hash, sample_set, model_run_hash, kind, exported_at, pairs }
//
// Image pairs carry filenames, not bytes; EXAMPLES-1.03 serves the files.
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
	batch, err := readManifest(h.samplesDir, aimRunHash)
	if errors.Is(err, errBatchNotExported) {
		http.Error(w, "batch not exported yet", http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	writeJSON(w, batch)
}
