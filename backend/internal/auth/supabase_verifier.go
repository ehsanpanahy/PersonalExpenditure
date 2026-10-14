package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

type SupabaseVerifier struct {
	baseURL string
	apiKey  string
	client  *http.Client
}

func NewSupabaseVerifier(baseURL, publishableKey string, client *http.Client) *SupabaseVerifier {
	return &SupabaseVerifier{
		baseURL: strings.TrimRight(baseURL, "/"),
		apiKey:  publishableKey,
		client:  client,
	}
}

func (v *SupabaseVerifier) Verify(ctx context.Context, accessToken string) (Principal, error) {
	if strings.TrimSpace(accessToken) == "" {
		return Principal{}, ErrInvalidToken
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, v.baseURL+"/auth/v1/user", nil)
	if err != nil {
		return Principal{}, fmt.Errorf("create Supabase Auth request: %w", err)
	}
	request.Header.Set("apikey", v.apiKey)
	request.Header.Set("Authorization", "Bearer "+accessToken)
	request.Header.Set("Accept", "application/json")

	response, err := v.client.Do(request)
	if err != nil {
		return Principal{}, fmt.Errorf("verify Supabase access token: %w", ErrVerifierUnavailable)
	}
	defer response.Body.Close()

	switch response.StatusCode {
	case http.StatusUnauthorized, http.StatusForbidden:
		return Principal{}, ErrInvalidToken
	case http.StatusOK:
		// Continue below and use the identity returned by Supabase Auth.
	default:
		if response.StatusCode >= http.StatusInternalServerError {
			return Principal{}, fmt.Errorf("verify Supabase access token: %w", ErrVerifierUnavailable)
		}
		return Principal{}, fmt.Errorf("unexpected Supabase Auth status %d: %w", response.StatusCode, ErrVerifierUnavailable)
	}

	var user struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&user); err != nil {
		return Principal{}, fmt.Errorf("decode Supabase Auth user: %w", ErrVerifierUnavailable)
	}
	if strings.TrimSpace(user.ID) == "" {
		return Principal{}, ErrInvalidToken
	}

	return Principal{UserID: user.ID}, nil
}
