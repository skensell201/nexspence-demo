package domain_test

import (
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/nexspence-oss/nexspence/internal/domain"
)

// The docs site states a format count in two languages and lists the formats in
// a table. All three drifted behind the code when OCI was added, and nothing
// caught it — the claim is only ever read by humans. This test reads the page
// and holds it to what the code actually supports.

const sitePath = "../../website/docs/index.html"

func siteHTML(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(sitePath)
	if err != nil {
		t.Fatalf("read docs site: %v", err)
	}
	return string(b)
}

func TestSiteFormatCountMatchesCode(t *testing.T) {
	html := siteHTML(t)
	want := len(domain.AllFormats)

	for _, tc := range []struct {
		lang string
		re   *regexp.Regexp
	}{
		{"en", regexp.MustCompile(`(\d+) artifact formats`)},
		{"ru", regexp.MustCompile(`(\d+) форматов артефактов`)},
	} {
		matches := tc.re.FindAllStringSubmatch(html, -1)
		if len(matches) == 0 {
			t.Errorf("%s: no format count found on the docs site", tc.lang)
			continue
		}
		for _, m := range matches {
			got, err := strconv.Atoi(m[1])
			if err != nil {
				t.Errorf("%s: unparsable count %q", tc.lang, m[1])
				continue
			}
			if got != want {
				t.Errorf("%s: docs site claims %d formats, code supports %d", tc.lang, got, want)
			}
		}
	}
}

func TestSiteFormatTableListsEveryFormat(t *testing.T) {
	html := siteHTML(t)
	i := strings.Index(html, "artifact formats")
	if i < 0 {
		t.Fatal("formats section not found on the docs site")
	}
	rows := regexp.MustCompile(`<tr><td>([^<]+)</td><td>([^<]+)</td>`).FindAllStringSubmatch(html[i:i+4000], -1)
	if len(rows) != len(domain.AllFormats) {
		t.Errorf("formats table has %d rows, code supports %d formats", len(rows), len(domain.AllFormats))
	}

	// Each format needs a row a reader can recognize it by; the table uses
	// display names, so match on a substring the row is expected to carry.
	labels := map[domain.RepoFormat]string{
		domain.FormatMaven2:    "Maven",
		domain.FormatNPM:       "npm",
		domain.FormatPyPI:      "PyPI",
		domain.FormatDocker:    "Docker",
		domain.FormatOCI:       "OCI",
		domain.FormatGo:        "Go Modules",
		domain.FormatNuGet:     "NuGet",
		domain.FormatHelm:      "Helm",
		domain.FormatCargo:     "Cargo",
		domain.FormatApt:       "Apt",
		domain.FormatYum:       "Yum",
		domain.FormatConan:     "Conan",
		domain.FormatRaw:       "Raw",
		domain.FormatConda:     "Conda",
		domain.FormatTerraform: "Terraform",
		domain.FormatRubyGems:  "RubyGems",
		domain.FormatCRAN:      "CRAN",
	}
	for _, f := range domain.AllFormats {
		label, ok := labels[f]
		if !ok {
			t.Errorf("format %q has no expected table label — add one when adding the format", f)
			continue
		}
		found := false
		for _, r := range rows {
			if strings.Contains(r[1], label) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("format %q (%s) is missing from the docs formats table", f, label)
		}
	}
}
