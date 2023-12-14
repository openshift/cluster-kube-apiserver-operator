package podsecurityreadinesscontroller

import (
	"strings"
	"testing"
)

func TestWarningHandler(t *testing.T) {
	w := warningsHandler{}
	warningMessage := "warning"
	w.HandleWarningHeader(0, "", warningMessage)

	actualWarnings := w.PopAll()
	if strings.Compare(actualWarnings[0], warningMessage) != 0 {
		t.Errorf("Expected warning to be %q, got %q", warningMessage, actualWarnings[0])
	}

	if len(w.PopAll()) != 0 {
		t.Error("Expected PopAll to return an empty slice")
	}
}
