package debt_test

import (
	"testing"

	"github.com/chibuike-kt/ruby/internal/debt"
)

func TestStatus_CanTransitionTo(t *testing.T) {
	cases := []struct {
		from, to debt.Status
		want     bool
	}{
		{debt.StatusOutstanding, debt.StatusPartiallyPaid, true},
		{debt.StatusOutstanding, debt.StatusSettled, true},
		{debt.StatusOutstanding, debt.StatusOutstanding, true},
		{debt.StatusPartiallyPaid, debt.StatusSettled, true},
		{debt.StatusPartiallyPaid, debt.StatusPartiallyPaid, true},
		{debt.StatusPartiallyPaid, debt.StatusOutstanding, false},
		{debt.StatusSettled, debt.StatusPartiallyPaid, false},
		{debt.StatusSettled, debt.StatusOutstanding, false},
		{debt.StatusSettled, debt.StatusSettled, true},
	}

	for _, c := range cases {
		got := c.from.CanTransitionTo(c.to)
		if got != c.want {
			t.Errorf("%s -> %s: got %v, want %v", c.from, c.to, got, c.want)
		}
	}
}
