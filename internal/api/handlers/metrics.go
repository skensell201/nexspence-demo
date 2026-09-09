package handlers

import (
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/nexspence-oss/nexspence/internal/logger"
	"github.com/nexspence-oss/nexspence/internal/metrics"
)

// MetricsMiddleware increments request counters after each request.
func MetricsMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		metrics.RequestsTotal.Add(1)
		c.Next()
		status := c.Writer.Status()
		if status >= 500 {
			metrics.RequestErrors.Add(1)
		}
		statusClass := strconv.Itoa(status/100) + "xx"
		metrics.RecordRequest(c.Request.Method, statusClass, time.Since(start))
	}
}

// MetricsHandler serves GET /api/v1/metrics — JSON snapshot (unchanged, public).
func MetricsHandler(pool *pgxpool.Pool) gin.HandlerFunc {
	return func(c *gin.Context) {
		snap := metrics.Snapshot()

		var artifactCount, bytesStored, downloadsTotal int64
		_ = pool.QueryRow(c.Request.Context(),
			`SELECT COUNT(*), COALESCE(SUM(size_bytes),0), COALESCE(SUM(download_count),0) FROM assets`,
		).Scan(&artifactCount, &bytesStored, &downloadsTotal)

		snap["artifacts_stored"] = artifactCount
		snap["bytes_stored"] = bytesStored
		snap["downloads_total"] = downloadsTotal

		c.JSON(http.StatusOK, snap)
	}
}

// HistoryHandler serves GET /api/v1/metrics/history — returns ring buffer as JSON.
func HistoryHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		points := metrics.History.Snapshot()
		if points == nil {
			points = []metrics.DataPoint{}
		}
		c.JSON(http.StatusOK, points)
	}
}

// RepoMetric is a per-repository metrics row returned by ReposHandler.
type RepoMetric struct {
	Name      string `json:"name"`
	Format    string `json:"format"`
	Type      string `json:"type"`
	Downloads int64  `json:"downloads"`
	SizeBytes int64  `json:"size_bytes"`
}

// ReposHandler serves GET /api/v1/metrics/repos — top 10 repos by downloads.
//
// A query failure is logged and answered with a fixed message, the way
// MetricsHandler above deliberately keeps its own query error off the wire: the
// driver's text names tables and columns, which a metrics reader has no need
// for and no way to act on.
func ReposHandler(pool *pgxpool.Pool, log logger.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		rows, err := pool.Query(c.Request.Context(), `
			SELECT r.name, r.format, r.type,
			       COALESCE(SUM(a.download_count), 0),
			       COALESCE(SUM(a.size_bytes), 0)
			FROM repositories r
			LEFT JOIN assets a ON a.repository_id = r.id
			GROUP BY r.id, r.name, r.format, r.type
			ORDER BY COALESCE(SUM(a.download_count), 0) DESC
			LIMIT 10
		`)
		if err != nil {
			log.Errorw("per-repository metrics query failed", "err", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "could not read repository metrics"})
			return
		}
		defer rows.Close()

		result := make([]RepoMetric, 0)
		for rows.Next() {
			var rm RepoMetric
			if err := rows.Scan(&rm.Name, &rm.Format, &rm.Type, &rm.Downloads, &rm.SizeBytes); err != nil {
				continue
			}
			result = append(result, rm)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			log.Errorw("per-repository metrics query failed", "err", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "could not read repository metrics"})
			return
		}
		c.JSON(http.StatusOK, result)
	}
}
