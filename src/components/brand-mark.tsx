import { cn } from "@/lib/utils";

/**
 * The alidade mark, inlined.
 *
 * Inlined rather than an <img> or background-image because the geometry reads
 * --alidade-mark-ink and --alidade-accent, and an <img> has no cascade — it would sit
 * at the brass fallback on every palette and in both themes. The var() fallbacks
 * below are brand.md's brass light values and only apply if the tokens are
 * missing entirely.
 *
 * The linework uses --alidade-mark-ink rather than --alidade-ink: in dark mode the
 * mark's coverage makes it read brighter than the wordmark at the same colour,
 * so it is measured down. In light the two are identical.
 *
 * One drawing at every size, minimum 16px. The embroidery cut is a separate
 * asset and is not used in the product. Geometry and draw order are specified in
 * docs/brand.md (alidade repo); the numbers are load-bearing, so change them
 * there first.
 */
export function BrandMark({ className }: { className?: string }) {
  return (
    <svg
      viewBox="0 0 64 64"
      role="img"
      aria-label="Alidade"
      strokeLinejoin="round"
      className={cn("h-6 w-6", className)}
    >
      <defs>
        {/* Reused three times: knocked out of the crescent, painted as ink off
            the crescent, and nowhere else. */}
        <g id="alidade-letter">
          <path d="M 32.8719 6.2233 L 22.1156 55.4700 L 16.8844 54.1300 L 31.1281 5.7767 Z" />
          <path d="M 32.8719 5.7767 L 47.1156 54.1300 L 41.8844 55.4700 L 31.1281 6.2233 Z" />
          <circle cx="32" cy="6" r="0.9" />
          <circle cx="19.5" cy="54.8" r="2.7" />
          <circle cx="44.5" cy="54.8" r="2.7" />
        </g>

        <mask id="alidade-crescent">
          <circle cx="32" cy="32" r="27.1" fill="#fff" />
          <circle cx="39" cy="25" r="23" fill="#000" />
          <use href="#alidade-letter" fill="#000" />
        </mask>

        {/* Black exactly on the mass: holds the ink letterform off it. Together
            with the knockout above, this is what produces the figure/ground
            flip — drop either and the letter hides behind the crescent. */}
        <mask id="alidade-offmass">
          <rect width="64" height="64" fill="#fff" />
          <circle cx="32" cy="32" r="27.1" fill="#000" />
          <circle cx="39" cy="25" r="23" fill="#fff" />
        </mask>

        <mask id="alidade-channel">
          <rect width="64" height="64" fill="#fff" />
          <g className="alidade-rule">
            <line
              x1="6.5682"
              y1="37.4057"
              x2="57.4318"
              y2="26.5943"
              stroke="#000"
              strokeWidth="5.2"
              strokeLinecap="round"
            />
          </g>
        </mask>
      </defs>

      <circle
        className="alidade-crescent"
        cx="32"
        cy="32"
        r="27.1"
        fill="var(--alidade-accent, #865900)"
        mask="url(#alidade-crescent)"
      />

      <g
        className="alidade-limb"
        mask="url(#alidade-channel)"
        fill="none"
        stroke="var(--alidade-mark-ink, #241D12)"
        strokeWidth="2.2"
        strokeLinecap="round"
      >
        <path pathLength="1" d="M 41.6346 56.1489 A 26 26 0 0 1 22.3654 56.1489" />
        <path pathLength="1" d="M 16.8280 53.1141 A 26 26 0 0 1 28.8314 6.1937" />
        <path pathLength="1" d="M 35.1686 6.1937 A 26 26 0 0 1 47.1720 53.1141" />
      </g>

      <g className="alidade-letter" mask="url(#alidade-offmass)">
        <use href="#alidade-letter" fill="var(--alidade-mark-ink, #241D12)" />
      </g>

      <g
        className="alidade-grads"
        stroke="var(--alidade-mark-ink, #241D12)"
        strokeWidth="1.5"
        strokeLinecap="round"
      >
        <line x1="50.6195" y1="21.2500" x2="54.5167" y2="19.0000" />
        <line x1="52.0720" y1="24.2950" x2="56.2731" y2="22.6824" />
        <line x1="53.0302" y1="27.5300" x2="57.4318" y2="26.5943" />
        <line x1="53.4705" y1="30.8747" x2="57.9644" y2="30.6392" />
        <line x1="53.3823" y1="34.2474" x2="57.8576" y2="34.7178" />
      </g>

      <g
        className="alidade-rule"
        fill="var(--alidade-mark-ink, #241D12)"
        stroke="var(--alidade-mark-ink, #241D12)"
        strokeLinecap="round"
      >
        <line x1="6.5682" y1="37.4057" x2="57.4318" y2="26.5943" strokeWidth="2.4" />
        <line x1="12.3321" y1="40.4744" x2="10.5857" y2="32.2580" strokeWidth="1.92" />
        <line x1="53.4143" y1="31.7420" x2="51.6679" y2="23.5256" strokeWidth="1.92" />
        <circle className="alidade-pivot" cx="32" cy="32" r="2.5" stroke="none" />
      </g>
    </svg>
  );
}

/**
 * The name beside the mark.
 *
 * brand.md's two sanctioned lockups both replace the word's first A with the
 * mark. This does not: the mark stays separate and the word is spelled in full.
 * The wordmark face carries the bare-chevron A's, so the letterform still reads
 * without the mark standing in for a letter.
 */
export function Wordmark({ className }: { className?: string }) {
  return <span className={cn("alidade-wordmark", className)}>Alidade</span>;
}
