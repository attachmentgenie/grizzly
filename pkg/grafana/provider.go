package grafana

import (
	"crypto/tls"
	"errors"
	"fmt"
	"net/http/httputil"
	"net/url"
	"path/filepath"

	gclient "github.com/grafana/grafana-openapi-client-go/client"
	"github.com/grafana/grizzly/internal/httputils"
	"github.com/grafana/grizzly/pkg/config"
	"github.com/grafana/grizzly/pkg/grizzly"
)

// Provider is a grizzly.Provider implementation for Grafana.
type Provider struct {
	config *config.GrafanaConfig
	client *gclient.GrafanaHTTPAPI
}

type ClientProvider interface {
	Client() (*gclient.GrafanaHTTPAPI, error)
	Config() *config.GrafanaConfig
}

// NewProvider instantiates a new Provider.
func NewProvider(config *config.GrafanaConfig) *Provider {
	return &Provider{
		config: config,
	}
}

func (p *Provider) Validate() error {
	if p.config.URL == "" {
		return fmt.Errorf("grafana URL is not set")
	}
	return nil
}

func (p *Provider) Status() grizzly.ProviderStatus {
	status := grizzly.ProviderStatus{}

	if err := p.Validate(); err != nil {
		status.ActiveReason = err.Error()
		return status
	}

	status.Active = true

	client, err := p.Client()
	if err != nil {
		status.OnlineReason = err.Error()
		return status
	}

	if _, err = client.Dashboards.GetHomeDashboard(); err != nil {
		status.OnlineReason = err.Error()
		return status
	}

	status.Online = true

	return status
}

func (p *Provider) Name() string {
	return "Grafana"
}

// Group returns the group name of the Grafana provider
func (p *Provider) Group() string {
	return "grizzly.grafana.com"
}

// Version returns the version of this provider
func (p *Provider) Version() string {
	return "v1alpha1"
}

func (p *Provider) Client() (*gclient.GrafanaHTTPAPI, error) {
	if p.client != nil {
		return p.client, nil
	}

	parsedURL, err := url.Parse(p.config.URL)
	if err != nil {
		return nil, fmt.Errorf("invalid Grafana URL")
	}

	transportConfig := gclient.DefaultTransportConfig().
		WithHost(parsedURL.Host).
		WithSchemes([]string{parsedURL.Scheme}).
		WithBasePath(filepath.Join(parsedURL.Path, "api"))

	httpClient, err := httputils.NewHTTPClient()
	if err != nil {
		return nil, err
	}
	transportConfig.Client = httpClient

	if parsedURL.Scheme == "https" && p.config.InsecureSkipVerify {
		transportConfig.TLSConfig = &tls.Config{
			InsecureSkipVerify: true,
			ServerName:         p.config.TLSHost,
		}
	}

	if p.config.Token != "" {
		if p.config.User != "" {
			transportConfig.BasicAuth = url.UserPassword(p.config.User, p.config.Token)
		} else {
			transportConfig.APIKey = p.config.Token
		}
	}
	grafanaClient := gclient.NewHTTPClientWithConfig(nil, transportConfig)
	p.client = grafanaClient
	return grafanaClient, nil
}

func (p *Provider) Config() *config.GrafanaConfig {
	return p.config
}

// APIVersion returns the group and version of this provider
func (p *Provider) APIVersion() string {
	return filepath.Join(p.Group(), p.Version())
}

// GetHandlers lists the resource handlers for the Grafana provider
func (p *Provider) GetHandlers() []grizzly.Handler {
	return []grizzly.Handler{
		NewDatasourceHandler(p),
		NewFolderHandler(p),
		NewLibraryElementHandler(p),
		NewDashboardHandler(p),
		NewAlertRuleGroupHandler(p),
		NewAlertNotificationPolicyHandler(p),
		NewAlertContactPointHandler(p),
		NewAlertNotificationTemplateHandler(p),
	}
}

func (p *Provider) SetupProxy() (*httputil.ReverseProxy, string, error) {
	status := p.Status()
	if !status.Active {
		return nil, "", errors.New(status.ActiveReason)
	}
	if !status.Online {
		return nil, "", errors.New(status.OnlineReason)
	}

	u, err := url.Parse(p.config.URL)
	if err != nil {
		return nil, "", err
	}

	return &httputil.ReverseProxy{
		Rewrite: func(r *httputil.ProxyRequest) {
			u.Path = "" // to ensure possible sub-paths won't be added twice.
			r.SetURL(u)

			authenticateRequest(p.config, r.Out)

			r.Out.Header.Del("Origin")
			r.Out.Header.Set("User-Agent", "Grizzly Proxy Server")
		},
	}, u.Path, nil
}
