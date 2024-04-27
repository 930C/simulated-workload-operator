/*
Copyright 2024.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"context"
	"fmt"
	simulationv1alpha1 "github.com/930C/simulated-workload-operator/api/v1alpha1"
	"github.com/930C/simulated-workload-operator/internal/simulation"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"strings"

	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"time"
)

const nanoSecondsLayout = "2006-01-02T15:04:05.999999999Z07:00"

// WorkloadReconciler reconciles a Workload object
type WorkloadReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder record.EventRecorder
}

// ReconcileRequiredPredicate is a predicate function that determines whether reconciliation is required for an event.
// It checks the 'processed' annotation in the new Objects and triggers reconciliation if the annotation is "false"
// or if the annotation does not exist.
type ReconcileRequiredPredicate struct {
	predicate.Funcs
}

//+kubebuilder:rbac:groups=simulation.c930.net,resources=workloads,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=simulation.c930.net,resources=workloads/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=simulation.c930.net,resources=workloads/finalizers,verbs=update
//+kubebuilder:rbac:groups=core,resources=configmaps,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups="",resources=events,verbs=create;patch

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the Workload object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.17.2/pkg/reconcile
func (r *WorkloadReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	// Store the current server time as the reconciliation start timestamp
	reconcileStartTime := time.Now().Format(nanoSecondsLayout)

	logger := log.FromContext(ctx)

	// Fetch the Workload instance
	workload, err := r.getWorkloadInstance(ctx, req)
	if workload == nil || err != nil {
		return ctrl.Result{}, err // err can be nil, so we return it directly
	}

	workload = workload.DeepCopy() // Create a deep copy to avoid overwriting the original object (to avoid conflicts)

	// Let's just set the status as Unknown when no status is available
	if err = r.handleInitialStatus(ctx, workload); err != nil {
		return ctrl.Result{}, err
	}

	// Run in-operator simulations
	alreadyProcessed, err := r.RunWorkload(ctx, workload)
	if err != nil {
		if apierrors.IsNotFound(err) {
			logger.Info("Workload resource not found. Ignoring since object must be deleted.")
			return ctrl.Result{}, nil
		}

		logger.Error(err, "Failed to run simulation")
		return ctrl.Result{}, err
	}

	if alreadyProcessed {
		return ctrl.Result{}, nil
	}

	if err = r.updateWorkloadProcessAnnotation(ctx, workload); err != nil {
		logger.Error(err, "Failed to process Workload annotation")
		return ctrl.Result{}, err
	}

	if err = r.updateWorkloadStatus(ctx, workload, reconcileStartTime); err != nil {
		logger.Error(err, "Failed to update Workload status")
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

func (r *WorkloadReconciler) RunWorkload(ctx context.Context, workload *simulationv1alpha1.Workload) (bool, error) {
	logger := log.FromContext(ctx)

	// Check if the workload has already been processed and if there have been no spec changes.
	if val, ok := workload.Annotations["processed"]; ok && val == "true" {
		logger.Info("SKIPPING processing as workload is marked processed and no spec change")
		return true, nil
	}

	// Depending on the Workloadtype, run the workload
	logger.Info("Running workload", "Workload.Spec.SimulationType", workload.Spec.SimulationType)

	switch strings.ToLower(workload.Spec.SimulationType) {
	case "cpu":
		simulation.RunCPUWorkload(workload.Spec.Duration)
	case "memory":
		simulation.RunMemoryLoad(workload.Spec.Duration, workload.Spec.Intensity)
	case "io":
		if err := simulation.SimulateIO(ctx, workload.Spec.Duration, workload.Spec.Intensity); err != nil {
			logger.Error(err, "Failed to simulate I/O workload")
			return false, err
		}
	case "sleep":
		sleepDuration := time.Duration(workload.Spec.Duration) * time.Second
		logger.Info("Sleeping for", "Duration", sleepDuration)
		time.Sleep(sleepDuration)
	default:
		logger.Info("No workload simulation required", "Workload.Spec.SimulationType", workload.Spec.SimulationType)
		return false, nil
	}

	logger.Info("Workload simulation completed", "Workload.Spec.SimulationType", workload.Spec.SimulationType)
	return false, nil
}

func (r *WorkloadReconciler) getWorkloadInstance(ctx context.Context, req ctrl.Request) (*simulationv1alpha1.Workload, error) {
	logger := log.FromContext(ctx)
	workload := &simulationv1alpha1.Workload{}
	err := r.Get(ctx, req.NamespacedName, workload)
	if err != nil {
		if apierrors.IsNotFound(err) {
			logger.Info("Workload resource not found. Ignoring since object must be deleted")
			return nil, nil // Return empty result when the resource is not found to avoid requeue
		}

		logger.Error(err, "failed to get workload")
		return nil, err // Return error if another error occurs and requeue
	}

	return workload, nil
}

func (r *WorkloadReconciler) handleInitialStatus(ctx context.Context, workload *simulationv1alpha1.Workload) error {
	logger := log.FromContext(ctx)
	if workload.Status.Conditions == nil || len(workload.Status.Conditions) == 0 {
		logger.Info("Setting initial status for Workload")

		meta.SetStatusCondition(&workload.Status.Conditions, metav1.Condition{Type: "Available", Status: metav1.ConditionUnknown, Reason: "Reconciling", Message: "Starting reconciliation"})

		if err := r.Update(ctx, workload); err != nil {
			logger.Error(err, "Failed to update Workload status")
			return err // Return error when the resource is not found to requeue and try again
		}
	}
	return nil
}

func (ReconcileRequiredPredicate) Update(e event.UpdateEvent) bool {
	// Check 'processed' annotation in new Objects
	newProcessed, annotationExists := e.ObjectNew.GetAnnotations()["processed"]

	// Trigger reconciliation if 'processed' annotation is "false" OR if 'processed' annotation does not exist
	if !annotationExists || newProcessed == "false" {
		fmt.Println("ReconcileRequiredPredicate.Update: Reconcile required")
		return true
	}

	return false
}

// SetupWithManager sets up the controller with the Manager.
func (r *WorkloadReconciler) SetupWithManager(mgr ctrl.Manager) error {
	r.Recorder = mgr.GetEventRecorderFor("workload-controller")
	return ctrl.NewControllerManagedBy(mgr).
		For(&simulationv1alpha1.Workload{}).
		WithEventFilter(ReconcileRequiredPredicate{}).
		Complete(r)
}

func (r *WorkloadReconciler) updateWithRetry(ctx context.Context, workload *simulationv1alpha1.Workload) error {
	return wait.ExponentialBackoff(wait.Backoff{
		Duration: 100 * time.Millisecond, // initial delay
		Factor:   2.0,                    // multiplier for subsequent delays
		Jitter:   0.1,                    // random variation in delay calculation
		Steps:    5,                      // max number of attempts
	}, func() (bool, error) {
		if err := r.Update(ctx, workload); err != nil {
			if apierrors.IsConflict(err) {
				// Conflict detected, refresh the object and try again
				if err := r.Get(ctx, types.NamespacedName{Namespace: workload.Namespace, Name: workload.Name}, workload); err != nil {
					return false, err // return error and stop retrying if fetch fails
				}
				return false, nil // fetch successful, retry the update
			}
			return false, err // some other error, stop retrying
		}
		return true, nil // success, stop retrying
	})
}

func (r *WorkloadReconciler) updateWorkloadProcessAnnotation(ctx context.Context, workload *simulationv1alpha1.Workload) error {
	// Mark the workload as processed before updating its status.
	if workload.Annotations == nil {
		workload.Annotations = make(map[string]string)
	}
	workload.Annotations["processed"] = "true"

	// Update the Workload to save the annotations
	if err := r.Update(ctx, workload); err != nil {
		return err
	}

	return nil
}

func (r *WorkloadReconciler) updateWorkloadStatus(ctx context.Context, workload *simulationv1alpha1.Workload, reconcileStartTime string) error {
	// The following implementation will update the status of the Workload Custom Resource to indicate that the
	// reconciliation is successful.
	logger := log.FromContext(ctx)
	logger.Info("Updating final Workload status")
	meta.SetStatusCondition(&workload.Status.Conditions, metav1.Condition{
		Type:    "Available",
		Status:  metav1.ConditionTrue,
		Reason:  "Reconciling",
		Message: fmt.Sprintf("The workload has been successfully processed."),
	})

	workload.Status.StartTime = reconcileStartTime
	// Store the current server time as the reconciliation end timestamp
	reconcileEndTime := time.Now().Format(nanoSecondsLayout)
	workload.Status.EndTime = reconcileEndTime

	//// Make the Kubernetes request within a retryable loop to handle potential conflicting writes from other components
	//err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
	//	// Fetch the latest version to get the latest ResourceVersion to avoid conflicts
	//	if err := r.Get(ctx, types.NamespacedName{Namespace: workload.Namespace, Name: workload.Name}, workload); err != nil {
	//		return err
	//	}
	//	return r.Status().Update(ctx, workload)
	//})

	err := r.Status().Update(ctx, workload)

	if err != nil {
		logger.Error(err, "Failed to update last Workload status")
		return err
	}

	return nil
}
