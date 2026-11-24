package products

import (
	"errors"
	"strings"
	"testing"
)

// TestValidateName pins the trimming and length rules.
func TestValidateName(t *testing.T) {
	valid := []string{"Widget", "  padded  ", strings.Repeat("a", maxNameLength)}
	for _, in := range valid {
		if err := ValidateName(in); err != nil {
			t.Errorf("ValidateName(%q) = %v, want nil", in, err)
		}
	}

	invalid := []string{"", "   ", strings.Repeat("a", maxNameLength+1)}
	for _, in := range invalid {
		if err := ValidateName(in); !errors.Is(err, ErrInvalidInput) {
			t.Errorf("ValidateName(%q) = %v, want ErrInvalidInput", in, err)
		}
	}
}

// TestValidateQty rejects negative quantities but allows zero.
func TestValidateQty(t *testing.T) {
	if err := ValidateQty(0); err != nil {
		t.Errorf("ValidateQty(0) = %v, want nil", err)
	}
	if err := ValidateQty(10); err != nil {
		t.Errorf("ValidateQty(10) = %v, want nil", err)
	}
	if err := ValidateQty(-1); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("ValidateQty(-1) = %v, want ErrInvalidInput", err)
	}
}

// TestValidatePrice rejects negative and out-of-range prices.
func TestValidatePrice(t *testing.T) {
	valid := []float64{0, 0.01, 9_999_999_999.99}
	for _, in := range valid {
		if err := ValidatePrice(in); err != nil {
			t.Errorf("ValidatePrice(%v) = %v, want nil", in, err)
		}
	}

	invalid := []float64{-0.01, maxPrice + 1}
	for _, in := range invalid {
		if err := ValidatePrice(in); !errors.Is(err, ErrInvalidInput) {
			t.Errorf("ValidatePrice(%v) = %v, want ErrInvalidInput", in, err)
		}
	}
}

// TestIsUUID pins the format that is allowed to reach a uuid column.
func TestIsUUID(t *testing.T) {
	valid := []string{
		"11111111-1111-1111-1111-111111111111",
		"ABCDEF01-2345-6789-abcd-ef0123456789",
	}
	for _, in := range valid {
		if !IsUUID(in) {
			t.Errorf("IsUUID(%q) = false, want true", in)
		}
	}

	invalid := []string{
		"",
		"not-a-uuid",
		"111111111111111111111111111111111111",  // no hyphens
		"11111111-1111-1111-1111-11111111111",   // too short
		"11111111-1111-1111-1111-1111111111111", // too long
		"11111111x1111-1111-1111-111111111111",  // wrong separator position
		"gggggggg-1111-1111-1111-111111111111",  // non-hex
	}
	for _, in := range invalid {
		if IsUUID(in) {
			t.Errorf("IsUUID(%q) = true, want false", in)
		}
	}
}
