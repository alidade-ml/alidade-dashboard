package api

import (
	"encoding/json"
	"errors"
	"net/http"
	neturl "net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Handler holds route handlers and the Aim client.
type Handler struct {
	aim    *AimClient
	state  *StateReader
	colors []string

	runCounts runCountCache
	blobURIs  blobURICache
}

// NewHandler creates a Handler with the given Aim client, state reader, and color palette.
func NewHandler(aim *AimClient, state *StateReader, colors []string) *Handler {
	h := &Handler{aim: aim, state: state, colors: colors}
	h.runCounts.ttl = DefaultRunCountTTL
	h.blobURIs.ttl = BlobURITTL
	return h
}

// DefaultRunCountTTL is how long a run-count map is reused.
//
// The experiments list has two freshness classes. State and outcome change
// constantly and sit behind the 2s response cache; run counts change only when
// a run is created, and cost a repo-wide scan in Aim to recompute — most of it
// spent scanning rather than matching, so no narrower query helps.
//
// The visible cost is that a new run takes up to 30s to appear in the badge.
const DefaultRunCountTTL = 30 * time.Second

// runCountCache memoises one map, not a keyed set of responses. Deliberately
// not the TTLCache middleware, which keys on request URI and caches a whole
// response; this is an inner value living behind a shorter-lived one.
type runCountCache struct {
	mu      sync.Mutex
	ttl     time.Duration
	counts  map[string]int
	fetched time.Time
}

// SetRunCountTTL overrides the reuse window. Zero disables reuse, so a test
// asserting twice does not measure its own first fetch.
func (h *Handler) SetRunCountTTL(d time.Duration) {
	h.runCounts.mu.Lock()
	defer h.runCounts.mu.Unlock()
	h.runCounts.ttl = d
	h.runCounts.counts = nil
}

// experimentRunCounts returns the cached count map, refreshing it when stale.
//
// Errors propagate rather than degrading to an empty map: every row would then
// render as having produced nothing, which a reader cannot tell apart from a
// machine nobody has used.
func (h *Handler) experimentRunCounts() (map[string]int, error) {
	h.runCounts.mu.Lock()
	defer h.runCounts.mu.Unlock()
	if h.runCounts.counts != nil && h.runCounts.ttl > 0 &&
		time.Since(h.runCounts.fetched) < h.runCounts.ttl {
		return h.runCounts.counts, nil
	}
	counts, err := h.aim.ExperimentRunCounts()
	if err != nil {
		return nil, err
	}
	h.runCounts.counts = counts
	h.runCounts.fetched = time.Now()
	return counts, nil
}

// --- JSON response types ---

type ExperimentSummary struct {
	Name      string `json:"name"`
	State     string `json:"state"`
	GPUType   string `json:"gpu_type"`
	StartedAt string `json:"started_at"`
	Duration  string `json:"duration"`
	Outcome   string `json:"outcome"`
	RunCount  int    `json:"run_count"`
	// v1.2.0 fields the dashboard frontend reads. Empty string / zero
	// values are tolerated by the dashboard's fallbacks for legacy
	// state files that pre-date these.
	Repo         string            `json:"repo,omitempty"`
	LinearDocURL string            `json:"linear_doc_url,omitempty"`
	VersionCount int               `json:"version_count,omitempty"`
	StateHistory []StateTransition `json:"state_history,omitempty"`
	// v1.4.0 — surfaced for the home-page filter shelf. Read from
	// the experiment's state file (ExperimentRecord.submitted_by). The
	// frontend renders this verbatim in the Submitter dropdown; legacy
	// records (pre-v1.2.1) have it empty and bucket under "unknown".
	SubmittedBy string `json:"submitted_by,omitempty"`
}

// RunSummary is the item shape of /api/runs.
//
// A published contract: docs/alternative-frontends.md documents this endpoint
// and this shape for third parties building their own UI. Fields are additive
// only — removing or renaming one breaks a consumer this repo cannot see.
type RunSummary struct {
	Hash           string  `json:"hash"`
	Name           string  `json:"name"`
	ExperimentName string  `json:"experiment"`
	CreationTime   float64 `json:"creation_time"`
	EndTime        float64 `json:"end_time"`
	Active         bool    `json:"active"`
	Duration       string  `json:"duration"`
	// v1.2.0 — which submit produced this run. Empty for legacy runs
	// that pre-date the astrolabe.version tag; the dashboard falls
	// back to "v1" in that case.
	Version  string `json:"version,omitempty"`
	SubmitID string `json:"submit_id,omitempty"`
	// v1.4.0 — submitter identity from the astrolabe.user tag. Used
	// by the dashboard's stats table to show "by alice" when comparing
	// across users; empty for legacy runs.
	SubmittedBy string `json:"submitted_by,omitempty"`
	// v1.9.0 — the astrolabe.kind tag, verbatim and possibly empty.
	//
	// Added because this endpoint filters, and until now a consumer had
	// no way to know that or to apply its own rule. Empty means the run
	// predates the tag, which is a legacy training run.
	Kind string `json:"kind,omitempty"`
}

type RunDetail struct {
	Hash string `json:"hash"`
	Name string `json:"name"`
	// Which astrolabe experiment this run actually lives in. Usually the
	// experiment being requested, but a model evaluated by this
	// experiment can live in another one — the row has to say so or the
	// user cannot tell where the model came from.
	ExperimentName string `json:"experiment"`
	// astrolabe.kind, passed through so the client can tell a training
	// run from an imported model. Empty means an untagged legacy run,
	// which is treated as training.
	Kind string `json:"kind,omitempty"`
	// True when this row is here because the experiment evaluated the
	// model, not because the experiment produced it.
	Evaluated    bool          `json:"evaluated,omitempty"`
	CreationTime float64       `json:"creation_time"`
	EndTime      float64       `json:"end_time"`
	Active       bool          `json:"active"`
	Duration     string        `json:"duration"`
	Metrics      []MetricEntry `json:"metrics"`
	FinalLoss    *float64      `json:"final_loss"`
	// v1.2.0 — which submit produced this run. Empty for legacy runs
	// that pre-date the astrolabe.version tag; the dashboard falls back
	// to "v1" in that case.
	Version  string `json:"version,omitempty"`
	SubmitID string `json:"submit_id,omitempty"`
	// v1.4.0 — submitter identity from the astrolabe.user tag. Empty
	// for legacy runs.
	SubmittedBy string `json:"submitted_by,omitempty"`
}

type MetricResponse struct {
	Name   string    `json:"name"`
	Steps  []int     `json:"steps"`
	Values []float64 `json:"values"`
	// WallTimes — elapsed seconds since run start at each step, index-aligned
	// with Steps. Populated when the run's wall_time metric is available (the
	// AstrolabeLogger callback writes it). Omitted entirely when missing —
	// frontend falls back to step number for the wall-time x-axis.
	//
	// A step the wall_time series does not cover is null, never 0: zero is a
	// legitimate elapsed reading at the first step, so a sentinel is
	// indistinguishable from a measurement and gets plotted at the origin.
	WallTimes []*float64 `json:"wall_times,omitempty"`
}

type MetricNameResponse struct {
	Metrics []MetricEntry `json:"metrics"`
}

type MetricEntry struct {
	Name    string                 `json:"name"`
	Context map[string]interface{} `json:"context"`
}

type IncludeEntry struct {
	Name string `json:"name"`
	// Type tells the frontend how the include resolved so it can render
	// distinct affordances (a hash chip differs from an experiment chip).
	// Values:
	//   "experiment"  — matched an Aim experiment name (multi-run)
	//   "hash"        — matched a single Aim run hash
	//   "run-name"    — matched a run.name across the corpus; resolves
	//                   to the SINGLE most recent matching run by
	//                   CreationTime (not every match — researchers
	//                   wanting wider scope can include the
	//                   experiment by name or paste specific hashes)
	//   "unknown"     — no match; frontend renders as struck-out
	Type string   `json:"type"`
	Runs []string `json:"runs"` // Aim run hashes
}

// --- Route handlers ---

// HandleExperiments returns one row per experiment_name, enriched with
// Aim run counts.
//
// GET /api/experiments
//
// Groups SQLite submits by “experiment_name“ and emits the newest
// submit per group as the representative. Before the SQLite cutover,
// state files were one-per-experiment-name (last-write-wins), so
// iterating them gave one row per experiment "for free"; after the
// cutover each version is its own submit row and we have to group
// explicitly — otherwise an experiment with five versions appears as
// five duplicate rows on the home page.
//
// “version_count“ is the number of distinct “version“ values in
// the SQLite group, not the count of “astrolabe.version“ tags on
// Aim runs. Backfilled metadata-only submits (no composer training
// run in Aim) would otherwise be undercounted.
func (h *Handler) HandleExperiments(w http.ResponseWriter, r *http.Request) {
	runCounts, err := h.experimentRunCounts()
	if err != nil {
		// Not a degraded 200: a page of zero-run experiments sends the
		// reader looking for their data rather than at the dashboard.
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}

	var experiments []ExperimentSummary

	if h.state != nil {
		// ListSummaries, not ListAll: this endpoint renders neither
		// includes nor git tags, and ListAll fetches both per submit.
		states, err := h.state.ListSummaries()
		if err == nil {
			// Track distinct versions per experiment so the home-page
			// "vN of M" badge counts ALL submits, not just those with
			// composer training runs in Aim.
			versionsByName := map[string]map[string]struct{}{}
			for _, s := range states {
				vs, ok := versionsByName[s.Name]
				if !ok {
					vs = map[string]struct{}{}
					versionsByName[s.Name] = vs
				}
				v := s.Version
				if v == "" {
					// Pre-v1.2.0 records lack the version field. Bucket
					// them as "v1" so the count stays non-zero and
					// matches the dashboard's legacy fallback.
					v = "v1"
				}
				vs[v] = struct{}{}
			}

			// ListAll returns submits newest-first by started_at, so
			// the first time we see each experiment name in iteration
			// IS the representative. No re-sort needed.
			seenName := map[string]struct{}{}
			for _, s := range states {
				if _, dup := seenName[s.Name]; dup {
					continue
				}
				seenName[s.Name] = struct{}{}
				experiments = append(experiments, ExperimentSummary{
					Name:         s.Name,
					State:        s.State,
					GPUType:      s.GPUType,
					StartedAt:    s.StartedAt,
					Duration:     stateDuration(s.StartedAt, s.FinishedAt),
					Outcome:      s.Outcome,
					RunCount:     runCounts[s.Name],
					Repo:         s.Repo,
					LinearDocURL: s.LinearDocURL,
					VersionCount: len(versionsByName[s.Name]),
					StateHistory: s.StateHistory,
					SubmittedBy:  s.SubmittedBy,
				})
			}
		}
	}

	// Sort by start time, newest first
	sort.Slice(experiments, func(i, j int) bool {
		return experiments[i].StartedAt > experiments[j].StartedAt
	})

	writeJSON(w, experiments)
}

// ExperimentDetail is the metadata one experiment's page renders in its header.
//
// Deliberately not ExperimentSummary: it carries no run count, so this endpoint
// answers entirely from the state DB. The header — state, history, timing,
// submitter — is SQLite data, and making it depend on an Aim search would mean
// losing all of it whenever Aim is down, to supply a number this page never
// displays.
type ExperimentDetail struct {
	Name         string            `json:"name"`
	State        string            `json:"state"`
	GPUType      string            `json:"gpu_type"`
	StartedAt    string            `json:"started_at"`
	Duration     string            `json:"duration"`
	Outcome      string            `json:"outcome"`
	Repo         string            `json:"repo,omitempty"`
	LinearDocURL string            `json:"linear_doc_url,omitempty"`
	VersionCount int               `json:"version_count"`
	StateHistory []StateTransition `json:"state_history,omitempty"`
	SubmittedBy  string            `json:"submitted_by,omitempty"`
}

// HandleExperimentDetail returns one experiment's header metadata.
//
// GET /api/experiments/{name}
//
// The page that renders this used to poll /api/experiments and pick its row out
// of every experiment on the machine — 202 KB per poll at 300 experiments, for
// one header.
func (h *Handler) HandleExperimentDetail(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.URL.Path, "/api/experiments/")
	if decoded, err := neturl.PathUnescape(name); err == nil {
		name = decoded
	}
	if name == "" {
		http.Error(w, "missing experiment name", http.StatusBadRequest)
		return
	}
	if h.state == nil {
		http.Error(w, "no state DB", http.StatusServiceUnavailable)
		return
	}

	state, err := h.state.GetState(name)
	if err != nil || state == nil {
		// 404 rather than an empty 200: a blank header is indistinguishable
		// from a slow load, and sends the reader to the wrong question.
		http.NotFound(w, r)
		return
	}
	versions, err := h.state.CountVersions(name)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	writeJSON(w, ExperimentDetail{
		Name:         state.Name,
		State:        state.State,
		GPUType:      state.GPUType,
		StartedAt:    state.StartedAt,
		Duration:     stateDuration(state.StartedAt, state.FinishedAt),
		Outcome:      state.Outcome,
		Repo:         state.Repo,
		LinearDocURL: state.LinearDocURL,
		VersionCount: versions,
		StateHistory: state.StateHistory,
		SubmittedBy:  state.SubmittedBy,
	})
}

// HandleExperimentRuns returns the runs an experiment's page should show.
// GET /api/experiments/{name}/runs
//
// That is a union of two sets, not one:
//
//   - the models the experiment **produced** — its own training runs
//   - the models the experiment **evaluated** — resolved through the
//     model_run_hash on eval runs filed here, which may point at a model
//     living in a different experiment
//
// The two genuinely differ. An eval-only submit produces no training run
// at all, and a submit can evaluate its own model alongside an imported
// one. Returning either set alone drops rows the page exists to show, so
// rows carry Evaluated to say which way they arrived.
//
// Bookkeeping runs are excluded: "metadata" (engine cost runs) and
// "eval" (the eval runs themselves, which surface on the Eval tab via
// HandleRunEvals). Everything else is returned with its Kind so the
// client decides what belongs in a training chart — an unrecognized kind
// must not silently become a training run.
func (h *Handler) HandleExperimentRuns(w http.ResponseWriter, r *http.Request) {
	name := extractPathParam(r.URL.Path, "/api/experiments/", "/runs")
	if name == "" {
		http.Error(w, "missing experiment name", http.StatusBadRequest)
		return
	}

	// Find the specific experiment by name (short-circuit instead of building full index)
	experiments, err := h.aim.ListExperiments()
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	var expID string
	for _, exp := range experiments {
		if exp.Name == name && !exp.Archived {
			expID = exp.ID
			break
		}
	}
	if expID == "" {
		writeJSON(w, []RunDetail{})
		return
	}

	expRuns, err := h.aim.ListExperimentRuns(expID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}

	// Fetch run info in parallel — previously this was serial per run
	type result struct {
		index int
		// Set when the run belongs on the page.
		detail *RunDetail
		// Set when the run is an eval — the model it scores is a
		// candidate for the evaluated half of the union.
		evaluates string
	}
	results := make(chan result, len(expRuns.Runs))
	var wg sync.WaitGroup

	for i, ar := range expRuns.Runs {
		if ar.Archived {
			continue
		}
		wg.Add(1)
		go func(idx int, ar AimRun) {
			defer wg.Done()
			detail := h.buildRunDetail(ar, name)

			info, err := h.aim.GetRunInfo(ar.RunID)
			if err == nil {
				tags := AstrolabeTagsFromParams(info.Params)
				switch tags.Kind {
				case KindEval:
					// Not a row itself — the Eval tab renders it. Its
					// model is a row, and may live elsewhere.
					results <- result{index: idx, evaluates: tags.ModelRunHash}
					return
				case SampleKind:
					// Not a row either — the Examples tab renders it, and
					// its model is the row.
					results <- result{index: idx}
					return
				case KindMetadata:
					// Engine-written cost run; carries no metrics.
					results <- result{index: idx}
					return
				}
				h.enrichRunDetail(&detail, info, ar.RunID)
			}
			results <- result{index: idx, detail: &detail}
		}(i, ar)
	}

	go func() {
		wg.Wait()
		close(results)
	}()

	detailsByIndex := make(map[int]RunDetail)
	evaluated := make(map[string]bool)
	for r := range results {
		if r.detail != nil {
			detailsByIndex[r.index] = *r.detail
		}
		if r.evaluates != "" {
			evaluated[r.evaluates] = true
		}
	}
	details := make([]RunDetail, 0, len(detailsByIndex))
	produced := make(map[string]bool, len(detailsByIndex))
	for i := 0; i < len(expRuns.Runs); i++ {
		if d, ok := detailsByIndex[i]; ok {
			details = append(details, d)
			produced[d.Hash] = true
		}
	}

	// The evaluated half. Models the experiment produced itself are
	// already present — evaluating your own model must not double it.
	details = append(details, h.evaluatedModelDetails(evaluated, produced)...)

	writeJSON(w, details)
}

// buildRunDetail fills the fields available from a run listing, before
// the per-run info fetch.
func (h *Handler) buildRunDetail(ar AimRun, experimentName string) RunDetail {
	return RunDetail{
		Hash:           ar.RunID,
		Name:           runDisplayName(ar, experimentName),
		ExperimentName: experimentName,
		CreationTime:   ar.CreationTime,
		EndTime:        ar.EndTime,
		Active:         ar.EndTime == 0,
		Duration:       formatDuration(ar.CreationTime, ar.EndTime),
	}
}

// enrichRunDetail adds everything that needs the run's info payload.
func (h *Handler) enrichRunDetail(detail *RunDetail, info *RunInfo, runHash string) {
	for _, m := range info.Traces.Metric {
		if strings.HasPrefix(m.Name, "__system__") {
			continue
		}
		detail.Metrics = append(detail.Metrics, MetricEntry{
			Name:    m.Name,
			Context: m.Context,
		})
	}
	// Aim's info.traces.metric[].last_value is unreliable — observed
	// showing the initial/default value (0.1) even when the actual
	// series has progressed. Fetch the real series and take values[-1]
	// for the displayed final loss.
	if loss, err := h.aim.GetMetric(runHash, "train/loss", nil); err == nil && len(loss.Values) > 0 {
		val := loss.Values[len(loss.Values)-1]
		detail.FinalLoss = &val
	}
	// Extract all astrolabe.* tags. Empty strings are fine — the
	// frontend falls back to v1 / "unknown" for legacy runs that
	// pre-date the tagging.
	tags := AstrolabeTagsFromParams(info.Params)
	detail.Version = tags.Version
	detail.SubmitID = tags.SubmitID
	detail.SubmittedBy = tags.SubmittedBy
	detail.Kind = tags.Kind
}

// evaluatedModelDetails resolves models this experiment evaluated but did
// not produce, in stable hash order so the response does not reshuffle
// between identical requests.
//
// Each model costs one GetRunInfo. A model whose run has been deleted
// from Aim is skipped rather than rendered as an empty row — the eval
// results still show on the Eval tab, keyed by hash.
func (h *Handler) evaluatedModelDetails(evaluated, produced map[string]bool) []RunDetail {
	missing := make([]string, 0, len(evaluated))
	for hash := range evaluated {
		if !produced[hash] {
			missing = append(missing, hash)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	sort.Strings(missing)

	out := make([]RunDetail, len(missing))
	ok := make([]bool, len(missing))
	var wg sync.WaitGroup
	for i, hash := range missing {
		wg.Add(1)
		go func(i int, hash string) {
			defer wg.Done()
			info, err := h.aim.GetRunInfo(hash)
			if err != nil {
				return
			}
			ar := AimRun{
				RunID:        hash,
				Name:         info.Props.Name,
				CreationTime: info.Props.CreationTime,
				EndTime:      info.Props.EndTime,
			}
			ownExperiment := info.Props.Experiment.Name
			detail := RunDetail{
				Hash:           hash,
				Name:           runLabel(ar, ownExperiment),
				ExperimentName: ownExperiment,
				CreationTime:   ar.CreationTime,
				EndTime:        ar.EndTime,
				Active:         ar.EndTime == 0,
				Duration:       formatDuration(ar.CreationTime, ar.EndTime),
				Evaluated:      true,
			}
			h.enrichRunDetail(&detail, info, hash)
			out[i], ok[i] = detail, true
		}(i, hash)
	}
	wg.Wait()

	details := make([]RunDetail, 0, len(missing))
	for i := range out {
		if ok[i] {
			details = append(details, out[i])
		}
	}
	return details
}

// HandleExperimentIncludes returns the --include entries for an experiment,
// resolved to Aim run hashes.
//
// Resolution order (first match wins):
//
//  1. Hash       — input matches /^[a-f0-9]{16,}$/. Treated as an Aim run
//     hash and looked up directly. Single-run include.
//  2. Experiment — input exact-matches an Aim experiment name. Multi-run
//     include (every run of that experiment).
//  3. Run name   — input exact-matches an Aim run.name across all
//     experiments. Pulls every matching run; type becomes
//     "run-name-multi" when matches span >1 experiment so
//     the frontend can flag the wider scope.
//  4. Unknown    — no match. Returned with type="unknown" and an empty
//     Runs slice so the frontend can render a struck-out
//     chip rather than silently dropping the include.
//
// GET /api/experiments/{name}/includes[?version=vN]
//
// The ?version query parameter scopes the returned includes to that
// specific version's submit. Without it (or with version=latest), the
// endpoint returns the most recent submit's includes. This preserves
// backward compatibility for callers that don't pass version, while
// letting the dashboard render version-accurate include lists when
// the user navigates between versions.
func (h *Handler) HandleExperimentIncludes(w http.ResponseWriter, r *http.Request) {
	name := extractPathParam(r.URL.Path, "/api/experiments/", "/includes")
	if name == "" {
		http.Error(w, "missing experiment name", http.StatusBadRequest)
		return
	}

	if h.state == nil {
		writeJSON(w, map[string]interface{}{"includes": []IncludeEntry{}})
		return
	}

	version := r.URL.Query().Get("version")
	includeNames, err := h.state.GetIncludes(name, version)
	if err != nil || len(includeNames) == 0 {
		writeJSON(w, map[string]interface{}{"includes": []IncludeEntry{}})
		return
	}

	// One query per include, rather than an index of the whole project
	// built once and thrown away.
	resolved := make([]IncludeEntry, 0, len(includeNames))
	for _, incName := range includeNames {
		resolved = append(resolved, h.resolveIncludeByQuery(incName))
	}

	writeJSON(w, map[string]interface{}{"includes": resolved})
}

// hashesOfNewestVersion keeps only the runs belonging to an experiment's
// most recent version, dropping archived ones.
//
// Version labels are "v1", "v2", … so they are compared numerically: "v10"
// sorts after "v9", which a string comparison gets backwards precisely
// when an experiment has been resubmitted enough for this to matter.
//
// Runs with no version tag are kept only when NO run carries one. A repo
// predating version tagging still resolves to something rather than
// nothing; a repo that has them should not have an untagged straggler
// outrank v3.
func hashesOfNewestVersion(runs []SearchedRun) []string {
	live := make([]SearchedRun, 0, len(runs))
	for _, r := range runs {
		if !r.Archived {
			live = append(live, r)
		}
	}

	newest := -1
	for _, r := range live {
		if n, ok := versionOrdinal(AstrolabeTagsFromParams(r.Params).Version); ok && n > newest {
			newest = n
		}
	}

	hashes := make([]string, 0, len(live))
	for _, r := range live {
		n, ok := versionOrdinal(AstrolabeTagsFromParams(r.Params).Version)
		if newest < 0 || (ok && n == newest) {
			hashes = append(hashes, r.Hash)
		}
	}
	return hashes
}

// versionOrdinal turns "v3" into 3. Anything else is not a version label.
func versionOrdinal(label string) (int, bool) {
	if len(label) < 2 || label[0] != 'v' {
		return 0, false
	}
	n, err := strconv.Atoi(label[1:])
	if err != nil {
		return 0, false
	}
	return n, true
}

// resolveIncludeByQuery applies the four-shape resolution order using
// Aim's search endpoint instead of an index of the whole project.
//
// The shapes, and what each costs now:
//
//	hash        one query, exact
//	experiment  one query, exact
//	run name    one query, then a sort here — Aim can filter on
//	            run.name but cannot do "most recent matching", so the
//	            newest-only rule stays client-side over a handful of
//	            rows rather than over every run in the project
//	unknown     no query at all
//
// Errors resolve to unknown rather than propagating. An include that
// cannot be resolved already has a rendering — the struck-out chip — and
// failing the whole endpoint because one spec is unresolvable would hide
// the others.
func (h *Handler) resolveIncludeByQuery(incName string) IncludeEntry {
	entry := IncludeEntry{Name: incName, Type: "unknown", Runs: []string{}}

	// 1. Hash — strict hex check, >=16 chars to avoid colliding with
	// short hex-shaped experiment names.
	if isHashLike(incName) {
		info, err := h.aim.GetRunInfo(incName)
		if err == nil {
			entry.Type = "hash"
			entry.Runs = []string{incName}
			// Surface the run's meaningful name so the chip reads
			// "bert-tiny" rather than a 24-char hash.
			// Aim's placeholder ("Run: <hash>") carries no information,
			// so it is left as the hash rather than shown as a name.
			if n := info.Props.Name; n != "" && !strings.HasPrefix(n, "Run: ") {
				entry.Name = n
			}
			return entry
		}
		// Hash-shaped but unknown to Aim — fall through, as before.
	}

	// 2. Aim experiment name — exact match, newest version only.
	//
	// intended-behavior.md section 2: an include resolves "against the most
	// recent submit of that experiment". Returning every version instead
	// meant naming one experiment pulled in every run it had ever produced
	// — a ten-version experiment brought ten training runs plus all of
	// their evals and samples. Older versions are reached by submit hash,
	// which resolves exactly one run and always has.
	if runs, err := h.aim.SearchRuns(QueryByExperiment(incName)); err == nil && len(runs) > 0 {
		entry.Type = "experiment"
		entry.Runs = hashesOfNewestVersion(runs)
		if len(entry.Runs) > 0 {
			return entry
		}
	}

	// 3. Run name — narrowed to the SINGLE most recent match across all
	// experiments. The same run.name commonly appears in many
	// experiments (e.g. "astrolabe_test" is the inner training name for
	// several configs); pulling every match flooded the comparison set.
	if runs, err := h.aim.SearchRuns(QueryByRunName(incName)); err == nil && len(runs) > 0 {
		latest := runs[0]
		for _, r := range runs[1:] {
			if r.CreationTime > latest.CreationTime {
				latest = r
			}
		}
		entry.Type = "run-name"
		entry.Runs = []string{latest.Hash}
		return entry
	}

	// 4. No match. Empty Runs, type="unknown" — the frontend renders a
	// struck-out chip so the operator sees the dropped include.
	return entry
}

func isHashLike(s string) bool {
	if len(s) < 16 {
		return false
	}
	for _, c := range s {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return false
		}
	}
	return true
}

// HandleRuns returns a flat list of runs across all experiments, newest first.
//
// GET /api/runs
//
// Nothing in this repo's frontend calls it. It is kept, and kept working,
// because docs/alternative-frontends.md publishes it as part of the API a
// third-party UI can build on — a survey of one repo is not evidence that a
// documented endpoint has no users.
//
// One search rather than the per-run fan-out this used to do. The filter is
// unchanged from that implementation: eval and metadata runs are omitted,
// sample runs are not. That is a quirk rather than a design — see
// NonRowKinds — but it is the behaviour consumers have, and correcting it
// here would be a silent contract change in a ticket about speed. Runs now
// carry Kind so a consumer can apply its own rule.
func (h *Handler) HandleRuns(w http.ResponseWriter, r *http.Request) {
	found, err := h.aim.SearchRuns(QueryNotArchived())
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}

	runs := make([]RunSummary, 0, len(found))
	for _, sr := range found {
		tags := AstrolabeTagsFromParams(sr.Params)
		if tags.Kind == KindEval || tags.Kind == KindMetadata {
			continue
		}
		runs = append(runs, RunSummary{
			Hash:           sr.Hash,
			Name:           runDisplayName(AimRun{RunID: sr.Hash, Name: sr.Name}, sr.ExperimentName),
			ExperimentName: sr.ExperimentName,
			CreationTime:   sr.CreationTime,
			EndTime:        sr.EndTime,
			Active:         sr.EndTime == 0,
			Duration:       formatDuration(sr.CreationTime, sr.EndTime),
			Version:        tags.Version,
			SubmitID:       tags.SubmitID,
			SubmittedBy:    tags.SubmittedBy,
			Kind:           tags.Kind,
		})
	}
	sort.Slice(runs, func(i, j int) bool {
		return runs[i].CreationTime > runs[j].CreationTime
	})
	writeJSON(w, runs)
}

// HandleRunMetrics returns available metric names for a run.
// GET /api/runs/{hash}/metrics
func (h *Handler) HandleRunMetrics(w http.ResponseWriter, r *http.Request) {
	hash := extractPathParam(r.URL.Path, "/api/runs/", "/metrics")
	if hash == "" {
		http.Error(w, "missing run hash", http.StatusBadRequest)
		return
	}

	info, err := h.aim.GetRunInfo(hash)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}

	var metrics []MetricEntry
	for _, m := range info.Traces.Metric {
		if strings.HasPrefix(m.Name, "__system__") {
			continue
		}
		metrics = append(metrics, MetricEntry{
			Name:    m.Name,
			Context: m.Context,
		})
	}

	writeJSON(w, MetricNameResponse{Metrics: metrics})
}

// HandleMetricData returns step/value data for a specific metric.
// GET /api/runs/{hash}/metrics/{name}
func (h *Handler) HandleMetricData(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	prefix := "/api/runs/"
	rest := strings.TrimPrefix(path, prefix)
	parts := strings.SplitN(rest, "/metrics/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		http.Error(w, "invalid path: expected /api/runs/{hash}/metrics/{name}", http.StatusBadRequest)
		return
	}
	hash := parts[0]
	metricName := parts[1]

	data, err := h.aim.GetMetric(hash, metricName, nil)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}

	resp := MetricResponse{
		Name:   data.Name,
		Steps:  data.Iters,
		Values: data.Values,
	}

	// Try to attach wall_time per step. The AstrolabeLogger writes
	// `wall_time` as its own metric (elapsed seconds since run start).
	//
	// Pairing is by exact step, and that is only correct because the
	// producer writes wall_time at the steps it writes other metrics at.
	//
	// Aim returns a RESERVOIR, not a stride: a metric's data is a
	// SequenceV2Data, and 200 points back means the reservoir's contents.
	// Measured — deterministic, independent of the values, and a function
	// of the item count alone: two series of equal length come back on an
	// identical grid, so every point pairs.
	//
	// One extra item does not extend the grid, it EVICTS one. Writing 301
	// wall_time samples against 300 of another metric dropped step 235 and
	// added 300, so a step mid-run lost its pair. The damage from a
	// spurious sample lands on an arbitrary earlier step, never the tail,
	// which is why "one extra point at the end" is not the harmless thing
	// it sounds like.
	//
	// The callback lost that property once by writing a trailing wall_time
	// no other metric shared. If interior nulls appear here again, suspect
	// the producer's step parity before this join.
	//
	// Skip the fetch when the requested metric IS wall_time — that would
	// be circular and pointless.
	if metricName != "wall_time" {
		if wt, err := h.aim.GetMetric(hash, "wall_time", nil); err == nil && len(wt.Iters) > 0 {
			byStep := make(map[int]float64, len(wt.Iters))
			for i, step := range wt.Iters {
				byStep[step] = wt.Values[i]
			}
			times := make([]*float64, len(data.Iters))
			anyMatched := false
			for i, step := range data.Iters {
				if v, ok := byStep[step]; ok {
					paired := v
					times[i] = &paired
					anyMatched = true
				}
			}
			if anyMatched {
				resp.WallTimes = times
			}
		}
	}

	writeJSON(w, resp)
}

// HandleRunInfo returns full run info (props + metric list).
// GET /api/runs/{hash}/info
func (h *Handler) HandleRunInfo(w http.ResponseWriter, r *http.Request) {
	hash := extractPathParam(r.URL.Path, "/api/runs/", "/info")
	if hash == "" {
		http.Error(w, "missing run hash", http.StatusBadRequest)
		return
	}

	info, err := h.aim.GetRunInfo(hash)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}

	writeJSON(w, info)
}

// EvalManifestEntry is one row in the eval-discovery response — one
// eval Aim run per (model_run, task_set) pair. The dashboard's Eval
// tab calls /api/runs/<eval_run_hash>/info and .../metrics from this
// hash to populate table cells or trace lines.
type EvalManifestEntry struct {
	AimRunHash   string  `json:"aim_run_hash"`
	TaskSet      string  `json:"task_set"`
	CreationTime float64 `json:"creation_time"`
}

// HandleRunEvals returns the manifest of eval Aim runs that score a
// given training run.
//
// GET /api/runs/{model_run_hash}/evals
// → [{ aim_run_hash, task_set, creation_time }, ...]
//
// Discovery filters Aim runs by “astrolabe.kind == "eval"“ and
// “astrolabe.model_run_hash == <hash>“. Multiple eval runs for the
// same (model_run, task_set) collapse to the newest by creation_time
// (re-eval over time leaves older runs in Aim for forensics; the
// dashboard shows the latest by default). See “plans/eval-runs.md“
// for the broader discovery contract.
func (h *Handler) HandleRunEvals(w http.ResponseWriter, r *http.Request) {
	modelRunHash := extractPathParam(r.URL.Path, "/api/runs/", "/evals")
	if modelRunHash == "" {
		http.Error(w, "missing run hash", http.StatusBadRequest)
		return
	}

	// Confirm the run exists before answering about it. The query below
	// cannot tell "this model has no evals" from "this hash is not a run
	// at all", and an empty list for a typo is a plausible and wrong
	// answer — a truncated hash read as a missing eval is how this was
	// found. Same guard the samples endpoint already applies.
	if _, err := h.aim.GetRunInfo(modelRunHash); err != nil {
		if errors.Is(err, ErrRunNotFound) {
			http.Error(w, "run not found", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}

	// One query instead of enumerating every run in the project and
	// reading each one's params. Cross-experiment on purpose: a model
	// evaluated by one experiment can have been produced by another, and
	// RunDetail carries a field to say so. In local-aim mode the sidecar
	// stamps synced eval runs with the *training* experiment name rather
	// than eval/<task_set>, so anything keyed on the experiment would
	// silently drop them. The tag is the source of truth.
	runs, err := h.aim.SearchRuns(QueryByTags(map[string]string{
		TagKind:         "eval",
		TagModelRunHash: modelRunHash,
	}))
	if err != nil {
		// 502 rather than an empty list. An empty list reads as "this run
		// has no evals", which is a plausible and wrong answer.
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}

	entries := make([]EvalManifestEntry, 0, len(runs))
	for _, run := range runs {
		if run.Archived {
			continue
		}
		tags := AstrolabeTagsFromParams(run.Params)
		// Re-check what the query asked for. The query is the
		// optimisation; this is the correctness. Aim's query semantics
		// are not ours to assume, and a mismatch would put another
		// model's evals on this run's tab.
		if tags.Kind != "eval" || tags.ModelRunHash != modelRunHash {
			continue
		}
		entries = append(entries, EvalManifestEntry{
			AimRunHash:   run.Hash,
			TaskSet:      tags.TaskSet,
			CreationTime: run.CreationTime,
		})
	}

	// Dedup by task_set keeping newest creation_time. Re-running an
	// eval mints a new Aim run with the same tags; this is the plan's
	// re-eval policy ("latest wins, older stays for forensics").
	newestByTaskSet := map[string]EvalManifestEntry{}
	for _, e := range entries {
		// task_set must be non-empty — without it the section can't be
		// labeled. Drop rather than show a blank section.
		if e.TaskSet == "" {
			continue
		}
		if existing, found := newestByTaskSet[e.TaskSet]; !found ||
			e.CreationTime > existing.CreationTime {
			newestByTaskSet[e.TaskSet] = e
		}
	}

	out := make([]EvalManifestEntry, 0, len(newestByTaskSet))
	for _, e := range newestByTaskSet {
		out = append(out, e)
	}
	// Deterministic order: newest first, ties broken by task_set.
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreationTime != out[j].CreationTime {
			return out[i].CreationTime > out[j].CreationTime
		}
		return out[i].TaskSet < out[j].TaskSet
	})

	writeJSON(w, out)
}

// HandleColors returns the configured color palette.
// GET /api/config/colors
func (h *Handler) HandleColors(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string][]string{"palette": h.colors})
}

// HandleHealth checks Aim API connectivity.
// GET /api/health
func (h *Handler) HandleHealth(w http.ResponseWriter, r *http.Request) {
	_, err := h.aim.ListExperiments()
	if err != nil {
		writeJSON(w, map[string]interface{}{"status": "error", "message": err.Error()})
		return
	}
	writeJSON(w, map[string]string{"status": "ok"})
}

// --- Helpers ---

func writeJSON(w http.ResponseWriter, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(data)
}

func extractPathParam(path, prefix, suffix string) string {
	rest := strings.TrimPrefix(path, prefix)
	if suffix != "" {
		idx := strings.Index(rest, suffix)
		if idx < 0 {
			return ""
		}
		rest = rest[:idx]
	}
	return rest
}

// runDisplayName picks the label shown to the user for a single Aim run.
//
// Prefers the run's own name (set by the training callback from Composer's
// `run_name` — e.g. "astrolabe_test_v2") so multiple runs within one
// experiment are distinguishable. Falls back to the experiment name when
// Aim returned its default placeholder ("Run: <hash>") or an empty name,
// since that value carries no useful information.
//
// The experiment fallback is only honest for a run that belongs to the
// experiment being displayed. A model pulled in because this experiment
// evaluated it usually lives somewhere else, so labelling it with the
// requesting experiment's name attributes it to the wrong experiment
// outright — worse than showing no name. Callers pass the run's own
// experiment; use runLabel for the rest of the fallback chain.
func runDisplayName(ar AimRun, experimentName string) string {
	name := strings.TrimSpace(ar.Name)
	if name == "" || strings.HasPrefix(name, "Run: ") {
		return experimentName
	}
	return name
}

// runLabel resolves a display label for a run that may not belong to the
// experiment being rendered.
//
// Order: the run's own Aim name, then the experiment it actually lives
// in, then a short hash. The short hash is a last resort but still beats
// an empty cell or a borrowed experiment name — it is at least a
// identifier the user can look up. Truncated for display only; every
// data path carries the full hash.
func runLabel(ar AimRun, ownExperiment string) string {
	if name := strings.TrimSpace(ar.Name); name != "" && !strings.HasPrefix(name, "Run: ") {
		return name
	}
	if exp := strings.TrimSpace(ownExperiment); exp != "" {
		return exp
	}
	if len(ar.RunID) > 12 {
		return ar.RunID[:12]
	}
	return ar.RunID
}

func formatDuration(creationTime, endTime float64) string {
	start := time.Unix(int64(creationTime), 0)
	var end time.Time
	if endTime > 0 {
		end = time.Unix(int64(endTime), 0)
	} else {
		end = time.Now()
	}
	d := end.Sub(start)
	if d.Hours() >= 1 {
		h := int(d.Hours())
		m := int(d.Minutes()) % 60
		if m == 0 {
			return strings.TrimRight(d.Truncate(time.Hour).String(), "0s") + ""
		}
		_ = h
		return d.Truncate(time.Minute).String()
	}
	return d.Truncate(time.Second).String()
}

func stateDuration(startedAt, finishedAt string) string {
	if startedAt == "" {
		return ""
	}
	start, err := time.Parse(time.RFC3339, startedAt)
	if err != nil {
		// Try ISO format without timezone
		start, err = time.Parse("2006-01-02T15:04:05", startedAt[:19])
		if err != nil {
			return ""
		}
	}
	var end time.Time
	if finishedAt != "" {
		end, err = time.Parse(time.RFC3339, finishedAt)
		if err != nil {
			end, _ = time.Parse("2006-01-02T15:04:05", finishedAt[:19])
		}
	} else {
		end = time.Now()
	}
	d := end.Sub(start)
	if d.Hours() >= 1 {
		return strings.Replace(d.Truncate(time.Minute).String(), "h0m", "h", 1)
	}
	return d.Truncate(time.Second).String()
}
