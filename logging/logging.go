package logging

import (
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"
)

var logger atomic.Pointer[logrus.Logger]

func init() {
	l := logrus.New()
	l.SetFormatter(&logrus.TextFormatter{
		FullTimestamp:   true,
		TimestampFormat: "2006-01-02 15:04:05",
	})
	logger.Store(l)

	// Set the global logrus instance to use the same formatter
	logrus.SetFormatter(&logrus.TextFormatter{
		FullTimestamp:   true,
		TimestampFormat: "2006-01-02 15:04:05",
	})
}

// GetLogger returns the configured logger instance
func GetLogger() *logrus.Logger {
	return logger.Load()
}

// SetLogger swaps the logger, or restores the package one on nil. Tests use
// it to capture what production code writes. The pointer swap is atomic
// because connections outlive the test that captured their output.
func SetLogger(l *logrus.Logger) {
	if l == nil {
		l = logrus.StandardLogger()
	}
	logger.Store(l)
}

// Info logs an info message
func Info(args ...interface{}) {
	logger.Load().Info(args...)
}

// Infof logs a formatted info message
func Infof(format string, args ...interface{}) {
	logger.Load().Infof(format, args...)
}

// Error logs an error message
func Error(args ...interface{}) {
	logger.Load().Error(args...)
}

// Errorf logs a formatted error message
func Errorf(format string, args ...interface{}) {
	logger.Load().Errorf(format, args...)
}

// Warn logs a warning message
func Warn(args ...interface{}) {
	logger.Load().Warn(args...)
}

// Warnf logs a formatted warning message
func Warnf(format string, args ...interface{}) {
	logger.Load().Warnf(format, args...)
}

// Debug logs a debug message
func Debug(args ...interface{}) {
	logger.Load().Debug(args...)
}

// Debugf logs a formatted debug message
func Debugf(format string, args ...interface{}) {
	logger.Load().Debugf(format, args...)
}

// Fields is the structured-field map WithFields takes. It is an alias rather
// than a new type so callers can name it without importing logrus, which the
// lint config denies outside this package.
type Fields = logrus.Fields

// WithField creates an entry with a single field
func WithField(key string, value interface{}) *logrus.Entry {
	return logger.Load().WithField(key, value)
}

// WithFields creates an entry with multiple fields
func WithFields(fields logrus.Fields) *logrus.Entry {
	return logger.Load().WithFields(fields)
}

func GinrusLogger() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()
		fields := logrus.Fields{
			"status":     c.Writer.Status(),
			"method":     c.Request.Method,
			"path":       c.Request.URL.Path,
			"ip":         c.Request.RemoteAddr,
			"latency":    time.Since(start),
			"user-agent": c.Request.UserAgent(),
			"time":       time.Now().Format(time.RFC1123),
		}
		// Handlers attach the cause of a refusal with c.Error and answer the
		// reader with a fixed public sentence. Without this the cause was
		// collected and dropped: the log showed a 503 with no way to tell a
		// dead cache from a build that ran out of time from a server already
		// building its fill, which are three different operational problems
		// wearing one status.
		if errs := c.Errors.Errors(); len(errs) > 0 {
			fields["errors"] = errs
		}
		logger.Load().WithFields(fields).Info("HTTP Request")
	}
}
