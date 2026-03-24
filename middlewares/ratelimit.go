package middlewares

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/go-redis/redis"
)

const (
	limitPerIP    = 10
	limitPerUser  = 5
	windowSeconds = 60
	keyPrefixIP   = "login_rl:ip:"
	keyPrefixUser = "login_rl:user:"
)

var rateLimitRedis *redis.Client

// SetRateLimitRedis sets the Redis client used for login rate limiting.
func SetRateLimitRedis(client *redis.Client) {
	rateLimitRedis = client
}

// increment increments the fixed-window counter and returns the new count.
// TTL is set only on first increment so the window is anchored to the first request.
func increment(key string) (int64, error) {
	count, err := rateLimitRedis.Incr(key).Result()
	if err != nil {
		return 0, err
	}
	if count == 1 {
		rateLimitRedis.Expire(key, windowSeconds*time.Second)
	}
	return count, nil
}

// retryAfter returns remaining TTL in seconds (minimum 1).
func retryAfter(key string) int {
	ttl, err := rateLimitRedis.TTL(key).Result()
	if err != nil || ttl <= 0 {
		return windowSeconds
	}
	if secs := int(ttl.Seconds()); secs > 0 {
		return secs
	}
	return 1
}

// LoginRateLimitMiddleware limits login attempts: 10/min per IP, 5/min per username.
func LoginRateLimitMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		if rateLimitRedis == nil {
			c.Next()
			return
		}

		// Check IP limit
		ipKey := fmt.Sprintf("%s%s", keyPrefixIP, c.ClientIP())
		if ipCount, err := increment(ipKey); err == nil && ipCount > limitPerIP {
			c.Header("Retry-After", strconv.Itoa(retryAfter(ipKey)))
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{"error": "too_many_requests"})
			return
		}

		// Read body once, restore it so AuthCheck can bind it
		body, err := c.GetRawData()
		if err == nil && len(body) > 0 {
			c.Request.Body = io.NopCloser(bytes.NewReader(body))

			var req struct {
				Username string `json:"username"`
			}
			if json.Unmarshal(body, &req) == nil && req.Username != "" {
				userKey := fmt.Sprintf("%s%s", keyPrefixUser, req.Username)
				if userCount, err := increment(userKey); err == nil && userCount > limitPerUser {
					c.Header("Retry-After", strconv.Itoa(retryAfter(userKey)))
					c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{"error": "too_many_requests"})
					return
				}
			}
		}

		c.Next()
	}
}