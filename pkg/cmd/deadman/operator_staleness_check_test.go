package deadman

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	configv1 "github.com/openshift/api/config/v1"
	configfake "github.com/openshift/client-go/config/clientset/versioned/fake"
	configv1listers "github.com/openshift/client-go/config/listers/config/v1"
	"github.com/openshift/library-go/pkg/operator/events"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clienttesting "k8s.io/client-go/testing"
	"k8s.io/client-go/tools/cache"
)

func TestOperatorStaleness_syncHandler(t *testing.T) {
	nowish := time.Now()
	ninetySecondsAgo := nowish.Add(-90 * time.Second)
	sixtySecondsAgo := nowish.Add(-60 * time.Second)
	sixtySecondsFromNow := nowish.Add(60 * time.Second)

	type fields struct {
		operatorToDeadlineForResponse      map[string]time.Time
		durationAllowedForOperatorResponse time.Duration
	}
	tests := []struct {
		name                    string
		fields                  fields
		existingClusterOperator *configv1.ClusterOperator
		wantedConditions        []configv1.ClusterOperatorStatusCondition
		wantErr                 string
		wantedNumEvents         int
	}{
		{
			name: "stale",
			fields: fields{
				operatorToDeadlineForResponse: map[string]time.Time{
					"operator": sixtySecondsAgo,
				},
				durationAllowedForOperatorResponse: 1 * time.Minute,
			},
			existingClusterOperator: &configv1.ClusterOperator{
				ObjectMeta: metav1.ObjectMeta{Name: "operator"},
				Status: configv1.ClusterOperatorStatus{
					Conditions: []configv1.ClusterOperatorStatusCondition{
						newCondition("Progressing", configv1.ConditionFalse, "OkReason", challengePrefix+"cool message", ninetySecondsAgo),
					},
				},
			},
			wantedConditions: []configv1.ClusterOperatorStatusCondition{
				newCondition("Progressing", configv1.ConditionUnknown, "OperatorFailedStalenessCheck", stalePrefix+"Last reason was \"OkReason\", last status was: cool message", nowish),
			},
			wantedNumEvents: 1,
			wantErr:         "",
		},
		{
			name: "stale-but-unseen", // in this case don't check because we aren't sure how old the challenge is.
			fields: fields{
				operatorToDeadlineForResponse:      map[string]time.Time{},
				durationAllowedForOperatorResponse: 1 * time.Minute,
			},
			existingClusterOperator: &configv1.ClusterOperator{
				ObjectMeta: metav1.ObjectMeta{Name: "operator"},
				Status: configv1.ClusterOperatorStatus{
					Conditions: []configv1.ClusterOperatorStatusCondition{
						newCondition("Degraded", configv1.ConditionFalse, "OkReason", challengePrefix+"cool message", ninetySecondsAgo),
						newCondition("Progressing", configv1.ConditionFalse, "OkReason", challengePrefix+"cool message", ninetySecondsAgo),
					},
				},
			},
			wantedConditions: []configv1.ClusterOperatorStatusCondition{},
			wantedNumEvents:  0,
			wantErr:          "",
		},
		{
			name: "degraded-goes-true",
			fields: fields{
				operatorToDeadlineForResponse: map[string]time.Time{
					"operator": sixtySecondsAgo,
				},
				durationAllowedForOperatorResponse: 1 * time.Minute,
			},
			existingClusterOperator: &configv1.ClusterOperator{
				ObjectMeta: metav1.ObjectMeta{Name: "operator"},
				Status: configv1.ClusterOperatorStatus{
					Conditions: []configv1.ClusterOperatorStatusCondition{
						newCondition("Degraded", configv1.ConditionFalse, "OkReason", challengePrefix+"cool message", ninetySecondsAgo),
					},
				},
			},
			wantedConditions: []configv1.ClusterOperatorStatusCondition{
				newCondition("Degraded", configv1.ConditionTrue, "OperatorFailedStalenessCheck", stalePrefix+"Last reason was \"OkReason\", last status was: cool message", nowish),
			},
			wantedNumEvents: 1,
			wantErr:         "",
		},
		{
			name: "upgradeable-false-doesn't-change-status", // if we changed this, clusters would be upgradeable and that would be bad.
			fields: fields{
				operatorToDeadlineForResponse: map[string]time.Time{
					"operator": sixtySecondsAgo,
				},
				durationAllowedForOperatorResponse: 1 * time.Minute,
			},
			existingClusterOperator: &configv1.ClusterOperator{
				ObjectMeta: metav1.ObjectMeta{Name: "operator"},
				Status: configv1.ClusterOperatorStatus{
					Conditions: []configv1.ClusterOperatorStatusCondition{
						newCondition("Upgradeable", configv1.ConditionFalse, "OkReason", challengePrefix+"cool message", ninetySecondsAgo),
					},
				},
			},
			wantedConditions: []configv1.ClusterOperatorStatusCondition{
				newCondition("Upgradeable", configv1.ConditionFalse, "OperatorFailedStalenessCheck", stalePrefix+"Last reason was \"OkReason\", last status was: cool message", ninetySecondsAgo),
			},
			wantedNumEvents: 1,
			wantErr:         "",
		},
		{
			name: "upgradeable-true-does-change-status",
			fields: fields{
				operatorToDeadlineForResponse: map[string]time.Time{
					"operator": sixtySecondsAgo,
				},
				durationAllowedForOperatorResponse: 1 * time.Minute,
			},
			existingClusterOperator: &configv1.ClusterOperator{
				ObjectMeta: metav1.ObjectMeta{Name: "operator"},
				Status: configv1.ClusterOperatorStatus{
					Conditions: []configv1.ClusterOperatorStatusCondition{
						newCondition("Upgradeable", configv1.ConditionTrue, "OkReason", challengePrefix+"cool message", ninetySecondsAgo),
					},
				},
			},
			wantedConditions: []configv1.ClusterOperatorStatusCondition{
				newCondition("Upgradeable", configv1.ConditionUnknown, "OperatorFailedStalenessCheck", stalePrefix+"Last reason was \"OkReason\", last status was: cool message", nowish),
			},
			wantedNumEvents: 1,
			wantErr:         "",
		},
		{
			name: "skip-check-we-already-did", // this checks to be sure we don't hot-loop on updates
			fields: fields{
				operatorToDeadlineForResponse: map[string]time.Time{
					"operator": sixtySecondsFromNow,
				},
				durationAllowedForOperatorResponse: 1 * time.Minute,
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
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			indexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{})
			indexer.Add(tt.existingClusterOperator)
			clusterOperatorLister := configv1listers.NewClusterOperatorLister(indexer)

			fakeClient := configfake.NewSimpleClientset(tt.existingClusterOperator)

			eventRecorder := events.NewInMemoryRecorder("testing")

			c := &OperatorStalenessChecker{
				operatorToDeadlineForResponse:      tt.fields.operatorToDeadlineForResponse,
				durationAllowedForOperatorResponse: tt.fields.durationAllowedForOperatorResponse,
				clusterOperatorClient:              fakeClient.ConfigV1().ClusterOperators(),
				clusterOperatorLister:              clusterOperatorLister,
				eventRecorder:                      eventRecorder,
				now:                                func() time.Time { return nowish },
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

			if len(eventRecorder.Events()) != tt.wantedNumEvents {
				t.Fatalf("wrong events: %v", eventRecorder.Events())
			}
		})
	}
}
