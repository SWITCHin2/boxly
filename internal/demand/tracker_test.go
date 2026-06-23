package demand

import "testing"

func TestDesiredClampAndFloor(t *testing.T) {
	tr := New(0.7)
	// No demand → desired honors the min floor and the max ceiling.
	if got := tr.Desired("x", 1, 5); got != 1 {
		t.Fatalf("floor: want 1, got %d", got)
	}
	if got := tr.Desired("x", 0, 5); got != 0 {
		t.Fatalf("zero demand: want 0, got %d", got)
	}
}

func TestEWMARisesWithDemandAndDecays(t *testing.T) {
	tr := New(0.7)
	// Sustained demand: 3 creates per interval for several intervals.
	for i := 0; i < 8; i++ {
		tr.Record("git")
		tr.Record("git")
		tr.Record("git")
		tr.Tick()
	}
	if r := tr.Rate("git"); r < 2.5 || r > 3.5 {
		t.Fatalf("steady-state EWMA should approach 3, got %.2f", r)
	}
	if d := tr.Desired("git", 0, 10); d < 3 {
		t.Fatalf("desired should cover demand (>=3), got %d", d)
	}

	// Idle: rate decays toward zero and is eventually forgotten.
	for i := 0; i < 30; i++ {
		tr.Tick()
	}
	if r := tr.Rate("git"); r != 0 {
		t.Fatalf("idle rate should decay to 0, got %.4f", r)
	}
}

func TestMaxCeiling(t *testing.T) {
	tr := New(0.5)
	for i := 0; i < 20; i++ {
		for j := 0; j < 50; j++ {
			tr.Record("hot")
		}
		tr.Tick()
	}
	if d := tr.Desired("hot", 0, 5); d != 5 {
		t.Fatalf("desired should clamp to max 5, got %d", d)
	}
}
