/** Entity and province normalization, ported from the Go implementation. */

/**
 * Names the same entity trades under, keyed by canonical form. This is the small
 * amount of Canadian-specific knowledge the matcher needs, and it earns its
 * keep: a bread price-fixing class is defined by banner names, not by the parent
 * company, so without it a Loblaws-shaped profile misses a No Frills-shaped
 * class definition entirely.
 */
const ALIASES: Record<string, string[]> = {
  loblaw: ["loblaws", "no-frills", "nofrills", "zehrs", "your-independent-grocer",
    "independent-grocer", "real-canadian-superstore", "superstore", "fortinos",
    "valu-mart", "provigo", "maxi", "atlantic-superstore", "dominion",
    "shoppers-drug-mart", "wholesale-club"],
  sobeys: ["safeway", "foodland", "iga", "freshco", "thrifty-foods", "empire", "needs", "farm-boy"],
  metro: ["food-basics", "super-c", "jean-coutu"],
  walmart: ["walmart-canada", "great-value"],
  costco: ["costco-wholesale", "kirkland-signature"],
  "canadian-tire": ["canadiantire", "sportchek", "mark-s", "marks-work-wearhouse", "partsource"],
  "giant-tiger": ["gianttiger"],

  cibc: ["canadian-imperial-bank-of-commerce", "simplii", "simplii-financial",
    "renaissance-investments", "renaissance"],
  rbc: ["royal-bank", "royal-bank-of-canada", "rbc-royal-bank"],
  td: ["toronto-dominion", "toronto-dominion-bank", "td-bank", "td-canada-trust"],
  bmo: ["bank-of-montreal", "bmo-bank-of-montreal", "bmo-harris", "bmo-nesbitt-burns"],
  scotiabank: ["bank-of-nova-scotia", "scotia", "tangerine", "scotia-itrade"],
  desjardins: ["caisse-desjardins", "desjardins-group"],
  "national-bank": ["banque-nationale", "national-bank-of-canada"],
  "eq-bank": ["equitable-bank"],
  visa: ["visa-canada"],
  mastercard: ["mastercard-canada"],
  amex: ["american-express", "american-express-canada"],

  rogers: ["fido", "chatr", "rogers-communications", "cityfone"],
  bell: ["bell-canada", "bce", "virgin-plus", "virgin-mobile", "lucky-mobile", "bell-mts", "bell-mobility"],
  telus: ["koodo", "public-mobile", "telus-mobility"],
  shaw: ["freedom-mobile", "shaw-communications"],
  videotron: ["fizz", "quebecor"],

  facebook: ["meta", "meta-platforms", "instagram", "whatsapp"],
  google: ["alphabet", "youtube", "google-canada"],
  apple: ["apple-canada", "itunes", "app-store"],
  amazon: ["amazon-canada", "amazon-ca", "prime"],
  uber: ["uber-eats", "ubereats", "uber-canada"],
  doordash: ["door-dash"],
  ticketmaster: ["live-nation"],

  "air-canada": ["aircanada", "air-canada-rouge", "rouge"],
  westjet: ["west-jet", "swoop"],
  flair: ["flair-airlines"],

  "sun-life": ["sunlife", "sun-life-financial"],
  manulife: ["manufacturers-life", "john-hancock"],
  intact: ["intact-insurance", "belairdirect", "belair-direct"],
  aviva: ["aviva-canada"],

  cra: ["canada-revenue-agency", "revenue-canada"],
  caf: ["canadian-armed-forces", "department-of-national-defence", "dnd"],
};

const ALIAS_INDEX: Map<string, string> = (() => {
  const m = new Map<string, string>();
  for (const [canon, alts] of Object.entries(ALIASES)) {
    m.set(canon, canon);
    for (const a of alts) m.set(a, canon);
  }
  return m;
})();

/**
 * Parts of a registered name that carry no identity. "-canada" is here because
 * many defendants are the Canadian subsidiary of a foreign parent and the class
 * definition and the profile rarely agree on whether to say so.
 */
const CORPORATE_SUFFIXES = [
  "-incorporated", "-inc", "-limited", "-ltd", "-ltee",
  "-corporation", "-corp", "-companies", "-company", "-co",
  "-holdings", "-group", "-llc", "-llp", "-lp", "-plc", "-ulc",
  "-of-canada", "-canada", "-international",
];

function slug(s: string): string {
  return s.toLowerCase().replace(/[^a-z0-9]+/g, "-").replace(/^-+|-+$/g, "");
}

function trimCorporateSuffix(key: string): string {
  for (const suffix of CORPORATE_SUFFIXES) {
    if (key.endsWith(suffix)) {
      const trimmed = key.slice(0, -suffix.length);
      if (trimmed) return trimmed;
    }
  }
  return key;
}

/**
 * Reduce a company or product name to a comparison key.
 *
 * Suffixes are stripped one at a time and the alias table is consulted after
 * every strip, never only at the end. Checking as we go is what keeps
 * "Air Canada Inc." from being shortened past its own name into "air": the key
 * "air-canada" is recognized the moment "-inc" comes off, and nothing further
 * is removed.
 */
export function normalizeEntity(s: string): string {
  let key = slug(s);
  for (;;) {
    const canon = ALIAS_INDEX.get(key);
    if (canon) return canon;
    const next = trimCorporateSuffix(key);
    if (next === key) return key;
    key = next;
  }
}

export const PROVINCES: Record<string, string> = {
  AB: "Alberta", BC: "British Columbia", MB: "Manitoba",
  NB: "New Brunswick", NL: "Newfoundland and Labrador", NS: "Nova Scotia",
  NT: "Northwest Territories", NU: "Nunavut", ON: "Ontario",
  PE: "Prince Edward Island", QC: "Quebec", SK: "Saskatchewan",
  YT: "Yukon", CA: "Canada (nationwide)",
};

const PROVINCE_ALIASES: Record<string, string> = {
  alberta: "AB", ab: "AB",
  "british-columbia": "BC", bc: "BC", "colombie-britannique": "BC",
  manitoba: "MB", mb: "MB",
  "new-brunswick": "NB", nb: "NB", "nouveau-brunswick": "NB",
  newfoundland: "NL", "newfoundland-and-labrador": "NL", nl: "NL", nfld: "NL",
  "nova-scotia": "NS", ns: "NS",
  "northwest-territories": "NT", nt: "NT", nwt: "NT",
  nunavut: "NU", nu: "NU",
  ontario: "ON", on: "ON", ont: "ON",
  "prince-edward-island": "PE", pe: "PE", pei: "PE",
  quebec: "QC", qc: "QC", que: "QC",
  saskatchewan: "SK", sk: "SK", sask: "SK",
  yukon: "YT", yt: "YT",
  canada: "CA", ca: "CA", nationwide: "CA", national: "CA",
  "all-of-canada": "CA", "canada-wide": "CA",
};

export function normalizeProvince(s: string): string {
  const key = normalizeEntity(s);
  if (PROVINCE_ALIASES[key]) return PROVINCE_ALIASES[key]!;
  const upper = s.trim().toUpperCase();
  return PROVINCES[upper] ? upper : "";
}

/**
 * Repairs hrefs of the form "https://https://example.com/x", produced by content
 * management systems that prepend a scheme to a field an author already filled
 * in with one. Left alone they resolve to a host of "https:" and quietly vanish
 * from the candidate list, which is a bad way to lose a settlement page.
 */
function stripDoubledScheme(raw: string): string {
  let out = raw;
  for (;;) {
    const lower = out.toLowerCase();
    if (lower.startsWith("https://https://") || lower.startsWith("https://http://")) {
      out = out.slice("https://".length);
    } else if (lower.startsWith("http://https://") || lower.startsWith("http://http://")) {
      out = out.slice("http://".length);
    } else {
      return out;
    }
  }
}

/**
 * Cleans a URL without changing where it points. The scheme and host are left
 * exactly as the site wrote them: rewriting http to https would quietly lose
 * every site that has not migrated, and stripping "www" breaks hosts that only
 * answer on it. This is the form that gets fetched.
 */
export function canonicalURL(raw: string): string {
  const cleaned = stripDoubledScheme(raw.trim());
  let u: URL;
  try {
    u = new URL(cleaned);
  } catch {
    return cleaned;
  }
  u.hash = "";
  for (const k of [...u.searchParams.keys()]) {
    const lk = k.toLowerCase();
    if (lk.startsWith("utm_") || lk === "fbclid" || lk === "gclid" || lk === "ref") {
      u.searchParams.delete(k);
    }
  }
  if (u.pathname !== "/" && u.pathname.endsWith("/")) {
    u.pathname = u.pathname.replace(/\/+$/, "");
  }
  return u.toString();
}

/**
 * The aggressive normalization: one settlement reached over http and https, with
 * and without "www", is one settlement. Nothing fetches this form; it exists so
 * two spellings collapse to one record and source patterns can be written once.
 */
export function identityURL(raw: string): string {
  let u: URL;
  try {
    u = new URL(canonicalURL(raw));
  } catch {
    return raw.trim();
  }
  u.protocol = "https:";
  u.hostname = u.hostname.toLowerCase().replace(/^www\./, "");
  return u.toString();
}

/** A stable id from the identity URL, so a re-scan updates rather than duplicates. */
export async function newID(rawURL: string): Promise<string> {
  const data = new TextEncoder().encode(identityURL(rawURL));
  const digest = await crypto.subtle.digest("SHA-256", data);
  return [...new Uint8Array(digest).slice(0, 8)]
    .map((b) => b.toString(16).padStart(2, "0")).join("");
}

export async function sha256Hex(s: string): Promise<string> {
  const digest = await crypto.subtle.digest("SHA-256", new TextEncoder().encode(s));
  return [...new Uint8Array(digest)].map((b) => b.toString(16).padStart(2, "0")).join("");
}
