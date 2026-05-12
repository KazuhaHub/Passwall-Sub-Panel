package yaml

import (
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// writeYAML writes a YAML file atomically: write to .tmp then rename.
func writeYAML(path string, v any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	b, err := yaml.Marshal(v)
	if err != nil {
		return err
	}
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
