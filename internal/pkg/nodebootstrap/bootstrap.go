// Package nodebootstrap renders a private, one-use Linux backend migration
// wrapper. It does not mint node identities or perform PSP database writes.
package nodebootstrap

import (
	_ "embed"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// Options contains only a short-lived bootstrap authorization, never the
// persistent Passwall Node credential. Callers bind the ticket server-side to
// the reviewed server/configuration and the exact selected release.
type Options struct {
	CompletionURL string
	Token         string
}

//go:embed migrate.sh
var linuxMigrationTemplate string

var ticketPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{32,512}$`)

// RenderLinuxMigration returns a secret-bearing script; callers must serve it
// privately with no-store and must not log its contents.
func RenderLinuxMigration(options Options) (string, error) {
	if !ticketPattern.MatchString(options.Token) {
		return "", fmt.Errorf("invalid bootstrap authorization: %w", domain.ErrValidation)
	}
	if len(options.CompletionURL) == 0 || len(options.CompletionURL) > 2048 || strings.ContainsAny(options.CompletionURL, "\\ \t\r\n") {
		return "", fmt.Errorf("invalid bootstrap completion endpoint: %w", domain.ErrValidation)
	}
	for _, character := range options.CompletionURL {
		if character < 0x20 || character == 0x7f {
			return "", fmt.Errorf("invalid bootstrap completion endpoint: %w", domain.ErrValidation)
		}
	}
	endpoint, err := url.Parse(options.CompletionURL)
	if err != nil || endpoint.Scheme != "https" || endpoint.Hostname() == "" || endpoint.Opaque != "" || endpoint.User != nil || endpoint.RawQuery != "" || endpoint.ForceQuery || endpoint.Fragment != "" || endpoint.RawFragment != "" || !strings.HasPrefix(endpoint.Path, "/") {
		return "", fmt.Errorf("invalid bootstrap completion endpoint: %w", domain.ErrValidation)
	}
	if port := endpoint.Port(); port != "" {
		number, err := strconv.Atoi(port)
		if err != nil || number < 1 || number > 65535 {
			return "", fmt.Errorf("invalid bootstrap completion endpoint: %w", domain.ErrValidation)
		}
	}
	replacer := strings.NewReplacer("@@COMPLETION_URL@@", shellQuote(endpoint.String()), "@@TICKET@@", shellQuote(options.Token))
	return replacer.Replace(linuxMigrationTemplate), nil
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}
