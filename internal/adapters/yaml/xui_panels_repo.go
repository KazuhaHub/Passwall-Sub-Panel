package yaml

import (
	"context"
	"fmt"
	"os"
	"sync"

	"gopkg.in/yaml.v3"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/crypto"
)

// XUIPanelRepo loads xui_panels.yaml. Sensitive fields (password / api_token)
// prefixed with "enc:" are automatically AES-GCM decrypted at load time.
type XUIPanelRepo struct {
	path      string
	secretKey []byte
	mu        sync.RWMutex
	cache     []*domain.XUIPanel
}

type xuiPanelsFile struct {
	Panels []xuiPanelEntry `yaml:"panels"`
}

type xuiPanelEntry struct {
	Name     string `yaml:"name"`
	URL      string `yaml:"url"`
	APIToken string `yaml:"api_token"`
	Username string `yaml:"username"`
	Password string `yaml:"password"`
	Remark   string `yaml:"remark"`
}

// NewXUIPanelRepo reads the file once and decrypts sensitive fields.
// secretKey comes from env PSP_SECRET_KEY. Pass nil/empty in dev when fields
// are plaintext.
func NewXUIPanelRepo(path string, secretKey []byte) (*XUIPanelRepo, error) {
	r := &XUIPanelRepo{path: path, secretKey: secretKey}
	if err := r.reload(); err != nil {
		return nil, err
	}
	return r, nil
}

func (r *XUIPanelRepo) reload() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	b, err := os.ReadFile(r.path)
	if err != nil {
		if os.IsNotExist(err) {
			r.cache = nil
			return nil
		}
		return err
	}
	var doc xuiPanelsFile
	if err := yaml.Unmarshal(b, &doc); err != nil {
		return err
	}
	r.cache = make([]*domain.XUIPanel, 0, len(doc.Panels))
	for _, e := range doc.Panels {
		token, err := r.maybeDecrypt(e.APIToken)
		if err != nil {
			return fmt.Errorf("decrypt api_token for panel %s: %w", e.Name, err)
		}
		pwd, err := r.maybeDecrypt(e.Password)
		if err != nil {
			return fmt.Errorf("decrypt password for panel %s: %w", e.Name, err)
		}
		r.cache = append(r.cache, &domain.XUIPanel{
			Name:     e.Name,
			URL:      e.URL,
			APIToken: token,
			Username: e.Username,
			Password: pwd,
			Remark:   e.Remark,
		})
	}
	return nil
}

// maybeDecrypt returns v decrypted if it starts with "enc:", or v unchanged
// otherwise (for plaintext dev configs).
func (r *XUIPanelRepo) maybeDecrypt(v string) (string, error) {
	const prefix = "enc:"
	if len(v) <= len(prefix) || v[:len(prefix)] != prefix {
		return v, nil
	}
	if len(r.secretKey) == 0 {
		return "", fmt.Errorf("encrypted field but PSP_SECRET_KEY not set")
	}
	return crypto.DecryptString(r.secretKey, v[len(prefix):])
}

func (r *XUIPanelRepo) List(ctx context.Context) ([]*domain.XUIPanel, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*domain.XUIPanel, len(r.cache))
	copy(out, r.cache)
	return out, nil
}

func (r *XUIPanelRepo) GetByName(ctx context.Context, name string) (*domain.XUIPanel, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, p := range r.cache {
		if p.Name == name {
			return p, nil
		}
	}
	return nil, domain.ErrNotFound
}
