package logging

import (
	"time"

	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"
)

var logger *logrus.Logger

func init() {
	logger = logrus.New()
	logger.SetFormatter(&logrus.TextFormatter{
		FullTimestamp:   true,
		TimestampFormat: "2006-01-02 15:04:05",
	})

	// Set the global logrus instance to use the same formatter
	logrus.SetFormatter(&logrus.TextFormatter{
		FullTimestamp:   true,
		TimestampFormat: "2006-01-02 15:04:05",
	})
}

// GetLogger returns the configured logger instance
func GetLogger() *logrus.Logger {
	return logger
}

// Info logs an info message
func Info(args ...interface{}) {
	logger.Info(args...)
}

// Infof logs a formatted info message
func Infof(format string, args ...interface{}) {
	logger.Infof(format, args...)
}

// Error logs an error message
func Error(args ...interface{}) {
	logger.Error(args...)
}

// Errorf logs a formatted error message
func Errorf(format string, args ...interface{}) {
	logger.Errorf(format, args...)
}

// Warn logs a warning message
func Warn(args ...interface{}) {
	logger.Warn(args...)
}

// Warnf logs a formatted warning message
func Warnf(format string, args ...interface{}) {
	logger.Warnf(format, args...)
}

// Debug logs a debug message
func Debug(args ...interface{}) {
	logger.Debug(args...)
}

// Debugf logs a formatted debug message
func Debugf(format string, args ...interface{}) {
	logger.Debugf(format, args...)
}

// WithField creates an entry with a single field
func WithField(key string, value interface{}) *logrus.Entry {
	return logger.WithField(key, value)
}

// WithFields creates an entry with multiple fields
func WithFields(fields logrus.Fields) *logrus.Entry {
	return logger.WithFields(fields)
}

func GinrusLogger() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()
		logger.WithFields(logrus.Fields{
			"status":     c.Writer.Status(),
			"method":     c.Request.Method,
			"path":       c.Request.URL.Path,
			"ip":         c.Request.RemoteAddr,
			"latency":    time.Since(start),
			"user-agent": c.Request.UserAgent(),
			"time":       time.Now().Format(time.RFC1123),
		}).Info("HTTP Request")
	}
}
