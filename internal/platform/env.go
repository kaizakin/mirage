// Package platform holds the process plumbing every mirage service shares.
package platform

import (
	"fmt"
	"os"
	"strings"
)

// RequireEnv returns the values of the named environment variables, or one error
// listing every variable that is unset or empty.
func RequireEnv(names ...string) (map[string]string, error) {
	values := make(map[string]string, len(names))
	var missing []string
	for _, name := range names {
		v := os.Getenv(name)
		if v == "" {
			missing = append(missing, name)
			continue
		}
		values[name] = v
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("missing required environment variables: %s", strings.Join(missing, ", "))
	}
	return values, nil
}
