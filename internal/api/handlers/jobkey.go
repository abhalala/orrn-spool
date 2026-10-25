package handlers

import (
	"database/sql"
	"net/http"
	"regexp"

	"github.com/gin-gonic/gin"

	"github.com/orrn/spool/internal/db"
)

// maxJobKeyLen bounds the stored key. ORRN keys are well under 100 bytes.
const maxJobKeyLen = 200

// jobKeyPattern limits a key to printable characters that survive logs, URLs
// and CSV exports without escaping: no whitespace, no control characters.
var jobKeyPattern = regexp.MustCompile(`^[A-Za-z0-9._:/#@=+~-]+$`)

// jobKeyFromRequest returns the client-provided exactly-once key from the JSON
// body or, when the body has none, the Idempotency-Key header. An empty key is
// valid and means "no exactly-once protection". On a malformed key it writes a
// 400 and returns ok=false.
func jobKeyFromRequest(c *gin.Context, bodyKey string) (key string, ok bool) {
	key = bodyKey
	if key == "" {
		key = c.GetHeader("Idempotency-Key")
	}
	if key == "" {
		return "", true
	}
	if len(key) > maxJobKeyLen || !jobKeyPattern.MatchString(key) {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "invalid_job_key",
			"message": "job_key must be 1-200 characters from A-Z a-z 0-9 . _ : / # @ = + ~ -",
		})
		return "", false
	}
	return key, true
}

// replayDuplicateJob answers a submit whose key is already stored. It returns
// true when it wrote the response (200 with the first job, or 409 when the same
// key arrives with a different payload) and false when the key is new.
func replayDuplicateJob(c *gin.Context, key string, printerID, templateID int64, variablesJSON string, copies int) bool {
	if key == "" {
		return false
	}
	existing, err := db.Jobs.GetJobByKey(c.Request.Context(), key)
	if err == sql.ErrNoRows {
		return false
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to look up job_key"})
		return true
	}
	if existing.PrinterID != printerID || existing.TemplateID != templateID ||
		existing.VariablesJSON != variablesJSON || existing.Copies != copies {
		c.JSON(http.StatusConflict, gin.H{
			"error":   "job_key_conflict",
			"message": "job_key was already used for a different label; use a new key to print a different label",
			"id":      existing.ID,
			"job_key": key,
		})
		return true
	}
	writeDuplicateJob(c, existing, key)
	return true
}

// replayExistingJob answers the race where two identical submits both passed
// the lookup and the unique index let only one insert win.
func replayExistingJob(c *gin.Context, jobID int64, key string) {
	existing, err := db.Jobs.GetJobByID(c.Request.Context(), jobID)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"id": jobID, "job_id": jobID, "duplicate": true, "job_key": key})
		return
	}
	writeDuplicateJob(c, existing, key)
}

func writeDuplicateJob(c *gin.Context, job *db.PrintJob, key string) {
	c.JSON(http.StatusOK, gin.H{
		"id":        job.ID,
		"job_id":    job.ID,
		"status":    job.Status,
		"duplicate": true,
		"job_key":   key,
		"message":   "job_key already submitted; returning the first job, nothing was printed again",
	})
}
