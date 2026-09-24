package podsecurityreadinesscontroller

// WarningsHandler collects the warnings and makes them available.
type WarningsHandler struct {
	warnings []string
}

// HandleWarningHeader implements the WarningHandler interface. It stores the
// warning headers.
func (w *WarningsHandler) HandleWarningHeader(code int, agent string, text string) {
	if text == "" {
		return
	}

	w.warnings = append(w.warnings, text)
}

// PopAll returns all warnings and clears the slice.
func (w *WarningsHandler) PopAll() []string {
	warnings := w.warnings
	w.warnings = []string{}

	return warnings
}
