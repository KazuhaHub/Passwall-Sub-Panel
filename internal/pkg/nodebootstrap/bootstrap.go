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
	endpoint, err := privateEndpoint(options.CompletionURL)
	if err != nil {
		return "", err
	}
	replacer := strings.NewReplacer("@@COMPLETION_URL@@", shellQuote(endpoint), "@@TICKET@@", shellQuote(options.Token))
	return replacer.Replace(linuxMigrationTemplate), nil
}

// InstallCommand buffers the complete successful HTTPS response, checks syntax,
// then executes it on stdin. Bash may implement here-strings using temporary
// files, so a private umask applies before fetching or creating shell input.
// No partial network transfer is executed and no installer file is left behind.
func InstallCommand(downloadURL string) (string, error) {
	endpoint, err := privateEndpoint(downloadURL)
	if err != nil {
		return "", err
	}
	// Disable allexport and unset first: inherited settings must not export the
	// private script to child processes. The URL is a separate shell word
	// to avoid nested quote escapes in the user-facing command.
	body := `set +a +x; umask 077; unset s; s=$(curl -qf --proto =https -m 30 --max-filesize 1048576 "$1") && [[ $s ]] && bash -n <<<"$s" 2>/dev/null && bash <<<"$s"`
	return "bash -c " + shellQuote(body) + " -- " + shellQuote(endpoint), nil
}

func privateEndpoint(value string) (string, error) {
	if len(value) == 0 || len(value) > 2048 || strings.ContainsAny(value, "\\ \t\r\n") {
		return "", fmt.Errorf("invalid bootstrap completion endpoint: %w", domain.ErrValidation)
	}
	for _, character := range value {
		if character < 0x20 || character == 0x7f {
			return "", fmt.Errorf("invalid bootstrap completion endpoint: %w", domain.ErrValidation)
		}
	}
	endpoint, err := url.Parse(value)
	if err != nil || endpoint.Scheme != "https" || endpoint.Hostname() == "" || endpoint.Opaque != "" || endpoint.User != nil || endpoint.RawQuery != "" || endpoint.ForceQuery || endpoint.Fragment != "" || endpoint.RawFragment != "" || !strings.HasPrefix(endpoint.Path, "/") {
		return "", fmt.Errorf("invalid bootstrap completion endpoint: %w", domain.ErrValidation)
	}
	if port := endpoint.Port(); port != "" {
		number, err := strconv.Atoi(port)
		if err != nil || number < 1 || number > 65535 {
			return "", fmt.Errorf("invalid bootstrap completion endpoint: %w", domain.ErrValidation)
		}
	}
	return endpoint.String(), nil
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}
