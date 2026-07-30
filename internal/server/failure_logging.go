package server

import (
	"bytes"
	"strings"
	"time"

	"src.solsynth.dev/sosys/filesystem/internal/service"

	"github.com/gin-gonic/gin"
)

const maxFailureDetailBytes = 4096

type failureLogWriter struct {
	gin.ResponseWriter
	body bytes.Buffer
}

func (w *failureLogWriter) Write(data []byte) (int, error) {
	w.capture(data)
	return w.ResponseWriter.Write(data)
}

func (w *failureLogWriter) WriteString(value string) (int, error) {
	w.capture([]byte(value))
	return w.ResponseWriter.WriteString(value)
}

func (w *failureLogWriter) capture(data []byte) {
	remaining := maxFailureDetailBytes - w.body.Len()
	if remaining <= 0 {
		return
	}
	if len(data) > remaining {
		data = data[:remaining]
	}
	_, _ = w.body.Write(data)
}

func recordServerFailures(files *service.FileService) gin.HandlerFunc {
	return func(c *gin.Context) {
		writer := &failureLogWriter{ResponseWriter: c.Writer}
		c.Writer = writer
		c.Next()
		if c.Writer.Status() < 500 {
			return
		}
		var message string
		if last := c.Errors.Last(); last != nil && last.Err != nil {
			message = last.Err.Error()
		}
		if message == "" {
			message = strings.TrimSpace(writer.body.String())
		}
		files.FailureLog().Record(service.ServerFailureEvent{
			OccurredAt:    time.Now(),
			Method:        c.Request.Method,
			Path:          c.Request.URL.Path,
			Status:        c.Writer.Status(),
			Detail:        message,
			UploadFailure: strings.HasPrefix(c.Request.URL.Path, "/api/files/upload/"),
		})
	}
}
