package auth

import (
	"errors"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"
)

var (
	ErrInvalidCredentials = errors.New("invalid credentials")
	ErrInvalidToken       = errors.New("invalid token")
	ErrExpiredToken       = errors.New("token expired")
)

// JWTService handles JWT token issuance and validation.
type JWTService struct {
	secret []byte
	expiry time.Duration
}

// NewJWTService creates a new JWTService with the given secret and expiry.
func NewJWTService(secret string, expiry time.Duration) *JWTService {
	return &JWTService{
		secret: []byte(secret),
		expiry: expiry,
	}
}

// Issue creates a signed JWT for the given username.
func (s *JWTService) Issue(username string) (string, error) {
	now := time.Now()
	claims := jwt.RegisteredClaims{
		Subject:   username,
		IssuedAt:  jwt.NewNumericDate(now),
		ExpiresAt: jwt.NewNumericDate(now.Add(s.expiry)),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(s.secret)
}

// Validate parses and validates a JWT token string.
// Returns the username (sub claim) on success.
func (s *JWTService) Validate(tokenString string) (string, error) {
	token, err := jwt.ParseWithClaims(tokenString, &jwt.RegisteredClaims{}, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, ErrInvalidToken
		}
		return s.secret, nil
	})
	if err != nil {
		if errors.Is(err, jwt.ErrTokenExpired) {
			return "", ErrExpiredToken
		}
		return "", ErrInvalidToken
	}

	claims, ok := token.Claims.(*jwt.RegisteredClaims)
	if !ok || !token.Valid {
		return "", ErrInvalidToken
	}

	sub, err := claims.GetSubject()
	if err != nil || sub == "" {
		return "", ErrInvalidToken
	}

	return sub, nil
}

// CheckPassword verifies a plaintext password against a bcrypt hash.
func CheckPassword(password, hash string) error {
	err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password))
	if err != nil {
		return ErrInvalidCredentials
	}
	return nil
}
