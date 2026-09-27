package axon

import (
	"fmt"
	"strings"
)

func requireNonBlank(field string, value string) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("%s must be non-empty", field)
	}
	return nil
}
