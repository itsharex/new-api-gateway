package employee

import (
	"fmt"
	"regexp"
	"strings"
)

func Normalize(raw string) string {
	return strings.ToUpper(strings.TrimSpace(raw))
}

func Validate(employeeNo string, pattern *regexp.Regexp) error {
	if pattern == nil {
		return fmt.Errorf("employee number pattern is nil")
	}
	if employeeNo == "" {
		return fmt.Errorf("employee number is empty")
	}
	if !pattern.MatchString(employeeNo) {
		return fmt.Errorf("employee number %q does not match required pattern", employeeNo)
	}
	return nil
}
