package api

import (
	"context"
	"errors"
	"net/http"
	"time"

	"gopds-api/httputil"
	"gopds-api/sessions"

	"github.com/gin-gonic/gin"
)

type themePreferenceRequest struct {
	Theme string `json:"theme"`
}

func getTokenFromRequest(c *gin.Context) (string, error) {
	if token := c.Request.Header.Get("Authorization"); token != "" {
		return token, nil
	}
	token, err := c.Cookie("token")
	if err != nil || token == "" {
		return "", errors.New("token_not_provided")
	}
	return token, nil
}

// GetThemePreference returns the theme stored in the current session.
// @Summary Get session theme
// @Description Get theme preference stored in the current session
// @Tags theme
// @Param Authorization header string true "Token without 'Bearer' prefix"
// @Accept  json
// @Produce  json
// @Success 200 {object} map[string]string
// @Failure 401 {object} httputil.HTTPError
// @Failure 500 {object} httputil.HTTPError
// @Router /api/books/theme [get]
func GetThemePreference(c *gin.Context) {
	token, err := getTokenFromRequest(c)
	if err != nil {
		httputil.NewError(c, http.StatusUnauthorized, err)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	theme, err := sessions.GetThemeForToken(ctx, token)
	if err != nil {
		httputil.NewError(c, http.StatusInternalServerError, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{"theme": theme})
}

// SetThemePreference saves the theme in the current session.
// @Summary Set session theme
// @Description Save theme preference in the current session
// @Tags theme
// @Param Authorization header string true "Token without 'Bearer' prefix"
// @Accept  json
// @Produce  json
// @Param  body body themePreferenceRequest true "Theme preference"
// @Success 200 {object} map[string]string
// @Failure 400 {object} httputil.HTTPError
// @Failure 401 {object} httputil.HTTPError
// @Failure 500 {object} httputil.HTTPError
// @Router /api/books/theme [post]
func SetThemePreference(c *gin.Context) {
	token, err := getTokenFromRequest(c)
	if err != nil {
		httputil.NewError(c, http.StatusUnauthorized, err)
		return
	}

	var req themePreferenceRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httputil.NewError(c, http.StatusBadRequest, err)
		return
	}

	if req.Theme != "light" && req.Theme != "dark" {
		httputil.NewError(c, http.StatusBadRequest, errors.New("invalid_theme"))
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := sessions.SetThemeForToken(ctx, token, req.Theme); err != nil {
		httputil.NewError(c, http.StatusInternalServerError, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{"theme": req.Theme})
}
