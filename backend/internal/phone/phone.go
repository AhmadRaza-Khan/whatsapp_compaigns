package phone

import (
	"errors"
	"math/big"
	"regexp"
	"strings"

	"github.com/nyaruka/phonenumbers"
)

var (
	sciNotation = regexp.MustCompile(`^\d+(\.\d+)?[eE]\+?\d+$`)
	nonDigit    = regexp.MustCompile(`[^\d+]`)
)

// Normalize converts a raw phone value (from CSV/Excel or a form) to E.164.
// Numbers without a country code are read as defaultRegion (e.g. "QA" => +974).
func Normalize(raw, defaultRegion string) (string, error) {
	s := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(raw), "'"))
	if s == "" {
		return "", errors.New("empty phone number")
	}
	// Excel sometimes exports long numbers as 9.7455123456E+10.
	if sciNotation.MatchString(s) {
		if f, ok := new(big.Float).SetPrec(128).SetString(s); ok {
			if i, acc := f.Int(nil); acc == big.Exact {
				s = i.String()
			}
		}
	}
	s = strings.TrimSuffix(s, ".0")
	cleaned := nonDigit.ReplaceAllString(s, "")
	if strings.Count(cleaned, "+") > 1 || (strings.Contains(cleaned, "+") && !strings.HasPrefix(cleaned, "+")) {
		return "", errors.New("malformed number")
	}
	digits := strings.TrimPrefix(cleaned, "+")
	if len(digits) < 6 || len(digits) > 15 {
		return "", errors.New("wrong number of digits")
	}

	candidates := []string{cleaned}
	if !strings.HasPrefix(cleaned, "+") {
		if strings.HasPrefix(digits, "00") {
			candidates = append(candidates, "+"+digits[2:])
		}
		// "97455123456" written without + or 00.
		candidates = append(candidates, "+"+digits)
	}
	for _, c := range candidates {
		num, err := phonenumbers.Parse(c, strings.ToUpper(defaultRegion))
		if err != nil {
			continue
		}
		if phonenumbers.IsValidNumber(num) {
			return phonenumbers.Format(num, phonenumbers.E164), nil
		}
	}
	return "", errors.New("not a valid phone number")
}
