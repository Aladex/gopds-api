package utils

import (
	"bytes"
	crand "crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"github.com/golang-jwt/jwt/v4"
	"github.com/spf13/viper"
	"golang.org/x/crypto/pbkdf2"
	"gopds-api/models"
	"hash"
	"math/big"
	"math/rand"
	"strconv"
	"strings"
	"time"
)

const (
	allowedChars     = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	allowedCharsSize = len(allowedChars)
	maxInt           = 1<<63 - 1
)

type source struct{}

func (s *source) Int63() int64 {
	return int64(s.Uint64() & ^uint64(1<<63))
}

func (s *source) Uint64() uint64 {
	i, err := crand.Int(crand.Reader, big.NewInt(maxInt))

	if err != nil {
		panic(err)
	}

	return i.Uint64()
}

func (s *source) Seed(seed int64) {}

// Token struct for token creation and checking
type Token struct {
	UserID     string
	DatabaseID int64
	jwt.StandardClaims
}

// GetRandomString returns a securely generated random string.
func GetRandomString(length int) string {
	b := make([]byte, length)
	rnd := rand.New(&source{})

	for i := range b {
		c := rnd.Intn(allowedCharsSize)
		b[i] = allowedChars[c]
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

// CreateToken creates a token for the user
func CreateToken(user models.User) (string, error) {
	tk := Token{
		UserID:     user.Login,
		DatabaseID: user.ID,
		StandardClaims: jwt.StandardClaims{
			Issuer:   "gopds-api",
			IssuedAt: time.Now().Unix(),
		},
	}
	token := jwt.NewWithClaims(jwt.GetSigningMethod("HS256"), tk)
	tokenString, err := token.SignedString([]byte(viper.GetString("sessions.key")))
	if err != nil {
		return "", err
	}
	return tokenString, nil
}

// CheckToken checks if the token is valid
func CheckToken(token string) (string, int64, error) {
	tokenCheck, err := jwt.ParseWithClaims(token, &Token{}, func(token *jwt.Token) (interface{}, error) {
		return []byte(viper.GetString("sessions.key")), nil
	})

	if tokenCheck == nil {
		return "", 0, errors.New("token is not valid")
	}

	if claims, ok := tokenCheck.Claims.(*Token); ok && tokenCheck.Valid {
		return claims.UserID, claims.DatabaseID, nil
	}
	return "", 0, err
}
