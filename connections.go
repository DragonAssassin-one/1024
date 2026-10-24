package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"syscall"
)

const githubConnection = "github"

type connectionStore struct {
	path string
}

func defaultConnectionStore() (connectionStore, error) {
	current, err := user.Current()
	if err != nil {
		return connectionStore{}, err
	}
	return connectionStore{path: filepath.Join(current.HomeDir, "Library", "Application Support", "1024", "connections.json")}, nil
}

func protectedPaths() ([]string, error) {
	store, err := defaultConnectionStore()
	if err != nil {
		return nil, err
	}
	current, err := user.Current()
	if err != nil {
		return nil, err
	}
	return []string{
		filepath.Dir(store.path),
		filepath.Join(current.HomeDir, "Library", "Keychains"),
		"/Library/Keychains",
	}, nil
}

func validateProject(project string) error {
	if project == "/" {
		return errors.New("refusing to authorize the filesystem root")
	}
	protected, err := protectedPaths()
	if err != nil {
		return err
	}
	for _, path := range protected {
		if info, err := os.Stat(path); err == nil && info.IsDir() {
			path, err = filepath.EvalSymlinks(path)
			if err != nil {
				return err
			}
		}
		if containsPath(project, path) || containsPath(path, project) {
			return fmt.Errorf("folder overlaps protected host state: %s", path)
		}
	}
	return nil
}

func containsPath(parent, path string) bool {
	relative, err := filepath.Rel(parent, path)
	return err == nil && relative != ".." && !filepath.IsAbs(relative) &&
		!strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func validAgent(agent string) bool {
	return agent == "claude" || agent == "codex" || agent == "dsh"
}

func (s connectionStore) enabled(agent string) (bool, error) {
	state, err := s.read()
	if err != nil {
		return false, err
	}
	return state[agent][githubConnection], nil
}

func (s connectionStore) read() (map[string]map[string]bool, error) {
	state := make(map[string]map[string]bool)
	file, err := os.Open(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return state, nil
	}
	if err != nil {
		return nil, err
	}
	defer file.Close()
	if err := json.NewDecoder(file).Decode(&state); err != nil {
		return nil, fmt.Errorf("invalid connection state: %w", err)
	}
	if state == nil {
		return nil, errors.New("invalid connection state: null")
	}
	return state, nil
}

func (s connectionStore) set(agent string, enabled bool) error {
	if !validAgent(agent) {
		return errors.New("agent must be claude, codex, or dsh")
	}
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	lock, err := os.OpenFile(filepath.Join(dir, "connections.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	state, err := s.read()
	if err != nil {
		return err
	}
	if state[agent] == nil {
		state[agent] = make(map[string]bool)
	}
	state[agent][githubConnection] = enabled

	temp, err := os.CreateTemp(dir, "connections-")
	if err != nil {
		return err
	}
	defer os.Remove(temp.Name())
	if err := json.NewEncoder(temp).Encode(state); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(temp.Name(), s.path)
}
