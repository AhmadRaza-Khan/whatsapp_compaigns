package phone

import "testing"

func TestNormalize(t *testing.T) {
	ok := map[string]string{
		"55123456":         "+97455123456",
		"5512 3456":        "+97455123456",
		"+974 5512 3456":   "+97455123456",
		"0097455123456":    "+97455123456",
		"97455123456":      "+97455123456",
		"9.7455123456E+10": "+97455123456",
		"'+971501234567":   "+971501234567",
		"+44 7911 123456":  "+447911123456",
	}
	for in, want := range ok {
		got, err := Normalize(in, "QA")
		if err != nil || got != want {
			t.Errorf("Normalize(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, in := range []string{"", "abc", "123", "+974 12", "++97455123456", "5512345678901234"} {
		if got, err := Normalize(in, "QA"); err == nil {
			t.Errorf("Normalize(%q) = %q; want error", in, got)
		}
	}
}
