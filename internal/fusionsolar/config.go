package fusionsolar

import (
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// Settings holds the FusionSolar connection defaults kept in a separate
// server-side YAML file (e.g. /etc/sestelemetry/fusionsolar.yaml), so an
// operator maintains the credentials in one place and the import page
// only needs the date range. Any value the import request supplies in
// its body still overrides the matching default.
//
// Example fusionsolar.yaml:
//
//	refresh_token: "hk..."
//	client_id: "602196255"
//	client_secret: "53UH..."
//	oauth_base: "https://oauth2.fusionsolar.huawei.com"
//	oauth_resolve: "80.158.45.213"
//	api_base: "https://eu5.fusionsolar.huawei.com"
type Settings struct {
	RefreshToken string `yaml:"refresh_token"`
	ClientID     string `yaml:"client_id"`
	ClientSecret string `yaml:"client_secret"`
	OAuthBase    string `yaml:"oauth_base"`
	OAuthResolve string `yaml:"oauth_resolve"`
	APIBase      string `yaml:"api_base"`
}

// LoadSettings reads the FusionSolar YAML config from path and trims all
// values. A missing file is reported via os.IsNotExist(err) so the
// caller can treat it as "no defaults configured" rather than fatal.
func LoadSettings(path string) (*Settings, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var s Settings
	if err := yaml.Unmarshal(b, &s); err != nil {
		return nil, fmt.Errorf("fusionsolar: parse %s: %w", path, err)
	}
	s.RefreshToken = strings.TrimSpace(s.RefreshToken)
	s.ClientID = strings.TrimSpace(s.ClientID)
	s.ClientSecret = strings.TrimSpace(s.ClientSecret)
	s.OAuthBase = strings.TrimSpace(s.OAuthBase)
	s.OAuthResolve = strings.TrimSpace(s.OAuthResolve)
	s.APIBase = strings.TrimSpace(s.APIBase)
	return &s, nil
}
