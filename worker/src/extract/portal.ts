/**
 * Finding the official claims portal a case page links to.
 *
 * This exists because of a pattern on nearly every law firm case page: the firm
 * describes the case and links out, and the date that actually matters is on the
 * other side of that link. The firm's page says a settlement was approved; the
 * administrator's page says claims close on the fifteenth. Without the second
 * hop glean collects a list of settlements with no deadlines, which is a
 * newsletter rather than a tool.
 */
import { Link } from "../html";

const HOST_WORDS = [
  "settlement", "reglement", "classaction", "class-action",
  "recourscollectif", "recours-collectif", "actioncollective", "claims", "claim",
  "indemnisation", "reclamation",
];

/** Administrators, who host many settlements each under one domain. */
const HOSTS = [
  "ricepoint.com", "veritaglobal.com", "epiqglobal.com", "mnp.ca",
  "deloitte.ca", "kpmg.ca", "crawfordandcompany.com", "nptelecom.com",
];

const LINK_TEXT = [
  "file a claim", "submit a claim", "make a claim", "claim form", "claims portal",
  "official website", "official settlement", "settlement website", "claims website",
  "deposer une reclamation", "formulaire de reclamation", "site officiel",
  "how to claim", "start your claim", "claim now", "open official source",
];

const SOCIAL = [
  "twitter.com", "x.com", "facebook.com", "linkedin.com", "youtube.com",
  "instagram.com", "reddit.com", "google.com", "apple.com", "wikipedia.org",
];

export function findPortal(links: Link[], pageURL: string): string {
  let pageHost: string;
  try {
    pageHost = new URL(pageURL).hostname.toLowerCase().replace(/^www\./, "");
  } catch {
    return "";
  }

  let best = "", bestScore = 0;
  for (const link of links) {
    let u: URL;
    try {
      u = new URL(link.url);
    } catch {
      continue;
    }
    if (u.protocol !== "http:" && u.protocol !== "https:") continue;
    const host = u.hostname.toLowerCase().replace(/^www\./, "");
    if (host === pageHost) continue; // the second hop is only worth taking off-site

    let score = 0;
    const flat = host.replace(/-/g, "");
    if (HOST_WORDS.some((w) => flat.includes(w.replace(/-/g, "")))) score += 5;
    if (HOSTS.some((h) => host.endsWith(h))) score += 4;
    const text = link.text.toLowerCase();
    if (LINK_TEXT.some((w) => text.includes(w))) score += 4;

    // A deep path on someone else's site is more likely a news article than a
    // settlement's own front page.
    const path = u.pathname.replace(/^\/+|\/+$/g, "");
    if (!path) score += 2;
    else if ((path.match(/\//g) ?? []).length >= 2) score -= 2;

    if (SOCIAL.some((b) => host.endsWith(b))) score = -100;
    if (score > bestScore) {
      best = link.url;
      bestScore = score;
    }
  }
  // Five is one strong signal. Anything weaker is a guess, and a wrong second
  // hop costs a fetch and imports a date from an unrelated settlement.
  return bestScore >= 5 ? best : "";
}
