package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

type githubIdentity struct {
	Login string `json:"login"`
	ID    int64  `json:"id"`
}

type githubService struct {
	store      connectionStore
	credential func() ([]byte, error)
	client     *http.Client
	endpoint   string
	apiBase    string
}

func newGitHubService(store connectionStore) githubService {
	return githubService{
		store:      store,
		credential: loadGitHubCredential,
		endpoint:   "https://api.github.com/user",
		apiBase:    "https://api.github.com",
		client: &http.Client{
			Timeout: 10 * time.Second,
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12},
			},
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return errors.New("GitHub redirect rejected")
			},
		},
	}
}

func (s githubService) identity(ctx context.Context, agent string) (githubIdentity, error) {
	var identity githubIdentity
	enabled, err := s.store.enabled(agent)
	if err != nil {
		return identity, fmt.Errorf("authorization check failed: %w", err)
	}
	if !enabled {
		return identity, errors.New("GitHub connection is disabled for this agent")
	}
	token, err := s.credential()
	if err != nil {
		return identity, errors.New("GitHub credential unavailable")
	}
	defer clearBytes(token)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, s.endpoint, nil)
	if err != nil {
		return identity, err
	}
	request.Header.Set("Authorization", "Bearer "+string(token))
	request.Header.Set("Accept", "application/vnd.github+json")
	response, err := s.client.Do(request)
	if err != nil {
		return identity, errors.New("GitHub request failed")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return identity, fmt.Errorf("GitHub returned HTTP %d", response.StatusCode)
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 8192)).Decode(&identity); err != nil ||
		identity.Login == "" || identity.ID <= 0 {
		return githubIdentity{}, errors.New("invalid GitHub response")
	}
	return identity, nil
}

func clearBytes(data []byte) {
	for i := range data {
		data[i] = 0
	}
}
