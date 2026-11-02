package status

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestSample(t *testing.T) {
	if got := Sample(nil); len(got) != len(promoLines) {
		t.Errorf("empty server: %d entries, want only the promo lines", len(got))
	}
	names := make([]string, 20)
	for i := range names {
		names[i] = fmt.Sprintf("P%d", i)
	}
	got := Sample(names)
	if len(got) != sampleSlots || got[0]["name"] != "P0" || got[sampleSlots-1]["name"] != promoLines[len(promoLines)-1] {
		t.Errorf("full server: %d entries %v", len(got), got)
	}
}

func TestCenterLine(t *testing.T) {
	if got := CenterLine("ab"); !strings.HasSuffix(got, "ab") || len(got) != 21+2 {
		t.Errorf("CenterLine(ab) = %q", got)
	}
	long := strings.Repeat("x", 60)
	if CenterLine(long) != long {
		t.Error("wide text is left alone")
	}
}

func TestResponse(t *testing.T) {
	var v struct {
		Version struct {
			Protocol int
		}
		Players struct {
			Max, Online int
		}
		Favicon string
		Secure  bool `json:"enforcesSecureChat"`
	}
	if err := json.Unmarshal(Response(Info{Online: 3, Max: 100, Names: []string{"a"}, Favicon: "data:x"}), &v); err != nil {
		t.Fatal(err)
	}
	if v.Version.Protocol != 763 || v.Players.Online != 3 || v.Players.Max != 100 || v.Favicon != "data:x" || v.Secure {
		t.Errorf("response %+v", v)
	}
	if strings.Contains(string(Response(Info{})), "favicon") {
		t.Error("no favicon key without an icon")
	}
}
