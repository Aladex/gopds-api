package logging

import (
	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"
	"time"
)

type loggerEntryWithFields interface {
	WithFields(fields logrus.Fields) *logrus.Entry
}

// SetLog настройка логов проекта
func SetLog() *logrus.Logger {
	log := logrus.New()
	//hook, err := lSyslog.NewSyslogHook(viper.GetString("app.log.proto"),
	//	viper.GetString("app.log.server"), syslog.LOG_LOCAL4, "gopds-api")
	//
	//if err == nil {
	//	log.AddHook(hook)
	//}

	log.SetFormatter(&logrus.JSONFormatter{})
	return log
}

// GinrusLogger функция для имплементации расширенного логирования в GIN
func GinrusLogger(logger loggerEntryWithFields) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		// some evil middlewares modify this values
		path := c.Request.URL.Path
		c.Next()

		end := time.Now()
		latency := end.Sub(start)

		entry := logger.WithFields(logrus.Fields{
			"status":     c.Writer.Status(),
			"method":     c.Request.Method,
			"path":       path,
			"ip":         c.ClientIP(),
			"latency":    latency,
			"user-agent": c.Request.UserAgent(),
			"time":       end,
		})

		if len(c.Errors) > 0 {
			// Append error field if this is an erroneous request.
			entry.Error(c.Errors.String())
		} else {
			entry.Info()
		}
	}
}
