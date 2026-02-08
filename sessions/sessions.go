package sessions

import (
	"context"
	"strings"
	"time"

	"gopds-api/logging"
	"gopds-api/models"
	"gopds-api/utils"

	"github.com/go-redis/redis"
	"github.com/google/uuid"
)

const themeKeyPrefix = "session:theme:"

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
		logging.Error(err)
		return err
	}
	return nil
}

// DeleteSessionKey deletes a user's session key.
func DeleteSessionKey(ctx context.Context, lu models.LoggedInUser) error {
	_, err := rdb.WithContext(ctx).Del(*lu.Token, themeKeyPrefix+*lu.Token).Result()
	if err != nil {
		logging.Error(err)
		return err
	}
	return nil
}

// DropAllSessions removes all session keys for a user.
func DropAllSessions(token string) {
	username, _, _, err := utils.CheckAccessToken(token)
	if err != nil {
		logging.Error(err)
		return
	}
	keys, err := rdb.Keys("*").Result()
	if err != nil {
		logging.Error(err)
		return
	}
	for _, k := range keys {
		if strings.HasPrefix(k, themeKeyPrefix) {
			keyToken := strings.TrimPrefix(k, themeKeyPrefix)
			if checkedUser, _, _, err := utils.CheckAccessToken(keyToken); err == nil && checkedUser == username {
				rdb.Del(k)
			}
			continue
		}
		if checkedUser, _, _, err := utils.CheckAccessToken(k); err == nil && checkedUser == username {
			rdb.Del(k, themeKeyPrefix+k)
		}
	}
}

func SetThemeForToken(ctx context.Context, token, theme string) error {
	ttl, err := rdb.WithContext(ctx).TTL(token).Result()
	if err != nil || ttl <= 0 {
		ttl = 24 * time.Hour
	}
	_, err = rdb.WithContext(ctx).Set(themeKeyPrefix+token, theme, ttl).Result()
	if err != nil {
		logging.Error(err)
		return err
	}
	return nil
}

func GetThemeForToken(ctx context.Context, token string) (string, error) {
	theme, err := rdb.WithContext(ctx).Get(themeKeyPrefix + token).Result()
	if err != nil {
		if err == redis.Nil {
			return "", nil
		}
		return "", err
	}
	return theme, nil
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
		logging.Error(err)
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
		logging.Error(err)
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
		logging.Error("Error blacklisting refresh token:", err)
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
