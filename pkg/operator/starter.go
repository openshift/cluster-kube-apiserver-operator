package operator

import (
	"fmt"

	"k8s.io/client-go/rest"
)

func RunOperator(clientConfig *rest.Config, stopCh <-chan struct{}) error {
	return fmt.Errorf("stopped")
}
