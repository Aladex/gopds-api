package utils

import (
	"bytes"
	crand "crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"hash"
	"math/big"
	"strconv"
	"strings"
	"time"

	"gopds-api/models"

	"github.com/golang-jwt/jwt/v4"
	"github.com/spf13/viper"
	"golang.org/x/crypto/pbkdf2"
)

const (
	allowedChars     = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	allowedCharsSize = len(allowedChars)
)

// Token struct for token creation and checking
type Token struct {
	UserID      string
	DatabaseID  int64
	IsSuperUser bool
	TokenType   string // "access" or "refresh"
	jwt.RegisteredClaims
}

// GetRandomString returns a securely generated random string.
//
// It draws straight from crypto/rand. It used to reach the same entropy the
// long way round, through a math/rand.Rand whose Source was backed by
// crypto/rand — sound, but it read as the weak generator to anyone scanning the
// file, gosec included, and the indirection bought nothing.
//
// A failure of the system entropy source is not something a caller can act on
// and not something to paper over with a predictable string, so it panics.
func GetRandomString(length int) string {
	b := make([]byte, length)
	limit := big.NewInt(int64(allowedCharsSize))

	for i := range b {
		n, err := crand.Int(crand.Reader, limit)
		if err != nil {
			panic(fmt.Sprintf("crypto/rand is unavailable: %v", err))
		}
		b[i] = allowedChars[n.Int64()]
	}

	return string(b)
}

// CreatePasswordHash creates a password hash using pbkdf2
func CreatePasswordHash(password string) string {
	salt := GetRandomString(12)
	if strings.Contains(salt, "$") {
		return ""
	}
	pHash := pbkdf2.Key([]byte(password), []byte(salt), 100000, sha256.Size, sha256.New)
	b64Hash := base64.StdEncoding.EncodeToString(pHash)
	return fmt.Sprintf("%s$%d$%s$%s", "pbkdf2_sha256", 100000, salt, b64Hash)
}

// CheckPbkdf2 checks if the password matches the hash using pbkdf2
func CheckPbkdf2(password, encoded string, keyLen int, h func() hash.Hash) (bool, error) {
	parts := strings.SplitN(encoded, "$", 4)
	if len(parts) != 4 {
		return false, errors.New("Hash must consist of 4 segments")
	}
	iter, err := strconv.Atoi(parts[1])
	if err != nil {
		return false, fmt.Errorf("Wrong number of iterations: %v", err)
	}
	salt := []byte(parts[2])
	k, err := base64.StdEncoding.DecodeString(parts[3])
	if err != nil {
		return false, fmt.Errorf("Wrong hash encoding: %v", err)
	}
	dk := pbkdf2.Key([]byte(password), salt, iter, keyLen, h)
	return bytes.Equal(k, dk), nil
}

// CreateTokenPair creates both access and refresh tokens for the user.
// Access tokens are signed with sessions.key, refresh tokens with sessions.refresh.
func CreateTokenPair(user models.User) (string, string, error) {
	// Create access token (15 minutes)
	accessToken := Token{
		UserID:      user.Login,
		DatabaseID:  user.ID,
		IsSuperUser: user.IsSuperUser,
		TokenType:   "access",
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    "gopds-api",
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(15 * time.Minute)),
		},
	}

	// Create refresh token (7 days)
	refreshToken := Token{
		UserID:      user.Login,
		DatabaseID:  user.ID,
		IsSuperUser: user.IsSuperUser,
		TokenType:   "refresh",
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    "gopds-api",
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(7 * 24 * time.Hour)),
		},
	}

	accessTokenString, err := jwt.NewWithClaims(jwt.GetSigningMethod("HS256"), accessToken).SignedString([]byte(viper.GetString("sessions.key")))
	if err != nil {
		return "", "", err
	}

	refreshTokenString, err := jwt.NewWithClaims(jwt.GetSigningMethod("HS256"), refreshToken).SignedString([]byte(viper.GetString("sessions.refresh")))
	if err != nil {
		return "", "", err
	}

	return accessTokenString, refreshTokenString, nil
}

// CheckAccessToken validates an access token: verifies the signature with
// sessions.key and rejects tokens that are not of type "access".
func CheckAccessToken(token string) (string, int64, bool, error) {
	tokenCheck, err := jwt.ParseWithClaims(token, &Token{}, func(token *jwt.Token) (interface{}, error) {
		return []byte(viper.GetString("sessions.key")), nil
	})

	if tokenCheck == nil {
		return "", 0, false, err
	}

	claims, ok := tokenCheck.Claims.(*Token)
	if !ok || !tokenCheck.Valid {
		return "", 0, false, errors.New("invalid_token")
	}

	if claims.TokenType != "access" {
		return "", 0, false, errors.New("invalid_token_type")
	}

	return claims.UserID, claims.DatabaseID, claims.IsSuperUser, nil
}

// CheckRefreshToken validates a refresh token: verifies the signature with
// sessions.refresh and rejects tokens that are not of type "refresh".
func CheckRefreshToken(token string) (string, int64, bool, error) {
	tokenCheck, err := jwt.ParseWithClaims(token, &Token{}, func(token *jwt.Token) (interface{}, error) {
		return []byte(viper.GetString("sessions.refresh")), nil
	})

	if tokenCheck == nil {
		return "", 0, false, err
	}

	claims, ok := tokenCheck.Claims.(*Token)
	if !ok || !tokenCheck.Valid {
		return "", 0, false, errors.New("invalid_token")
	}

	if claims.TokenType != "refresh" {
		return "", 0, false, errors.New("invalid_token_type")
	}

	return claims.UserID, claims.DatabaseID, claims.IsSuperUser, nil
}
