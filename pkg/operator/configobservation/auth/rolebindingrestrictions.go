package auth

import (
	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/configobservation"
	"github.com/openshift/library-go/pkg/operator/configobserver"
	"github.com/openshift/library-go/pkg/operator/events"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/util/sets"
)

var (
	disableAdmissionPluginsPath = []string{"apiServerArguments", "disable-admission-plugins"}

	rbrPlugins = []string{
		"authorization.openshift.io/RestrictSubjectBindings",
		"authorization.openshift.io/ValidateRoleBindingRestriction",
	}
)

// ObserveRoleBindingRestrictionPlugins observes the cluster authentication type and explicitly disables
// the plugins related to the RoleBindingRestriction API, when authentication type is anything other than
// the built-in OAuth stack (i.e. .Spec.Type of `authentications.config.openshift.io/cluster` is neither
// "IntegratedOAuth" nor the empty string).
//
// The observer relies on the plugins to be enabled in the default kube-apiserver config, and therefore
// will not explicitly enable them, but only disable them when necessary.
func ObserveRoleBindingRestrictionPlugins(genericListers configobserver.Listers, recorder events.Recorder, existingConfig map[string]interface{}) (map[string]interface{}, []error) {
	observedConfig := map[string]interface{}{}
	listers := genericListers.(configobservation.Listers)

	auth, err := listers.AuthConfigLister.Get("cluster")
	if errors.IsNotFound(err) {
		recorder.Eventf("ObserveRoleBindingRestrictions", "authentications.config.openshift.io/cluster: not found")
		return observedConfig, nil
	} else if err != nil {
		return existingConfig, []error{err}
	}

	if auth.Spec.Type == configv1.AuthenticationTypeIntegratedOAuth || len(auth.Spec.Type) == 0 {
		// the plugins will be enabled by default
		return existingConfig, nil
	}

	// the merger used to merge the observed configs will not merge slices, so we have to do it manually
	// if there are existing elements in the --disable-admission-plugins slice
	disabled, _, err := unstructured.NestedStringSlice(existingConfig, disableAdmissionPluginsPath...)
	if err != nil {
		return existingConfig, []error{err}
	}
	disabledSet := sets.NewString(disabled...)
	disabledSet.Insert(rbrPlugins...)

	err = unstructured.SetNestedStringSlice(observedConfig, disabledSet.List(), disableAdmissionPluginsPath...)
	if err != nil {
		return existingConfig, []error{err}
	}

	return observedConfig, nil
}
