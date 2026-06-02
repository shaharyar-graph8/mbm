package controller

import (
	"context"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	axonv1alpha1 "github.com/axon-core/axon/api/v1alpha1"
)

func TestTTLExpired(t *testing.T) {
	r := &TaskReconciler{}

	int32Ptr := func(v int32) *int32 { return &v }
	timePtr := func(t time.Time) *metav1.Time {
		mt := metav1.NewTime(t)
		return &mt
	}

	tests := []struct {
		name            string
		task            *axonv1alpha1.Task
		wantExpired     bool
		wantRequeueMin  time.Duration
		wantRequeueMax  time.Duration
		wantZeroRequeue bool
	}{
		{
			name: "No TTL set",
			task: &axonv1alpha1.Task{
				Spec: axonv1alpha1.TaskSpec{
					TTLSecondsAfterFinished: nil,
				},
				Status: axonv1alpha1.TaskStatus{
					Phase:          axonv1alpha1.TaskPhaseSucceeded,
					CompletionTime: timePtr(time.Now().Add(-10 * time.Second)),
				},
			},
			wantExpired:     false,
			wantZeroRequeue: true,
		},
		{
			name: "Not in terminal phase",
			task: &axonv1alpha1.Task{
				Spec: axonv1alpha1.TaskSpec{
					TTLSecondsAfterFinished: int32Ptr(60),
				},
				Status: axonv1alpha1.TaskStatus{
					Phase: axonv1alpha1.TaskPhaseRunning,
				},
			},
			wantExpired:     false,
			wantZeroRequeue: true,
		},
		{
			name: "CompletionTime not set",
			task: &axonv1alpha1.Task{
				Spec: axonv1alpha1.TaskSpec{
					TTLSecondsAfterFinished: int32Ptr(60),
				},
				Status: axonv1alpha1.TaskStatus{
					Phase:          axonv1alpha1.TaskPhaseSucceeded,
					CompletionTime: nil,
				},
			},
			wantExpired:     false,
			wantZeroRequeue: true,
		},
		{
			name: "TTL=0 and completed",
			task: &axonv1alpha1.Task{
				Spec: axonv1alpha1.TaskSpec{
					TTLSecondsAfterFinished: int32Ptr(0),
				},
				Status: axonv1alpha1.TaskStatus{
					Phase:          axonv1alpha1.TaskPhaseSucceeded,
					CompletionTime: timePtr(time.Now().Add(-1 * time.Second)),
				},
			},
			wantExpired:     true,
			wantZeroRequeue: true,
		},
		{
			name: "TTL expired for succeeded task",
			task: &axonv1alpha1.Task{
				Spec: axonv1alpha1.TaskSpec{
					TTLSecondsAfterFinished: int32Ptr(10),
				},
				Status: axonv1alpha1.TaskStatus{
					Phase:          axonv1alpha1.TaskPhaseSucceeded,
					CompletionTime: timePtr(time.Now().Add(-20 * time.Second)),
				},
			},
			wantExpired:     true,
			wantZeroRequeue: true,
		},
		{
			name: "TTL expired for failed task",
			task: &axonv1alpha1.Task{
				Spec: axonv1alpha1.TaskSpec{
					TTLSecondsAfterFinished: int32Ptr(5),
				},
				Status: axonv1alpha1.TaskStatus{
					Phase:          axonv1alpha1.TaskPhaseFailed,
					CompletionTime: timePtr(time.Now().Add(-10 * time.Second)),
				},
			},
			wantExpired:     true,
			wantZeroRequeue: true,
		},
		{
			name: "TTL not yet expired",
			task: &axonv1alpha1.Task{
				Spec: axonv1alpha1.TaskSpec{
					TTLSecondsAfterFinished: int32Ptr(60),
				},
				Status: axonv1alpha1.TaskStatus{
					Phase:          axonv1alpha1.TaskPhaseSucceeded,
					CompletionTime: timePtr(time.Now()),
				},
			},
			wantExpired:    false,
			wantRequeueMin: 50 * time.Second,
			wantRequeueMax: 61 * time.Second,
		},
		{
			name: "Pending phase with TTL",
			task: &axonv1alpha1.Task{
				Spec: axonv1alpha1.TaskSpec{
					TTLSecondsAfterFinished: int32Ptr(10),
				},
				Status: axonv1alpha1.TaskStatus{
					Phase: axonv1alpha1.TaskPhasePending,
				},
			},
			wantExpired:     false,
			wantZeroRequeue: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			expired, requeueAfter := r.ttlExpired(tt.task)
			if expired != tt.wantExpired {
				t.Errorf("ttlExpired() expired = %v, want %v", expired, tt.wantExpired)
			}
			if tt.wantZeroRequeue {
				if requeueAfter != 0 {
					t.Errorf("ttlExpired() requeueAfter = %v, want 0", requeueAfter)
				}
			} else {
				if requeueAfter < tt.wantRequeueMin || requeueAfter > tt.wantRequeueMax {
					t.Errorf("ttlExpired() requeueAfter = %v, want between %v and %v",
						requeueAfter, tt.wantRequeueMin, tt.wantRequeueMax)
				}
			}
		})
	}
}

// TestMarkOrphanedJobFailed verifies the fix for the perpetual-Pending bug:
// a non-terminal Task whose Job has been GC'd (cleanup CronJob deleted a
// finished Job before its status was observed) must transition to Failed
// rather than have its Job recreated and re-run forever. A Task that has
// already settled must be left untouched.
func TestMarkOrphanedJobFailed(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := axonv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("AddToScheme: %v", err)
	}

	tests := []struct {
		name       string
		startPhase axonv1alpha1.TaskPhase
		wantPhase  axonv1alpha1.TaskPhase
	}{
		{name: "orphaned Pending becomes Failed", startPhase: axonv1alpha1.TaskPhasePending, wantPhase: axonv1alpha1.TaskPhaseFailed},
		{name: "orphaned Running becomes Failed", startPhase: axonv1alpha1.TaskPhaseRunning, wantPhase: axonv1alpha1.TaskPhaseFailed},
		{name: "already Succeeded is untouched", startPhase: axonv1alpha1.TaskPhaseSucceeded, wantPhase: axonv1alpha1.TaskPhaseSucceeded},
		{name: "already Failed is untouched", startPhase: axonv1alpha1.TaskPhaseFailed, wantPhase: axonv1alpha1.TaskPhaseFailed},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			task := &axonv1alpha1.Task{
				ObjectMeta: metav1.ObjectMeta{Name: "t1", Namespace: "axon-system"},
				Spec:       axonv1alpha1.TaskSpec{Type: "codex"},
				Status: axonv1alpha1.TaskStatus{
					Phase:   tt.startPhase,
					JobName: "t1",
				},
			}
			cl := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(task).
				WithStatusSubresource(task).
				Build()
			r := &TaskReconciler{Client: cl}

			if _, err := r.markOrphanedJobFailed(context.Background(), task); err != nil {
				t.Fatalf("markOrphanedJobFailed: %v", err)
			}

			var got axonv1alpha1.Task
			if err := cl.Get(context.Background(), client.ObjectKeyFromObject(task), &got); err != nil {
				t.Fatalf("get task: %v", err)
			}
			if got.Status.Phase != tt.wantPhase {
				t.Errorf("phase = %q, want %q", got.Status.Phase, tt.wantPhase)
			}
			if tt.wantPhase == axonv1alpha1.TaskPhaseFailed && tt.startPhase != axonv1alpha1.TaskPhaseFailed && got.Status.CompletionTime == nil {
				t.Errorf("expected CompletionTime to be set on newly-failed task")
			}
		})
	}
}
