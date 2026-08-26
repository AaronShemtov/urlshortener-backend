package shortcode

import "testing"

func TestGenerateLength(t *testing.T) {
    s, err := Generate(8)
    if err != nil {
        t.Fatalf("Generate returned error: %v", err)
    }
    if len(s) != 8 {
        t.Fatalf("expected length 8, got %d", len(s))
    }
}

func TestGenerateBadLength(t *testing.T) {
    if _, err := Generate(0); err == nil {
        t.Fatalf("expected error for length 0")
    }
}
