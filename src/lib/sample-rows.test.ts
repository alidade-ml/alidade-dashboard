/**
 * Tests for rowsFromBatch.
 *
 * Contract from the engine's samples endpoint, not from the function:
 *
 *   * `kind` describes the OUTPUT. A prompt-to-image batch is kind "image"
 *     with input_text set.
 *   * Absent and empty are different: a set logged without inputs has no
 *     input_text key at all; a model that returned "" has the key with "".
 *   * Image pairs carry a hub URL, used verbatim. It is stable across fetches
 *     by construction, and rebuilding it client-side would forfeit that.
 */
import assert from "node:assert/strict";
import { describe, it } from "node:test";

import { rowsFromBatch } from "./sample-rows.ts";
import type { SampleBatch } from "./types.ts";

function batch(over: Partial<SampleBatch> = {}): SampleBatch {
  return {
    aim_run_hash: "batch-hash",
    sample_set: "completions",
    kind: "text",
    pairs: [],
    ...over,
  };
}

describe("rowsFromBatch", () => {
  it("keeps an input logged as the empty string distinct from an absent one", () => {
    const rows = rowsFromBatch(
      batch({
        pairs: [
          { step: 0, input_text: "", output_text: "from an empty prompt" },
          { step: 1, output_text: "unconditional" },
        ],
      }),
      "model-a",
      "hash-a",
    );
    assert.equal(rows[0].input, "", 'an input logged as "" became something else');
    assert.equal(rows[1].input, null, 'an absent input should be null, not ""');
  });

  it("treats kind as describing the output only", () => {
    // Prompt to image: text in, image out, kind "image".
    const rows = rowsFromBatch(
      batch({
        sample_set: "faces",
        kind: "image",
        pairs: [
          {
            step: 0,
            input_text: "a golden retriever",
            output_url: "/api/samples/blob?role=output&run=r&set=faces&step=0",
          },
        ],
      }),
      "model-a",
      "hash-a",
    );
    assert.equal(rows[0].input, "a golden retriever", "a text input was lost on an image batch");
    assert.equal(rows[0].inputUrl, undefined, "a text input must not become an image URL");
    assert.equal(
      rows[0].outputUrl,
      "/api/samples/blob?role=output&run=r&set=faces&step=0",
      "the output URL did not reach the row",
    );
    assert.equal(rows[0].output, undefined, "an image output must not carry output text");
  });

  it("labels an image input by its step, since it has no text", () => {
    const rows = rowsFromBatch(
      batch({
        sample_set: "denoise",
        kind: "image",
        pairs: [{ step: 7, input_url: "/in", output_url: "/out" }],
      }),
      "model-a",
      "hash-a",
    );
    assert.equal(rows[0].input, "step 7");
    assert.equal(rows[0].inputUrl, "/in");
    assert.equal(rows[0].outputUrl, "/out");
  });

  it("uses the server's URL verbatim, without rebuilding it", () => {
    // The hub now names an image by (run, set, role, step) so the address is
    // the same every fetch — that stability is the whole point, and any
    // client-side reconstruction would put it back at risk.
    const url = "/api/samples/blob?role=output&run=abc&set=faces&step=0";
    const rows = rowsFromBatch(
      batch({ kind: "image", pairs: [{ step: 0, output_url: url }] }),
      "model-a",
      "hash-a",
    );
    assert.equal(rows[0].outputUrl, url);
  });

  it("carries the model identity onto every row", () => {
    const rows = rowsFromBatch(
      batch({
        pairs: [
          { step: 0, output_text: "x" },
          { step: 1, output_text: "y" },
        ],
      }),
      "latent-bert-256",
      "abc123",
    );
    assert.deepEqual(
      rows.map((r) => [r.model, r.modelHash, r.sampleSet]),
      [
        ["latent-bert-256", "abc123", "completions"],
        ["latent-bert-256", "abc123", "completions"],
      ],
    );
  });

  it("returns nothing for a batch with no pairs", () => {
    assert.deepEqual(rowsFromBatch(batch(), "model-a", "hash-a"), []);
  });
});
