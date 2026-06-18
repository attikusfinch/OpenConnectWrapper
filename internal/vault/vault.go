package vault

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"golang.org/x/crypto/argon2"
)

const (
	appDirName       = "OpenConnectMulti"
	vaultFileName    = "vault.json"
	deviceSecretName = "device.key"
)

var (
	ErrAlreadyInitialized = errors.New("vault already initialized")
	ErrNotInitialized     = errors.New("vault is not initialized")
	ErrInvalidPIN         = errors.New("invalid PIN or damaged vault")
)

type KDFConfig struct {
	Name        string `json:"name"`
	MemoryKiB   uint32 `json:"memory_kib"`
	Iterations  uint32 `json:"iterations"`
	Parallelism uint8  `json:"parallelism"`
	Salt        string `json:"salt"`
}

type File struct {
	Version    int       `json:"version"`
	KDF        KDFConfig `json:"kdf"`
	Nonce      string    `json:"nonce"`
	Ciphertext string    `json:"ciphertext"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

type Data struct {
	Settings Settings  `json:"settings"`
	Profiles []Profile `json:"profiles"`
}

type Settings struct {
	OpenConnectPath string `json:"openconnect_path"`
}

type Profile struct {
	ID                 string    `json:"id"`
	Name               string    `json:"name"`
	Server             string    `json:"server"`
	Username           string    `json:"username"`
	Password           string    `json:"password"`
	AuthGroup          string    `json:"auth_group"`
	Protocol           string    `json:"protocol"`
	UserAgent          string    `json:"user_agent"`
	ServerCert         string    `json:"server_cert"`
	NoCertCheck        bool      `json:"no_cert_check"`
	ExtraArgs          []string  `json:"extra_args"`
	LastConnectedAt    time.Time `json:"last_connected_at,omitempty"`
	LastConnectedError string    `json:"last_connected_error,omitempty"`
}

type Store struct {
	dir          string
	vaultPath    string
	devicePath   string
	deviceSecret []byte
}

type Unlocked struct {
	store *Store
	file  File
	key   []byte
	Data  Data
}

func DefaultDir() (string, error) {
	switch runtime.GOOS {
	case "windows":
		if base := os.Getenv("APPDATA"); base != "" {
			return filepath.Join(base, appDirName), nil
		}
	case "darwin":
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, "Library", "Application Support", appDirName), nil
		}
	}

	if config := os.Getenv("XDG_CONFIG_HOME"); config != "" {
		return filepath.Join(config, "openconnectmulti"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "openconnectmulti"), nil
}

func NewStore(dir string) (*Store, error) {
	if dir == "" {
		var err error
		dir, err = DefaultDir()
		if err != nil {
			return nil, err
		}
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	secret, err := ensureDeviceSecret(filepath.Join(dir, deviceSecretName))
	if err != nil {
		return nil, err
	}
	return &Store{
		dir:          dir,
		vaultPath:    filepath.Join(dir, vaultFileName),
		devicePath:   filepath.Join(dir, deviceSecretName),
		deviceSecret: secret,
	}, nil
}

func (s *Store) Dir() string {
	return s.dir
}

func (s *Store) VaultPath() string {
	return s.vaultPath
}

func (s *Store) DeviceSecretPath() string {
	return s.devicePath
}

func (s *Store) Initialized() bool {
	_, err := os.Stat(s.vaultPath)
	return err == nil
}

func (s *Store) Initialize(pin string) (*Unlocked, error) {
	if s.Initialized() {
		return nil, ErrAlreadyInitialized
	}

	file := File{
		Version: 1,
		KDF: KDFConfig{
			Name:        "argon2id",
			MemoryKiB:   128 * 1024,
			Iterations:  3,
			Parallelism: 4,
			Salt:        mustB64(randomBytes(16)),
		},
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	key, err := s.deriveKey(pin, file.KDF)
	if err != nil {
		return nil, err
	}

	unlocked := &Unlocked{
		store: s,
		file:  file,
		key:   key,
		Data: Data{
			Settings: Settings{OpenConnectPath: "openconnect"},
			Profiles: []Profile{},
		},
	}
	if err := unlocked.Save(); err != nil {
		return nil, err
	}
	return unlocked, nil
}

func (s *Store) Unlock(pin string) (*Unlocked, error) {
	raw, err := os.ReadFile(s.vaultPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNotInitialized
	}
	if err != nil {
		return nil, err
	}

	var file File
	if err := json.Unmarshal(raw, &file); err != nil {
		return nil, fmt.Errorf("read vault metadata: %w", err)
	}
	key, err := s.deriveKey(pin, file.KDF)
	if err != nil {
		return nil, err
	}

	plaintext, err := decrypt(key, file.Nonce, file.Ciphertext)
	if err != nil {
		return nil, ErrInvalidPIN
	}

	var data Data
	if err := json.Unmarshal(plaintext, &data); err != nil {
		return nil, fmt.Errorf("read vault data: %w", err)
	}
	if data.Settings.OpenConnectPath == "" {
		data.Settings.OpenConnectPath = "openconnect"
	}
	if data.Profiles == nil {
		data.Profiles = []Profile{}
	}

	return &Unlocked{
		store: s,
		file:  file,
		key:   key,
		Data:  data,
	}, nil
}

func (u *Unlocked) Save() error {
	plaintext, err := json.MarshalIndent(u.Data, "", "  ")
	if err != nil {
		return err
	}
	nonce, ciphertext, err := encrypt(u.key, plaintext)
	if err != nil {
		return err
	}
	u.file.Nonce = nonce
	u.file.Ciphertext = ciphertext
	u.file.UpdatedAt = time.Now()
	if u.file.CreatedAt.IsZero() {
		u.file.CreatedAt = time.Now()
	}

	raw, err := json.MarshalIndent(u.file, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(u.store.vaultPath, raw, 0o600)
}

func (u *Unlocked) ChangePIN(newPIN string) error {
	salt := mustB64(randomBytes(16))
	u.file.KDF.Salt = salt
	key, err := u.store.deriveKey(newPIN, u.file.KDF)
	if err != nil {
		return err
	}
	u.key = key
	return u.Save()
}

func (s *Store) deriveKey(pin string, cfg KDFConfig) ([]byte, error) {
	if cfg.Name != "argon2id" {
		return nil, fmt.Errorf("unsupported KDF: %s", cfg.Name)
	}
	salt, err := base64.StdEncoding.DecodeString(cfg.Salt)
	if err != nil {
		return nil, fmt.Errorf("decode salt: %w", err)
	}
	password := make([]byte, 0, len(pin)+len(s.deviceSecret)+32)
	password = append(password, []byte("openconnectmulti:v1:")...)
	password = append(password, s.deviceSecret...)
	password = append(password, ':')
	password = append(password, []byte(pin)...)

	return argon2.IDKey(password, salt, cfg.Iterations, cfg.MemoryKiB, cfg.Parallelism, 32), nil
}

func encrypt(key, plaintext []byte) (string, string, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", "", err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return "", "", err
	}
	nonce := randomBytes(aead.NonceSize())
	ciphertext := aead.Seal(nil, nonce, plaintext, nil)
	return mustB64(nonce), mustB64(ciphertext), nil
}

func decrypt(key []byte, nonceB64, ciphertextB64 string) ([]byte, error) {
	nonce, err := base64.StdEncoding.DecodeString(nonceB64)
	if err != nil {
		return nil, err
	}
	ciphertext, err := base64.StdEncoding.DecodeString(ciphertextB64)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return aead.Open(nil, nonce, ciphertext, nil)
}

func ensureDeviceSecret(path string) ([]byte, error) {
	raw, err := os.ReadFile(path)
	if err == nil {
		secret, err := base64.StdEncoding.DecodeString(string(raw))
		if err != nil {
			return nil, fmt.Errorf("decode device secret: %w", err)
		}
		if len(secret) != 32 {
			return nil, fmt.Errorf("device secret has invalid length")
		}
		return secret, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}

	secret := randomBytes(32)
	if err := writeFileAtomic(path, []byte(mustB64(secret)), 0o600); err != nil {
		return nil, err
	}
	return secret, nil
}

func randomBytes(n int) []byte {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		panic(err)
	}
	return buf
}

func mustB64(raw []byte) string {
	return base64.StdEncoding.EncodeToString(raw)
}

func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer func() {
		_ = os.Remove(tmpPath)
	}()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(perm); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return os.Rename(tmpPath, path)
}
