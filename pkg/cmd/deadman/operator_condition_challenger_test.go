package deadman

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	clienttesting "k8s.io/client-go/testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	configv1 "github.com/openshift/api/config/v1"
	configfake "github.com/openshift/client-go/config/clientset/versioned/fake"
	configv1listers "github.com/openshift/client-go/config/listers/config/v1"
	"github.com/openshift/library-go/pkg/operator/events"
	"k8s.io/client-go/tools/cache"
)

func newCondition(name string, status configv1.ConditionStatus, reason, message string, lastTransition time.Time) configv1.ClusterOperatorStatusCondition {
	return configv1.ClusterOperatorStatusCondition{
		Type:               configv1.ClusterStatusConditionType(name),
		Status:             status,
		LastTransitionTime: metav1.Time{Time: lastTransition},
		Reason:             reason,
		Message:            message,
	}
}

func TestOperatorConditionChallenger_syncHandler(t *testing.T) {
	nowish := time.Now()
	ninetySecondsAgo := nowish.Add(-90 * time.Second)
	thirtySecondsAgo := nowish.Add(-30 * time.Second)
	sixtySecondsFromNow := nowish.Add(60 * time.Second)

	type fields struct {
		operatorToTimeToCheckForStaleness     map[string]time.Time
		durationAllowedBetweenStalenessChecks time.Duration
	}
	tests := []struct {
		name                    string
		fields                  fields
		existingClusterOperator *configv1.ClusterOperator
		wantedConditions        []configv1.ClusterOperatorStatusCondition
		wantErr                 string
	}{
		{
			name: "empty",
			fields: fields{
				operatorToTimeToCheckForStaleness:     map[string]time.Time{},
				durationAllowedBetweenStalenessChecks: 1 * time.Minute,
			},
			existingClusterOperator: &configv1.ClusterOperator{
				ObjectMeta: metav1.ObjectMeta{Name: "operator"},
				Status: configv1.ClusterOperatorStatus{
					Conditions: []configv1.ClusterOperatorStatusCondition{
						newCondition("Degraded", configv1.ConditionFalse, "OkReason", "cool message", ninetySecondsAgo),
					},
				},
			},
			wantedConditions: []configv1.ClusterOperatorStatusCondition{
				newCondition("Degraded", configv1.ConditionFalse, "OkReason", challengePrefix+"cool message", ninetySecondsAgo),
			},
			wantErr: "",
		},
		{
			name: "skip-check-we-already-did", // this checks to be sure we don't hot-loop on updates
			fields: fields{
				operatorToTimeToCheckForStaleness: map[string]time.Time{
					"operator": sixtySecondsFromNow,
				},
				durationAllowedBetweenStalenessChecks: 1 * time.Minute,
			},
			existingClusterOperator: &configv1.ClusterOperator{
				ObjectMeta: metav1.ObjectMeta{Name: "operator"},
				Status: configv1.ClusterOperatorStatus{
					Conditions: []configv1.ClusterOperatorStatusCondition{
						newCondition("Degraded", configv1.ConditionFalse, "OkReason", "cool message", ninetySecondsAgo),
					},
				},
			},
			wantedConditions: []configv1.ClusterOperatorStatusCondition{},
			wantErr:          "",
		},
		{
			name: "do-check-we're-stale-for", // this checks to be sure we check once the cache expires
			fields: fields{
				operatorToTimeToCheckForStaleness: map[string]time.Time{
					"operator": thirtySecondsAgo,
				},
				durationAllowedBetweenStalenessChecks: 1 * time.Minute,
			},
			existingClusterOperator: &configv1.ClusterOperator{
				ObjectMeta: metav1.ObjectMeta{Name: "operator"},
				Status: configv1.ClusterOperatorStatus{
					Conditions: []configv1.ClusterOperatorStatusCondition{
						newCondition("Degraded", configv1.ConditionFalse, "OkReason", "cool message", ninetySecondsAgo),
					},
				},
			},
			wantedConditions: []configv1.ClusterOperatorStatusCondition{
				newCondition("Degraded", configv1.ConditionFalse, "OkReason", challengePrefix+"cool message", ninetySecondsAgo),
			},
			wantErr: "",
		},
		{
			name: "recent-enough",
			fields: fields{
				operatorToTimeToCheckForStaleness:     map[string]time.Time{},
				durationAllowedBetweenStalenessChecks: 1 * time.Minute,
			},
			existingClusterOperator: &configv1.ClusterOperator{
				ObjectMeta: metav1.ObjectMeta{Name: "operator"},
				Status: configv1.ClusterOperatorStatus{
					Conditions: []configv1.ClusterOperatorStatusCondition{
						newCondition("Degraded", configv1.ConditionFalse, "OkReason", "cool message", thirtySecondsAgo),
					},
				},
			},
			wantedConditions: []configv1.ClusterOperatorStatusCondition{},
			wantErr:          "",
		},
		{
			name: "already-checking",
			fields: fields{
				operatorToTimeToCheckForStaleness:     map[string]time.Time{},
				durationAllowedBetweenStalenessChecks: 1 * time.Minute,
			},
			existingClusterOperator: &configv1.ClusterOperator{
				ObjectMeta: metav1.ObjectMeta{Name: "operator"},
				Status: configv1.ClusterOperatorStatus{
					Conditions: []configv1.ClusterOperatorStatusCondition{
						newCondition("Degraded", configv1.ConditionFalse, "OkReason", challengePrefix+"cool message", ninetySecondsAgo),
					},
				},
			},
			wantedConditions: []configv1.ClusterOperatorStatusCondition{},
			wantErr:          "",
		},
		{
			name: "no-conditions",
			fields: fields{
				operatorToTimeToCheckForStaleness:     map[string]time.Time{},
				durationAllowedBetweenStalenessChecks: 1 * time.Minute,
			},
			existingClusterOperator: &configv1.ClusterOperator{
				ObjectMeta: metav1.ObjectMeta{Name: "operator"},
				Status: configv1.ClusterOperatorStatus{
					Conditions: []configv1.ClusterOperatorStatusCondition{},
				},
			},
			wantedConditions: []configv1.ClusterOperatorStatusCondition{},
			wantErr:          "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			indexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{})
			indexer.Add(tt.existingClusterOperator)
			clusterOperatorLister := configv1listers.NewClusterOperatorLister(indexer)

			fakeClient := configfake.NewSimpleClientset(tt.existingClusterOperator)

			c := &OperatorConditionChallenger{
				operatorToTimeToCheckForStaleness:     tt.fields.operatorToTimeToCheckForStaleness,
				durationAllowedBetweenStalenessChecks: tt.fields.durationAllowedBetweenStalenessChecks,
				clusterOperatorClient:                 fakeClient.ConfigV1().ClusterOperators(),
				clusterOperatorLister:                 clusterOperatorLister,
				eventRecorder:                         events.NewInMemoryRecorder("testing"),
			}

			actualErr := c.syncHandler(context.Background(), tt.existingClusterOperator.Name)
			switch {
			case actualErr == nil && len(tt.wantErr) == 0:
			case actualErr != nil && len(tt.wantErr) == 0:
				t.Fatal(actualErr)
			case actualErr == nil && len(tt.wantErr) != 0:
				t.Fatalf("missing error: %v", tt.wantErr)
			case actualErr != nil && len(tt.wantErr) != 0 && !strings.Contains(actualErr.Error(), tt.wantErr):
				t.Fatalf("desired %v, got %v", tt.wantErr, actualErr)
			}

			switch {
			case len(fakeClient.Actions()) > 1:
				t.Fatalf("too many actions: %v", fakeClient.Actions())
			case len(fakeClient.Actions()) == 0 && len(tt.wantedConditions) == 0:
				return
			case len(fakeClient.Actions()) != 0 && len(tt.wantedConditions) == 0:
				t.Fatal(fakeClient.Actions())
			case len(fakeClient.Actions()) == 0 && len(tt.wantedConditions) != 0:
				t.Fatalf("missing: %v", tt.wantedConditions)

			default:
				// otherwise we need to check the one action
			}

			actualClusterOperator := fakeClient.Actions()[0].(clienttesting.UpdateAction).GetObject().(*configv1.ClusterOperator)
			if len(actualClusterOperator.Status.Conditions) != len(tt.wantedConditions) {
				t.Fatalf("wrong actions: %v", actualClusterOperator.Status.Conditions)
			}
			for i := range tt.wantedConditions {
				expected := tt.wantedConditions[i]
				actual := actualClusterOperator.Status.Conditions[i]
				if !reflect.DeepEqual(expected, actual) {
					t.Errorf("diff: %v", cmp.Diff(expected, actual))
				}
			}

		})
	}
}
