package auth

import (
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"
)

const testSecret = "this-is-a-very-long-secret-key-for-testing-purposes"

func TestIssueAndValidate(t *testing.T) {
	svc := NewJWTService(testSecret, time.Hour)

	token, err := svc.Issue("alice")
	if err != nil {
		t.Fatalf("Issue() error: %v", err)
	}

	username, err := svc.Validate(token)
	if err != nil {
		t.Fatalf("Validate() error: %v", err)
	}
	if username != "alice" {
		t.Errorf("username = %q, want %q", username, "alice")
	}
}

func TestValidateExpiredToken(t *testing.T) {
	svc := NewJWTService(testSecret, time.Hour)

	// Create a token that expired in the past
	now := time.Now()
	claims := jwt.RegisteredClaims{
		Subject:   "alice",
		IssuedAt:  jwt.NewNumericDate(now.Add(-2 * time.Hour)),
		ExpiresAt: jwt.NewNumericDate(now.Add(-1 * time.Hour)),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	tokenStr, err := token.SignedString([]byte(testSecret))
	if err != nil {
		t.Fatalf("SignedString() error: %v", err)
	}

	_, err = svc.Validate(tokenStr)
	if err != ErrExpiredToken {
		t.Errorf("error = %v, want ErrExpiredToken", err)
	}
}

func TestValidateInvalidSignature(t *testing.T) {
	svc := NewJWTService(testSecret, time.Hour)

	// Issue with a different secret
	other := NewJWTService("different-secret-key-that-is-long-enough-for-test", time.Hour)
	token, err := other.Issue("alice")
	if err != nil {
		t.Fatalf("Issue() error: %v", err)
	}

	_, err = svc.Validate(token)
	if err != ErrInvalidToken {
		t.Errorf("error = %v, want ErrInvalidToken", err)
	}
}

func TestValidateMalformedToken(t *testing.T) {
	svc := NewJWTService(testSecret, time.Hour)

	_, err := svc.Validate("not-a-jwt")
	if err != ErrInvalidToken {
		t.Errorf("error = %v, want ErrInvalidToken", err)
	}
}

func TestValidateEmptySubject(t *testing.T) {
	svc := NewJWTService(testSecret, time.Hour)

	now := time.Now()
	claims := jwt.RegisteredClaims{
		Subject:   "",
		IssuedAt:  jwt.NewNumericDate(now),
		ExpiresAt: jwt.NewNumericDate(now.Add(time.Hour)),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	tokenStr, err := token.SignedString([]byte(testSecret))
	if err != nil {
		t.Fatalf("SignedString() error: %v", err)
	}

	_, err = svc.Validate(tokenStr)
	if err != ErrInvalidToken {
		t.Errorf("error = %v, want ErrInvalidToken", err)
	}
}

func TestCheckPasswordCorrect(t *testing.T) {
	hash, err := bcrypt.GenerateFromPassword([]byte("hunter2"), bcrypt.DefaultCost)
	if err != nil {
		t.Fatalf("bcrypt error: %v", err)
	}

	if err := CheckPassword("hunter2", string(hash)); err != nil {
		t.Errorf("CheckPassword() error: %v", err)
	}
}

func TestCheckPasswordWrong(t *testing.T) {
	hash, err := bcrypt.GenerateFromPassword([]byte("hunter2"), bcrypt.DefaultCost)
	if err != nil {
		t.Fatalf("bcrypt error: %v", err)
	}

	err = CheckPassword("wrong-password", string(hash))
	if err != ErrInvalidCredentials {
		t.Errorf("error = %v, want ErrInvalidCredentials", err)
	}
}
