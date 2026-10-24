package handlers

import (
	"io"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/orrn/spool/internal/scale"
)

// ScaleReader is what the scale handler needs from scale.Service.
type ScaleReader interface {
	Configured() bool
	Reading() scale.Reading
	Status() scale.Status
}

// ScaleHandler serves the weighing-scale reading to ORRN.
type ScaleHandler struct {
	scale        ScaleReader
	streamPeriod time.Duration
}

// NewScaleHandler wraps a reader; svc may be a nil *scale.Service (no scale).
func NewScaleHandler(svc ScaleReader) *ScaleHandler {
	return &ScaleHandler{scale: svc, streamPeriod: 250 * time.Millisecond}
}

// RegisterRoutes mounts GET /scale/reading, /scale/status and /scale/stream on
// an authenticated group. None of them talks to the indicator.
func (h *ScaleHandler) RegisterRoutes(r gin.IRoutes) {
	r.GET("/scale/reading", h.Reading)
	r.GET("/scale/status", h.Status)
	r.GET("/scale/stream", h.Stream)
}

func (h *ScaleHandler) reading() scale.Reading {
	if h.scale == nil || !h.scale.Configured() {
		return scale.Reading{Stable: false, Reason: scale.ReasonNotConfigured}
	}
	return h.scale.Reading()
}

// Reading returns {grams, stable, ageMs, frames} or {stable:false}. It is
// always 200 with no-store caching; "configured": false means no scale.
func (h *ScaleHandler) Reading(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, h.reading())
}

// Status reports presence: configured, connected, lastFrameAt, counters.
func (h *ScaleHandler) Status(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	if h.scale == nil {
		c.JSON(http.StatusOK, scale.Status{Configured: false})
		return
	}
	c.JSON(http.StatusOK, h.scale.Status())
}

// Stream pushes a reading as a server-sent event every 250 ms until the
// client goes away. Same JSON as Reading, event name "reading".
func (h *ScaleHandler) Stream(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	c.Header("X-Accel-Buffering", "no")
	ticker := time.NewTicker(h.streamPeriod)
	defer ticker.Stop()
	c.SSEvent("reading", h.reading())
	c.Writer.Flush()
	c.Stream(func(w io.Writer) bool {
		select {
		case <-c.Request.Context().Done():
			return false
		case <-ticker.C:
			c.SSEvent("reading", h.reading())
			return true
		}
	})
}
