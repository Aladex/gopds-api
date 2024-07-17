package logging

import (
	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"
	"time"
)

var CustomLog = logrus.New()

func init() {
	CustomLog.SetFormatter(&logrus.TextFormatter{
		FullTimestamp:   true,
		TimestampFormat: "2006-01-02 15:04:05",
	})
}

// GinrusLogger - middleware that logs requests using logrus
func GinrusLogger() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()
		end := time.Now()
		latency := end.Sub(start)

		CustomLog.WithFields(logrus.Fields{
			"status":     c.Writer.Status(),
			"method":     c.Request.Method,
			"path":       c.Request.URL.Path,
			"ip":         c.Request.RemoteAddr,
			"latency":    latency,
			"user-agent": c.Request.UserAgent(),
			"time":       end.Format(time.RFC1123),
		}).Info()
	}
}
