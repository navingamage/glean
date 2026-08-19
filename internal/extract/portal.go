package extract

import (
	"net/url"
	"strings"

	"github.com/navingamage/glean/internal/htmlx"
)

// portalWords appear in the hostname of a claims portal far more often than
// anywhere else. Administrators register a domain per settlement, and they name
// it after the settlement.
var portalHostWords = []string{
	"settlement", "reglement", "règlement", "classaction", "class-action",
	"recourscollectif", "recours-collectif", "actioncollective", "claims", "claim",
	"indemnisation", "reclamation",
}

// portalHosts are the administrators themselves, which host many settlements
// each under one domain.
var portalHosts = []string{
	"ricepoint.com", "veritaglobal.com", "epiqglobal.com", "mnp.ca",
	"deloitte.ca", "kpmg.ca", "settlements.mnp.ca", "crawfordandcompany.com",
	"nptelecom.com", "classaction.deloitte.ca",
}

// portalText is what the link itself says when it leads to the claim form.
var portalText = []string{
	"file a claim", "submit a claim", "make a claim", "claim form", "claims portal",
	"official website", "official settlement", "settlement website", "claims website",
	"deposer une reclamation", "formulaire de reclamation", "site officiel",
	"how to claim", "start your claim", "claim now", "open official source",
}

// FindPortal picks the link most likely to be the official claims portal for a
// settlement described on pageURL.
//
// This exists because of a pattern that shows up on nearly every law firm case
// page: the firm describes the case and links out, and the date that actually
// matters is on the other side of that link. The firm's page says a settlement
// was approved; the administrator's page says claims close on the fifteenth.
// Without the second hop glean collects a list of settlements with no deadlines,
// which is a newsletter rather than a tool.
func FindPortal(doc, pageURL string) string {
	base, err := url.Parse(pageURL)
	if err != nil {
		return ""
	}
	pageHost := strings.ToLower(strings.TrimPrefix(base.Host, "www."))

	best, bestScore := "", 0
	for _, link := range htmlx.ExtractLinks(doc, pageURL) {
		u, err := url.Parse(link.URL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
			continue
		}
		host := strings.ToLower(strings.TrimPrefix(u.Host, "www."))
		if host == pageHost {
			continue // the second hop is only worth taking off-site
		}

		score := 0
		flat := strings.ReplaceAll(host, "-", "")
		for _, w := range portalHostWords {
			if strings.Contains(flat, strings.ReplaceAll(w, "-", "")) {
				score += 5
				break
			}
		}
		for _, h := range portalHosts {
			if strings.HasSuffix(host, h) {
				score += 4
				break
			}
		}
		text := strings.ToLower(link.Text)
		for _, w := range portalText {
			if strings.Contains(text, w) {
				score += 4
				break
			}
		}
		// A deep path on someone else's site is more likely a news article than
		// a settlement's own front page.
		if p := strings.Trim(u.Path, "/"); p == "" {
			score += 2
		} else if strings.Count(p, "/") >= 2 {
			score -= 2
		}
		for _, bad := range []string{
			"twitter.com", "x.com", "facebook.com", "linkedin.com", "youtube.com",
			"instagram.com", "reddit.com", "google.com", "apple.com", "wikipedia.org",
		} {
			if strings.HasSuffix(host, bad) {
				score = -100
				break
			}
		}
		if score > bestScore {
			best, bestScore = link.URL, score
		}
	}
	// Five is one strong signal. Anything weaker is a guess, and a wrong second
	// hop costs a fetch and imports a date from an unrelated settlement.
	if bestScore < 5 {
		return ""
	}
	return best
}
