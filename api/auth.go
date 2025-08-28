package api

import (
	"context"
	"errors"
	"gopds-api/database"
	"gopds-api/httputil"
	"gopds-api/logging"
	"gopds-api/middlewares"
	"gopds-api/models"
	"gopds-api/sessions"
	"gopds-api/utils"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/spf13/viper"
)

// DropAllSessions method for dropping all sessions from Redis
// Auth godoc
// @Summary Drop all sessions from Redis
// @Description Remove all sessions from Redis
// @Tags login
// @Param Authorization header string true "Token without 'Bearer' prefix"
// @Accept  json
// @Produce  json
// @Success 200 {object} models.LoggedInUser
// @Failure 400 {object} httputil.HTTPError
// @Failure 403 {object} httputil.HTTPError
// @Router /api/drop-sessions [get]
func DropAllSessions(c *gin.Context) {
	// Create context with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// Get token from header or cookie
	userToken := c.Request.Header.Get("Authorization")
	if userToken == "" {
		var err error
		userToken, err = c.Cookie("token")
		if err != nil {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Token not provided"})
			return
		}
	}

	// Get username from context
	username := c.GetString("username")
	if username == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Username not provided"})
		return
	}

	// Delete session key
	err := sessions.DeleteSessionKey(ctx, models.LoggedInUser{
		User:  strings.ToLower(username),
		Token: &userToken,
	})
	if err != nil {
		c.JSON(http.StatusForbidden, gin.H{"error": err.Error()})
		return
	}

	// Drop all sessions
	go sessions.DropAllSessions(userToken)

	// Set cookie to expire
	c.JSON(http.StatusOK, gin.H{"result": "ok"})
}

// AuthCheck method for returning a user and token for header
// Auth godoc
// @Summary Return user and token for header
// @Description Login method for token generation
// @Tags login
// @Accept  json
// @Produce  json
// @Param  body body models.LoginRequest true "Login Data"
// @Success 200 {object} models.LoggedInUser
// @Failure 400 {object} httputil.HTTPError
// @Failure 403 {object} httputil.HTTPError
// @Failure 500 {object} httputil.HTTPError
// @Router /api/login [post]
func AuthCheck(c *gin.Context) {
	var user models.LoginRequest
	if err := c.ShouldBindJSON(&user); err != nil {
		httputil.NewError(c, http.StatusBadRequest, err)
		return
	}

	res, dbUser, err := database.CheckUser(user)
	if err != nil || !dbUser.Active {
		status := http.StatusForbidden
		var errMsg error
		if err != nil {
			errMsg = errors.New("bad_credentials")
		} else {
			errMsg = errors.New("user not active")
		}
		httputil.NewError(c, status, errMsg)
		return
	}

	if !res {
		httputil.NewError(c, http.StatusForbidden, errors.New("bad password"))
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// Create token pair instead of single token
	accessToken, refreshToken, err := utils.CreateTokenPair(dbUser)
	if err != nil {
		httputil.NewError(c, http.StatusForbidden, err)
		return
	}

	hf, err := database.HaveFavs(dbUser.ID)
	if err != nil {
		httputil.NewError(c, http.StatusBadRequest, err)
		return
	}

	thisUser := models.LoggedInUser{
		User:        dbUser.Login,
		FirstName:   dbUser.FirstName,
		LastName:    dbUser.LastName,
		Token:       &accessToken,
		IsSuperuser: &dbUser.IsSuperUser,
		HaveFavs:    &hf,
	}

	if err := sessions.SetSessionKey(ctx, thisUser); err != nil {
		httputil.NewError(c, http.StatusForbidden, err)
		return
	}

	go database.LoginDateSet(&dbUser)
	c.SetSameSite(http.SameSiteLaxMode)
	// Set access token (15 minutes)
	c.SetCookie("token", accessToken, 900, "/", viper.GetString("project_domain"), !viper.GetBool("app.devel_mode"), true)
	// Set refresh token (7 days)
	c.SetCookie("refresh_token", refreshToken, 604800, "/", viper.GetString("project_domain"), !viper.GetBool("app.devel_mode"), true)

	c.JSON(200, thisUser)
}

// LogOut method for logging out the user
// Auth godoc
// @Summary Log out user
// @Description Log out the user by invalidating their session
// @Tags login
// @Param Authorization header string true "Token without 'Bearer' prefix"
// @Accept  json
// @Produce  json
// @Success 200 {object} models.LoggedInUser
// @Failure 400 {object} httputil.HTTPError
// @Failure 403 {object} httputil.HTTPError
// @Router /api/logout [get]
func LogOut(c *gin.Context) {
	username := strings.ToLower(c.GetString("username"))
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// Get token from header or cookie
	userToken := c.Request.Header.Get("Authorization")
	if userToken == "" {
		var err error
		userToken, err = c.Cookie("token")
		if err != nil {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Token not provided"})
			return
		}
	}

	// Get refresh token and blacklist it
	refreshToken, err := c.Cookie("refresh_token")
	if err == nil && refreshToken != "" {
		// Blacklist the refresh token to prevent its reuse
		if blacklistErr := sessions.BlacklistRefreshToken(ctx, refreshToken); blacklistErr != nil {
			logging.Errorf("Failed to blacklist refresh token: %v", blacklistErr)
		}
	}

	// Get username from context
	if username == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Username not provided"})
		return
	}

	// Delete session key
	err = sessions.DeleteSessionKey(ctx, models.LoggedInUser{
		User:  strings.ToLower(username),
		Token: &userToken,
	})
	if err != nil {
		c.JSON(http.StatusForbidden, gin.H{"error": err.Error()})
		return
	}

	// Clear authentication cookies by setting them to expire immediately
	c.SetCookie("token", "", -1, "/", viper.GetString("project_domain"), !viper.GetBool("app.devel_mode"), true)
	c.SetCookie("refresh_token", "", -1, "/", viper.GetString("project_domain"), !viper.GetBool("app.devel_mode"), true)

	// Set cookie to expire
	c.JSON(http.StatusOK, gin.H{"result": "ok"})
}

// SelfUser method for retrieving user information by token
// Auth godoc
// @Summary Get user information by token
// @Description Retrieve user information using the provided token
// @Tags login
// @Param Authorization header string true "Token without 'Bearer' prefix"
// @Accept  json
// @Produce  json
// @Success 200 {object} models.LoggedInUser
// @Failure 400 {object} httputil.HTTPError
// @Failure 403 {object} httputil.HTTPError
// @Router /api/books/self-user [get]
func SelfUser(c *gin.Context) {
	username := strings.ToLower(c.GetString("username"))
	dbUser, err := database.GetUser(username)
	if err != nil {
		httputil.NewError(c, http.StatusBadRequest, err)
		return
	}

	if hf, err := database.HaveFavs(dbUser.ID); err != nil {
		httputil.NewError(c, http.StatusBadRequest, err)
	} else {
		c.JSON(http.StatusOK, models.LoggedInUser{
			User:        dbUser.Login,
			BooksLang:   dbUser.BooksLang,
			IsSuperuser: &dbUser.IsSuperUser,
			FirstName:   dbUser.FirstName,
			LastName:    dbUser.LastName,
			HaveFavs:    &hf,
			Collections: dbUser.Collections,
		})
	}
}

// GetCSRFToken method for getting CSRF token
// Auth godoc
// @Summary Get CSRF token
// @Description Get CSRF token for form protection
// @Tags login
// @Accept  json
// @Produce  json
// @Success 200 {object} map[string]string
// @Router /api/csrf-token [get]
func GetCSRFToken(c *gin.Context) {
	middlewares.SetCSRFToken(c)
}

// ChangeUser method for updating user information by token
// Auth godoc
// @Summary Update user information
// @Description Update user information based on the provided token
// @Tags users
// @Param Authorization header string true "Token without 'Bearer' prefix"
// @Accept  json
// @Produce  json
// @Param  body body models.SelfUserChangeRequest true "User update information"
// @Success 200 {object} models.LoggedInUser
// @Failure 400 {object} httputil.HTTPError
// @Failure 403 {object} httputil.HTTPError
// @Failure 500 {object} httputil.HTTPError
// @Router /api/books/change-me [post]
func ChangeUser(c *gin.Context) {
	var userNewData models.SelfUserChangeRequest
	if err := c.ShouldBindJSON(&userNewData); err != nil {
		httputil.NewError(c, http.StatusBadRequest, err)
		return
	}

	username := c.GetString("username")
	var dbUser models.User
	var err error

	// Проверка пароля требуется ТОЛЬКО при смене пароля
	if len(userNewData.NewPassword) > 0 {
		// Для смены пароля обязательно нужен текущий пароль
		if len(userNewData.Password) == 0 {
			httputil.NewError(c, http.StatusBadRequest, errors.New("current_password_required_for_password_change"))
			return
		}

		u := models.LoginRequest{Login: username, Password: userNewData.Password}
		result, tempUser, err := database.CheckUser(u)

		if err != nil || !result {
			httputil.NewError(c, http.StatusForbidden, errors.New("auth_error"))
			return
		}

		dbUser = tempUser

		// Валидация нового пароля
		if len(userNewData.NewPassword) < 8 {
			httputil.NewError(c, http.StatusBadRequest, errors.New("bad_password"))
			return
		}
		if userNewData.NewPassword == userNewData.Password {
			httputil.NewError(c, http.StatusBadRequest, errors.New("same_password"))
			return
		}
	} else {
		// Для изменения профильных данных (имя, фамилия, язык) пароль не нужен
		// Просто получаем пользователя по имени
		dbUser, err = database.GetUser(username)
		if err != nil {
			httputil.NewError(c, http.StatusBadRequest, err)
			return
		}
	}

	// Валидация новых данных
	if len(userNewData.FirstName) > 100 || len(userNewData.LastName) > 100 {
		httputil.NewError(c, http.StatusBadRequest, errors.New("name_too_long"))
		return
	}

	// Валидация языка книг
	validLangs := map[string]bool{"en": true, "ru": true, "de": true, "fr": true, "es": true, "it": true}
	if len(userNewData.BooksLang) > 0 && !validLangs[userNewData.BooksLang] {
		httputil.NewError(c, http.StatusBadRequest, errors.New("invalid_language"))
		return
	}

	// Используем новую безопасную функцию для обновления профиля
	updatedUser, err := database.UpdateUserProfile(dbUser.ID, userNewData)
	if err != nil {
		httputil.NewError(c, http.StatusInternalServerError, err)
		return
	}

	if hf, err := database.HaveFavs(updatedUser.ID); err != nil {
		httputil.NewError(c, http.StatusBadRequest, err)
		return
	} else {
		c.JSON(http.StatusOK, models.LoggedInUser{
			User:        updatedUser.Login,
			FirstName:   updatedUser.FirstName,
			LastName:    updatedUser.LastName,
			IsSuperuser: &updatedUser.IsSuperUser,
			BooksLang:   updatedUser.BooksLang,
			HaveFavs:    &hf,
		})
	}
}

// RefreshToken method for refreshing access token using refresh token
// Auth godoc
// @Summary Refresh access token
// @Description Refresh access token using refresh token
// @Tags login
// @Accept  json
// @Produce  json
// @Success 200 {object} map[string]string
// @Failure 400 {object} httputil.HTTPError
// @Failure 401 {object} httputil.HTTPError
// @Router /api/refresh-token [post]
func RefreshToken(c *gin.Context) {
	refreshToken, err := c.Cookie("refresh_token")
	if err != nil {
		httputil.NewError(c, http.StatusUnauthorized, errors.New("refresh_token_missing"))
		return
	}

	username, _, _, tokenType, err := utils.CheckTokenWithType(refreshToken)
	if err != nil || tokenType != "refresh" {
		httputil.NewError(c, http.StatusUnauthorized, errors.New("invalid_refresh_token"))
		return
	}

	// Check if refresh token is blacklisted
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if sessions.IsRefreshTokenBlacklisted(ctx, refreshToken) {
		httputil.NewError(c, http.StatusUnauthorized, errors.New("refresh_token_revoked"))
		return
	}

	// Get user from database
	dbUser, err := database.GetUser(username)
	if err != nil || !dbUser.Active {
		httputil.NewError(c, http.StatusUnauthorized, errors.New("user_not_found_or_inactive"))
		return
	}

	// Blacklist the old refresh token (token rotation)
	if blacklistErr := sessions.BlacklistRefreshToken(ctx, refreshToken); blacklistErr != nil {
		logging.Errorf("Failed to blacklist old refresh token: %v", blacklistErr)
		// Continue anyway - this is not critical for the refresh operation
	}

	// Create new token pair
	accessToken, newRefreshToken, err := utils.CreateTokenPair(dbUser)
	if err != nil {
		httputil.NewError(c, http.StatusInternalServerError, err)
		return
	}

	// Update session in Redis
	thisUser := models.LoggedInUser{
		User:  strings.ToLower(dbUser.Login),
		Token: &accessToken,
	}

	if err := sessions.SetSessionKey(ctx, thisUser); err != nil {
		httputil.NewError(c, http.StatusInternalServerError, err)
		return
	}

	// Set new cookies
	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie("token", accessToken, 900, "/", viper.GetString("project_domain"), !viper.GetBool("app.devel_mode"), true)                // 15 min
	c.SetCookie("refresh_token", newRefreshToken, 604800, "/", viper.GetString("project_domain"), !viper.GetBool("app.devel_mode"), true) // 7 days

	c.JSON(http.StatusOK, gin.H{
		"access_token":  accessToken,
		"refresh_token": newRefreshToken,
		"expires_in":    900, // 15 minutes
	})
}
