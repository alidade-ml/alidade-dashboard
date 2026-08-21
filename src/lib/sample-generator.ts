/**
 * Procedural sample data, so a layout can be stressed instead of admired.
 *
 * The hand-written fixtures answered "does this work on the data I chose",
 * which is the question a prototype always passes. The counts that actually
 * decide whether a view holds up — how many models, how many inputs, how many
 * repeats of the same prompt — need to be turnable while looking at it.
 *
 * Deliberately keeps the two awkward properties of real sample data that make
 * grouping non-trivial: inputs repeat (the same prompt sampled twice), and
 * coverage is ragged (not every model is run on every input).
 */
import { MODEL_COLORS, MODEL_HASHES, type SampleRow } from "./sample-fixtures.ts";

export interface FixtureShape {
  sets: number;
  models: number;
  /** Distinct inputs within each sample set. */
  inputs: number;
  /** Samples per (input, model) — the repeated-prompt case at >1. */
  repeats: number;
  /** Leave a hole in the grid: one model skips one input per set. */
  ragged: boolean;
  kind: "text" | "image" | "mixed";
}

export const DEFAULT_SHAPE: FixtureShape = {
  sets: 3,
  models: 3,
  inputs: 3,
  repeats: 1,
  ragged: true,
  kind: "mixed",
};

export const MODEL_NAMES = Object.keys(MODEL_COLORS);

const SET_NAMES = [
  "sentence-completion",
  "faces",
  "denoise",
  "captions",
  "summaries",
  "style-transfer",
  "code-review",
  "translations",
];

const PROMPTS = [
  "The capital of France is",
  "def fib(n):",
  "Summarise in one line:",
  "a golden retriever",
  "a lighthouse at dusk",
  "a cracked ceramic bowl",
  "Translate to German: good morning",
  "Explain gradient clipping",
  "a folded paper crane",
  "rain on a window",
  "Rewrite this as a haiku",
  "What went wrong in this trace?",
];

const COMPLETIONS = [
  " Paris, which sits on the Seine.",
  "\n    return n if n < 2 else fib(n-1) + fib(n-2)",
  " A transformer variant that halves KV-cache memory.",
  " Guten Morgen",
  " Rescale gradients whose norm exceeds a threshold, before the optimiser step.",
  " The run died in the eval hook, not in training.",
];

/** Which payload a set uses. `mixed` cycles so every combination of text and
 *  image input/output appears once the set count is high enough. */
function payloadFor(setIndex: number, kind: FixtureShape["kind"]) {
  if (kind === "text") return { imageIn: false, imageOut: false };
  if (kind === "image") return { imageIn: true, imageOut: true };
  return [
    { imageIn: false, imageOut: false }, // completion
    { imageIn: false, imageOut: true }, // prompt to image
    { imageIn: true, imageOut: true }, // denoise
    { imageIn: true, imageOut: false }, // captioning
  ][setIndex % 4];
}

export function generateSamples(shape: FixtureShape): SampleRow[] {
  const rows: SampleRow[] = [];
  const models = MODEL_NAMES.slice(0, Math.max(1, Math.min(shape.models, MODEL_NAMES.length)));

  for (let s = 0; s < shape.sets; s++) {
    const setName = SET_NAMES[s % SET_NAMES.length] + (s >= SET_NAMES.length ? `-${s}` : "");
    const { imageIn, imageOut } = payloadFor(s, shape.kind);

    for (let m = 0; m < models.length; m++) {
      const model = models[m];
      let step = 0;

      for (let i = 0; i < shape.inputs; i++) {
        // Ragged coverage: the last model skips one input per set, so the
        // grid has a hole and "not sampled" has something to render.
        if (shape.ragged && models.length > 1 && m === models.length - 1 && i === 1) continue;

        for (let rep = 0; rep < shape.repeats; rep++) {
          const label = imageIn ? `frame ${i + 1}` : PROMPTS[(s * 3 + i) % PROMPTS.length];
          rows.push({
            sampleSet: setName,
            kind: imageOut ? "image" : "text",
            input: label,
            // The same input image across models — the point of a restoration
            // comparison — so the seed depends on the input, not the run.
            inputSeed: imageIn ? 1000 + s * 50 + i : undefined,
            model,
            modelHash: MODEL_HASHES[model] ?? "0".repeat(12),
            step,
            output: imageOut
              ? undefined
              : COMPLETIONS[(m * 2 + i + rep) % COMPLETIONS.length] +
                (rep > 0 ? ` (sample ${rep + 1})` : ""),
            outputSeed: imageOut ? 5000 + s * 500 + m * 50 + i * 5 + rep : undefined,
          });
          step += 1;
        }
      }
    }
  }
  return rows;
}
