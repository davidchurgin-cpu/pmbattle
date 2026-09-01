package fixed

import (
	"testing"

	"github.com/davidchurgin-cpu/pmbattle/internal/domain"
)

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

func TestFormatCountUsesTwoDecimalsAndRoundsDown(t *testing.T) {
	cases := map[domain.Money]string{
		0:                         "0.00",
		domain.Dollar:             "1.00",
		12_345:                    "1.23", // 1.2345 contracts rounds down to 1.23
		12_399:                    "1.23",
		12_300:                    "1.23",
		10 * domain.Dollar:        "10.00",
		domain.ContractStep:       "0.01",
		domain.ContractStep - 1:   "0.00",
		123*domain.Dollar + 4_567: "123.45",
	}
	for value, want := range cases {
		if got := FormatCount(value); got != want {
			t.Errorf("FormatCount(%d) = %q, want %q", value, got, want)
		}
	}
	if got := FloorToStep(12_345, domain.ContractStep); got != 12_300 {
		t.Fatalf("FloorToStep = %d, want 12300", got)
	}
}
