package fixed

import "testing"

func TestParse(t *testing.T) {
	tests := map[string]int64{"0.5600": 5600, "10.00": 100000, "-0.25": -2500, "$1.23456": 12345}
	for input, want := range tests {
		got, err := Parse(input)
		if err != nil {
			t.Fatal(err)
		}
		if int64(got) != want {
			t.Errorf("%s got %d want %d", input, got, want)
		}
	}
}
