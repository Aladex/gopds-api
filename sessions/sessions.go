package sessions

import (
	"context"
	"github.com/google/uuid"
	"github.com/sirupsen/logrus"
	"gopds-api/models"
	"gopds-api/utils"
	"strings"
	"time"
)

func SetSessionKey(ctx context.Context, lu models.LoggedInUser) error {
	_, err := rdb.WithContext(ctx).Set(*lu.Token, strings.ToLower(lu.User), 24*time.Hour).Result()
	if err != nil {
		logrus.Println(err)
		return err
	}
	return nil
}

// CheckSessionKey checks if a session key exists for a user.
func CheckSessionKey(ctx context.Context, lu models.LoggedInUser) bool {
	// return rdb.Get(*lu.Token).Val() == strings.ToLower(lu.User)
	val, err := rdb.WithContext(ctx).Get(*lu.Token).Result()
	if err != nil {
		logrus.Println(err)
		return false
	}
	return val == strings.ToLower(lu.User)
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
	username, _, err := utils.CheckToken(token)
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
		if checkedUser, _, err := utils.CheckToken(k); err == nil && checkedUser == username {
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
