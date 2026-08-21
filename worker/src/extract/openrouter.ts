/**
 * The OpenRouter client, and the two tiers it drives.
 *
 * Hand-rolled rather than an SDK because the surface in use is one POST with a
 * JSON body, and Workers has fetch built in.
 */
import { Criterion, Settlement, Stage, validKind } from "../model";
import { canonicalURL, newID, normalizeEntity, normalizeProvince } from "../normalize";
import { OUTPUT_SCHEMA, SYSTEM_PROMPT } from "./schema";

export const DEFAULT_CHEAP_MODEL = "google/gemini-2.5-flash-lite";
export const DEFAULT_PRIME_MODEL = "google/gemini-3.7-flash";

/**
 * OpenRouter direct. Overridable so the same calls can be sent through a proxy
 * that speaks the same protocol, which in practice means a Cloudflare AI
 * Gateway: its OpenRouter endpoint passes the body and the Authorization header
 * through untouched, so the model, the schema and the answer are the same and
 * only the observability changes. Kept as configuration rather than a code
 * change so that falling back to the direct URL when the proxy has a bad minute
 * is one `wrangler secret`, with no deploy.
 */
export const DEFAULT_BASE_URL = "https://openrouter.ai/api/v1";

/** Prime sends the whole notice; cheap sends less, because input dominates the bill. */
const PRIME_MAX_CHARS = 60_000;
const CHEAP_MAX_CHARS = 18_000;

export class NotClassAction extends Error {
  constructor() {
    super("page is not about a class proceeding");
  }
}

export interface Usage {
  calls: number;
  inTokens: number;
  outTokens: number;
  cost: number;
}

export function newUsage(): Usage {
  return { calls: 0, inTokens: 0, outTokens: 0, cost: 0 };
}

export function usageString(u: Usage, models: string): string {
  if (u.calls === 0) return `no model calls (${models})`;
  return `${u.calls} model call(s), ${u.inTokens} in / ${u.outTokens} out tokens, ` +
    `$${u.cost.toFixed(4)} (${models})`;
}

export type Tier = "cheap" | "prime";

/**
 * Which tier a page is worth.
 *
 * Reading a deadline is transcription: a small model does it as well as a large
 * one, and when it fails the date is visibly absent rather than quietly wrong.
 * Decomposing a class definition is not. A model that reads "bread at Loblaws,
 * No Frills or Zehrs" as three requirements rather than three alternatives
 * produces a settlement the reader is confidently told they do not qualify for,
 * and nobody ever finds out.
 */
const CLAIMABLE = [
  "file a claim", "submit a claim", "make a claim", "claim form",
  "claims administrator", "claims deadline", "claim deadline",
  "deadline to file", "claims process is open", "how to claim",
  "deposer une reclamation", "formulaire de reclamation",
  "opt out deadline", "opt-out deadline", "settlement approved",
  "court approved the settlement", "you may be entitled",
];

export function stakes(text: string, authoritative: boolean): Tier {
  if (authoritative) return "prime";
  const low = text.toLowerCase();
  let hits = 0;
  for (const phrase of CLAIMABLE) {
    if (low.includes(phrase)) {
      hits++;
      if (hits >= 2) return "prime";
    }
  }
  return "cheap";
}

/**
 * Whether a cheap extraction came back too thin to act on. Narrow on purpose: a
 * settlement you cannot claim from yet is allowed to be vague, but one open for
 * claims with no class definition is useless, and that is what the cheap tier
 * was measured actually getting wrong.
 */
export function underspecified(s: Settlement | undefined): boolean {
  if (!s) return false;
  return (s.stage === "claims-open" || s.stage === "settled") &&
    (!s.criteria || s.criteria.length === 0);
}

interface Wire {
  is_class_action: boolean;
  title: string; case_name: string; court: string;
  defendants: string[]; jurisdiction: string[];
  stage: string; summary: string; class_text: string;
  criteria: Array<{
    kind: string; entity: string; label: string; aliases: string[];
    from: string; until: string; text: string; group: string; prevalence: number;
  }>;
  claims_open_on: string; claim_deadline: string; opt_out_deadline: string;
  settlement_fund: number; payout_low: number; payout_high: number;
  payout_note: string; estimated_class_size: number;
  proof_required: boolean; proof_note: string; effort_minutes: number;
  claim_url: string; administrator: string; confidence: number;
}

export class Extractor {
  constructor(
    private key: string,
    readonly cheapModel: string,
    readonly primeModel: string,
    readonly usage: Usage = newUsage(),
    /** Where the chat completions endpoint lives, with no trailing slash. */
    readonly baseURL: string = DEFAULT_BASE_URL,
  ) {}

  /** True when calls are going somewhere other than OpenRouter itself. */
  proxied(): boolean {
    return this.baseURL !== DEFAULT_BASE_URL;
  }

  models(): string {
    return this.cheapModel === this.primeModel
      ? this.primeModel
      : `${this.cheapModel} / ${this.primeModel}`;
  }

  async extract(tier: Tier, pageURL: string, title: string, text: string): Promise<Settlement> {
    const model = tier === "prime" ? this.primeModel : this.cheapModel;
    const limit = tier === "prime" ? PRIME_MAX_CHARS : CHEAP_MAX_CHARS;
    const body = JSON.stringify({
      model,
      messages: [
        { role: "system", content: SYSTEM_PROMPT },
        { role: "user", content: `URL: ${pageURL}\nPage title: ${title}\n\n---\n${text.slice(0, limit)}` },
      ],
      response_format: {
        type: "json_schema",
        json_schema: { name: "settlement", strict: true, schema: OUTPUT_SCHEMA },
      },
      max_tokens: 8000,
      // Extraction is not a creative task, and a class definition that came back
      // differently on two runs of the same page would show up as a spurious
      // change in the log every night.
      temperature: 0,
      usage: { include: true },
    });

    const raw = await this.post(body, pageURL);
    let w: Wire;
    try {
      w = JSON.parse(raw) as Wire;
    } catch (e) {
      throw new Error(`extract ${pageURL}: decoding model output: ${e}`);
    }
    if (!w.is_class_action) throw new NotClassAction();
    return await toSettlement(w, pageURL);
  }

  /**
   * One request, retried on the failures worth retrying. A scan touches a few
   * dozen pages in a burst, so rate limiting is expected rather than
   * exceptional. A 4xx that is not 429 is a bug in the request, and retrying it
   * only spends money more slowly.
   */
  private async post(body: string, pageURL: string): Promise<string> {
    const attempts = 4;
    let lastErr = "";
    for (let attempt = 0; attempt < attempts; attempt++) {
      if (attempt > 0) {
        await new Promise((r) => setTimeout(r, 1000 * 2 ** attempt));
      }
      const resp = await fetch(`${this.baseURL}/chat/completions`, {
        method: "POST",
        headers: {
          Authorization: `Bearer ${this.key}`,
          "Content-Type": "application/json",
          "HTTP-Referer": "https://github.com/navingamage/glean",
          "X-Title": "glean",
        },
        body,
      });
      const payload = await resp.text();

      if (resp.status === 429 || resp.status >= 500) {
        lastErr = `${resp.status}: ${payload.slice(0, 200)}`;
        continue;
      }
      if (!resp.ok) {
        throw new Error(`extract ${pageURL}: ${resp.status}: ${payload.slice(0, 200)}`);
      }

      let out: any;
      try {
        out = JSON.parse(payload);
      } catch (e) {
        throw new Error(`extract ${pageURL}: ${e}`);
      }
      // OpenRouter reports some upstream failures inside a 200 body.
      if (out.error) {
        lastErr = String(out.error.message ?? out.error);
        continue;
      }
      const choice = out.choices?.[0];
      if (!choice) {
        lastErr = "no choices returned";
        continue;
      }
      if (choice.message?.refusal) {
        throw new Error(`extract ${pageURL}: model declined: ${choice.message.refusal}`);
      }
      this.usage.calls++;
      this.usage.inTokens += out.usage?.prompt_tokens ?? 0;
      this.usage.outTokens += out.usage?.completion_tokens ?? 0;
      this.usage.cost += out.usage?.cost ?? 0;
      if (choice.finish_reason === "length") {
        throw new Error(`extract ${pageURL}: output truncated`);
      }
      return choice.message?.content ?? "";
    }
    throw new Error(`extract ${pageURL}: after ${attempts} attempts: ${lastErr}`);
  }
}

async function toSettlement(w: Wire, pageURL: string): Promise<Settlement> {
  const criteria: Criterion[] = [];
  for (const c of w.criteria ?? []) {
    const kind = validKind(c.kind) ? c.kind : "other";
    let entity = c.entity;
    if (kind === "residency") {
      entity = normalizeProvince(entity) || normalizeProvince(c.label) || "";
    } else {
      entity = normalizeEntity(entity);
    }
    if (!entity) continue; // a clause with nothing to test is not a criterion
    criteria.push({
      kind, entity, label: (c.label ?? "").trim(), aliases: c.aliases ?? [],
      window: { from: c.from ?? "", until: c.until ?? "" },
      text: (c.text ?? "").trim(), group: (c.group ?? "").trim(),
      prevalence: c.prevalence ?? 0,
    });
  }

  const jurisdiction: string[] = [];
  for (const code of w.jurisdiction ?? []) {
    const p = normalizeProvince(code);
    if (p) jurisdiction.push(p);
  }

  // The claim URL is the one field a person clicks straight through to, so it is
  // checked rather than trusted.
  const claimURL = (w.claim_url ?? "").trim();
  const safeClaimURL = /^https?:\/\//i.test(claimURL) ? claimURL : "";

  let effort = w.effort_minutes ?? 0;
  if (effort <= 0) effort = w.proof_required ? 45 : 15;

  return {
    id: await newID(pageURL),
    title: (w.title ?? "").trim(),
    case_name: (w.case_name ?? "").trim(),
    court: (w.court ?? "").trim(),
    defendants: w.defendants ?? [],
    jurisdiction,
    stage: (w.stage || "unknown") as Stage,
    summary: (w.summary ?? "").trim(),
    class_text: (w.class_text ?? "").trim(),
    criteria,
    claims_open_on: w.claims_open_on ?? "",
    claim_deadline: w.claim_deadline ?? "",
    opt_out_deadline: w.opt_out_deadline ?? "",
    settlement_fund: w.settlement_fund ?? 0,
    payout_low: w.payout_low ?? 0,
    payout_high: w.payout_high ?? 0,
    payout_note: (w.payout_note ?? "").trim(),
    estimated_class_size: w.estimated_class_size ?? 0,
    proof_required: !!w.proof_required,
    proof_note: (w.proof_note ?? "").trim(),
    effort_minutes: effort,
    url: canonicalURL(pageURL),
    claim_url: safeClaimURL,
    administrator: (w.administrator ?? "").trim(),
    source_id: "",
    extractor: "llm",
    confidence: w.confidence ?? 0,
    first_seen: "",
    last_seen: "",
  };
}
