/**
 * Tests for rowsFromBatch.
 *
 * Contract from the engine's samples endpoint, not from the function:
 *
 *   * `kind` describes the OUTPUT. A prompt-to-image batch is kind "image"
 *     with input_text set.
 *   * Absent and empty are different: a set logged without inputs has no
 *     input_text key at all; a model that returned "" has the key with "".
 *   * Image pairs carry an opaque uri, which becomes a hub URL. The uri is
 *     never parsed or rebuilt.
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
        pairs: [{ step: 0, input_text: "a golden retriever", output_uri: "TOKEN" }],
      }),
      "model-a",
      "hash-a",
    );
    assert.equal(rows[0].input, "a golden retriever", "a text input was lost on an image batch");
    assert.equal(rows[0].inputUrl, undefined, "a text input must not become an image URL");
    assert.ok(rows[0].outputUrl?.includes("TOKEN"), "the output uri did not reach a URL");
    assert.equal(rows[0].output, undefined, "an image output must not carry output text");
  });

  it("labels an image input by its step, since it has no text", () => {
    const rows = rowsFromBatch(
      batch({
        sample_set: "denoise",
        kind: "image",
        pairs: [{ step: 7, input_uri: "IN", output_uri: "OUT" }],
      }),
      "model-a",
      "hash-a",
    );
    assert.equal(rows[0].input, "step 7");
    assert.ok(rows[0].inputUrl?.includes("IN"));
    assert.ok(rows[0].outputUrl?.includes("OUT"));
  });

  it("passes the uri through url-encoded, never rebuilt", () => {
    // Fernet tokens contain -, _ and = padding. Any re-encoding produces a
    // token Aim cannot decrypt, and it fails as a broken image, not an error.
    const uri = "gAAAAABq-i_L8Q==";
    const rows = rowsFromBatch(
      batch({ kind: "image", pairs: [{ step: 0, output_uri: uri }] }),
      "model-a",
      "hash-a",
    );
    const parsed = new URL(rows[0].outputUrl!, "http://localhost");
    assert.equal(
      parsed.searchParams.get("uri"),
      uri,
      "the uri did not survive the round trip through the URL",
    );
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
