//go:build contract

package api

// Contract tests against a LIVE Aim server.
//
// Build-tagged, so `go test ./...` never runs them. Run deliberately:
//
//	aim init --repo /tmp/aim-contract && aim up --repo /tmp/aim-contract --port 43899
//	go test -tags contract ./api/ -aim-url=http://127.0.0.1:43899 -run TestAimContract
//
// WHY THIS EXISTS, IN ONE SENTENCE
//
// aim_encoding_test.go decodes captured response bodies from testdata/. That
// pins our decoder against its own regressions and CANNOT tell us Aim changed
// its encoding, because the fixture changes only when we re-capture it.
//
// The encoding is an internal: aim/storage/encoding/encoding_native.pyx, no
// version field, no negotiation, and frame lengths in NATIVE byte order. If a
// future Aim alters it, every unit test in this package stays green while the
// hub silently mis-decodes — text that renders as an empty string, which reads
// as a model that produced nothing.
//
// Only a real server can catch that. This file is the only thing in the repo
// that can fail for that reason.
//
// HONEST LIMIT: the hub has no CI, so nothing runs this on a schedule. It is a
// command a human runs when bumping Aim. Written down rather than left implied,
// because a guard nobody runs and a guard that cannot fail are the same guard.
// If the hub gains CI, this belongs on the same nightly pattern astrolabe uses
// for `pytest -m contract`.

import (
	"flag"
	"os/exec"
	"strings"
	"testing"
)

var (
	aimURL  = flag.String("aim-url", "", "base URL of a live aim server for contract tests")
	aimRepo = flag.String("aim-repo", "", "filesystem path of the repo -aim-url is serving")
)

func requireLiveAim(t *testing.T) *AimClient {
	t.Helper()
	if *aimURL == "" {
		t.Fatal("contract tests need -aim-url pointing at a live `aim up`")
	}
	return NewAimClient(*aimURL)
}

// TestAimContractTextSequenceStillDecodes is the whole point of the file.
//
// It writes a run through the Aim SDK, reads it back through the hub's
// decoder, and asserts the text survives. A change to the wire format fails
// here and nowhere else in the repo.
func TestAimContractTextSequenceStillDecodes(t *testing.T) {
	client := requireLiveAim(t)

	const marker = "contract-test-payload-Übersetze"
	hash := writeSampleRunViaSDK(t, marker)

	seq, err := client.GetTextSequence(hash, "sample/contract/output")
	if err != nil {
		t.Fatalf("GetTextSequence against a live Aim: %v", err)
	}
	if len(seq.Records) == 0 {
		t.Fatal("live Aim returned no records; either the write failed or the " +
			"encoding changed shape")
	}
	rec, ok := seq.At(0)
	if !ok {
		t.Fatal("step 0 missing — iters no longer align with values")
	}
	if rec.Text != marker {
		t.Fatalf("decoded %q, wrote %q — Aim's encoding has moved and "+
			"aim_encoding.go needs re-deriving from encoding_native.pyx",
			rec.Text, marker)
	}
}

// TestAimContractImagesStillTakeTwoHops pins the resolve_blobs=False shape.
//
// If a future Aim resolves image blobs inline, EXAMPLES-1.03's second hop
// becomes dead code and the pixels arrive somewhere this package does not
// look. Either way the tab breaks quietly.
func TestAimContractImagesStillTakeTwoHops(t *testing.T) {
	client := requireLiveAim(t)
	hash := writeImageRunViaSDK(t)

	seq, err := client.GetImageSequence(hash, "sample/contract-img/output")
	if err != nil {
		t.Fatalf("GetImageSequence: %v", err)
	}
	if len(seq.Records) == 0 {
		t.Fatal("no image records returned")
	}
	rec := seq.Records[0]
	if rec.BlobURI == "" {
		t.Fatal("image record carries no blob_uri — Aim may have switched to " +
			"resolve_blobs=True, which puts the pixels somewhere GetBlobs never looks")
	}
	if rec.Format == "" {
		t.Error("image record carries no format; HandleSampleBlob derives " +
			"Content-Type from it")
	}

	blobs, err := client.GetBlobs([]string{rec.BlobURI})
	if err != nil {
		t.Fatalf("GetBlobs: %v", err)
	}
	data, ok := blobs[rec.BlobURI]
	if !ok {
		t.Fatal("the blob response is no longer keyed by uri")
	}
	if len(data) < 8 || string(data[:8]) != "\x89PNG\r\n\x1a\n" {
		t.Fatalf("resolved blob is not a PNG: % x", data[:min(8, len(data))])
	}
}

// --- writing through the real SDK ---
//
// Shelling out to python is deliberate. Constructing the run any other way
// would mean writing Aim's format from Go, and a test that both writes and
// reads with our own code proves only that it is self-consistent.

func writeSampleRunViaSDK(t *testing.T, payload string) string {
	t.Helper()
	return runPython(t, `
from aim import Run, Text
run = Run(repo=REPO, experiment="contract-test")
run["astrolabe.kind"] = "sample"
run.track(Text(`+pyQuote(payload)+`), name="sample/contract/output", step=0)
print(run.hash)
run.close()
`)
}

func writeImageRunViaSDK(t *testing.T) string {
	t.Helper()
	return runPython(t, `
import numpy as np
from aim import Image, Run
run = Run(repo=REPO, experiment="contract-test")
run["astrolabe.kind"] = "sample"
run.track(Image(np.full((8, 8, 3), 90, dtype=np.uint8)),
          name="sample/contract-img/output", step=0)
print(run.hash)
run.close()
`)
}

// indexRun makes a freshly-written run visible to a server that is already
// running.
//
// Without this the read returns nothing and the test fails for a reason that
// has nothing to do with the encoding — which is worse than not having the
// test. It is not a test artifact either: the sync sidecar calls exactly this
// in production, and `repo.get_run` on an unindexed repo returns None rather
// than raising, so nothing anywhere reports the omission.
const indexRun = `
from aim import Repo
from aim.sdk.index_manager import RepoIndexManager
repo = Repo(REPO)
repo.request_props(HASH, read_only=False)
RepoIndexManager.get_index_manager(repo).index(HASH)
`

func runPython(t *testing.T, body string) string {
	t.Helper()
	if *aimRepo == "" {
		t.Fatal("contract tests also need -aim-repo, the path `aim up` is serving")
	}
	repo := *aimRepo
	script := "REPO = " + pyQuote(repo) + "\n" + body
	out, err := exec.Command("python3", "-c", script).Output()
	if err != nil {
		t.Fatalf("writing through the Aim SDK failed: %v\n%s", err, out)
	}
	hash := strings.TrimSpace(string(out))

	idx := "REPO = " + pyQuote(repo) + "\nHASH = " + pyQuote(hash) + "\n" + indexRun
	if idxOut, err := exec.Command("python3", "-c", idx).CombinedOutput(); err != nil {
		t.Fatalf("indexing the run failed: %v\n%s", err, idxOut)
	}
	return hash
}

func pyQuote(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`).Replace(s) + `"`
}
