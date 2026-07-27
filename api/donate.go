package api

import (
	"net/http"

	"gopds-api/config"

	"github.com/gin-gonic/gin"
)

// DonateMethods serves the ways of supporting the service that the operator has
// configured, in the order they wrote them.
//
// The list is closed over rather than read from a package-level configuration
// so that what is served is fixed when the routes are built, and so a test can
// hand it a list without arranging global state.
//
// It is public: every value in it is an address or a link meant to be given
// out. Nothing here is worth a session.
func DonateMethods(methods []config.DonateMethod) gin.HandlerFunc {
	// Serve an array rather than null when nothing is configured, so the
	// interface has one shape to handle instead of two.
	if methods == nil {
		methods = []config.DonateMethod{}
	}

	return func(c *gin.Context) {
		c.JSON(http.StatusOK, methods)
	}
}
