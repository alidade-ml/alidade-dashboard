package api

// The engine↔dashboard contract, which until now did not exist.
//
// astrolabe's contract.py is the source of truth for the astrolabe.* tag
// names. The hub types them into Go source as string literals. Nothing
// checked that they agreed, and the failure mode is silent: a tag renamed
// engine-side makes a tab return an empty list, which is indistinguishable
// from "nobody logged any".
//
// docs/contracts-and-testbeds.md in astrolabe says it plainly — the
// engine↔callback contract is vendored and hash-pinned, and the
// engine↔dashboard contract is Go source literals. This file is the second
// half.
//
// WHAT THIS CATCHES AND WHAT IT DOES NOT
//
// The fixture is a copy. It proves the Go literals match contract.py *as of
// the vendored ref*. It cannot tell us the engine moved on — that needs a
// re-vendor, which is a maintainer step (tools/vendor-contract.sh), exactly
// as in the callbacks repo. Recording the limit here rather than letting the
// green checkmark imply more than it means.
//
// Two paths were weighed and this is the one chosen:
//
//   - Parse contract.py from a sibling astrolabe checkout at test time.
//     Catches drift immediately, and needs a checkout at a guessed path that
//     a fresh clone will not have. `go test ./...` would pass by skipping.
//   - Vendor a copy with a hash pin. Self-contained, works on a fresh clone,
//     goes stale silently. Same trade the callbacks repo already made, and
//     consistency across the two readers is worth more than the difference.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

// pyConstant matches `NAME = "value"` at the top level of contract.py.
var pyConstant = regexp.MustCompile(`(?m)^([A-Z][A-Z0-9_]*)\s*=\s*"([^"]*)"`)

func loadVendoredContract(t *testing.T) map[string]string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "contract.py"))
	if err != nil {
		t.Fatalf("reading the vendored contract: %v", err)
	}
	out := map[string]string{}
	for _, m := range pyConstant.FindAllStringSubmatch(string(raw), -1) {
		out[m[1]] = m[2]
	}
	if len(out) < 10 {
		t.Fatalf("parsed only %d constants from contract.py; the parse is broken "+
			"and every assertion below would be vacuous", len(out))
	}
	return out
}

func TestContractVendoredCopyIsIntact(t *testing.T) {
	// Integrity, not currency. A hand-edit to the fixture would let a wrong
	// Go literal pass by moving the goalpost instead of the code.
	raw, err := os.ReadFile(filepath.Join("testdata", "contract.py"))
	if err != nil {
		t.Fatal(err)
	}
	pinRaw, err := os.ReadFile(filepath.Join("testdata", "contract-vendor.json"))
	if err != nil {
		t.Fatal(err)
	}
	var pin struct {
		VendoredFrom string `json:"vendored_from"`
		SHA256       string `json:"sha256"`
	}
	if err := json.Unmarshal(pinRaw, &pin); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(raw)
	if got := hex.EncodeToString(sum[:]); got != pin.SHA256 {
		t.Fatalf("testdata/contract.py does not match its recorded hash.\n"+
			" got:  %s\n want: %s\n"+
			"If you meant to update the contract, run tools/vendor-contract.sh "+
			"rather than editing the fixture.", got, pin.SHA256)
	}
	if pin.VendoredFrom == "" {
		t.Error("contract-vendor.json records no engine ref")
	}
}

func TestContractTagNamesMatchTheEngine(t *testing.T) {
	// Every astrolabe.* string the hub reads, against the constant that
	// produces it. A rename engine-side fails here instead of emptying a tab.
	c := loadVendoredContract(t)

	// goLiteral is what this package actually reads from Aim params;
	// pyName is the constant in contract.py that writes it.
	for _, tc := range []struct{ pyName, goLiteral string }{
		{"TAG_SUBMIT_ID", "astrolabe.submit_id"},
		{"TAG_USER", "astrolabe.user"},
		{"TAG_VERSION", "astrolabe.version"},
		{"TAG_EXPERIMENT", "astrolabe.experiment"},
		{"TAG_GPU_TYPE", "astrolabe.gpu_type"},
		{"TAG_GPU_RATE_CENTS_PER_HOUR", "astrolabe.gpu_rate_cents_per_hour"},
		{"TAG_OUTCOME", "astrolabe.outcome"},
		{"TAG_KIND", "astrolabe.kind"},
		{"TAG_TASK_SET", "astrolabe.task_set"},
		{"TAG_MODEL_RUN_HASH", "astrolabe.model_run_hash"},
		{"TAG_SAMPLE_SET", "astrolabe.sample_set"},
	} {
		want, ok := c[tc.pyName]
		if !ok {
			t.Errorf("%s is not in contract.py — the engine removed or renamed it, "+
				"and %q in Go now reads a tag nothing writes", tc.pyName, tc.goLiteral)
			continue
		}
		if want != tc.goLiteral {
			t.Errorf("%s = %q engine-side, but Go reads %q", tc.pyName, want, tc.goLiteral)
		}
	}
}

func TestContractKindAndSequenceConstantsMatch(t *testing.T) {
	// The values, not just the tag names. SampleKind and SampleSeqPrefix are
	// exported constants in this package, so these compare code to contract
	// rather than one string literal to another.
	c := loadVendoredContract(t)

	if want := c["KIND_SAMPLE"]; want != SampleKind {
		t.Errorf("KIND_SAMPLE = %q engine-side, SampleKind = %q", want, SampleKind)
	}

	// SampleSeqPrefix is derived, not copied: contract.py owns the template
	// and the hub only needs its constant leading segment. Deriving it here
	// means a template change fails rather than quietly leaving the hub
	// searching for the old prefix.
	tmpl, ok := c["SAMPLE_SEQUENCE_TEMPLATE"]
	if !ok {
		t.Fatal("SAMPLE_SEQUENCE_TEMPLATE missing from contract.py")
	}
	const wantTmpl = "sample/{sample_set}/{role}"
	if tmpl != wantTmpl {
		t.Errorf("SAMPLE_SEQUENCE_TEMPLATE = %q, but the hub builds names assuming %q.\n"+
			"HandleSampleBatch composes SampleSeqPrefix + set + \"/\" + role; a template "+
			"change means that composition is now wrong.", tmpl, wantTmpl)
	}
	if SampleSeqPrefix != "sample/" {
		t.Errorf("SampleSeqPrefix = %q, which no longer matches the template", SampleSeqPrefix)
	}

	for _, tc := range []struct{ pyName, want string }{
		{"SAMPLE_ROLE_INPUT", "input"},
		{"SAMPLE_ROLE_OUTPUT", "output"},
	} {
		if got := c[tc.pyName]; got != tc.want {
			t.Errorf("%s = %q engine-side, the hub requests %q", tc.pyName, got, tc.want)
		}
	}
}

func TestContractVersionIsRecorded(t *testing.T) {
	// Not an assertion about which version — that would fail on every
	// additive bump for no reason. It asserts the field is parseable, so the
	// failure message above can name it.
	c := loadVendoredContract(t)
	v, ok := c["CONTRACT_VERSION"]
	if !ok || v == "" {
		t.Fatal("CONTRACT_VERSION missing from the vendored contract")
	}
	if !regexp.MustCompile(`^\d+\.\d+\.\d+$`).MatchString(v) {
		t.Errorf("CONTRACT_VERSION = %q, not semver", v)
	}
	t.Logf("vendored contract version: %s", v)
}

func TestContractGuardCoversEveryAstrolabeLiteralInSource(t *testing.T) {
	// The anti-rot check. Adding a new astrolabe.* literal to Go without
	// adding it to the table above would leave it unguarded, and the guard
	// would still be green — which is the failure mode this whole file
	// exists to remove.
	guarded := map[string]bool{}
	for _, tc := range []string{
		"astrolabe.submit_id", "astrolabe.user", "astrolabe.version",
		"astrolabe.experiment", "astrolabe.gpu_type",
		"astrolabe.gpu_rate_cents_per_hour", "astrolabe.outcome",
		"astrolabe.kind", "astrolabe.task_set", "astrolabe.model_run_hash",
		"astrolabe.sample_set", "astrolabe.repo", "astrolabe.backend",
		"astrolabe.started_at_iso", "astrolabe.finished_at_iso",
	} {
		guarded[tc] = true
	}

	literal := regexp.MustCompile(`"(astrolabe\.[a-z_]+)"`)
	found := map[string]bool{}
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || filepath.Ext(name) != ".go" {
			continue
		}
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range literal.FindAllStringSubmatch(string(src), -1) {
			found[m[1]] = true
		}
	}
	if len(found) < 10 {
		t.Fatalf("found only %d astrolabe.* literals in source; the scan is broken",
			len(found))
	}
	for lit := range found {
		if !guarded[lit] {
			t.Errorf("%q appears in Go source but is not in the guard table. "+
				"Add it, or the engine can rename it and nothing here will notice.",
				fmt.Sprintf("%s", lit))
		}
	}
}
