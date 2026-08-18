// Package customer manages trader-owned customer records and identity
// resolution (spec §6-8). A customer name is never a unique identifier —
// two customers can share a name under the same trader, so every lookup
// is scoped by user_id and callers must be prepared for ambiguous matches.
package customer

import "time"

type Customer struct {
	ID          int64
	UserID      int64
	Name        string
	PhoneNumber *string
	Alias       *string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}
