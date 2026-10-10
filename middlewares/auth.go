package middlewares

import (
	"context"
	"errors"
	"net/http"
	"time"

	"gopds-api/models"
	"gopds-api/sessions"
	"gopds-api/utils"

	"github.com/gin-gonic/gin"
)

// sessionCheckTimeout bounds one session-store round trip.
const sessionCheckTimeout = 2 * time.Second

// Session verdicts for the WebSocket revalidation loop. The socket needs to
// tell "the session is confirmed gone" apart from "Redis hiccuped, ask again
// later"; the HTTP middleware keeps collapsing both into 401.
var (
	// ErrSessionMissing: the token has no live session or fails signature
	// verification — a confirmed invalid session.
	ErrSessionMissing = errors.New("session_missing")
	// ErrSessionInconclusive: the session store could not be reached, so the
	// session's fate is unknown. Not an authorization decision.
	ErrSessionInconclusive = errors.New("session_inconclusive")
)

// ValidateWSSession checks the session behind token and classifies the
// failure for the socket's revalidation loop: ErrSessionMissing is a
// confirmed loss, anything else is an infrastructure error. It never updates
// the session timestamp: a liveness check must not extend the session.
func ValidateWSSession(token string) error {
	ctx, cancel := context.WithTimeout(context.Background(), sessionCheckTimeout)
	defer cancel()

	if err := sessions.CheckSessionExists(ctx, token); err != nil {
		if errors.Is(err, sessions.ErrSessionNotFound) {
			return ErrSessionMissing
		}
		return ErrSessionInconclusive
	}

	if _, _, _, err := utils.CheckAccessToken(token); err != nil {
		return ErrSessionMissing
	}
	return nil
}

// ValidateTokenPublic is a public wrapper for validateToken for use in WebSocket handlers
func ValidateTokenPublic(token string) (string, int64, bool, error) {
	return validateToken(token)
}

// validateToken validates an access token: checks Redis session and verifies
// the JWT signature with the access token key (sessions.key).
func validateToken(token string) (string, int64, bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// If token not in Redis, return error
	_, err := sessions.CheckSessionKeyInRedis(ctx, token)
	if err != nil {
		return "", 0, false, errors.New("invalid_session")
	}

	username, dbID, isSuperUser, err := utils.CheckAccessToken(token)
	if err != nil {
		return "", 0, false, errors.New("invalid_session")
	}

	// Update the session timestamp in Redis
	err = sessions.SetSessionKey(ctx, models.LoggedInUser{User: username, Token: &token})
	if err != nil {
		return "", 0, false, errors.New("session_update_failed")
	}

	return username, dbID, isSuperUser, nil
}

// abortWithStatus simplifies error responses.
func abortWithStatus(c *gin.Context, status int, message string) {
	c.AbortWithStatusJSON(status, gin.H{"error": message})
}

// AuthMiddleware checks if user is logged in and sets username in context.
func AuthMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		var token string
		var err error

		// Try to get token from header or cookie
		authHeader := c.GetHeader("Authorization")
		if authHeader != "" {
			token = authHeader
		} else {
			token, err = c.Cookie("token")
			if err != nil || token == "" {
				abortWithStatus(c, http.StatusUnauthorized, "required_token")
				return
			}
		}

		// Validate token
		username, dbID, isSuperUser, err := validateToken(token)
		if err != nil {
			abortWithStatus(c, http.StatusUnauthorized, err.Error())
			return
		}

		// Set username and user_id in context
		c.Set("username", username)
		c.Set("user_id", dbID)
		c.Set("is_superuser", isSuperUser)
		c.Next()
	}
}
