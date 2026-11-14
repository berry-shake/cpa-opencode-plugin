package opencode

import "testing"

// CPA's panel labels a bar with its description when there is one, so the
// buckets carry only the window name: 5h, 7d and monthly.
func TestQuotaFromAccountNamesWindows(t *testing.T) {
	out := QuotaFromAccount(AccountInfo{Go: &GoUsage{
		Rolling: &GoWindow{Percent: 0, ResetsAt: "2026-11-14T06:28:13Z"},
		Weekly:  &GoWindow{Percent: 40, Limited: true, ResetsAt: "2026-11-16T00:00:00Z"},
		Monthly: &GoWindow{Percent: 10, ResetsAt: "2026-11-17T09:36:59Z"},
	}})
	if len(out.Groups) != 1 || out.Groups[0].DisplayName != "OpenCode Go" || len(out.Groups[0].Buckets) != 3 {
		t.Fatalf("groups = %+v", out.Groups)
	}
	want := []struct {
		window string
		left   float64
	}{{"5h", 1}, {"7d", 0}, {"monthly", 0.9}}
	for i, b := range out.Groups[0].Buckets {
		if b.Window != want[i].window || b.RemainingFraction != want[i].left || b.Description != "" || b.ResetTime == "" {
			t.Errorf("bucket %d = %+v, want window %s left %v and no description", i, b, want[i].window, want[i].left)
		}
	}
}
