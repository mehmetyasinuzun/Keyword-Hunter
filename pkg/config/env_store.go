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
	"LOG_LEVEL",
	"TOR_PROXY",
	"DB_PATH",
	"WEB_ADDR",
	"LOG_DIR",
	"REQUIRE_2FA",
	"APP_SECRET",
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

// SetPreserving tek bir anahtarı .env'de yazar ve dosyanın geri kalanını
// (yorumlar, boş satırlar, sıra, bilinmeyen satırlar) olduğu gibi korur.
// Anahtar varsa ilk geçtiği satır yerinde değiştirilir, yoksa sona eklenir.
// Update'ten farkı: Update dosyayı anahtar haritasından yeniden üretir ve
// yorumları atar; başlangıçta (ör. APP_SECRET üretimi) kullanıcının
// .env.example'dan kopyaladığı açıklamalar sessizce silinmemeli.
func (s *EnvStore) SetPreserving(key, value string) error {
	key = strings.TrimSpace(key)
	value = strings.TrimSpace(value)
	if key == "" || strings.ContainsAny(key, "=\n\r#") {
		return fmt.Errorf("gecersiz env anahtari")
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	raw, err := os.ReadFile(s.path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err != nil {
		if err := os.MkdirAll(filepath.Dir(s.path), 0o750); err != nil && filepath.Dir(s.path) != "." {
			return err
		}
	}
	entry := key + "=" + formatEnvValue(value)
	text := string(raw)
	lines := strings.SplitAfter(text, "\n")
	replaced := false
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		k, _, ok := strings.Cut(trimmed, "=")
		if !ok || strings.TrimSpace(k) != key {
			continue
		}
		nl := ""
		if strings.HasSuffix(line, "\n") {
			nl = "\n"
		}
		lines[i] = entry + nl
		replaced = true
		break
	}
	if replaced {
		text = strings.Join(lines, "")
	} else {
		if text != "" && !strings.HasSuffix(text, "\n") {
			text += "\n"
		}
		text += entry + "\n"
	}
	if err := os.WriteFile(s.path, []byte(text), 0o600); err != nil { //nolint:gosec // G703: yol operatörün ENV_FILE değeri; kullanıcı girdisi değil
		return fmt.Errorf("env dosyasi yazilamadi: %w", err)
	}
	return nil
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
	defer func() { _ = file.Close() }() // salt okuma

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

		values[key] = unquoteEnvValue(rawValue)
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}

	return values, nil
}

func (s *EnvStore) writeUnlocked(values map[string]string) error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o750); err != nil {
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
	if strings.ContainsAny(value, " \t#=\"'\\\n\r") {
		return strconv.Quote(value)
	}
	return value
}

// unquoteEnvValue formatEnvValue'nun tam tersidir: Go tarzı çift tırnaklı
// değerler strconv.Unquote ile (kaçış dizileri dahil) çözülür; tek tırnaklı
// değerler olduğu gibi alınır; diğerleri yalnızca kırpılır. Eski sürüm
// tırnakları naif biçimde kırpıyor ve içinde " veya \ geçen parolalar
// /settings ile kaydedildikten sonra bozuluyordu.
func unquoteEnvValue(raw string) string {
	value := strings.TrimSpace(raw)
	if len(value) >= 2 && value[0] == '"' && value[len(value)-1] == '"' {
		if unquoted, err := strconv.Unquote(value); err == nil {
			return unquoted
		}
		return value[1 : len(value)-1]
	}
	if len(value) >= 2 && value[0] == '\'' && value[len(value)-1] == '\'' {
		return value[1 : len(value)-1]
	}
	// Satır sonu yorumu: KEY=value # açıklama (tırnaksız değerlerde)
	if i := strings.Index(value, " #"); i >= 0 {
		value = strings.TrimSpace(value[:i])
	}
	return value
}
