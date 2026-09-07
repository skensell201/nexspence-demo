package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

func TestRateLimitMiddleware_BlocksOverBurst(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(RateLimitMiddleware(nil, 0.0001, 2))
	r.GET("/x", func(c *gin.Context) { c.Status(http.StatusOK) })
	codes := []int{}
	for i := 0; i < 3; i++ {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/x", nil)
		req.RemoteAddr = "10.0.0.1:1234"
		r.ServeHTTP(rec, req)
		codes = append(codes, rec.Code)
	}
	assert.Equal(t, http.StatusOK, codes[0])
	assert.Equal(t, http.StatusOK, codes[1])
	assert.Equal(t, http.StatusTooManyRequests, codes[2])
}

func TestRateLimitMiddleware_RetryAfterIsNumeric(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(RateLimitMiddleware(nil, 0.0001, 1))
	r.GET("/x", func(c *gin.Context) { c.Status(http.StatusOK) })
	var throttled *httptest.ResponseRecorder
	for i := 0; i < 2; i++ {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/x", nil)
		req.RemoteAddr = "10.0.0.2:1234"
		r.ServeHTTP(rec, req)
		if rec.Code == http.StatusTooManyRequests {
			throttled = rec
		}
	}
	if assert.NotNil(t, throttled) {
		assert.Regexp(t, `^\d+$`, throttled.Header().Get("Retry-After"))
	}
}
