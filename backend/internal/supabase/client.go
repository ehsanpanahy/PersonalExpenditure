package supabase

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/ehsanpanahy/PersonalExpenditure/backend/internal/auth"
)

var ErrInvalidResource = errors.New("invalid Supabase resource path")

type Client struct {
	baseURL    string
	apiKey     string
	httpClient *http.Client
}

func NewClient(baseURL, publishableKey string, httpClient *http.Client) *Client {
	return &Client{
		baseURL:    strings.TrimRight(baseURL, "/"),
		apiKey:     publishableKey,
		httpClient: httpClient,
	}
}

// Do makes a user-scoped PostgREST/RPC request. The verified caller token is
// mandatory and is forwarded unchanged so Postgres can apply auth.uid() and RLS.
// Callers must use fixed, application-owned resource paths and build filters
// from validated values; never pass an arbitrary client URL here.
func (c *Client) Do(ctx context.Context, method, resource string, session auth.Session, body io.Reader) (*http.Response, error) {
	if session.AccessToken == "" || session.Principal.UserID == "" {
		return nil, errors.New("verified user session is required")
	}
	if err := validateResource(resource); err != nil {
		return nil, err
	}

	resourceURL, err := url.Parse(resource)
	if err != nil {
		return nil, fmt.Errorf("parse Supabase resource path: %w", err)
	}
	endpoint, err := url.JoinPath(c.baseURL, "rest", "v1", resourceURL.Path)
	if err != nil {
		return nil, fmt.Errorf("build Supabase endpoint: %w", err)
	}
	if resourceURL.RawQuery != "" {
		endpoint += "?" + resourceURL.RawQuery
	}

	request, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return nil, fmt.Errorf("create Supabase request: %w", err)
	}
	request.Header.Set("apikey", c.apiKey)
	request.Header.Set("Authorization", "Bearer "+session.AccessToken)
	request.Header.Set("Accept", "application/json")
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}

	return c.httpClient.Do(request)
}

func validateResource(resource string) error {
	if resource == "" || strings.HasPrefix(resource, "/") || strings.Contains(resource, "\\") {
		return ErrInvalidResource
	}
	parsed, err := url.Parse(resource)
	if err != nil || parsed.IsAbs() || parsed.Host != "" || parsed.Path == "" {
		return ErrInvalidResource
	}
	for _, segment := range strings.Split(parsed.Path, "/") {
		if segment == "." || segment == ".." {
			return ErrInvalidResource
		}
	}
	return nil
}
