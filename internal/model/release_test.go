package model

import "testing"

func TestReleaseVersionOrderingAndPathSafety(t *testing.T) {
	for _, v := range []string{"../1.0.0", "01.0.0", "1.2", "1.0.0/evil", "1.2.3;id", "1.2.3-beta"} {
		if ValidVersion(v) {
			t.Fatal(v)
		}
	}
	for _, pair := range [][2]string{{"0.2.0", "0.10.0"}, {"1.9.9", "2.0.0"}, {"0.2.9", "0.2.10"}} {
		if cmp, err := CompareVersions(pair[0], pair[1]); err != nil || cmp != -1 {
			t.Fatalf("%v %v %v", pair, cmp, err)
		}
	}
	if _, err := CompareVersions("unknown", "0.2.0"); err == nil {
		t.Fatal("unknown version compared")
	}
}
