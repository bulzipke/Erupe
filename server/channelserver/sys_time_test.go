package channelserver

import (
	"erupe-ce/common/gametime"
	"testing"
	"time"
)

func TestTimeGameAbsolute(t *testing.T) {
	result := TimeGameAbsolute()

	// TimeGameAbsolute returns (adjustedUnix - 2160) % 5760
	// Result should be in range [0, 5760)
	if result >= 5760 {
		t.Errorf("TimeGameAbsolute() = %d, should be < 5760", result)
	}
}

func TestTimeClientAdjustedOverride(t *testing.T) {
	hour := 3
	result := TimeClientAdjusted(&hour)
	if got := gametime.GameAbsoluteAt(result); got != 720 {
		t.Errorf("TimeClientAdjusted(3) position = %d, want 720", got)
	}
}

func TestTimeClientAdjustedWithoutOverride(t *testing.T) {
	before := TimeAdjusted()
	result := TimeClientAdjusted(nil)
	after := TimeAdjusted()
	if result.Before(before.Add(-time.Second)) || result.After(after.Add(time.Second)) {
		t.Errorf("TimeClientAdjusted(nil) = %v, want current server time between %v and %v", result, before, after)
	}
}

func TestTimeGameAbsoluteAdjustedOverride(t *testing.T) {
	hour := 3
	if got := TimeGameAbsoluteAdjusted(&hour); got != 720 {
		t.Errorf("TimeGameAbsoluteAdjusted(3) = %d, want 720", got)
	}
}
