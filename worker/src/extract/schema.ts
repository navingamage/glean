/**
 * The shape the extraction model must return.
 *
 * Written as a literal rather than derived from the types on purpose: the wire
 * contract with a language model is a prompt, and the field descriptions are
 * doing as much work as the types. Deriving it would put the descriptions
 * somewhere else, or nowhere.
 */
export const OUTPUT_SCHEMA = {
  type: "object",
  additionalProperties: false,
  required: [
    "is_class_action", "title", "stage", "summary", "confidence",
    "criteria", "claim_deadline", "opt_out_deadline", "claims_open_on",
    "settlement_fund", "payout_low", "payout_high", "estimated_class_size",
    "proof_required", "effort_minutes", "claim_url", "administrator",
    "defendants", "jurisdiction", "class_text", "case_name", "court",
    "payout_note", "proof_note",
  ],
  properties: {
    is_class_action: {
      type: "boolean",
      description: "False if this page is not about a specific class proceeding (a practice-area page, a news index, a privacy policy). When false, every other field may be empty.",
    },
    title: { type: "string", description: "Short plain-language name, e.g. 'Loblaw packaged bread price-fixing'." },
    case_name: { type: "string", description: "Style of cause if stated, e.g. 'Smith v. Loblaw Companies Ltd.'. Empty if not stated." },
    court: { type: "string", description: "Court seized of the matter. Empty if not stated." },
    defendants: { type: "array", items: { type: "string" }, description: "Named defendants." },
    jurisdiction: {
      type: "array", items: { type: "string" },
      description: "Two-letter province codes the class covers, or ['CA'] for a nationwide class. Use ['CA'] when the notice says Canada or does not restrict by province.",
    },
    stage: {
      type: "string",
      enum: ["filed", "certified", "settled", "claims-open", "claims-closed", "concluded", "unknown"],
      description: "Use 'claims-open' only when a claim can be filed right now: there is a claim form or portal and the deadline has not passed. Use 'settled' when a settlement is approved but the claims process has not opened. Use 'claims-closed' when the deadline has passed.",
    },
    summary: {
      type: "string",
      description: "Two or three sentences: what the defendant is alleged to have done, and what a class member gets. No marketing language.",
    },
    class_text: {
      type: "string",
      description: "The class definition, quoted verbatim from the page. Empty if the page does not state one.",
    },
    criteria: {
      type: "array",
      description: "The class definition decomposed into testable clauses. Extract only what the page states. If the page gives no class definition, return an empty array rather than guessing.",
      items: {
        type: "object",
        additionalProperties: false,
        required: ["kind", "entity", "label", "aliases", "from", "until", "text", "group", "prevalence"],
        properties: {
          kind: {
            type: "string",
            enum: ["residency", "purchase", "account", "service", "employment", "vehicle", "device", "medical", "other"],
          },
          entity: {
            type: "string",
            description: "The thing being tested, lowercase, hyphenated: 'loblaw', 'cibc', 'facebook'. For residency use a two-letter province code or 'CA'.",
          },
          label: { type: "string", description: "How a person would say it: 'No Frills', 'CIBC', 'Ontario'." },
          aliases: {
            type: "array", items: { type: "string" },
            description: "Other names for the same thing, especially banner or brand names of the same parent.",
          },
          from: { type: "string", description: "Start of the class period for this clause as YYYY-MM-DD, or empty if open-ended." },
          until: { type: "string", description: "End of the class period as YYYY-MM-DD, or empty if open-ended or ongoing." },
          text: { type: "string", description: "The clause as the page words it." },
          group: {
            type: "string",
            description: "Clauses that are ALTERNATIVES to each other share a group name; satisfying any one satisfies the group. A class covering purchases at any of several banner stores is one group. Leave empty when the clause is independently required.",
          },
          prevalence: {
            type: "number",
            description: "Your estimate, between 0 and 1, of the fraction of Canadian adults who satisfy this clause. 'Resident in Canada' is 1.0; 'shopped at a major grocery chain over twenty years' is around 0.85; 'was prescribed a specific recalled drug' is around 0.001. Used only to decide which question to ask first.",
          },
        },
      },
    },
    claims_open_on: { type: "string", description: "YYYY-MM-DD the claims process opens or opened. Empty if unstated." },
    claim_deadline: { type: "string", description: "YYYY-MM-DD final date to submit a claim. Empty if unstated. Do not infer one." },
    opt_out_deadline: { type: "string", description: "YYYY-MM-DD deadline to opt out. Empty if unstated." },
    settlement_fund: { type: "number", description: "Total settlement in Canadian dollars, 0 if unstated." },
    payout_low: { type: "number", description: "Lowest stated per-claimant amount in dollars, 0 if unstated." },
    payout_high: { type: "number", description: "Highest stated per-claimant amount in dollars, 0 if unstated." },
    payout_note: { type: "string", description: "How the payout is calculated, if the page explains it." },
    estimated_class_size: { type: "integer", description: "Your estimate of how many people are in the class, 0 if you have no basis for one. Only used to turn a fund total into a per-person figure." },
    proof_required: { type: "boolean", description: "True if a claimant must supply receipts, records or documents beyond attesting to their own membership." },
    proof_note: { type: "string", description: "What specifically has to be produced." },
    effort_minutes: { type: "integer", description: "How long filing a claim would realistically take someone with the information to hand. An attestation-only web form is 5 to 10. A form needing documents you must dig up is 45 or more." },
    claim_url: { type: "string", description: "The official claim portal or claim form URL, if the page links one. Empty otherwise. Never invent a URL." },
    administrator: { type: "string", description: "Claims administrator, e.g. 'RicePoint', 'Verita', 'Epiq'. Empty if unstated." },
    confidence: { type: "number", description: "0 to 1: how much of this record is stated on the page rather than inferred by you." },
  },
} as const;

export const SYSTEM_PROMPT = `You read notices of Canadian class proceedings and reduce them to a structured record.

You are working for one person who wants to know which settlements they can claim from, so the record has to be good enough to act on and honest enough not to waste their evening.

Rules:

1. Extract, do not infer. Every date, dollar figure and clause must be on the page. If a deadline is not stated, leave it empty. An invented deadline is worse than a missing one, because a missing one shows up as a question and an invented one shows up as a plan.

2. The class definition is the important part. Decompose it into clauses a person could answer yes or no about. "All persons resident in Canada who purchased packaged bread at a Loblaw banner store between January 1, 2001 and December 31, 2021" is a residency clause plus one group of purchase clauses, one per banner named, all sharing a group.

3. Alternatives share a group; requirements do not. Getting this backwards either demands that someone shopped at nine stores at once, or lets anyone who shopped anywhere into a class defined by residency. Read the conjunctions in the definition carefully.

4. List banner and brand names as aliases when a parent company is named, and vice versa. People remember the store on the sign, not the holding company on the receipt.

5. Distinguish "a settlement exists" from "you can claim today". stage is claims-open only when there is a live claim form or portal and the deadline has not passed.

6. Note honestly what proof is needed and how long the form takes. This is what decides whether a claim is worth filing.

7. Set is_class_action false for pages that are not about one specific proceeding, and stop there.

Prices and amounts are Canadian dollars unless the page says otherwise.

Reply with JSON matching the schema and nothing else.`;
