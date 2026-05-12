package yaml

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"

	"github.com/KazuhaHub/passwall-sub-panel/internal/config"
)

// LoadSAMLConfig reads saml.yaml and applies defaults.
// If the file does not exist, it returns SAMLConfig{Enabled:false} without
// error (SSO is treated as an optional feature).
//
// The type itself lives in internal/config so that the consumer
// (service/auth) does not need to import this adapter package — see the
// hexagonal dependency rule.
func LoadSAMLConfig(path string) (*config.SAMLConfig, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &config.SAMLConfig{Enabled: false}, nil
		}
		return nil, err
	}
	var c config.SAMLConfig
	if err := yaml.Unmarshal(b, &c); err != nil {
		return nil, fmt.Errorf("parse saml.yaml: %w", err)
	}
	config.ApplySAMLDefaults(&c)
	return &c, nil
}
