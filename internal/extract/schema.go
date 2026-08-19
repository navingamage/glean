package extract

// outputSchema is the shape the extraction model must return. It is written out
// as a literal rather than reflected off the model.Settlement struct on purpose:
// the wire contract with a language model is a prompt, and the field
// descriptions below are doing as much work as the types. Deriving it from Go
// structs would mean the descriptions live somewhere else, or nowhere.
var outputSchema = map[string]any{
	"type":                 "object",
	"additionalProperties": false,
	"required": []string{
		"is_class_action", "title", "stage", "summary", "confidence",
		"criteria", "claim_deadline", "opt_out_deadline", "claims_open_on",
		"settlement_fund", "payout_low", "payout_high", "estimated_class_size",
		"proof_required", "effort_minutes", "claim_url", "administrator",
		"defendants", "jurisdiction", "class_text", "case_name", "court",
		"payout_note", "proof_note",
	},
	"properties": map[string]any{
		"is_class_action": map[string]any{
			"type":        "boolean",
			"description": "False if this page is not about a specific class proceeding (a practice-area page, a news index, a privacy policy). When false, every other field may be empty.",
		},
		"title":     map[string]any{"type": "string", "description": "Short plain-language name, e.g. 'Loblaw packaged bread price-fixing'."},
		"case_name": map[string]any{"type": "string", "description": "Style of cause if stated, e.g. 'Smith v. Loblaw Companies Ltd.'. Empty if not stated."},
		"court":     map[string]any{"type": "string", "description": "Court seized of the matter, e.g. 'Ontario Superior Court of Justice'. Empty if not stated."},
		"defendants": map[string]any{
			"type": "array", "items": map[string]any{"type": "string"},
			"description": "Named defendants.",
		},
		"jurisdiction": map[string]any{
			"type": "array", "items": map[string]any{"type": "string"},
			"description": "Two-letter province codes the class covers, or ['CA'] for a nationwide class. Use ['CA'] when the notice says Canada or does not restrict by province.",
		},
		"stage": map[string]any{
			"type":        "string",
			"enum":        []string{"filed", "certified", "settled", "claims-open", "claims-closed", "concluded", "unknown"},
			"description": "Use 'claims-open' only when a claim can be filed right now: there is a claim form or portal and the deadline has not passed. Use 'settled' when a settlement is approved but the claims process has not opened. Use 'claims-closed' when the deadline has passed.",
		},
		"summary": map[string]any{
			"type":        "string",
			"description": "Two or three sentences: what the defendant is alleged to have done, and what a class member gets. No marketing language.",
		},
		"class_text": map[string]any{
			"type":        "string",
			"description": "The class definition, quoted verbatim from the page. Empty if the page does not state one.",
		},
		"criteria": map[string]any{
			"type":        "array",
			"description": "The class definition decomposed into testable clauses. Extract only what the page states. If the page gives no class definition, return an empty array rather than guessing.",
			"items": map[string]any{
				"type":                 "object",
				"additionalProperties": false,
				"required":             []string{"kind", "entity", "label", "aliases", "from", "until", "text", "group", "prevalence"},
				"properties": map[string]any{
					"kind": map[string]any{
						"type": "string",
						"enum": []string{"residency", "purchase", "account", "service", "employment", "vehicle", "device", "medical", "other"},
					},
					"entity": map[string]any{
						"type":        "string",
						"description": "The thing being tested, lowercase, hyphenated: 'loblaw', 'cibc', 'facebook'. For residency use a two-letter province code or 'CA'.",
					},
					"label": map[string]any{"type": "string", "description": "How a person would say it: 'No Frills', 'CIBC', 'Ontario'."},
					"aliases": map[string]any{
						"type": "array", "items": map[string]any{"type": "string"},
						"description": "Other names for the same thing, especially banner or brand names of the same parent.",
					},
					"from":  map[string]any{"type": "string", "description": "Start of the class period for this clause as YYYY-MM-DD, or empty if open-ended."},
					"until": map[string]any{"type": "string", "description": "End of the class period as YYYY-MM-DD, or empty if open-ended or ongoing."},
					"text":  map[string]any{"type": "string", "description": "The clause as the page words it."},
					"group": map[string]any{
						"type":        "string",
						"description": "Clauses that are ALTERNATIVES to each other share a group name; satisfying any one of them satisfies the group. A class covering purchases at any of several banner stores is one group. Leave empty when the clause is independently required.",
					},
					"prevalence": map[string]any{
						"type":        "number",
						"description": "Your estimate, between 0 and 1, of the fraction of Canadian adults who satisfy this clause. 'Resident in Canada' is 1.0; 'shopped at a major grocery chain over twenty years' is around 0.85; 'was prescribed a specific recalled drug' is around 0.001. Used only to decide which question to ask a user first.",
					},
				},
			},
		},
		"claims_open_on":       map[string]any{"type": "string", "description": "YYYY-MM-DD the claims process opens or opened. Empty if unstated."},
		"claim_deadline":       map[string]any{"type": "string", "description": "YYYY-MM-DD final date to submit a claim. Empty if unstated. Do not infer one."},
		"opt_out_deadline":     map[string]any{"type": "string", "description": "YYYY-MM-DD deadline to opt out. Empty if unstated."},
		"settlement_fund":      map[string]any{"type": "number", "description": "Total settlement in Canadian dollars, 0 if unstated."},
		"payout_low":           map[string]any{"type": "number", "description": "Lowest stated per-claimant amount in dollars, 0 if unstated."},
		"payout_high":          map[string]any{"type": "number", "description": "Highest stated per-claimant amount in dollars, 0 if unstated."},
		"payout_note":          map[string]any{"type": "string", "description": "How the payout is calculated, if the page explains it."},
		"estimated_class_size": map[string]any{"type": "integer", "description": "Your estimate of how many people are in the class, 0 if you have no basis for one. Only used to turn a fund total into a per-person figure."},
		"proof_required":       map[string]any{"type": "boolean", "description": "True if a claimant must supply receipts, records or documents beyond attesting to their own membership."},
		"proof_note":           map[string]any{"type": "string", "description": "What specifically has to be produced."},
		"effort_minutes":       map[string]any{"type": "integer", "description": "How long filing a claim would realistically take someone who has the information to hand. An attestation-only web form is 5 to 10. A form needing documents you must dig up is 45 or more."},
		"claim_url":            map[string]any{"type": "string", "description": "The official claim portal or claim form URL, if the page links one. Empty otherwise. Never invent a URL."},
		"administrator":        map[string]any{"type": "string", "description": "Claims administrator, e.g. 'RicePoint', 'Verita', 'Epiq'. Empty if unstated."},
		"confidence":           map[string]any{"type": "number", "description": "0 to 1: how much of this record is stated on the page rather than inferred by you."},
	},
}
