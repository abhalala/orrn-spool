package db

import (
	"errors"
	"strings"
)

// JobOrderQueue is the queue page's order: highest priority first, then
// oldest first within a priority.
const JobOrderQueue = "queue"

// ErrInvalidJobOrder means a sort name or direction is not on the allow-list.
var ErrInvalidJobOrder = errors.New("invalid job sort")

// jobOrderSQL maps each accepted sort name to a fixed SQL fragment. Request
// text is only ever used as a map key, never written into the query.
var jobOrderSQL = map[string]string{
	"created_at":   "created_at",
	"id":           "id",
	"priority":     "priority",
	"status":       "status",
	"printer_id":   "printer_id",
	"template_id":  "template_id",
	"started_at":   "started_at",
	"completed_at": "completed_at",
	JobOrderQueue:  "priority DESC, created_at",
}

// JobOrderClause returns the ORDER BY body for a sort name and direction.
// An empty name means created_at and an empty direction means DESC. Names
// match exactly; the direction is ASC or DESC in any letter case. Anything
// else returns ErrInvalidJobOrder.
func JobOrderClause(by, dir string) (string, error) {
	if by == "" {
		by = "created_at"
	}
	col, ok := jobOrderSQL[by]
	if !ok {
		return "", ErrInvalidJobOrder
	}
	switch strings.ToUpper(dir) {
	case "", "DESC":
		return col + " DESC", nil
	case "ASC":
		return col + " ASC", nil
	default:
		return "", ErrInvalidJobOrder
	}
}
