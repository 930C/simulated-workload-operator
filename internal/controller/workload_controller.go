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
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"strings"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"time"
)

const nanoSecondsLayout = "2006-01-02T15:04:05.999999999Z07:00"

// WorkloadReconciler reconciles a Workload object
type WorkloadReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

//+kubebuilder:rbac:groups=simulation.c930.net,resources=workloads,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=simulation.c930.net,resources=workloads/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=simulation.c930.net,resources=workloads/finalizers,verbs=update
//+kubebuilder:rbac:groups=core,resources=configmaps,verbs=get;list;watch;create;update;patch;delete

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
	workload := &simulationv1alpha1.Workload{}
	if err := r.Get(ctx, req.NamespacedName, workload); err != nil {
		if apierrors.IsNotFound(err) {
			logger.Info("Workload resource not found. Ignoring since object must be deleted")
			return ctrl.Result{}, nil // Return empty result when the resource is not found to avoid requeue
		}

		logger.Error(err, "failed to get workload")
		return ctrl.Result{}, err // Return error if another error occurs and requeue
	}

	workload = workload.DeepCopy() // Create a deep copy to avoid overwriting the original object (to avoid conflicts)

	// Set the status condition of "Available" as Unknown when no status is available
	if err := r.handleInitialStatusCondition(ctx, workload); err != nil {
		logger.Error(err, "failed to set initial status condition")
		return ctrl.Result{}, err
	}

	// Run in-operator simulations
	r.runWorkload(ctx, workload)

	// Reconcile optional NGINX deployment
	err := r.ReconcileNginxDeployment(ctx, workload)
	if err != nil {
		logger.Error(err, "Failed to reconcile NGINX deployment")
		return ctrl.Result{}, err
	}

	// Update the status of the Workload CR to indicate that the reconciliation is successful.
	if err := r.updateWorkloadStatusCondition(ctx, workload, reconcileStartTime); err != nil {
		logger.Error(err, "Failed to update Workload status")
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

func (r *WorkloadReconciler) runWorkload(ctx context.Context, workload *simulationv1alpha1.Workload) {
	logger := log.FromContext(ctx)

	// Check if the workload has already been processed and if there have been no spec changes.
	if workload.Status.Executed {
		logger.Info("SKIPPING processing as workload is marked processed and no spec change")
	}

	// Depending on the Workloadtype, run the workload
	logger.Info("Running workload", "Workload.Spec.SimulationType", workload.Spec.SimulationType)

	switch strings.ToLower(workload.Spec.SimulationType) {
	case "cpu":
		simulation.RunCPUWorkload(workload.Spec.Duration)
	case "memory":
		simulation.RunMemoryLoad(workload.Spec.Duration, workload.Spec.Intensity)
	case "io":
		simulation.SimulateIO(ctx, workload.Spec.Duration, workload.Spec.Intensity)
	case "sleep":
		sleepDuration := time.Duration(workload.Spec.Duration) * time.Second
		logger.Info("Sleeping for", "Duration", sleepDuration)
		time.Sleep(sleepDuration)
	default:
		logger.Info("No workload simulation required", "Workload.Spec.SimulationType", workload.Spec.SimulationType)
	}

	logger.Info("Workload simulation completed", "Workload.Spec.SimulationType", workload.Spec.SimulationType)
}

// handleInitialStatusCondition sets the initial status condition for the Workload CR when no condition is available.
func (r *WorkloadReconciler) handleInitialStatusCondition(ctx context.Context, workload *simulationv1alpha1.Workload) error {
	logger := log.FromContext(ctx)
	if workload.Status.Conditions == nil || len(workload.Status.Conditions) == 0 {
		logger.Info("Setting initial status for Workload")
		meta.SetStatusCondition(&workload.Status.Conditions, metav1.Condition{
			Type:    "Available",
			Status:  metav1.ConditionUnknown,
			Reason:  "Reconciling",
			Message: "Starting reconciliation",
		})
		return r.Status().Update(ctx, workload)
	}
	return nil
}

func (r *WorkloadReconciler) updateWorkloadStatusCondition(ctx context.Context, workload *simulationv1alpha1.Workload, reconcileStartTime string) error {
	// update the status of the Workload CR to indicate that the reconciliation is successful.
	logger := log.FromContext(ctx)
	logger.Info("Updating final Workload status")

	meta.SetStatusCondition(&workload.Status.Conditions, metav1.Condition{
		Type:    "Available",
		Status:  metav1.ConditionTrue,
		Reason:  "Reconciling",
		Message: fmt.Sprintf("The workload has been successfully processed."),
	})

	workload.Status.StartTime = reconcileStartTime
	workload.Status.EndTime = time.Now().Format(nanoSecondsLayout) // Store the current server time as the reconciliation end timestamp

	workload.Status.Executed = true // Mark the workload as executed in the Workload Status

	return r.Status().Update(ctx, workload)
}

// SetupWithManager sets up the controller with the Manager.
func (r *WorkloadReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&simulationv1alpha1.Workload{}).
		WithEventFilter(predicate.Or(
			predicate.GenerationChangedPredicate{},
			predicate.LabelChangedPredicate{},
			predicate.AnnotationChangedPredicate{})).
		WithOptions(controller.Options{MaxConcurrentReconciles: 1}).
		Complete(r)
}
