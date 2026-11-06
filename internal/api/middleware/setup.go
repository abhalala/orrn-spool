package middleware

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strings"

	"golang.org/x/crypto/bcrypt"

	"github.com/orrn/spool/internal/db"
)

// First-boot setup. While no admin password is stored, the spool makes a
// random one-time setup token when it starts and main prints it to the
// console and writes it to SetupTokenFile next to the database. Setting the
// first password needs that token, so the person with access to the machine
// claims the spool, not whoever reaches the port first. The token is
// single use and gone once a password exists. To reset, delete the
// admin_password setting and restart: a new token is made.

// SetupTokenFile is the token file's name inside the database directory.
const SetupTokenFile = "setup-token"

var (
	// ErrSetupDone means an admin password already exists.
	ErrSetupDone = errors.New("setup already completed")
	// ErrSetupToken means the setup token was missing or wrong.
	ErrSetupToken = errors.New("setup token missing or wrong")
)

func newSetupToken() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("make setup token: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// SetupToken returns the one-time setup token, or "" when setup is done.
func (a *AuthMiddleware) SetupToken() string {
	a.setupMu.Lock()
	defer a.setupMu.Unlock()
	return a.setupToken
}

// OnSetupComplete registers fn to run once, after the first password is
// stored (main removes the token file there).
func (a *AuthMiddleware) OnSetupComplete(fn func()) {
	a.setupMu.Lock()
	defer a.setupMu.Unlock()
	a.onSetupDone = fn
}

// completeSetup stores the first admin password if token is the current
// setup token. The insert never overwrites, so of two racing requests only
// one succeeds, and only that one gets a session.
func (a *AuthMiddleware) completeSetup(ctx context.Context, password, token string) error {
	a.setupMu.Lock()
	defer a.setupMu.Unlock()
	if !a.isSetupRequired() {
		return ErrSetupDone
	}
	got := []byte(strings.TrimSpace(token))
	if a.setupToken == "" || subtle.ConstantTimeCompare(got, []byte(a.setupToken)) != 1 {
		return ErrSetupToken
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("hash password: %w", err)
	}
	created, err := db.Settings.CreateSettingIfAbsent(ctx, settingsKeyPassword, string(hash), false)
	if err != nil {
		return err
	}
	a.setupToken = ""
	if !created {
		return ErrSetupDone
	}
	if fn := a.onSetupDone; fn != nil {
		a.onSetupDone = nil
		fn()
	}
	return nil
}

// WriteSetupTokenFile writes token to path, readable and writable by the
// owner only. An older file is replaced, never reused with its old mode.
func WriteSetupTokenFile(path, token string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.WriteString(token + "\n"); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}
