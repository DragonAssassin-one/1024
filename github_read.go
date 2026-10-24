package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"unicode/utf8"
)

const maxRepositoryFile = 64 << 10

type githubFile struct {
	Repository string `json:"repository"`
	Path       string `json:"path"`
	Content    string `json:"content"`
}

func repositoryFilePath(name string) (string, error) {
	if name == "" || len(name) > 240 || !utf8.ValidString(name) {
		return "", errors.New("invalid repository file path")
	}
	parts := strings.Split(name, "/")
	for i, part := range parts {
		if part == "" || part == "." || part == ".." || strings.ContainsAny(part, "\\\x00\r\n") {
			return "", errors.New("invalid repository file path")
		}
		parts[i] = url.PathEscape(part)
	}
	return strings.Join(parts, "/"), nil
}

func (s githubService) readFile(ctx context.Context, agent, repo, name string) (githubFile, error) {
	var result githubFile
	repo, err := canonicalRepository(repo)
	if err != nil {
		return result, err
	}
	escapedPath, err := repositoryFilePath(name)
	if err != nil {
		return result, err
	}
	allowed, err := s.store.repositoryAllowed(agent, repo)
	if err != nil {
		return result, errors.New("GitHub authorization state unavailable")
	}
	if !allowed {
		return result, errors.New("GitHub repository is not authorized for this agent")
	}
	token, err := s.credential()
	if err != nil {
		return result, errors.New("GitHub credential unavailable")
	}
	defer clearBytes(token)
	base := s.apiBase
	if base == "" {
		base = "https://api.github.com"
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet,
		base+"/repos/"+repo+"/contents/"+escapedPath, nil)
	if err != nil {
		return result, err
	}
	request.Header.Set("Authorization", "Bearer "+string(token))
	request.Header.Set("Accept", "application/vnd.github+json")
	response, err := s.client.Do(request)
	if err != nil {
		return result, errors.New("GitHub request failed")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return result, fmt.Errorf("GitHub returned HTTP %d", response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, 150<<10+1))
	if err != nil || len(body) > 150<<10 {
		return result, errors.New("GitHub response too large or unreadable")
	}
	var file struct {
		Type     string `json:"type"`
		Encoding string `json:"encoding"`
		Content  string `json:"content"`
		Size     int64  `json:"size"`
	}
	if err := json.Unmarshal(body, &file); err != nil || file.Type != "file" ||
		file.Encoding != "base64" || file.Size < 0 || file.Size > maxRepositoryFile {
		return result, errors.New("unsupported GitHub content")
	}
	content, err := base64.StdEncoding.DecodeString(file.Content)
	if err != nil || len(content) > maxRepositoryFile || int64(len(content)) != file.Size ||
		!utf8.Valid(content) || bytes.Contains(content, token) {
		return result, errors.New("unsupported GitHub content")
	}
	return githubFile{Repository: repo, Path: name, Content: string(content)}, nil
}
