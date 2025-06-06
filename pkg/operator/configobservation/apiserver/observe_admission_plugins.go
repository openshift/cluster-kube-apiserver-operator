package apiserver

import (
	"fmt"
	"slices"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/configobservation"
	"github.com/openshift/library-go/pkg/operator/configobserver"
	"github.com/openshift/library-go/pkg/operator/events"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/util/sets"
)

type pluginCheckerFunc func(listers configobservation.Listers) (enabled, disabled []string, err error)

var (
	enableAdmissionPluginsPath  = []string{"apiServerArguments", "enable-admission-plugins"}
	disableAdmissionPluginsPath = []string{"apiServerArguments", "disable-admission-plugins"}

	pluginCheckers = []pluginCheckerFunc{
		roleBindingRestrictionPluginChecker,
	}
)

// ObserveAdmissionPlugins manages the apiServerArguments.enable-admission-plugins and
// apiServerArguments.disable-admission-plugins fields of the configuration. It defines a list of
// plugin checkers which check the state of specific plugins, and add them to the enabled or disabled
// list as required. This observer will overwrite any pre-existing values of the two fields in the existingConfig.
func ObserveAdmissionPlugins(genericListers configobserver.Listers, recorder events.Recorder, existingConfig map[string]any) (ret map[string]any, _ []error) {
	defer func() {
		ret = configobserver.Pruned(ret, enableAdmissionPluginsPath, disableAdmissionPluginsPath)
	}()

	if len(pluginCheckers) == 0 {
		return existingConfig, nil
	}

	listers := genericListers.(configobservation.Listers)
	enabledSet := sets.New[string]()
	disabledSet := sets.New[string]()

	for _, pluginChecker := range pluginCheckers {
		enabled, disabled, err := pluginChecker(listers)
		if err != nil {
			return existingConfig, []error{err}
		}

		enabledSet.Insert(enabled...)
		disabledSet.Insert(disabled...)
	}

	if intersection := enabledSet.Intersection(disabledSet); intersection.Len() > 0 {
		return existingConfig, []error{fmt.Errorf("plugins cannot be enabled and disabled at the same time: %v", intersection.UnsortedList())}
	}

	observedConfig := map[string]any{}

	if enabledSet.Len() > 0 {
		sorted := slices.Sorted[string](slices.Values(enabledSet.UnsortedList()))
		err := unstructured.SetNestedStringSlice(observedConfig, sorted, enableAdmissionPluginsPath...)
		if err != nil {
			return existingConfig, []error{err}
		}
	}

	if disabledSet.Len() > 0 {
		sorted := slices.Sorted[string](slices.Values(disabledSet.UnsortedList()))
		err := unstructured.SetNestedStringSlice(observedConfig, sorted, disableAdmissionPluginsPath...)
		if err != nil {
			return existingConfig, []error{err}
		}
	}

	return observedConfig, nil
}

func roleBindingRestrictionPluginChecker(listers configobservation.Listers) (enabled, disabled []string, err error) {
	auth, err := listers.AuthConfigLister.Get("cluster")
	if err != nil {
		return
	}

	rbrPlugins := []string{
		"authorization.openshift.io/RestrictSubjectBindings",
		"authorization.openshift.io/ValidateRoleBindingRestriction",
	}

	switch auth.Spec.Type {
	case configv1.AuthenticationTypeIntegratedOAuth, "":
		enabled = rbrPlugins

	case configv1.AuthenticationTypeNone, configv1.AuthenticationTypeOIDC:
		disabled = rbrPlugins
	}

	return
}
