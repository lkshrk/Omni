//go:build canary

package app

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/lkshrk/omni/internal/agent"
)

func TestCanarySkillsCatalogSearch(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	results, err := fetchCatalog(ctx, &http.Client{Timeout: 30 * time.Second}, defaultSkillsCatalogURL, "git", "")
	if err != nil {
		t.Fatalf("fetchCatalog(%s): %v", defaultSkillsCatalogURL, err)
	}
	if len(results) == 0 {
		t.Fatalf("%s returned no parsable rows for a stable query", defaultSkillsCatalogURL)
	}
	for _, result := range results {
		if result.Source == "" || result.Skill == "" || result.Installs == "" {
			t.Fatalf("catalog row = %+v, want source, skill and install count", result)
		}
		if _, err := agent.ParseSkillSource(result.Source + "@" + result.Skill); err != nil {
			t.Fatalf("catalog row %+v is not installable: %v", result, err)
		}
	}
}
