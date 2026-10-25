package handlers

import (
	"bytes"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/orrn/spool/internal/db"
)

// bufferedWriter holds a handler's reply back so HTMXFragments can swap it for
// HTML. Headers still go straight to the real writer.
type bufferedWriter struct {
	gin.ResponseWriter
	status int
	body   bytes.Buffer
}

func (w *bufferedWriter) WriteHeader(code int) { w.status = code }
func (w *bufferedWriter) WriteHeaderNow()      {}
func (w *bufferedWriter) Status() int          { return w.status }
func (w *bufferedWriter) Size() int            { return w.body.Len() }
func (w *bufferedWriter) Written() bool        { return w.body.Len() > 0 || w.status != http.StatusOK }
func (w *bufferedWriter) Write(b []byte) (int, error) {
	return w.body.Write(b)
}
func (w *bufferedWriter) WriteString(s string) (int, error) {
	return w.body.WriteString(s)
}

// HTMXFragments makes the page buttons that POST/PUT/DELETE to the JSON API
// work in place: when HTMX made the call and names an element to refresh, a
// successful reply is replaced by that element's fresh HTML. Plain API callers
// (no HX-Request header) and errors keep the untouched JSON reply.
func (h *WebUIHandler) HTMXFragments() gin.HandlerFunc {
	return func(c *gin.Context) {
		target := c.GetHeader("HX-Target")
		if c.Request.Method == http.MethodGet || c.GetHeader("HX-Request") != "true" || target == "" {
			c.Next()
			return
		}
		original := c.Writer
		buf := &bufferedWriter{ResponseWriter: original, status: http.StatusOK}
		c.Writer = buf
		c.Next()
		c.Writer = original

		if buf.status < 300 && h.renderFragment(c, target) {
			return
		}
		original.WriteHeader(buf.status)
		_, _ = original.Write(buf.body.Bytes())
	}
}

// renderFragment writes the HTML for the element id HTMX targets. It reports
// false when the id is not one the pages refresh.
func (h *WebUIHandler) renderFragment(c *gin.Context, target string) bool {
	switch {
	case target == "printer-grid":
		c.HTML(http.StatusOK, "printers-grid", PrintersPage{Printers: h.printerViews(c)})
	case target == "queue-content":
		c.HTML(http.StatusOK, "queue-sections", h.queueData(c))
	case target == "history-table":
		c.HTML(http.StatusOK, "history-table", h.historyData(c))
	case strings.HasPrefix(target, "printer-card-"):
		id, err := strconv.ParseInt(strings.TrimPrefix(target, "printer-card-"), 10, 64)
		if err != nil {
			return false
		}
		p, err := db.Printers.GetPrinterByID(c.Request.Context(), id)
		if err != nil {
			// The printer is gone (deleted): an empty swap removes the card.
			c.Status(http.StatusOK)
			return true
		}
		c.HTML(http.StatusOK, "printer-card", h.printerStatus(p))
	default:
		return false
	}
	return true
}
