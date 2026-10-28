package install

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

var (
	ErrChecksumMismatch = errors.New("artifact SHA-256 checksum mismatch")
)

func FileSHA256(filePath string) (string, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return "", fmt.Errorf("open artifact %s: %w", filePath, err)
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", fmt.Errorf("hash artifact %s: %w", filePath, err)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func VerifyFileSHA256(filePath string, expectedHex string) error {
	expectedHex = strings.ToLower(strings.TrimSpace(expectedHex))
	if len(expectedHex) != 64 {
		return fmt.Errorf("%w: expected 64-character hex digest, got %q", ErrChecksumMismatch, expectedHex)
	}

	gotHex, err := FileSHA256(filePath)
	if err != nil {
		return err
	}
	if gotHex != expectedHex {
		return fmt.Errorf("%w: got %s, want %s", ErrChecksumMismatch, gotHex, expectedHex)
	}
	return nil
}
