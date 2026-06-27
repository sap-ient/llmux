// Package byok stores per-account "bring your own key" (BYOK) provider
// credentials, ENCRYPTED AT REST. It is part of the open-core: it has no
// dependency on the control plane, builds standalone, and is the authority the
// gateway consults to decide whether a request uses an account's OWN provider
// key (BYOK, unmetered) or the central keys (metered).
//
// Security posture:
//   - Secrets are encrypted with AES-256-GCM under a key-encryption key (KEK)
//     supplied by the operator (LLMUX_BYOK_KEK). Plaintext keys are never
//     persisted and never logged.
//   - The store exposes provider NAMES for an account but never returns a stored
//     key except to the gateway's request path (Get).
package byok

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"sync"
)

// Store is the per-account BYOK credential store. Keys are addressed by
// (account, provider). Implementations MUST encrypt at rest and MUST NOT log
// secret values.
type Store interface {
	// Get returns the decrypted API key the account set for provider, or
	// ("", false) when none is set. This is the only method that exposes a
	// plaintext key, and only to the gateway's request path.
	Get(account, provider string) (string, bool)
	// Set stores (encrypts) the account's API key for provider, replacing any
	// existing one. An empty key is rejected (use Clear to remove).
	Set(account, provider, apiKey string) error
	// Clear removes the account's BYOK key for provider. Removing an absent key
	// is not an error.
	Clear(account, provider string) error
	// Providers returns the sorted provider names the account has BYOK keys for
	// (never the keys themselves).
	Providers(account string) []string
}

// ---------------------------------------------------------------------------
// Crypter: AES-256-GCM envelope.
// ---------------------------------------------------------------------------

// Crypter seals and opens secrets with AES-256-GCM under a 32-byte KEK.
type Crypter struct {
	aead cipher.AEAD
}

// NewCrypter builds a Crypter from a 32-byte key. The key may be passed raw
// (exactly 32 bytes), hex-encoded (64 chars), or base64-encoded.
func NewCrypter(kek []byte) (*Crypter, error) {
	if len(kek) != 32 {
		return nil, fmt.Errorf("byok: KEK must be 32 bytes, got %d", len(kek))
	}
	block, err := aes.NewCipher(kek)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Crypter{aead: aead}, nil
}

// ParseKEK decodes a KEK provided as a raw 32-byte string, 64-char hex, or
// base64 (std or url, with/without padding). Returns the 32 raw bytes.
func ParseKEK(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, errors.New("byok: empty KEK")
	}
	// hex
	if len(s) == 64 {
		if b, err := hex.DecodeString(s); err == nil {
			return b, nil
		}
	}
	// base64 variants
	for _, enc := range []*base64.Encoding{
		base64.StdEncoding, base64.RawStdEncoding,
		base64.URLEncoding, base64.RawURLEncoding,
	} {
		if b, err := enc.DecodeString(s); err == nil && len(b) == 32 {
			return b, nil
		}
	}
	// raw bytes
	if len(s) == 32 {
		return []byte(s), nil
	}
	return nil, errors.New("byok: KEK must be 32 raw bytes, 64 hex chars, or base64 of 32 bytes")
}

// seal encrypts plaintext, returning base64(nonce||ciphertext).
func (c *Crypter) seal(plaintext string) (string, error) {
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	ct := c.aead.Seal(nonce, nonce, []byte(plaintext), nil)
	return base64.StdEncoding.EncodeToString(ct), nil
}

// open decrypts a base64(nonce||ciphertext) blob produced by seal.
func (c *Crypter) open(blob string) (string, error) {
	raw, err := base64.StdEncoding.DecodeString(blob)
	if err != nil {
		return "", err
	}
	ns := c.aead.NonceSize()
	if len(raw) < ns {
		return "", errors.New("byok: ciphertext too short")
	}
	nonce, ct := raw[:ns], raw[ns:]
	pt, err := c.aead.Open(nil, nonce, ct, nil)
	if err != nil {
		return "", err
	}
	return string(pt), nil
}

// ---------------------------------------------------------------------------
// MemStore: in-memory, encrypted-at-rest values.
// ---------------------------------------------------------------------------

// MemStore keeps encrypted BYOK keys in memory (lost on restart). Values are
// held as ciphertext so a heap/core dump never exposes plaintext keys.
type MemStore struct {
	c  *Crypter
	mu sync.RWMutex
	// account -> provider -> sealed key
	data map[string]map[string]string
}

// NewMemStore builds an in-memory store sealing values under c.
func NewMemStore(c *Crypter) *MemStore {
	return &MemStore{c: c, data: map[string]map[string]string{}}
}

// Get implements Store.
func (m *MemStore) Get(account, provider string) (string, bool) {
	m.mu.RLock()
	sealed, ok := m.data[account][provider]
	m.mu.RUnlock()
	if !ok {
		return "", false
	}
	pt, err := m.c.open(sealed)
	if err != nil {
		return "", false
	}
	return pt, true
}

// Set implements Store.
func (m *MemStore) Set(account, provider, apiKey string) error {
	if err := validate(account, provider, apiKey); err != nil {
		return err
	}
	sealed, err := m.c.seal(apiKey)
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.data[account] == nil {
		m.data[account] = map[string]string{}
	}
	m.data[account][provider] = sealed
	return nil
}

// Clear implements Store.
func (m *MemStore) Clear(account, provider string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.data[account], provider)
	if len(m.data[account]) == 0 {
		delete(m.data, account)
	}
	return nil
}

// Providers implements Store.
func (m *MemStore) Providers(account string) []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return sortedKeys(m.data[account])
}

// ---------------------------------------------------------------------------
// FileStore: encrypted JSON persisted to disk.
// ---------------------------------------------------------------------------

// FileStore persists encrypted BYOK keys to a JSON file (0600). The on-disk
// form contains only ciphertext; the KEK lives outside the file.
type FileStore struct {
	c    *Crypter
	path string
	mu   sync.RWMutex
	data map[string]map[string]string
}

// NewFileStore opens (or creates) an encrypted BYOK store at path.
func NewFileStore(c *Crypter, path string) (*FileStore, error) {
	f := &FileStore{c: c, path: path, data: map[string]map[string]string{}}
	raw, err := os.ReadFile(path)
	switch {
	case err == nil:
		if len(raw) > 0 {
			if err := json.Unmarshal(raw, &f.data); err != nil {
				return nil, fmt.Errorf("byok: parse %s: %w", path, err)
			}
		}
	case os.IsNotExist(err):
		// fresh store
	default:
		return nil, fmt.Errorf("byok: read %s: %w", path, err)
	}
	return f, nil
}

// Get implements Store.
func (f *FileStore) Get(account, provider string) (string, bool) {
	f.mu.RLock()
	sealed, ok := f.data[account][provider]
	f.mu.RUnlock()
	if !ok {
		return "", false
	}
	pt, err := f.c.open(sealed)
	if err != nil {
		return "", false
	}
	return pt, true
}

// Set implements Store.
func (f *FileStore) Set(account, provider, apiKey string) error {
	if err := validate(account, provider, apiKey); err != nil {
		return err
	}
	sealed, err := f.c.seal(apiKey)
	if err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.data[account] == nil {
		f.data[account] = map[string]string{}
	}
	f.data[account][provider] = sealed
	return f.flushLocked()
}

// Clear implements Store.
func (f *FileStore) Clear(account, provider string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.data[account], provider)
	if len(f.data[account]) == 0 {
		delete(f.data, account)
	}
	return f.flushLocked()
}

// Providers implements Store.
func (f *FileStore) Providers(account string) []string {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return sortedKeys(f.data[account])
}

// flushLocked atomically writes the encrypted store to disk. Caller holds mu.
func (f *FileStore) flushLocked() error {
	raw, err := json.Marshal(f.data)
	if err != nil {
		return err
	}
	tmp := f.path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, f.path)
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func validate(account, provider, apiKey string) error {
	if strings.TrimSpace(account) == "" {
		return errors.New("byok: account is required")
	}
	if strings.TrimSpace(provider) == "" {
		return errors.New("byok: provider is required")
	}
	if apiKey == "" {
		return errors.New("byok: api key is required (use Clear to remove)")
	}
	return nil
}

func sortedKeys(m map[string]string) []string {
	if len(m) == 0 {
		return nil
	}
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
