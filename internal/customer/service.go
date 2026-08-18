package customer

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/chibuike-kt/ruby/internal/db"
)

var ErrInvalidName = errors.New("customer: name is required")
var ErrNoIdentitySignal = errors.New("customer: no identity signal provided")

type Service struct {
	q db.Querier
}

func NewService(q db.Querier) *Service {
	return &Service{q: q}
}

func (s *Service) Create(ctx context.Context, userID int64, name string, phone, alias *string) (Customer, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return Customer{}, ErrInvalidName
	}
	return Create(ctx, s.q, Customer{UserID: userID, Name: name, PhoneNumber: phone, Alias: alias})
}

// Ref identifies a customer via one of the signals from spec §8, in
// descending order of reliability. Set only the strongest signal
// available — Resolve stops at the first non-nil one. Explicit selection
// from an ambiguity prompt (signal 3) and recent conversation context
// (signal 4) are resolved above this package, by whatever holds that
// conversation state (the AI/WhatsApp layer, not built in Slice 1); once
// resolved they arrive here as CustomerID.
type Ref struct {
	CustomerID *int64
	Phone      *string
	Alias      *string
	Name       *string
}

// AmbiguousError means a Ref resolved to more than one customer with no
// stronger signal available to break the tie (spec §7/§11).
type AmbiguousError struct {
	Candidates []Customer
}

func (e *AmbiguousError) Error() string {
	return fmt.Sprintf("customer: %d candidates match, disambiguation required", len(e.Candidates))
}

func (s *Service) Resolve(ctx context.Context, userID int64, ref Ref) (Customer, error) {
	if ref.CustomerID != nil {
		return GetByID(ctx, s.q, userID, *ref.CustomerID)
	}
	if phone := trimmed(ref.Phone); phone != "" {
		return s.resolveOne(ctx, userID, phone, FindByPhone)
	}
	if alias := trimmed(ref.Alias); alias != "" {
		return s.resolveOne(ctx, userID, alias, FindByAlias)
	}
	if name := trimmed(ref.Name); name != "" {
		return s.resolveOne(ctx, userID, name, FindByName)
	}
	return Customer{}, ErrNoIdentitySignal
}

func (s *Service) resolveOne(ctx context.Context, userID int64, value string, find func(context.Context, db.Querier, int64, string) ([]Customer, error)) (Customer, error) {
	matches, err := find(ctx, s.q, userID, value)
	if err != nil {
		return Customer{}, err
	}
	switch len(matches) {
	case 0:
		return Customer{}, ErrNotFound
	case 1:
		return matches[0], nil
	default:
		return Customer{}, &AmbiguousError{Candidates: matches}
	}
}

func trimmed(s *string) string {
	if s == nil {
		return ""
	}
	return strings.TrimSpace(*s)
}
