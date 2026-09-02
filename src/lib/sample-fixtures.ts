/**
 * Stand-in sample data for the grouping prototype.
 *
 * Shaped exactly like what `alidade_callbacks.log_samples` writes, so the
 * grouping logic here is exercised against the real field set rather than a
 * convenient one:
 *
 *   tags       alidade.kind=sample, alidade.sample_set, alidade.model_run_hash
 *   sequences  sample/<set>/input  and  sample/<set>/output, paired by step
 *
 * Two properties of the real data are deliberately preserved because they are
 * what make the grouping question non-trivial:
 *
 *  - **Inputs are not unique.** The same prompt appears at two steps (the docs
 *    call out "the same prompt at two temperatures" as normal), so `input` and
 *    `step` are genuinely different axes rather than two names for one.
 *  - **Not every set covers every model.** `denoise` has one model only. A
 *    grouping order that assumes a full grid produces empty cells.
 */

export type SampleKind = "text" | "image";

/** One (input, output) pair — one step of one sample_set on one model. */
export interface SampleRow {
  sampleSet: string;
  kind: SampleKind;
  /** Prompt text for text inputs; label for image inputs; null when the
   *  generation was unconditional. */
  input: string | null;
  /** Present when the input is itself an image (denoising, style transfer).
   *  Lab only: the workbench draws tiles from a seed. Real samples carry
   *  `inputUrl`. */
  inputSeed?: number;
  /** Hub URL for a real input image. Wins over `inputSeed` when both are
   *  set, which never happens outside a test. */
  inputUrl?: string;
  /** Run name, as shown on Training and Eval. */
  model: string;
  /** alidade.model_run_hash — the join key, never typed by a human. */
  modelHash: string;
  step: number;
  /** Text output. */
  output?: string;
  /** Image output. Lab only — see `inputSeed`. */
  outputSeed?: number;
  /** Hub URL for a real output image. */
  outputUrl?: string;
}

/** Colour per model, standing in for the hub's shared runColors map so a run
 *  looks the same here as on Training and Eval. */
export const MODEL_COLORS: Record<string, string> = {
  "latent-bert-256": "oklch(0.55 0.11 245)",
  "latent-bert-512": "oklch(0.62 0.16 145)",
  "gan-baseline": "oklch(0.68 0.15 40)",
  "unet-sm-v3": "oklch(0.58 0.14 300)",
  "latent-bert-1b": "oklch(0.52 0.13 20)",
  "sd-turbo-ft": "oklch(0.6 0.12 190)",
  "unet-lg-v3": "oklch(0.5 0.12 330)",
  "restormer-ft": "oklch(0.66 0.13 100)",
};

export const MODEL_HASHES: Record<string, string> = {
  "latent-bert-256": "a71e4c09d2b8",
  "latent-bert-512": "c04b8fa1e775",
  "gan-baseline": "3f9c1a2b7e40",
  "unet-sm-v3": "9d21b7e4f0aa",
  "latent-bert-1b": "5e8032c6ab19",
  "sd-turbo-ft": "b6470fd1c3e2",
  "unet-lg-v3": "7c19ae5b804d",
  "restormer-ft": "d3f5620ba9c7",
};

const TEXT_PROMPTS = ["The capital of France is", "def fib(n):", "Summarise in one line:"];

const TEXT_COMPLETIONS: Record<string, string[]> = {
  "latent-bert-256": [
    " Paris, which sits on the Seine.",
    "\n    return n",
    " The paper is about attention.",
  ],
  "latent-bert-512": [
    " Paris. It has been the seat of government since 987.",
    "\n    return n if n < 2 else fib(n-1) + fib(n-2)",
    " A transformer variant that halves KV-cache memory.",
  ],
  "latent-bert-1b": [
    " Paris, the largest city in France and its administrative centre.",
    "\n    if n < 2:\n        return n\n    return fib(n-1) + fib(n-2)",
    " Halves KV-cache memory by sharing projections across heads.",
  ],
};

const IMAGE_PROMPTS = ["a golden retriever", "a lighthouse at dusk", "a cracked ceramic bowl"];

function textRows(): SampleRow[] {
  const rows: SampleRow[] = [];
  for (const model of ["latent-bert-256", "latent-bert-512", "latent-bert-1b"]) {
    TEXT_PROMPTS.forEach((prompt, i) => {
      rows.push({
        sampleSet: "sentence-completion",
        kind: "text",
        input: prompt,
        model,
        modelHash: MODEL_HASHES[model],
        step: i,
        output: TEXT_COMPLETIONS[model][i],
      });
    });
    // The repeated-prompt case: same input, later step, different sampling
    // temperature. This is why `input` and `step` cannot be collapsed.
    rows.push({
      sampleSet: "sentence-completion",
      kind: "text",
      input: TEXT_PROMPTS[0],
      model,
      modelHash: MODEL_HASHES[model],
      step: 3,
      output:
        model === "latent-bert-256"
          ? " Paris — a city of about two million."
          : model === "latent-bert-512"
            ? " Paris, though the largest metro area is Île-de-France."
            : " Paris. Population roughly 2.1 million intra-muros.",
    });
  }
  return rows;
}

function imageRows(): SampleRow[] {
  const rows: SampleRow[] = [];
  let seed = 100;
  for (const model of ["latent-bert-512", "gan-baseline", "sd-turbo-ft", "latent-bert-1b"]) {
    IMAGE_PROMPTS.forEach((prompt, i) => {
      rows.push({
        sampleSet: "faces",
        kind: "image",
        input: prompt,
        model,
        modelHash: MODEL_HASHES[model],
        step: i,
        outputSeed: seed++,
      });
    });
  }
  return rows;
}

function denoiseRows(): SampleRow[] {
  // Image in, image out, across several models — the case the grid has to
  // serve and could not be judged on while only one model was present.
  //
  // `unet-lg-v3` deliberately skips one frame. Not every model is run on
  // every input in practice, and a layout that assumes a full grid hides
  // that rather than showing it.
  const rows: SampleRow[] = [];
  const models = ["unet-sm-v3", "unet-lg-v3", "restormer-ft"];
  models.forEach((model, m) => {
    [0, 1, 2].forEach((step) => {
      if (model === "unet-lg-v3" && step === 1) return;
      rows.push({
        sampleSet: "denoise",
        kind: "image" as const,
        input: `frame ${step + 1}`,
        // The SAME input image across models — that is the whole point of a
        // denoising comparison, so the seed depends on the frame, not the run.
        inputSeed: 300 + step,
        model,
        modelHash: MODEL_HASHES[model],
        step,
        outputSeed: 400 + m * 10 + step,
      });
    });
  });
  return rows;
}

export const SAMPLE_ROWS: SampleRow[] = [...textRows(), ...imageRows(), ...denoiseRows()];
