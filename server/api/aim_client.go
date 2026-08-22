package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	neturl "net/url"
	"sort"
	"strings"
	"time"
)

// AimClient talks to the Aim REST API served by `aim up`.
type AimClient struct {
	baseURL    string
	httpClient *http.Client
}

// NewAimClient creates a client pointing at the Aim REST API.
func NewAimClient(baseURL string) *AimClient {
	return &AimClient{
		baseURL: strings.TrimRight(baseURL, "/"),
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

// --- Response types (match Aim's JSON shapes) ---

type Experiment struct {
	ID           string  `json:"id"`
	Name         string  `json:"name"`
	RunCount     int     `json:"run_count"`
	Archived     bool    `json:"archived"`
	CreationTime float64 `json:"creation_time"`
}

type ExperimentRuns struct {
	ID   string   `json:"id"`
	Runs []AimRun `json:"runs"`
}

type AimRun struct {
	RunID        string  `json:"run_id"`
	Name         string  `json:"name"`
	CreationTime float64 `json:"creation_time"`
	EndTime      float64 `json:"end_time"`
	Archived     bool    `json:"archived"`
}

type RunInfo struct {
	Params map[string]interface{} `json:"params"`
	Traces RunTraces              `json:"traces"`
	Props  RunProps               `json:"props"`
}

type RunTraces struct {
	Metric []MetricInfo `json:"metric"`
}

type MetricInfo struct {
	Name      string                 `json:"name"`
	Context   map[string]interface{} `json:"context"`
	LastValue float64                `json:"last_value"`
}

type RunProps struct {
	Name         string        `json:"name"`
	Description  *string       `json:"description"`
	Experiment   RunExperiment `json:"experiment"`
	Tags         []interface{} `json:"tags"`
	CreationTime float64       `json:"creation_time"`
	EndTime      float64       `json:"end_time"`
	Archived     bool          `json:"archived"`
	Active       bool          `json:"active"`
}

type RunExperiment struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type MetricData struct {
	Name    string                 `json:"name"`
	Context map[string]interface{} `json:"context"`
	Values  []float64              `json:"values"`
	Iters   []int                  `json:"iters"`
}

// --- Client methods ---

// ListExperiments returns all experiments.
func (c *AimClient) ListExperiments() ([]Experiment, error) {
	resp, err := c.httpClient.Get(c.baseURL + "/api/experiments/")
	if err != nil {
		return nil, fmt.Errorf("aim API unreachable: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("aim API returned %d", resp.StatusCode)
	}

	var experiments []Experiment
	if err := json.NewDecoder(resp.Body).Decode(&experiments); err != nil {
		return nil, fmt.Errorf("decoding experiments: %w", err)
	}
	return experiments, nil
}

// ListExperimentRuns returns all runs for a given experiment ID.
func (c *AimClient) ListExperimentRuns(experimentID string) (*ExperimentRuns, error) {
	url := fmt.Sprintf("%s/api/experiments/%s/runs/", c.baseURL, experimentID)
	resp, err := c.httpClient.Get(url)
	if err != nil {
		return nil, fmt.Errorf("aim API unreachable: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("aim API returned %d for experiment %s", resp.StatusCode, experimentID)
	}

	var result ExperimentRuns
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decoding experiment runs: %w", err)
	}
	return &result, nil
}

// GetRunInfo returns full info for a run (props, metric names, etc.).
// ErrRunNotFound is returned when Aim has no run with the given hash.
var ErrRunNotFound = errors.New("run not found")

func (c *AimClient) GetRunInfo(runHash string) (*RunInfo, error) {
	url := fmt.Sprintf("%s/api/runs/%s/info/", c.baseURL, runHash)
	resp, err := c.httpClient.Get(url)
	if err != nil {
		return nil, fmt.Errorf("aim API unreachable: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		// Distinct from "Aim is down": a caller that starts from a run
		// hash needs to tell a bad hash from a broken dependency, and
		// the two want different HTTP statuses at the edge.
		return nil, fmt.Errorf("%w: %s", ErrRunNotFound, runHash)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("aim API returned %d for run %s", resp.StatusCode, runHash)
	}

	var info RunInfo
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return nil, fmt.Errorf("decoding run info: %w", err)
	}
	return &info, nil
}

// AstrolabeTags is the set of astrolabe.* tags written by the
// astrolabe-composer-callback. Returned by AstrolabeTagsFromParams as
// a struct so adding new tags doesn't require renaming a positional
// return at every call site.
type AstrolabeTags struct {
	Version        string
	SubmitID       string
	ExperimentName string
	// SubmittedBy was added in v1.2.1; legacy runs have it empty and
	// the dashboard's filter dropdown surfaces them as "unknown".
	SubmittedBy string
	// GPUType / GPURateCentsPerHour / Outcome were added in v1.7.4 so
	// the cost page can derive per-version spend directly from Aim
	// instead of the state file (which is last-write-wins per
	// experiment, so older versions lose their data). All three are
	// empty/nil for runs that predate v1.7.4; the cost handler runs
	// a fallback rate lookup for those.
	GPUType             string
	GPURateCentsPerHour *int
	Outcome             string
	// Kind discriminates engine-created metadata runs (astrolabe.kind=
	// "metadata", carrying cost info) from composer-created training
	// runs (kind empty or "training"). Added v1.7.5 — the cost handler
	// reads only metadata runs; everywhere else hides them so the
	// experiment-detail page doesn't show a confusing extra row.
	Kind string
	// Repo / Backend rounds out the cost-page dimensions. Engine writes
	// these onto the metadata run at acquire time so the cost handler
	// doesn't need to join against state files for repo/backend
	// stacking.
	Repo    string
	Backend string
	// StartedAtISO / FinishedAtISO are ISO-8601 timestamps written by
	// the engine (and by the v1.7.5 backfill). These take priority
	// over Aim's own creation_time/end_time because the engine knows
	// the real instance acquire/release moments — Aim's
	// ``Run.creation_time`` reflects when the Run() constructor ran,
	// which is identical to instance-acquire for live submits but
	// equal to backfill-time for retroactively created metadata runs.
	StartedAtISO  string
	FinishedAtISO string
	// TaskSet / ModelRunHash are written by astrolabe.eval_results onto
	// eval runs (``Kind == "eval"``). The eval-discovery handler reads
	// these to group sections by ``TaskSet`` and to join eval runs back
	// to the model run they score. Empty on non-eval runs.
	TaskSet      string
	ModelRunHash string
	// SampleSet is written by astrolabe_callbacks.log_samples onto sample
	// runs (``Kind == "sample"``). It labels one batch of qualitative
	// outputs — "faces", "sentence-completion" — and the Examples tab
	// groups by it. Empty on non-sample runs.
	//
	// It shares ModelRunHash with eval runs: both kinds attribute
	// themselves to the training run they describe, and both use the same
	// tag to do it.
	SampleSet string
}

// AstrolabeTagsFromParams extracts the astrolabe.* tags the
// astrolabe-composer-callback writes to an Aim run. The callback does
// “run["astrolabe.version"] = "v3"“ etc., which Aim may serialize
// either as a flat key (“params["astrolabe.version"]“) or nested
// under a top-level "astrolabe" mapping (“params["astrolabe"]["version"]“)
// depending on the Aim version. Try both before giving up.
//
// Any field may be empty if the run wasn't tagged or the params shape
// is unexpected; callers are responsible for the legacy fallback.
func AstrolabeTagsFromParams(params map[string]interface{}) AstrolabeTags {
	if params == nil {
		return AstrolabeTags{}
	}
	tags := AstrolabeTags{
		Version:        stringFromAny(params["astrolabe.version"]),
		SubmitID:       stringFromAny(params[TagSubmitID]),
		ExperimentName: stringFromAny(params[TagExperiment]),
		SubmittedBy:    stringFromAny(params["astrolabe.user"]),
		GPUType:        stringFromAny(params["astrolabe.gpu_type"]),
		Outcome:        stringFromAny(params["astrolabe.outcome"]),
		Kind:           stringFromAny(params[TagKind]),
		Repo:           stringFromAny(params["astrolabe.repo"]),
		Backend:        stringFromAny(params["astrolabe.backend"]),
		StartedAtISO:   stringFromAny(params["astrolabe.started_at_iso"]),
		FinishedAtISO:  stringFromAny(params["astrolabe.finished_at_iso"]),
		TaskSet:        stringFromAny(params[TagTaskSet]),
		ModelRunHash:   stringFromAny(params[TagModelRunHash]),
		SampleSet:      stringFromAny(params[TagSampleSet]),
	}
	if r := intFromAny(params["astrolabe.gpu_rate_cents_per_hour"]); r != nil {
		tags.GPURateCentsPerHour = r
	}

	// Nested layout — fall back if any key is empty above.
	if tags.Version == "" || tags.SubmitID == "" ||
		tags.ExperimentName == "" || tags.SubmittedBy == "" ||
		tags.GPUType == "" || tags.Outcome == "" ||
		tags.Kind == "" || tags.Repo == "" || tags.Backend == "" ||
		tags.TaskSet == "" || tags.ModelRunHash == "" ||
		tags.SampleSet == "" || tags.GPURateCentsPerHour == nil {
		if nested, ok := params["astrolabe"].(map[string]interface{}); ok {
			if tags.Version == "" {
				tags.Version = stringFromAny(nested["version"])
			}
			if tags.SubmitID == "" {
				tags.SubmitID = stringFromAny(nested["submit_id"])
			}
			if tags.ExperimentName == "" {
				tags.ExperimentName = stringFromAny(nested["experiment"])
			}
			if tags.SubmittedBy == "" {
				tags.SubmittedBy = stringFromAny(nested["user"])
			}
			if tags.GPUType == "" {
				tags.GPUType = stringFromAny(nested["gpu_type"])
			}
			if tags.Outcome == "" {
				tags.Outcome = stringFromAny(nested["outcome"])
			}
			if tags.Kind == "" {
				tags.Kind = stringFromAny(nested["kind"])
			}
			if tags.Repo == "" {
				tags.Repo = stringFromAny(nested["repo"])
			}
			if tags.Backend == "" {
				tags.Backend = stringFromAny(nested["backend"])
			}
			if tags.TaskSet == "" {
				tags.TaskSet = stringFromAny(nested["task_set"])
			}
			if tags.ModelRunHash == "" {
				tags.ModelRunHash = stringFromAny(nested["model_run_hash"])
			}
			if tags.SampleSet == "" {
				tags.SampleSet = stringFromAny(nested["sample_set"])
			}
			if tags.GPURateCentsPerHour == nil {
				if r := intFromAny(nested["gpu_rate_cents_per_hour"]); r != nil {
					tags.GPURateCentsPerHour = r
				}
			}
		}
	}
	return tags
}

func stringFromAny(v interface{}) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

// intFromAny extracts an integer from a tag value, accepting either
// JSON numbers (Aim stores small ints as float64 after json.Unmarshal)
// or string-encoded ints (the engine writes the rate as a string into
// AIM_RUN_TAGS, and the callback may preserve that shape).
func intFromAny(v interface{}) *int {
	if v == nil {
		return nil
	}
	switch x := v.(type) {
	case float64:
		i := int(x)
		return &i
	case int:
		return &x
	case string:
		if x == "" {
			return nil
		}
		var i int
		if _, err := fmt.Sscanf(x, "%d", &i); err == nil {
			return &i
		}
	}
	return nil
}

// GetMetric fetches metric data (step/value pairs) for a run.
func (c *AimClient) GetMetric(runHash string, metricName string, context map[string]interface{}) (*MetricData, error) {
	url := fmt.Sprintf("%s/api/runs/%s/metric/get-batch/", c.baseURL, runHash)

	if context == nil {
		context = map[string]interface{}{}
	}

	reqBody := []map[string]interface{}{
		{"name": metricName, "context": context},
	}
	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshalling request: %w", err)
	}

	resp, err := c.httpClient.Post(url, "application/json", strings.NewReader(string(bodyBytes)))
	if err != nil {
		return nil, fmt.Errorf("aim API unreachable: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("aim API returned %d: %s", resp.StatusCode, string(body))
	}

	var results []MetricData
	if err := json.NewDecoder(resp.Body).Decode(&results); err != nil {
		return nil, fmt.Errorf("decoding metric data: %w", err)
	}

	// Empty result set means the run hasn't logged this metric yet —
	// a normal state for runs in the window between submit and the
	// first batch_end hook. Return an empty MetricData (not an error)
	// so the handler propagates "no data yet" as a 200 + empty arrays
	// rather than a 502, which the frontend used to interpret as a
	// failure and silently substitute a synthetic exponential-decay
	// curve, rendering filler traces for runs that simply hadn't
	// produced data yet. Empty MetricData → empty trace → chart
	// correctly omits the run from the rendered series.
	if len(results) == 0 {
		return &MetricData{
			Name:    metricName,
			Context: context,
			Values:  []float64{},
			Iters:   []int{},
		}, nil
	}
	return &results[0], nil
}

// --- Object sequences: text and image payloads ---
//
// Same request shape as GetMetric, different decoder. Aim serves
// metrics as JSON and objects as an encoded tree; see aim_encoding.go.

// ObjectRecord is one value in an object sequence.
//
// Which fields are populated depends on the sequence type. Text arrives
// inline in Text; images arrive as metadata plus a BlobURI, because Aim
// registers image routes with resolve_blobs=False.
type ObjectRecord struct {
	Text    string
	BlobURI string
	Format  string
	Width   int64
	Height  int64
	Caption string
}

// ObjectSequence is one decoded get-batch response.
//
// Steps and Records are parallel: Records[i] was logged at Steps[i].
// Callers join on the STEP, never on the index — two sequences of the
// same run need not share a step set.
type ObjectSequence struct {
	Name    string
	Steps   []int64
	Records []ObjectRecord
}

// At returns the record logged at the given step.
func (s *ObjectSequence) At(step int64) (ObjectRecord, bool) {
	for i, st := range s.Steps {
		if st == step {
			return s.Records[i], true
		}
	}
	return ObjectRecord{}, false
}

// GetTextSequence fetches a text sequence. One hop: Aim configures
// TextApiConfig with resolve_blobs=True, so the text is inline.
func (c *AimClient) GetTextSequence(runHash, name string) (*ObjectSequence, error) {
	return c.getObjectSequence(runHash, "texts", name)
}

// GetImageSequence fetches image METADATA and blob URIs. No pixels:
// ImageApiConfig sets resolve_blobs=False. Resolve with GetBlobs.
func (c *AimClient) GetImageSequence(runHash, name string) (*ObjectSequence, error) {
	return c.getObjectSequence(runHash, "images", name)
}

func (c *AimClient) getObjectSequence(runHash, seqType, name string) (*ObjectSequence, error) {
	url := fmt.Sprintf("%s/api/runs/%s/%s/get-batch/", c.baseURL, runHash, seqType)
	reqBody := []map[string]interface{}{{"name": name, "context": map[string]interface{}{}}}
	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshalling request: %w", err)
	}
	resp, err := c.httpClient.Post(url, "application/json", strings.NewReader(string(bodyBytes)))
	if err != nil {
		return nil, fmt.Errorf("aim API unreachable: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("aim API returned %d for %s sequence %q on run %s",
			resp.StatusCode, seqType, name, runHash)
	}
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading %s sequence: %w", seqType, err)
	}
	return ParseObjectSequence(raw)
}

// ParseObjectSequence turns a decoded get-batch body into a sequence.
//
// Exported so tests can run it over a captured response with no server.
// The paths it reads, from a real body:
//
//	name                     -> "sample/completions/input"
//	iters/<i>                -> the step of record i
//	values/<i>/0/data        -> text, or image bytes when resolved
//	values/<i>/0/blob_uri    -> image, unresolved
//	values/<i>/0/format|width|height|caption
//
// The trailing 0 is the index within a record collection: one log call
// can carry a list of images. Only index 0 is read here — rendering a
// list within one step is not something log_samples produces.
func ParseObjectSequence(body []byte) (*ObjectSequence, error) {
	entries, err := DecodeTree(body)
	if err != nil {
		return nil, err
	}
	seq := &ObjectSequence{}
	byIndex := map[int64]*ObjectRecord{}
	steps := map[int64]int64{} // record index -> step

	record := func(i int64) *ObjectRecord {
		if r, ok := byIndex[i]; ok {
			return r
		}
		r := &ObjectRecord{}
		byIndex[i] = r
		return r
	}

	for _, e := range entries {
		if len(e.Path) == 1 {
			if key, ok := e.Path[0].(string); ok && key == "name" {
				if s, ok := e.Value.(string); ok {
					seq.Name = s
				}
			}
			continue
		}
		head, _ := e.Path[0].(string)
		switch head {
		case "iters":
			idx, ok := e.Path[1].(int64)
			if !ok || len(e.Path) != 2 {
				continue
			}
			step, ok := e.Value.(int64)
			if !ok {
				continue
			}
			steps[idx] = step
			record(idx)
		case "values":
			idx, ok := e.Path[1].(int64)
			if !ok || len(e.Path) != 4 {
				continue
			}
			field, ok := e.Path[3].(string)
			if !ok {
				continue
			}
			r := record(idx)
			switch field {
			case "data":
				switch v := e.Value.(type) {
				case string:
					r.Text = v
				}
			case "blob_uri":
				if s, ok := e.Value.(string); ok {
					r.BlobURI = s
				}
			case "format":
				if s, ok := e.Value.(string); ok {
					r.Format = s
				}
			case "caption":
				if s, ok := e.Value.(string); ok {
					r.Caption = s
				}
			case "width":
				if n, ok := e.Value.(int64); ok {
					r.Width = n
				}
			case "height":
				if n, ok := e.Value.(int64); ok {
					r.Height = n
				}
			}
		}
	}

	// Iterate the indexes that exist and sort them, rather than counting
	// 0..max. The max comes off the wire: a mis-decoded path integer
	// makes it astronomically large and the counting loop hangs the
	// handler. Found by a mutation that flipped the path byte order.
	indexes := make([]int64, 0, len(byIndex))
	for i := range byIndex {
		indexes = append(indexes, i)
	}
	sort.Slice(indexes, func(a, b int) bool { return indexes[a] < indexes[b] })

	for _, i := range indexes {
		step, ok := steps[i]
		if !ok {
			// A value with no matching iters entry has no step, so it
			// cannot be paired with anything. Dropping it is better than
			// inventing a step from its position.
			continue
		}
		seq.Steps = append(seq.Steps, step)
		seq.Records = append(seq.Records, *byIndex[i])
	}
	return seq, nil
}

// GetBlobs resolves image blob URIs to bytes.
//
// Repo-level route with NO run hash in the path: Aim registers it once
// per sequence type whose config sets resolve_blobs=False, which is
// Images and Audios. The uri is Aim's own opaque token (a Fernet-
// encrypted resource path); it is passed through verbatim and must
// never be parsed, rebuilt or normalised on this side — only Aim can
// mint one, and a hub-side reconstruction would appear to work against
// today's Aim and break silently on any upgrade.
//
// Batched because the route is batched, and keyed by uri in the
// response. Callers must look up by uri and never zip the result
// against request order: with one image the two are indistinguishable,
// and with two they are not.
func (c *AimClient) GetBlobs(uris []string) (map[string][]byte, error) {
	out := map[string][]byte{}
	if len(uris) == 0 {
		return out, nil
	}
	url := fmt.Sprintf("%s/api/runs/images/get-batch", c.baseURL)
	bodyBytes, err := json.Marshal(uris)
	if err != nil {
		return nil, fmt.Errorf("marshalling blob request: %w", err)
	}
	resp, err := c.httpClient.Post(url, "application/json", strings.NewReader(string(bodyBytes)))
	if err != nil {
		return nil, fmt.Errorf("aim API unreachable: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("aim API returned %d resolving %d blob(s)", resp.StatusCode, len(uris))
	}
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading blobs: %w", err)
	}
	entries, err := DecodeTree(raw)
	if err != nil {
		return nil, err
	}
	// The blob stream is a flat tree keyed by the uri itself.
	for _, e := range entries {
		if len(e.Path) != 1 {
			continue
		}
		uri, ok := e.Path[0].(string)
		if !ok {
			continue
		}
		if b, ok := e.Value.([]byte); ok {
			out[uri] = b
		}
	}
	return out, nil
}

// --- Asking Aim, instead of walking it ---
//
// Three handlers in this package used to enumerate every run in the
// project and filter in Go, because nobody had checked whether Aim could
// be asked. It can: /api/runs/search/run/ is the endpoint Aim's own UI
// uses to filter interactively, it composes, and it returns the same
// encoded tree the object routes do — so DecodeTree handles it unchanged.

// Param keys the hub queries on. Named constants rather than literals at
// call sites so a query is built from the same string the param reader
// uses, and so contract_test.go has one place to check.
const (
	TagKind         = "astrolabe.kind"
	TagModelRunHash = "astrolabe.model_run_hash"
	TagSampleSet    = "astrolabe.sample_set"
	TagTaskSet      = "astrolabe.task_set"
	TagSubmitID     = "astrolabe.submit_id"
	TagExperiment   = "astrolabe.experiment"
)

// SearchedRun is one run from a search.
//
// Deliberately not the whole tree: a search returns everything about a
// run and the callers here read a handful of fields.
type SearchedRun struct {
	Hash           string
	Name           string
	ExperimentName string
	CreationTime   float64
	EndTime        float64
	Archived       bool
	Params         map[string]any
}

// QueryByTag builds `run['<tag>'] == '<value>'`.
//
// A constructor rather than a formatted string at each call site, so the
// tag comes from the constants this package already declares and a
// rename is a compile error instead of a silently empty result.
func QueryByTag(tag, value string) string {
	return fmt.Sprintf("run[%s] == %s", quoteAimLiteral(tag), quoteAimLiteral(value))
}

// QueryByTags joins several tag equalities with `and`.
func QueryByTags(pairs map[string]string) string {
	terms := make([]string, 0, len(pairs))
	for tag, value := range pairs {
		terms = append(terms, QueryByTag(tag, value))
	}
	// Sorted so the query string is deterministic — a map's iteration
	// order would make two identical requests look different to a cache
	// or a test.
	sort.Strings(terms)
	return strings.Join(terms, " and ")
}

// QueryByExperiment builds `run.experiment == '<name>'`.
func QueryByExperiment(name string) string {
	return "run.experiment == " + quoteAimLiteral(name)
}

// quoteAimLiteral renders a Go string as a single-quoted literal in Aim's
// query language.
//
// The expression is evaluated server-side by RestrictedPython. An
// experiment name is user-supplied, and one containing a quote would
// either break the query or change what it asks. The engine's validation
// gate happens to forbid quotes today, but this must not depend on a rule
// enforced in another repo.
func quoteAimLiteral(s string) string {
	var b strings.Builder
	b.WriteByte('\'')
	for _, r := range s {
		switch r {
		case '\'', '\\':
			b.WriteByte('\\')
			b.WriteRune(r)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('\'')
	return b.String()
}

// SearchRuns asks Aim which runs match a query.
//
// q is Aim's own query language — build it with QueryByTag and friends
// rather than by hand:
//
//	run['astrolabe.kind'] == 'sample'
//	run.experiment == 'my-experiment'
//
// An empty result means no run matched, which is a real answer. It is
// returned as an empty slice, never as "everything" — a filter that
// silently falls back to the whole project is the failure that would make
// this worse than the walk it replaces.
func (c *AimClient) SearchRuns(q string) ([]SearchedRun, error) {
	url := fmt.Sprintf("%s/api/runs/search/run/?q=%s", c.baseURL, neturl.QueryEscape(q))
	resp, err := c.httpClient.Get(url)
	if err != nil {
		return nil, fmt.Errorf("aim API unreachable: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		// Naming the query matters: a 400 here is a syntax error in an
		// expression this package built, and the message is the only way
		// to find which builder produced it.
		return nil, fmt.Errorf("aim search returned %d for query %q: %s",
			resp.StatusCode, q, strings.TrimSpace(string(body)))
	}
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading search response: %w", err)
	}
	return ParseSearchedRuns(raw)
}

// ParseSearchedRuns decodes a search response body.
//
// Exported so tests can run it over a captured response with no server.
func ParseSearchedRuns(body []byte) ([]SearchedRun, error) {
	entries, err := DecodeTree(body)
	if err != nil {
		return nil, err
	}

	byHash := map[string]*SearchedRun{}
	var order []string

	for _, e := range entries {
		if len(e.Path) < 2 {
			continue
		}
		hash, ok := e.Path[0].(string)
		if !ok || isProgressKey(hash) {
			continue
		}
		run, seen := byHash[hash]
		if !seen {
			run = &SearchedRun{Hash: hash, Params: map[string]any{}}
			byHash[hash] = run
			order = append(order, hash)
		}

		section, _ := e.Path[1].(string)
		switch section {
		case "props":
			if len(e.Path) < 3 {
				continue
			}
			field, _ := e.Path[2].(string)
			switch field {
			case "name":
				if s, ok := e.Value.(string); ok {
					run.Name = s
				}
			case "archived":
				if b, ok := e.Value.(bool); ok {
					run.Archived = b
				}
			case "creation_time":
				if f, ok := e.Value.(float64); ok {
					run.CreationTime = f
				}
			case "end_time":
				if f, ok := e.Value.(float64); ok {
					run.EndTime = f
				}
			case "experiment":
				// props/experiment/name — one level deeper than the rest.
				if len(e.Path) == 4 {
					if key, _ := e.Path[3].(string); key == "name" {
						if s, ok := e.Value.(string); ok {
							run.ExperimentName = s
						}
					}
				}
			}
		case "params":
			if len(e.Path) != 3 {
				continue
			}
			if key, ok := e.Path[2].(string); ok {
				run.Params[key] = e.Value
			}
		}
	}

	out := make([]SearchedRun, 0, len(order))
	for _, h := range order {
		out = append(out, *byHash[h])
	}
	return out, nil
}

// isProgressKey reports whether a top-level key is streaming bookkeeping
// rather than a run.
//
// The response interleaves keys named progress_0, progress_1 … as it
// streams. Measured: four of them alongside three real runs. Treating
// them as runs puts phantom entries in every caller's result, and they
// look plausible enough to survive review.
func isProgressKey(key string) bool {
	return strings.HasPrefix(key, "progress_")
}

// QueryByRunName builds `run.name == '<name>'`.
//
// Aim can filter on the name; it cannot do "the most recent one", so a
// caller wanting newest-only sorts the result itself.
func QueryByRunName(name string) string {
	return "run.name == " + quoteAimLiteral(name)
}
