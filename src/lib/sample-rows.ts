/**
 * Batch -> SampleRow, the one piece of real logic between the API and the
 * grouping layer.
 *
 * Lives in lib rather than beside the component because it is where the
 * contract's two asymmetries get handled, and both have already caused bugs:
 * absent is not empty, and `kind` describes only the output.
 */
import type { SampleRow } from "./sample-fixtures.ts";
import type { SampleBatch } from "./types.ts";

/** URL for one image sample.
 *
 * The uri is Aim's opaque Fernet token, arriving in a batch response and
 * going back untouched. Never construct or edit one: only Aim can mint a
 * valid token, and a hand-built one fails as a broken image rather than
 * as an error.
 *
 * A plain URL rather than a fetch so the browser owns it — progressive
 * rendering, per-image caching, and cancellation when the user scrolls
 * away all come free with <img src>.
 */
const API_BASE = "/api";

export function sampleBlobUrl(uri: string, format = "png"): string {
  return `${API_BASE}/samples/blob?uri=${encodeURIComponent(uri)}&format=${encodeURIComponent(format)}`;
}

/**
 * Turn one batch into rows the grouping logic already understands.
 *
 * `kind` describes the OUTPUT. A prompt-to-image batch is kind "image" with
 * input_text set, so the input and output halves are read independently from
 * the per-pair fields rather than both inferred from kind. That asymmetry has
 * caused a bug on each side of this contract already.
 */
export function rowsFromBatch(batch: SampleBatch, model: string, modelHash: string): SampleRow[] {
  return batch.pairs.map((pair) => {
    const inputURI = pair.input_uri;
    const outputURI = pair.output_uri;
    return {
      sampleSet: batch.sample_set,
      kind: batch.kind,
      // An image input has no text to label it, so the step stands in.
      // `null` means unconditional generation, which the grouping logic
      // already renders as "no input" rather than as an empty string —
      // hence the `in` test rather than a truthiness check, so an input
      // logged as "" stays distinct from one that was never logged.
      input:
        "input_text" in pair
          ? (pair.input_text ?? null)
          : inputURI !== undefined
            ? `step ${pair.step}`
            : null,
      inputUrl: inputURI !== undefined ? sampleBlobUrl(inputURI) : undefined,
      model,
      modelHash,
      step: pair.step,
      output: "output_text" in pair ? pair.output_text : undefined,
      outputUrl: outputURI !== undefined ? sampleBlobUrl(outputURI) : undefined,
    };
  });
}
