package config

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
)

var envWriteOrder = []string{
	"ADMIN_USER",
	"ADMIN_PASS",
	"WEB_SECURE_COOKIES",
	"SESSION_TTL_HOURS",
	"RATE_LIMIT_RPS",
	"RATE_LIMIT_BURST",
	"TOR_PROXY",
	"DB_PATH",
	"WEB_ADDR",
	"LOG_DIR",
}

// EnvStore .env dosyasini okur ve gunceller.
type EnvStore struct {
	path string
	mu   sync.Mutex
}

func NewEnvStore(path string) *EnvStore {
	if strings.TrimSpace(path) == "" {
		path = ".env"
	}
	return &EnvStore{path: path}
}

func (s *EnvStore) Path() string {
	return s.path
}

func (s *EnvStore) Read() (map[string]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.readUnlocked()
}

func (s *EnvStore) Update(updates map[string]string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	current, err := s.readUnlocked()
	if err != nil {
		return err
	}

	for key, value := range updates {
		cleanKey := strings.TrimSpace(key)
		if cleanKey == "" {
			continue
		}

		cleanValue := strings.TrimSpace(value)
		if cleanValue == "" {
			delete(current, cleanKey)
			continue
		}
		current[cleanKey] = cleanValue
	}

	return s.writeUnlocked(current)
}

func (s *EnvStore) readUnlocked() (map[string]string, error) {
	values := make(map[string]string)

	file, err := os.Open(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return values, nil
		}
		return nil, err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		key, rawValue, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}

		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}

		value := strings.TrimSpace(rawValue)
		value = strings.Trim(value, "\"")
		value = strings.Trim(value, "'")
		values[key] = value
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}

	return values, nil
}

func (s *EnvStore) writeUnlocked(values map[string]string) error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		if filepath.Dir(s.path) != "." {
			return err
		}
	}

	var builder strings.Builder
	builder.WriteString("# KeywordHunter runtime configuration\n")
	builder.WriteString("# This file is managed by /settings page and manual edits.\n\n")

	written := make(map[string]bool)
	for _, key := range envWriteOrder {
		value, ok := values[key]
		if !ok {
			continue
		}
		builder.WriteString(key)
		builder.WriteString("=")
		builder.WriteString(formatEnvValue(value))
		builder.WriteString("\n")
		written[key] = true
	}

	var extraKeys []string
	for key := range values {
		if !written[key] {
			extraKeys = append(extraKeys, key)
		}
	}
	sort.Strings(extraKeys)
	if len(extraKeys) > 0 {
		builder.WriteString("\n# Additional keys\n")
		for _, key := range extraKeys {
			builder.WriteString(key)
			builder.WriteString("=")
			builder.WriteString(formatEnvValue(values[key]))
			builder.WriteString("\n")
		}
	}

	if err := os.WriteFile(s.path, []byte(builder.String()), 0o600); err != nil {
		return fmt.Errorf("env dosyasi yazilamadi: %w", err)
	}

	return nil
}

func formatEnvValue(value string) string {
	if strings.ContainsAny(value, " \t#=") {
		return strconv.Quote(value)
	}
	return value
}
