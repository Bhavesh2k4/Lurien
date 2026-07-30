package provider_test

import (
	"testing"

	"lurien/internal/provider"
	_ "lurien/internal/provider/amazon"
	_ "lurien/internal/provider/ashby"
	_ "lurien/internal/provider/eightfold"
	_ "lurien/internal/provider/greenhouse"
	_ "lurien/internal/provider/lever"
	_ "lurien/internal/provider/uber"
	_ "lurien/internal/provider/workday"
)

// TestRegistered asserts every provider self-registers under its Kind. Adding a
// provider means adding a package + a blank import here — nothing else.
func TestRegistered(t *testing.T) {
	for _, kind := range []string{"greenhouse", "ashby", "lever", "workday", "eightfold", "amazon", "uber"} {
		p, err := provider.Get(kind)
		if err != nil {
			t.Fatalf("%s not registered: %v", kind, err)
		}
		if p.Kind() != kind {
			t.Fatalf("kind mismatch: got %q want %q", p.Kind(), kind)
		}
	}
}
