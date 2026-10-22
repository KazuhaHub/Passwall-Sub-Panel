package version

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
)

type buildPublication struct{ Version, Channel string }

var currentPublication atomic.Value

// Channel never infers stable approval from a plain version number: publication
// and promotion reuse its bytes. A suffix proves Testing immediately; plain
// identities use the cached release metadata and stay unknown until verified.
func Channel() string {
	if IsReleaseVersion(Version) && strings.Contains(Version, "-") {
		return "testing"
	}
	if v, ok := currentPublication.Load().(buildPublication); ok && v.Version == Version {
		return v.Channel
	}
	return "unknown"
}

func refreshBuildChannel(ctx context.Context) error {
	if !IsReleaseVersion(Version) || strings.Contains(Version, "-") {
		return nil
	}
	tag, _ := ReleaseTagFor(Version)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.github.com/repos/KazuhaHub/passwall-sub-panel/releases/tags/"+tag, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("current build release: HTTP %d", resp.StatusCode)
	}
	var release struct {
		Tag        string `json:"tag_name"`
		Draft      *bool  `json:"draft"`
		Prerelease *bool  `json:"prerelease"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&release); err != nil {
		return err
	}
	if release.Tag != tag || release.Draft == nil || release.Prerelease == nil || *release.Draft {
		return fmt.Errorf("current build release is not published")
	}
	channel := "stable"
	if *release.Prerelease {
		channel = "testing"
	}
	currentPublication.Store(buildPublication{Version, channel})
	return nil
}
