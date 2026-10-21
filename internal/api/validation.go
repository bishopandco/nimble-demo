package api

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

func text(value *string, name string, max int) error {
	*value = strings.TrimSpace(*value)
	length := utf8.RuneCountInString(*value)
	if length == 0 || length > max {
		return fmt.Errorf("%s must contain between 1 and %d characters", name, max)
	}
	return nil
}
