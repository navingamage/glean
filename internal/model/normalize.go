package model

import (
	"strings"
	"unicode"
)

// NormalizeEntity reduces a company or product name to a comparison key.
// Punctuation, corporate suffixes and case all vary between a court document, a
// law firm's case page and whatever the user typed into their profile, and none
// of that variation is meaningful.
//
// Suffixes are stripped one at a time and the alias table is consulted after
// every strip, never only at the end. Checking as we go is what keeps
// "Air Canada Inc." from being shortened past its own name into "air": the key
// "air-canada" is recognized the moment "-inc" comes off, and nothing further
// is removed.
func NormalizeEntity(s string) string {
	key := slug(s)
	for {
		if canon, ok := aliasIndex[key]; ok {
			return canon
		}
		next := trimCorporateSuffix(key)
		if next == key {
			return key
		}
		key = next
	}
}

// slug lowercases and reduces any run of non-alphanumerics to a single dash.
func slug(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	lastDash := true // suppresses a leading separator
	for _, r := range s {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(r)
			lastDash = false
		case !lastDash:
			b.WriteByte('-')
			lastDash = true
		}
	}
	return strings.Trim(b.String(), "-")
}

// corporateSuffixes are the parts of a registered name that carry no identity.
// "-canada" is here because a great many defendants are the Canadian subsidiary
// of a foreign parent and the class definition and the profile rarely agree on
// whether to say so.
var corporateSuffixes = []string{
	"-incorporated", "-inc", "-limited", "-ltd", "-ltee", "-ltee",
	"-corporation", "-corp", "-companies", "-company", "-co",
	"-holdings", "-group", "-llc", "-llp", "-lp", "-plc", "-ulc",
	"-of-canada", "-canada", "-international",
}

func trimCorporateSuffix(key string) string {
	for _, suffix := range corporateSuffixes {
		if trimmed := strings.TrimSuffix(key, suffix); trimmed != key && trimmed != "" {
			return trimmed
		}
	}
	return key
}

// aliases maps a canonical key to the other names the same entity trades under.
// This is the small amount of Canadian-specific knowledge the matcher needs, and
// it earns its keep: a bread price-fixing class is defined by banner names, not
// by the parent company, so without it a Loblaws-shaped profile misses a
// No Frills-shaped class definition entirely.
//
// It is a starting set, not an attempt at completeness. Anything not listed
// falls through to plain key comparison, which handles the ordinary case where
// both sides say the same word.
var aliases = map[string][]string{
	// Grocery banners, grouped by the parent whose conduct is usually at issue.
	"loblaw":        {"loblaws", "no-frills", "nofrills", "zehrs", "your-independent-grocer", "independent-grocer", "real-canadian-superstore", "superstore", "fortinos", "valu-mart", "provigo", "maxi", "atlantic-superstore", "dominion", "shoppers-drug-mart", "wholesale-club"},
	"sobeys":        {"safeway", "foodland", "iga", "freshco", "thrifty-foods", "empire", "needs", "farm-boy"},
	"metro":         {"food-basics", "super-c", "jean-coutu"},
	"walmart":       {"walmart-canada", "great-value"},
	"costco":        {"costco-wholesale", "kirkland-signature"},
	"canadian-tire": {"canadiantire", "sportchek", "mark-s", "marks-work-wearhouse", "partsource"},
	"giant-tiger":   {"gianttiger"},

	// Banks and card issuers.
	"cibc":          {"canadian-imperial-bank-of-commerce", "simplii", "simplii-financial", "renaissance-investments", "renaissance"},
	"rbc":           {"royal-bank", "royal-bank-of-canada", "rbc-royal-bank"},
	"td":            {"toronto-dominion", "toronto-dominion-bank", "td-bank", "td-canada-trust"},
	"bmo":           {"bank-of-montreal", "bmo-bank-of-montreal", "bmo-harris", "bmo-nesbitt-burns"},
	"scotiabank":    {"bank-of-nova-scotia", "scotia", "tangerine", "scotia-itrade"},
	"desjardins":    {"caisse-desjardins", "desjardins-group"},
	"national-bank": {"banque-nationale", "national-bank-of-canada"},
	"eq-bank":       {"equitable-bank"},
	"visa":          {"visa-canada"},
	"mastercard":    {"mastercard-canada"},
	"amex":          {"american-express", "american-express-canada"},

	// Telecom and media.
	"rogers":    {"fido", "chatr", "rogers-communications", "cityfone"},
	"bell":      {"bell-canada", "bce", "virgin-plus", "virgin-mobile", "lucky-mobile", "bell-mts", "bell-mobility"},
	"telus":     {"koodo", "public-mobile", "telus-mobility"},
	"shaw":      {"freedom-mobile", "shaw-communications"},
	"videotron": {"fizz", "quebecor"},

	// Platforms and consumer services.
	"facebook":     {"meta", "meta-platforms", "instagram", "whatsapp"},
	"google":       {"alphabet", "youtube", "google-canada"},
	"apple":        {"apple-canada", "itunes", "app-store"},
	"amazon":       {"amazon-canada", "amazon-ca", "prime"},
	"uber":         {"uber-eats", "ubereats", "uber-canada"},
	"doordash":     {"door-dash"},
	"ticketmaster": {"live-nation"},

	// Airlines.
	"air-canada": {"aircanada", "air-canada-rouge", "rouge"},
	"westjet":    {"west-jet", "swoop"},
	"flair":      {"flair-airlines"},

	// Insurers frequently named in Canadian consumer classes.
	"sun-life": {"sunlife", "sun-life-financial"},
	"manulife": {"manufacturers-life", "john-hancock"},
	"intact":   {"intact-insurance", "belairdirect", "belair-direct"},
	"aviva":    {"aviva-canada"},

	// Government bodies that have been defendants.
	"cra": {"canada-revenue-agency", "revenue-canada"},
	"caf": {"canadian-armed-forces", "department-of-national-defence", "dnd"},
}

// aliasIndex is the reverse of aliases, built once at init.
var aliasIndex = func() map[string]string {
	m := map[string]string{}
	for canon, alts := range aliases {
		m[canon] = canon
		for _, a := range alts {
			m[a] = canon
		}
	}
	return m
}()

// Provinces recognized in residency criteria, keyed by the two-letter code the
// rest of the system uses.
var Provinces = map[string]string{
	"AB": "Alberta", "BC": "British Columbia", "MB": "Manitoba",
	"NB": "New Brunswick", "NL": "Newfoundland and Labrador", "NS": "Nova Scotia",
	"NT": "Northwest Territories", "NU": "Nunavut", "ON": "Ontario",
	"PE": "Prince Edward Island", "QC": "Quebec", "SK": "Saskatchewan",
	"YT": "Yukon", "CA": "Canada (nationwide)",
}

var provinceAliases = map[string]string{
	"alberta": "AB", "ab": "AB",
	"british-columbia": "BC", "bc": "BC", "colombie-britannique": "BC",
	"manitoba": "MB", "mb": "MB",
	"new-brunswick": "NB", "nb": "NB", "nouveau-brunswick": "NB",
	"newfoundland": "NL", "newfoundland-and-labrador": "NL", "nl": "NL", "nfld": "NL",
	"nova-scotia": "NS", "ns": "NS",
	"northwest-territories": "NT", "nt": "NT", "nwt": "NT",
	"nunavut": "NU", "nu": "NU",
	"ontario": "ON", "on": "ON", "ont": "ON",
	"prince-edward-island": "PE", "pe": "PE", "pei": "PE",
	"quebec": "QC", "qc": "QC", "que": "QC",
	"saskatchewan": "SK", "sk": "SK", "sask": "SK",
	"yukon": "YT", "yt": "YT",
	"canada": "CA", "ca": "CA", "nationwide": "CA", "national": "CA",
	"all-of-canada": "CA", "canada-wide": "CA",
}

// NormalizeProvince maps a free-text place to a province code, returning the
// empty string when the text names somewhere glean does not model.
func NormalizeProvince(s string) string {
	key := NormalizeEntity(s)
	if code, ok := provinceAliases[key]; ok {
		return code
	}
	upper := strings.ToUpper(strings.TrimSpace(s))
	if _, ok := Provinces[upper]; ok {
		return upper
	}
	return ""
}
