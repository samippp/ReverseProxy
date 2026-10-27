// Package auth issues and validates JWTs and guards routes with them.
package auth

import (
	"errors"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// Claims is the JWT payload.
type Claims struct {
	UserID   int32  `json:"user_id"`
	Username string `json:"username"`
	jwt.RegisteredClaims
}

// GenerateToken signs a 24h token and returns it with its lifetime in
// seconds. jwtSecret is ignored; JWT_SECRET is read from the environment.
func GenerateToken(userID int32, username, jwtSecret string) (string, int64, error) {
	expiry := time.Now().Add(24 * time.Hour)
	claims := Claims{
		UserID:   userID,
		Username: username,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(expiry),
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signedToken, err := token.SignedString([]byte(os.Getenv("JWT_SECRET")))
	if err != nil {
		return "", 0, err
	}

	return signedToken, int64(24 * 60 * 60), nil // 86400 seconds
}

// ValidateToken parses tokenStr and returns its claims if it's valid.
func ValidateToken(tokenStr string) (*Claims, error) {
	token, err := jwt.ParseWithClaims(tokenStr, &Claims{}, func(t *jwt.Token) (interface{}, error) {
		return []byte(os.Getenv("JWT_SECRET")), nil
	})
	if err != nil {
		return nil, err
	}

	claims, ok := token.Claims.(*Claims)
	if !ok || !token.Valid {
		return nil, errors.New("invalid token")
	}

	return claims, nil
}

// extractToken returns the bearer token from the Authorization header.
func extractToken(r *http.Request) (string, error) {
	authHeader := r.Header.Get("Authorization")
	if authHeader == "" {
		return "", errors.New("missing authorization header")
	}

	const prefix = "Bearer "
	if !strings.HasPrefix(authHeader, prefix) {
		return "", errors.New("authorization header must start with 'Bearer '")
	}

	return strings.TrimPrefix(authHeader, prefix), nil
}

// HandleAuthMiddleware rejects requests without a valid token with 401,
// and sets X-User-Id and X-Internal-Secret for the backend.
func HandleAuthMiddleware(h http.Handler, proxySecret string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token, err := extractToken(r)
		if err != nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		claims, err := ValidateToken(token)
		if err != nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		r.Header.Set("X-User-Id", strconv.Itoa(int(claims.UserID)))
		r.Header.Set("X-Internal-Secret", proxySecret)
		h.ServeHTTP(w, r)
	})
}
