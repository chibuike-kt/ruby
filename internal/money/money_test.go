package money

import (
	"errors"
	"testing"
)

func TestNewFromMajor(t *testing.T) {
	m, err := NewFromMajor(75000, NGN)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m.MinorUnits() != 7500000 {
		t.Fatalf("got %d minor units, want 7500000", m.MinorUnits())
	}
}

func TestNewFromMajor_UnsupportedCurrency(t *testing.T) {
	_, err := NewFromMajor(100, "XYZ")
	if err == nil {
		t.Fatal("expected error for unsupported currency, got nil")
	}
}

func TestSub_PartialPayment(t *testing.T) {
	debt := New(12000000, NGN)   // 120,000
	payment := New(5000000, NGN) // 50,000
	outstanding, err := debt.Sub(payment)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if outstanding.MinorUnits() != 7000000 {
		t.Fatalf("got %d, want 7000000", outstanding.MinorUnits())
	}

	// second partial payment of 20,000
	second := New(2000000, NGN)
	outstanding, err = outstanding.Sub(second)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if outstanding.MinorUnits() != 5000000 {
		t.Fatalf("got %d, want 5000000", outstanding.MinorUnits())
	}
}

func TestSub_Overpayment_ProducesNegative(t *testing.T) {
	// Sub itself does not reject this — the payment service is
	// responsible for catching it before it reaches the ledger.
	outstanding := New(7500000, NGN) // 75,000
	attempted := New(10000000, NGN)  // 100,000
	result, err := outstanding.Sub(attempted)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsNegative() {
		t.Fatal("expected negative result for overpayment, got non-negative")
	}
}

func TestAdd_CurrencyMismatch(t *testing.T) {
	ngn := New(1000, NGN)
	usd := New(1000, USD)
	_, err := ngn.Add(usd)
	if !errors.Is(err, ErrCurrencyMismatch) {
		t.Fatalf("got %v, want ErrCurrencyMismatch", err)
	}
}

func TestGreaterThan_CurrencyMismatchPanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic on cross-currency comparison")
		}
	}()
	ngn := New(1000, NGN)
	usd := New(1000, USD)
	_ = ngn.GreaterThan(usd)
}

func TestString(t *testing.T) {
	m := New(7500000, NGN)
	if got, want := m.String(), "NGN 75000.00"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestIsZero(t *testing.T) {
	var zero Money
	if !zero.IsZero() {
		t.Fatal("expected zero-value Money to be zero")
	}
}

func TestFormatNaira(t *testing.T) {
	cases := []struct {
		minor int64
		want  string
	}{
		{0, "₦0"},
		{100, "₦1"},
		{7_500_000, "₦75,000"},
		{1_234_567_00, "₦1,234,567"},
		{150, "₦1.50"},
		{-7_500_000, "-₦75,000"},
	}
	for _, tc := range cases {
		if got := FormatNaira(tc.minor); got != tc.want {
			t.Errorf("FormatNaira(%d) = %q, want %q", tc.minor, got, tc.want)
		}
	}
}

func TestGroupThousands(t *testing.T) {
	cases := map[string]string{
		"0":         "0",
		"75":        "75",
		"750":       "750",
		"7500":      "7,500",
		"75000":     "75,000",
		"1234567":   "1,234,567",
		"123456789": "123,456,789",
	}
	for in, want := range cases {
		if got := groupThousands(in); got != want {
			t.Errorf("groupThousands(%q) = %q, want %q", in, got, want)
		}
	}
}
