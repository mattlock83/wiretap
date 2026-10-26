package bundle

import (
	"errors"
	"fmt"
	"strings"
)

// Application identifies the tested software independently of API ownership.
// A UI may have an application identity and no provided APIs.
type Application struct {
	Name        string `json:"name,omitempty"`
	ID          string `json:"id,omitempty"`
	Description string `json:"description,omitempty"`
}

func validateApplication(app *Application) error {
	if app != nil && strings.TrimSpace(app.Name) == "" && strings.TrimSpace(app.ID) == "" {
		return errors.New("application requires a name or ID")
	}
	return nil
}

func checkCapabilities(required []string) error {
	if required == nil {
		return errors.New("missing required_capabilities declaration")
	}
	if len(required) > 0 {
		return fmt.Errorf("unsupported required capability %q; only HTTP/OpenAPI evidence is supported", required[0])
	}
	return nil
}
