package fixed

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/davidchurgin-cpu/pmbattle/internal/domain"
)

func Parse(value string) (domain.Money, error) {
	value = strings.TrimSpace(strings.TrimPrefix(value, "$"))
	if value == "" {
		return 0, nil
	}
	negative := strings.HasPrefix(value, "-")
	value = strings.TrimPrefix(value, "-")
	parts := strings.SplitN(value, ".", 2)
	whole, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return 0, fmt.Errorf("parse fixed money %q: %w", value, err)
	}
	fraction := ""
	if len(parts) == 2 {
		fraction = parts[1]
	}
	if len(fraction) > 4 {
		fraction = fraction[:4]
	}
	fraction += strings.Repeat("0", 4-len(fraction))
	frac := int64(0)
	if fraction != "" {
		frac, err = strconv.ParseInt(fraction, 10, 64)
		if err != nil {
			return 0, err
		}
	}
	result := domain.Money(whole*10_000 + frac)
	if negative {
		return -result, nil
	}
	return result, nil
}

func Format(value domain.Money) string {
	sign := ""
	if value < 0 {
		sign = "-"
		value = -value
	}
	return fmt.Sprintf("%s%d.%04d", sign, value/domain.Dollar, value%domain.Dollar)
}
