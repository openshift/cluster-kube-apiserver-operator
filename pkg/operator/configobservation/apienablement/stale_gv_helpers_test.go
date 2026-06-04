package apienablement

import (
	"fmt"
	"testing"

	"github.com/blang/semver/v4"
	"github.com/google/go-cmp/cmp"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/sets"

	configv1 "github.com/openshift/api/config/v1"
)

type violation struct {
	FeatureGate configv1.FeatureGateName
	Kind        string
	Message     string
}

// findStaleGroupVersionEntries checks whether the API versions listed in the map
// are stale relative to what the scheme actually has registered. It supports two
// modes depending on whether Kinds is set on each entry.
func findStaleGroupVersionEntries(
	groupVersionsByFeatureGate map[configv1.FeatureGateName][]groupVersionKindsByOpenshiftVersion,
	scheme *runtime.Scheme,
	kubeVersion semver.Version,
) []violation {
	var violations []violation

	for featureGate, entries := range groupVersionsByFeatureGate {
		// Filter to entries whose KubeVersionRange matches the current kube version.
		var resolved []groupVersionKindsByOpenshiftVersion
		for _, entry := range entries {
			if entry.KubeVersionRange == nil || entry.KubeVersionRange(kubeVersion) {
				resolved = append(resolved, entry)
			}
		}

		for _, entry := range resolved {
			// Check that the GV itself is registered in the scheme.
			if !scheme.IsVersionRegistered(entry.GroupVersion) {
				violations = append(violations, violation{
					FeatureGate: featureGate,
					Message: fmt.Sprintf(
						"API version %s is not registered in the scheme (kube %s) — "+
							"the version may have been removed",
						entry.GroupVersion, kubeVersion,
					),
				})
				continue
			}

			if len(entry.Kinds) == 0 {
				// Kinds not specified — fall back to a simple version priority check.
				// Flag if any higher-priority version is registered for the same group.
				// This is imprecise (the higher version may be for unrelated resources)
				// but safe — it nudges maintainers to specify Kinds for precise checking.
				for _, gv := range scheme.PrioritizedVersionsForGroup(entry.Group) {
					if gv == entry.GroupVersion {
						break
					}
					violations = append(violations, violation{
						FeatureGate: featureGate,
						Message: fmt.Sprintf(
							"serves %s but higher-priority %s exists (kube %s); "+
								"set Kinds on the entry for precise checking, or update the version",
							entry.GroupVersion, gv, kubeVersion,
						),
					})
					break
				}
				continue
			}

			// Kinds specified — run precise per-kind checks.
			entryTypes := scheme.KnownTypes(entry.GroupVersion)
			for _, kind := range entry.Kinds {
				// Check that the kind actually exists in the listed GV.
				if _, exists := entryTypes[kind]; !exists {
					violations = append(violations, violation{
						FeatureGate: featureGate,
						Kind:        kind,
						Message: fmt.Sprintf(
							"kind %q is not registered in %s (kube %s) — "+
								"the kind may have been removed or renamed",
							kind, entry.GroupVersion, kubeVersion,
						),
					})
					continue
				}

				// Check 1: GA graduation. If the kind exists in v1, the API has
				// graduated and is served by default — the pre-release runtime-config
				// entry is stale and should be removed.
				gaGV := schema.GroupVersion{Group: entry.Group, Version: "v1"}
				if knownTypes := scheme.KnownTypes(gaGV); knownTypes != nil {
					if _, exists := knownTypes[kind]; exists {
						violations = append(violations, violation{
							FeatureGate: featureGate,
							Kind:        kind,
							Message: fmt.Sprintf(
								"kind %q exists in stable %s (kube %s) — "+
									"remove pre-release entries",
								kind, gaGV, kubeVersion,
							),
						})
						continue
					}
				}

				// Check 2: highest pre-release version is listed. Collect all versions
				// listed for this kind across resolved entries, then verify that the
				// highest-priority pre-release version containing this kind is among them.
				listedVersions := sets.New[string]()
				for _, other := range resolved {
					if other.Group != entry.Group {
						continue
					}
					for _, k := range other.Kinds {
						if k == kind {
							listedVersions.Insert(other.Version)
						}
					}
				}

				for _, gv := range scheme.PrioritizedVersionsForGroup(entry.Group) {
					// Skip v1 — already handled by check 1 above.
					if gv.Version == "v1" {
						continue
					}
					if knownTypes := scheme.KnownTypes(gv); knownTypes != nil {
						if _, exists := knownTypes[kind]; exists {
							if !listedVersions.Has(gv.Version) {
								violations = append(violations, violation{
									FeatureGate: featureGate,
									Kind:        kind,
									Message: fmt.Sprintf(
										"kind %q exists in %s but only %v are listed (kube %s) — "+
											"update entries",
										kind, gv, sets.List(listedVersions), kubeVersion,
									),
								})
							}
							break
						}
					}
				}
			}
		}
	}

	return violations
}

func TestFindStaleGroupVersionEntries(t *testing.T) {
	scheme := newTestScheme()
	kubeVersion := semver.MustParse("1.35.0")

	for _, tc := range []struct {
		name           string
		entries        map[configv1.FeatureGateName][]groupVersionKindsByOpenshiftVersion
		wantViolations []violation
	}{
		// Kinds unset — simple mode
		{
			name: "no kinds, no higher version — no violation",
			entries: map[configv1.FeatureGateName][]groupVersionKindsByOpenshiftVersion{
				"TestGate": {{GroupVersion: schema.GroupVersion{Group: "othergroup", Version: "v1alpha1"}}},
			},
		},
		{
			name: "no kinds, higher version exists — violation",
			entries: map[configv1.FeatureGateName][]groupVersionKindsByOpenshiftVersion{
				"TestGate": {{GroupVersion: schema.GroupVersion{Group: "testgroup", Version: "v1alpha1"}}},
			},
			wantViolations: []violation{{
				FeatureGate: "TestGate",
				Message:     "serves testgroup/v1alpha1 but higher-priority testgroup/v1 exists (kube 1.35.0); set Kinds on the entry for precise checking, or update the version",
			}},
		},
		{
			name: "no kinds, entry is highest version — no violation",
			entries: map[configv1.FeatureGateName][]groupVersionKindsByOpenshiftVersion{
				"TestGate": {{GroupVersion: schema.GroupVersion{Group: "testgroup", Version: "v1"}}},
			},
		},

		// Kinds set — GA graduation (check 1)
		{
			name: "kind exists in v1 — violation",
			entries: map[configv1.FeatureGateName][]groupVersionKindsByOpenshiftVersion{
				"TestGate": {{GroupVersion: schema.GroupVersion{Group: "testgroup", Version: "v1alpha1"}, Kinds: []string{"KindA"}}},
			},
			wantViolations: []violation{{
				FeatureGate: "TestGate",
				Kind:        "KindA",
				Message:     `kind "KindA" exists in stable testgroup/v1 (kube 1.35.0) — remove pre-release entries`,
			}},
		},
		{
			name: "kind does not exist in v1 — no violation",
			entries: map[configv1.FeatureGateName][]groupVersionKindsByOpenshiftVersion{
				"TestGate": {{GroupVersion: schema.GroupVersion{Group: "testgroup", Version: "v1beta2"}, Kinds: []string{"KindC"}}},
			},
		},
		{
			name: "multiple kinds all in v1 — violations for all",
			entries: map[configv1.FeatureGateName][]groupVersionKindsByOpenshiftVersion{
				"TestGate": {{GroupVersion: schema.GroupVersion{Group: "testgroup", Version: "v1beta1"}, Kinds: []string{"KindA", "KindB"}}},
			},
			wantViolations: []violation{
				{FeatureGate: "TestGate", Kind: "KindA", Message: `kind "KindA" exists in stable testgroup/v1 (kube 1.35.0) — remove pre-release entries`},
				{FeatureGate: "TestGate", Kind: "KindB", Message: `kind "KindB" exists in stable testgroup/v1 (kube 1.35.0) — remove pre-release entries`},
			},
		},
		{
			name: "multiple kinds, only some in v1 — violation only for graduated",
			entries: map[configv1.FeatureGateName][]groupVersionKindsByOpenshiftVersion{
				"TestGate": {{GroupVersion: schema.GroupVersion{Group: "testgroup", Version: "v1beta1"}, Kinds: []string{"KindA", "KindD"}}},
			},
			wantViolations: []violation{{
				FeatureGate: "TestGate",
				Kind:        "KindA",
				Message:     `kind "KindA" exists in stable testgroup/v1 (kube 1.35.0) — remove pre-release entries`,
			}},
		},
		{
			name: "v1 does not exist for group — no GA violation",
			entries: map[configv1.FeatureGateName][]groupVersionKindsByOpenshiftVersion{
				"TestGate": {{GroupVersion: schema.GroupVersion{Group: "othergroup", Version: "v1alpha1"}, Kinds: []string{"KindX"}}},
			},
		},

		// Kinds set — highest pre-release version (check 2)
		{
			name: "highest pre-release is listed — no violation",
			entries: map[configv1.FeatureGateName][]groupVersionKindsByOpenshiftVersion{
				"TestGate": {{GroupVersion: schema.GroupVersion{Group: "testgroup", Version: "v1beta1"}, Kinds: []string{"KindD"}}},
			},
		},
		{
			name: "highest pre-release not listed — violation",
			entries: map[configv1.FeatureGateName][]groupVersionKindsByOpenshiftVersion{
				"TestGate": {{GroupVersion: schema.GroupVersion{Group: "testgroup", Version: "v1alpha1"}, Kinds: []string{"KindD"}}},
			},
			wantViolations: []violation{{
				FeatureGate: "TestGate",
				Kind:        "KindD",
				Message:     `kind "KindD" exists in testgroup/v1beta1 but only [v1alpha1] are listed (kube 1.35.0) — update entries`,
			}},
		},
		{
			name: "each kind flags its own highest version independently",
			entries: map[configv1.FeatureGateName][]groupVersionKindsByOpenshiftVersion{
				"TestGate": {{GroupVersion: schema.GroupVersion{Group: "testgroup", Version: "v1alpha1"}, Kinds: []string{"KindC", "KindD"}}},
			},
			wantViolations: []violation{
				{FeatureGate: "TestGate", Kind: "KindC", Message: `kind "KindC" exists in testgroup/v1beta2 but only [v1alpha1] are listed (kube 1.35.0) — update entries`},
				{FeatureGate: "TestGate", Kind: "KindD", Message: `kind "KindD" exists in testgroup/v1beta1 but only [v1alpha1] are listed (kube 1.35.0) — update entries`},
			},
		},
		{
			name: "multiple entries cover highest for kind — no violation",
			entries: map[configv1.FeatureGateName][]groupVersionKindsByOpenshiftVersion{
				"TestGate": {
					{GroupVersion: schema.GroupVersion{Group: "testgroup", Version: "v1alpha1"}, Kinds: []string{"KindD"}},
					{GroupVersion: schema.GroupVersion{Group: "testgroup", Version: "v1beta1"}, Kinds: []string{"KindD"}},
				},
			},
		},

		// Kube version range filtering
		{
			name: "entry filtered by kube version range — skipped",
			entries: map[configv1.FeatureGateName][]groupVersionKindsByOpenshiftVersion{
				"TestGate": {{
					KubeVersionRange: semver.MustParseRange("< 1.30.0"),
					GroupVersion:     schema.GroupVersion{Group: "testgroup", Version: "v1alpha1"},
					Kinds:            []string{"KindD"},
				}},
			},
		},
		{
			name: "all entries filtered out — no violations",
			entries: map[configv1.FeatureGateName][]groupVersionKindsByOpenshiftVersion{
				"TestGate": {
					{KubeVersionRange: semver.MustParseRange("< 1.30.0"), GroupVersion: schema.GroupVersion{Group: "testgroup", Version: "v1alpha1"}, Kinds: []string{"KindD"}},
					{KubeVersionRange: semver.MustParseRange(">= 1.40.0"), GroupVersion: schema.GroupVersion{Group: "testgroup", Version: "v1beta1"}, Kinds: []string{"KindD"}},
				},
			},
		},
		{
			name: "overlapping ranges both resolve, highest listed — no violation",
			entries: map[configv1.FeatureGateName][]groupVersionKindsByOpenshiftVersion{
				"TestGate": {
					{KubeVersionRange: semver.MustParseRange(">= 1.33.0"), GroupVersion: schema.GroupVersion{Group: "testgroup", Version: "v1alpha1"}, Kinds: []string{"KindD"}},
					{KubeVersionRange: semver.MustParseRange(">= 1.34.0"), GroupVersion: schema.GroupVersion{Group: "testgroup", Version: "v1beta1"}, Kinds: []string{"KindD"}},
				},
			},
		},

		// Unregistered GV or kind
		{
			name: "GV not registered in scheme — violation",
			entries: map[configv1.FeatureGateName][]groupVersionKindsByOpenshiftVersion{
				"TestGate": {{GroupVersion: schema.GroupVersion{Group: "nonexistent", Version: "v1alpha1"}, Kinds: []string{"Foo"}}},
			},
			wantViolations: []violation{{
				FeatureGate: "TestGate",
				Message:     "API version nonexistent/v1alpha1 is not registered in the scheme (kube 1.35.0) — the version may have been removed",
			}},
		},
		{
			name: "kind not in listed GV — violation",
			entries: map[configv1.FeatureGateName][]groupVersionKindsByOpenshiftVersion{
				"TestGate": {{GroupVersion: schema.GroupVersion{Group: "testgroup", Version: "v1beta1"}, Kinds: []string{"NoSuchKind"}}},
			},
			wantViolations: []violation{{
				FeatureGate: "TestGate",
				Kind:        "NoSuchKind",
				Message:     `kind "NoSuchKind" is not registered in testgroup/v1beta1 (kube 1.35.0) — the kind may have been removed or renamed`,
			}},
		},
		{
			name: "same kind in different groups — checked independently",
			entries: map[configv1.FeatureGateName][]groupVersionKindsByOpenshiftVersion{
				"TestGate": {
					{GroupVersion: schema.GroupVersion{Group: "testgroup", Version: "v1alpha1"}, Kinds: []string{"KindA"}},
					{GroupVersion: schema.GroupVersion{Group: "othergroup", Version: "v1alpha1"}, Kinds: []string{"KindA"}},
				},
			},
			wantViolations: []violation{
				{FeatureGate: "TestGate", Kind: "KindA", Message: `kind "KindA" exists in stable testgroup/v1 (kube 1.35.0) — remove pre-release entries`},
				{FeatureGate: "TestGate", Kind: "KindA", Message: `kind "KindA" is not registered in othergroup/v1alpha1 (kube 1.35.0) — the kind may have been removed or renamed`},
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			violations := findStaleGroupVersionEntries(tc.entries, scheme, kubeVersion)
			if diff := cmp.Diff(tc.wantViolations, violations); diff != "" {
				t.Errorf("unexpected violations (-want +got):\n%s", diff)
			}
		})
	}
}

// Test types for synthetic scheme.

type KindA struct{ metav1.TypeMeta }
type KindB struct{ metav1.TypeMeta }
type KindC struct{ metav1.TypeMeta }
type KindD struct{ metav1.TypeMeta }
type KindX struct{ metav1.TypeMeta }

func (o *KindA) DeepCopyObject() runtime.Object { cp := *o; return &cp }
func (o *KindB) DeepCopyObject() runtime.Object { cp := *o; return &cp }
func (o *KindC) DeepCopyObject() runtime.Object { cp := *o; return &cp }
func (o *KindD) DeepCopyObject() runtime.Object { cp := *o; return &cp }
func (o *KindX) DeepCopyObject() runtime.Object { cp := *o; return &cp }

func newTestScheme() *runtime.Scheme {
	scheme := runtime.NewScheme()

	testGroup := "testgroup"
	scheme.AddKnownTypes(schema.GroupVersion{Group: testGroup, Version: "v1"}, &KindA{}, &KindB{})
	scheme.AddKnownTypes(schema.GroupVersion{Group: testGroup, Version: "v1beta2"}, &KindA{}, &KindB{}, &KindC{})
	scheme.AddKnownTypes(schema.GroupVersion{Group: testGroup, Version: "v1beta1"}, &KindA{}, &KindB{}, &KindC{}, &KindD{})
	scheme.AddKnownTypes(schema.GroupVersion{Group: testGroup, Version: "v1alpha1"}, &KindA{}, &KindB{}, &KindC{}, &KindD{})

	if err := scheme.SetVersionPriority(
		schema.GroupVersion{Group: testGroup, Version: "v1"},
		schema.GroupVersion{Group: testGroup, Version: "v1beta2"},
		schema.GroupVersion{Group: testGroup, Version: "v1beta1"},
		schema.GroupVersion{Group: testGroup, Version: "v1alpha1"},
	); err != nil {
		panic(err)
	}

	otherGroup := "othergroup"
	scheme.AddKnownTypes(schema.GroupVersion{Group: otherGroup, Version: "v1alpha1"}, &KindX{})

	if err := scheme.SetVersionPriority(
		schema.GroupVersion{Group: otherGroup, Version: "v1alpha1"},
	); err != nil {
		panic(err)
	}

	return scheme
}
