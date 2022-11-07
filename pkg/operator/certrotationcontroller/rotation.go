package certrotationcontroller

import (
	"fmt"
	"strconv"
)

// CertRotationAccelerator, if set to a positive integer value will
// accelerate cert rotation.
// NOTE: The default value should ALWAYS be empty! We DO NOT want to
// accelerate cert rotation in a real-world cluster.
// A positive value (ie "20") will accelerate the cert expiry by
// a factor specified by the specified value.
// This is intended for CI builds where we want an accelerated cert
// expiration for testing purposes, and it should be set by during
// build time using the -ldflags option:
var certRotationAccelerator string

func parseCertRotationAccelerator() (int, error) {
	if len(certRotationAccelerator) == 0 {
		return 0, nil
	}

	factor, err := strconv.Atoi(certRotationAccelerator)
	if err != nil {
		err = fmt.Errorf("failed to parse cert rotation accelerator: %q, err: %v", certRotationAccelerator, err)
		return 0, err
	}
	return factor, nil
}
