package scan

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/navingamage/glean/internal/fetch"
	"github.com/navingamage/glean/internal/htmlx"
)

// TestChurnProbe fetches a page twice and reports what differs in the extracted
// text. Diagnostic only; skipped unless GLEAN_PROBE names URLs.
func TestChurnProbe(t *testing.T) {
	urls := strings.Fields(os.Getenv("GLEAN_PROBE"))
	if len(urls) == 0 {
		t.Skip("set GLEAN_PROBE to a space-separated list of URLs")
	}
	c := fetch.New("")
	c.IgnoreCache = true
	for _, url := range urls {
		var texts []string
		for range 2 {
			r, err := c.Get(context.Background(), url)
			if err != nil {
				t.Logf("%s: %v", url, err)
				break
			}
			texts = append(texts, htmlx.ExtractText(r.Body))
		}
		if len(texts) < 2 {
			continue
		}
		t.Logf("=== %s", url)
		if texts[0] == texts[1] {
			t.Logf("    identical across two fetches")
			continue
		}
		a, b := strings.Split(texts[0], "\n"), strings.Split(texts[1], "\n")
		n := min(len(a), len(b))
		shown := 0
		for i := 0; i < n && shown < 5; i++ {
			if a[i] != b[i] {
				t.Logf("    line %d\n      A: %.110s\n      B: %.110s", i, a[i], b[i])
				shown++
			}
		}
		if len(a) != len(b) {
			t.Logf("    line count %d vs %d", len(a), len(b))
		}
	}
}
