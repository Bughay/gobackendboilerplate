package orders

import (
	"errors"
	"testing"
)

// validOrderProductID is a canonical UUID used by validation and service tests.
const validOrderProductID = "22222222-2222-2222-2222-222222222222"

// TestCleanCreateTrimsAndPreservesOrder proves product IDs are cleaned without
// reordering the client's lines.
func TestCleanCreateTrimsAndPreservesOrder(t *testing.T) {
	first := "  11111111-1111-1111-1111-111111111111  "
	second := "33333333-3333-3333-3333-333333333333"

	got, err := cleanCreate(CreateInput{Items: []CreateItemInput{
		{ProductID: first, Qty: 2},
		{ProductID: second, Qty: 5},
	}})
	if err != nil {
		t.Fatalf("cleanCreate() error = %v, want success", err)
	}
	if len(got.Items) != 2 {
		t.Fatalf("items = %d, want 2", len(got.Items))
	}
	if got.Items[0].ProductID != "11111111-1111-1111-1111-111111111111" {
		t.Errorf("first product ID = %q, want trimmed ID", got.Items[0].ProductID)
	}
	if got.Items[0].Qty != 2 || got.Items[1].Qty != 5 {
		t.Errorf("quantities = %d/%d, want 2/5", got.Items[0].Qty, got.Items[1].Qty)
	}
}

// TestCleanCreateRejectsInvalid requests proves every domain rule is enforced
// before storage is touched.
func TestCleanCreateRejectsInvalid(t *testing.T) {
	tooMany := make([]CreateItemInput, maxItems+1)
	for i := range tooMany {
		tooMany[i] = CreateItemInput{ProductID: validOrderProductID, Qty: 1}
	}

	tests := []struct {
		name string
		in   CreateInput
	}{
		{"empty", CreateInput{}},
		{"too many items", CreateInput{Items: tooMany}},
		{"malformed product id", CreateInput{Items: []CreateItemInput{{ProductID: "not-a-uuid", Qty: 1}}}},
		{"zero qty", CreateInput{Items: []CreateItemInput{{ProductID: validOrderProductID, Qty: 0}}}},
		{"negative qty", CreateInput{Items: []CreateItemInput{{ProductID: validOrderProductID, Qty: -1}}}},
		{"qty over max", CreateInput{Items: []CreateItemInput{{ProductID: validOrderProductID, Qty: maxItemQty + 1}}}},
		{"duplicate product", CreateInput{Items: []CreateItemInput{
			{ProductID: validOrderProductID, Qty: 1},
			{ProductID: validOrderProductID, Qty: 2},
		}}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := cleanCreate(tc.in); !errors.Is(err, ErrInvalidInput) {
				t.Fatalf("cleanCreate() error = %v, want ErrInvalidInput", err)
			}
		})
	}
}

// TestIsUUID pins the canonical hyphenated UUID shape used by order path IDs and
// product IDs in the request body.
func TestIsUUID(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want bool
	}{
		{"lowercase", "11111111-1111-1111-1111-111111111111", true},
		{"uppercase", "AAAAAAAA-BBBB-CCCC-DDDD-EEEEEEEEEEEE", true},
		{"too short", "11111111-1111-1111-1111-11111111111", false},
		{"no hyphens", "111111111111111111111111111111111111", false},
		{"non hex", "11111111-1111-1111-1111-11111111111g", false},
		{"empty", "", false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := isUUID(tc.in); got != tc.want {
				t.Fatalf("isUUID(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}
