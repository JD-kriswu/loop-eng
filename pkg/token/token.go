// Package token provides token generation and validation.
package token

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"time"
)

const (
	// Token types
	TokenTypeDevice = "device"
	TokenTypeRun    = "run"

	// Token expiry
	DeviceTokenExpiry = 365 * 24 * time.Hour // 1 year
	RunTokenExpiry    = 24 * time.Hour        // 1 day
)

// Generator creates and validates tokens.
type Generator struct {
	secret []byte
}

// NewGenerator creates a token generator.
func NewGenerator(secret string) *Generator {
	return &Generator{secret: []byte(secret)}
}

// GenerateDeviceToken creates a new device token.
// Format: {machineID}:{timestamp}:device:{signature}
func (g *Generator) GenerateDeviceToken(machineID string) (string, error) {
	timestamp := time.Now().Unix()
	data := fmt.Sprintf("%s:%d:%s", machineID, timestamp, TokenTypeDevice)
	signature := g.sign(data)
	return fmt.Sprintf("%s:%s", data, signature), nil
}

// GenerateRunToken creates a new run token.
// Format: {runID}:{machineID}:{timestamp}:run:{signature}
func (g *Generator) GenerateRunToken(runID, machineID string) (string, error) {
	timestamp := time.Now().Unix()
	data := fmt.Sprintf("%s:%s:%d:%s", runID, machineID, timestamp, TokenTypeRun)
	signature := g.sign(data)
	return fmt.Sprintf("%s:%s", data, signature), nil
}

// ValidateDeviceToken validates a device token and returns the machine ID.
func (g *Generator) ValidateDeviceToken(token string) (machineID string, err error) {
	parts := strings.Split(token, ":")
	if len(parts) != 4 {
		return "", ErrInvalidToken
	}

	machineID = parts[0]
	timestampStr := parts[1]
	tokenType := parts[2]
	signature := parts[3]

	if tokenType != TokenTypeDevice {
		return "", ErrInvalidTokenType
	}

	// Check expiry
	timestamp, err := strconv.ParseInt(timestampStr, 10, 64)
	if err != nil {
		return "", ErrInvalidToken
	}
	if time.Now().Unix()-timestamp > int64(DeviceTokenExpiry.Seconds()) {
		return "", ErrTokenExpired
	}

	// Verify signature
	data := fmt.Sprintf("%s:%s:%s", machineID, timestampStr, tokenType)
	if !g.verify(data, signature) {
		return "", ErrInvalidSignature
	}

	return machineID, nil
}

// ValidateRunToken validates a run token and returns run ID and machine ID.
func (g *Generator) ValidateRunToken(token string) (runID, machineID string, err error) {
	parts := strings.Split(token, ":")
	if len(parts) != 5 {
		return "", "", ErrInvalidToken
	}

	runID = parts[0]
	machineID = parts[1]
	timestampStr := parts[2]
	tokenType := parts[3]
	signature := parts[4]

	if tokenType != TokenTypeRun {
		return "", "", ErrInvalidTokenType
	}

	// Check expiry
	timestamp, err := strconv.ParseInt(timestampStr, 10, 64)
	if err != nil {
		return "", "", ErrInvalidToken
	}
	if time.Now().Unix()-timestamp > int64(RunTokenExpiry.Seconds()) {
		return "", "", ErrTokenExpired
	}

	// Verify signature
	data := fmt.Sprintf("%s:%s:%s:%s", runID, machineID, timestampStr, tokenType)
	if !g.verify(data, signature) {
		return "", "", ErrInvalidSignature
	}

	return runID, machineID, nil
}

// ParseRunToken extracts run ID without full validation (for quick parsing).
func ParseRunToken(token string) (runID string) {
	parts := strings.Split(token, ":")
	if len(parts) >= 1 {
		return parts[0]
	}
	return ""
}

// sign creates an HMAC signature.
func (g *Generator) sign(data string) string {
	h := hmac.New(sha256.New, g.secret)
	h.Write([]byte(data))
	return hex.EncodeToString(h.Sum(nil))[:32]
}

// verify checks the signature.
func (g *Generator) verify(data, signature string) bool {
	expected := g.sign(data)
	return hmac.Equal([]byte(expected), []byte(signature))
}

// GenerateID creates a random ID.
func GenerateID() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// Errors
var (
	ErrInvalidToken     = fmt.Errorf("invalid token format")
	ErrInvalidTokenType = fmt.Errorf("invalid token type")
	ErrTokenExpired     = fmt.Errorf("token expired")
	ErrInvalidSignature = fmt.Errorf("invalid token signature")
)