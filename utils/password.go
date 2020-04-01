package utils

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"github.com/dgrijalva/jwt-go"
	"github.com/spf13/viper"
	"golang.org/x/crypto/pbkdf2"
	"hash"
	"strconv"
	"strings"
	"time"
)

// Token структура токена для работы с API
type Token struct {
	UserID string
	jwt.StandardClaims
}

// CheckPbkdf2 проверка пароля в pbdkf2
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

// CreateToken Создание токена при логине пользователя в систему
func CreateToken(username string) (string, error) {
	tk := Token{
		UserID: username,
		StandardClaims: jwt.StandardClaims{
			Issuer:   "dashboard-api",
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

// CheckToken Проверка токена на подмену
func CheckToken(token string) (string, error) {
	tokenCheck, err := jwt.ParseWithClaims(token, &Token{}, func(token *jwt.Token) (interface{}, error) {
		return []byte(viper.GetString("sessions.key")), nil
	})

	if tokenCheck == nil {
		return "", errors.New("token is not valid")
	}

	if claims, ok := tokenCheck.Claims.(*Token); ok && tokenCheck.Valid {
		return claims.UserID, nil
	}
	return "", err
}
