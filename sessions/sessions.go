package sessions

import (
	"context"
	"gopds-api/models"
	"gopds-api/utils"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/sirupsen/logrus"
)

func CheckSessionKeyInRedis(ctx context.Context, token string) (string, error) {
	username, err := rdb.WithContext(ctx).Get(token).Result()
	if err != nil {
		return "", err
	}
	return username, nil
}

func SetSessionKey(ctx context.Context, lu models.LoggedInUser) error {
	_, err := rdb.WithContext(ctx).Set(*lu.Token, strings.ToLower(lu.User), 24*time.Hour).Result()
	if err != nil {
		logrus.Println(err)
		return err
	}
	return nil
}

// UpdateSessionKey updates a user's session key.
func UpdateSessionKey(ctx context.Context, lu models.LoggedInUser) error {
	_, err := rdb.WithContext(ctx).Expire(*lu.Token, 24*time.Hour).Result()
	if err != nil {
		logrus.Println(err)
		return err
	}
	return nil
}

// DeleteSessionKey deletes a user's session key.
func DeleteSessionKey(ctx context.Context, lu models.LoggedInUser) error {
	_, err := rdb.WithContext(ctx).Del(*lu.Token).Result()
	if err != nil {
		logrus.Println(err)
		return err
	}
	return nil
}

// DropAllSessions removes all session keys for a user.
func DropAllSessions(token string) {
	username, _, _, err := utils.CheckToken(token)
	if err != nil {
		logrus.Println(err)
		return
	}
	keys, err := rdb.Keys("*").Result()
	if err != nil {
		logrus.Println(err)
		return
	}
	for _, k := range keys {
		if checkedUser, _, _, err := utils.CheckToken(k); err == nil && checkedUser == username {
			rdb.Del(k)
		}
	}
}

// GenerateTokenPassword generates a temporary token for password change.
func GenerateTokenPassword(user string) string {
	passwordToken := uuid.New().String()
	rdbToken.Set(user, passwordToken, 90*time.Minute)
	return passwordToken
}

// CheckTokenPassword checks if a temporary token exists for a user.
func CheckTokenPassword(token string) string {
	keys, err := rdbToken.Keys("*").Result()
	if err != nil {
		logrus.Println(err)
		return ""
	}
	for _, k := range keys {
		if rdbToken.Get(k).Val() == token {
			return k
		}
	}
	return ""
}

// DeleteTokenPassword removes a user's temporary token.
func DeleteTokenPassword(token string) {
	keys, err := rdbToken.Keys("*").Result()
	if err != nil {
		logrus.Println(err)
		return
	}
	for _, k := range keys {
		if rdbToken.Get(k).Val() == token {
			rdbToken.Del(k)
		}
	}
}

// BlacklistRefreshToken adds a refresh token to blacklist
func BlacklistRefreshToken(ctx context.Context, refreshToken string) error {
	// Calculate remaining time until token expires (7 days max)
	remainingTime := 7 * 24 * time.Hour

	// Store in Redis with prefix to identify blacklisted tokens
	blacklistKey := "blacklist:refresh:" + refreshToken
	_, err := rdb.WithContext(ctx).Set(blacklistKey, "revoked", remainingTime).Result()
	if err != nil {
		logrus.Println("Error blacklisting refresh token:", err)
		return err
	}
	return nil
}

// IsRefreshTokenBlacklisted checks if refresh token is blacklisted
func IsRefreshTokenBlacklisted(ctx context.Context, refreshToken string) bool {
	blacklistKey := "blacklist:refresh:" + refreshToken
	_, err := rdb.WithContext(ctx).Get(blacklistKey).Result()
	return err == nil // If key exists, token is blacklisted
}

// CleanupExpiredBlacklistedTokens removes expired entries from blacklist
func CleanupExpiredBlacklistedTokens(ctx context.Context) error {
	// Redis automatically removes expired keys, so this is mainly for manual cleanup if needed
	pattern := "blacklist:refresh:*"
	keys, err := rdb.WithContext(ctx).Keys(pattern).Result()
	if err != nil {
		return err
	}

	for _, key := range keys {
		// Check if token is still valid
		token := strings.TrimPrefix(key, "blacklist:refresh:")
		_, _, _, _, err := utils.CheckTokenWithType(token)
		if err != nil {
			// Token is expired, remove from blacklist
			rdb.WithContext(ctx).Del(key)
		}
	}
	return nil
}
