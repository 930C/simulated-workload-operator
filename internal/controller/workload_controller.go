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
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	simulationv1alpha1 "github.com/930C/simulated-workload-operator/api/v1alpha1"
)

const workloadFinalizer = "workload.c930.net/finalizer"

// WorkloadReconciler reconciles a Workload object
type WorkloadReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder record.EventRecorder
}

//+kubebuilder:rbac:groups=simulation.c930.net,resources=workloads,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=simulation.c930.net,resources=workloads/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=simulation.c930.net,resources=workloads/finalizers,verbs=update

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
	log := log.FromContext(ctx)

	// Fetch the Workload instance
	workload := &simulationv1alpha1.Workload{}
	err := r.Get(ctx, req.NamespacedName, workload)

	if err != nil {
		if apierrors.IsNotFound(err) {
			log.Info("simulation resource not found. It must have been deleted")
			return ctrl.Result{}, nil
		}

		log.Error(err, "failed to get workload")
		return ctrl.Result{}, err
	}

	// Let's just set the status as Unknown when no status is available
	if workload.Status.Conditions == nil || len(workload.Status.Conditions) == 0 {
		meta.SetStatusCondition(&workload.Status.Conditions, metav1.Condition{Type: "Available", Status: metav1.ConditionUnknown, Reason: "Reconciling", Message: "Starting reconciliation"})
		if err := r.Status().Update(ctx, workload); err != nil {
			log.Error(err, "Failed to update Workload status")
			return ctrl.Result{}, err
		}

		// Let's re-fetch the Workload Custom Resource after updating the status
		// so that we have the latest state of the resource on the cluster and we will avoid
		// raising the error "the object has been modified, please apply
		// your changes to the latest version and try again" which would re-trigger the reconciliation
		// if we try to update it again in the following operations
		if err := r.Get(ctx, req.NamespacedName, workload); err != nil {
			log.Error(err, "Failed to re-fetch Workload")
			return ctrl.Result{}, err
		}
	}

	// Let's add a finalizer. Then, we can define some operations which should
	// occur before the custom resource is deleted.
	// More info: https://kubernetes.io/docs/concepts/overview/working-with-objects/finalizers
	if !controllerutil.ContainsFinalizer(workload, workloadFinalizer) {
		log.Info("Adding Finalizer for Workload")
		if ok := controllerutil.AddFinalizer(workload, workloadFinalizer); !ok {
			log.Error(err, "Failed to add finalizer into the custom resource")
			return ctrl.Result{Requeue: true}, nil
		}

		if err = r.Update(ctx, workload); err != nil {
			log.Error(err, "Failed to update custom resource to add finalizer")
			return ctrl.Result{}, err
		}
	}

	// Check if Workload instance is about to be deleted
	isWorkloadMarkedToBeDeleted := workload.GetDeletionTimestamp() != nil
	if isWorkloadMarkedToBeDeleted {
		if controllerutil.ContainsFinalizer(workload, workloadFinalizer) {
			log.Info("Performing Finalizer Operations for Workload before the deletion of the CR")

			// Add "Downgrade" status to indicate that this process is terminating
			meta.SetStatusCondition(&workload.Status.Conditions, metav1.Condition{
				Type:    "Degraded",
				Status:  metav1.ConditionUnknown,
				Reason:  "Finalizing",
				Message: fmt.Sprintf("Performing finalizer operations for the custom resource: %s ", workload.Name),
			})

			if err := r.Status().Update(ctx, workload); err != nil {
				log.Error(err, "Failed to update Workload status")
				return ctrl.Result{}, err
			}

			r.Recorder.Event(workload, "Warning", "Deleting", fmt.Sprintf("Custom Resource %s is being deleted from the namespace %s", workload.Name, workload.Namespace))

			if err := r.Get(ctx, req.NamespacedName, workload); err != nil {
				log.Error(err, "failed to re-fetch workload")
				return ctrl.Result{}, err
			}

			meta.SetStatusCondition(&workload.Status.Conditions, metav1.Condition{
				Type:    "Downgraded",
				Status:  metav1.ConditionTrue,
				Reason:  "Finalizing",
				Message: fmt.Sprintf("Finalizer operations for custom resource %s name were successfully accomplished", workload.Name),
			})

			if err := r.Status().Update(ctx, workload); err != nil {
				log.Error(err, "Failed to update Workload status")
				return ctrl.Result{}, err
			}

			log.Info("Removing Finalizer for Workload after successfully perform the operations")
			if ok := controllerutil.RemoveFinalizer(workload, workloadFinalizer); !ok {
				log.Error(err, "Failed to remove finalizer for Workload")
				return ctrl.Result{Requeue: true}, nil
			}

			if err := r.Update(ctx, workload); err != nil {
				log.Error(err, "Failed to remove finalizer for Workload")
				return ctrl.Result{}, err
			}
		}
		return ctrl.Result{}, nil
	}

	configMap := &v1.ConfigMap{}
	err = r.Get(ctx, types.NamespacedName{Name: workload.Name, Namespace: workload.Namespace}, configMap)
	if err != nil && apierrors.IsNotFound(err) {
		// Define a new ConfigMap
		cm := &v1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      workload.Name,
				Namespace: workload.Namespace,
			},
			Data: map[string]string{
				"key1": "value1",
			},
		}

		if err := ctrl.SetControllerReference(workload, cm, r.Scheme); err != nil {
			log.Error(err, "failed to set controller reference for the new ConfigMap for Workload CR")

			meta.SetStatusCondition(&workload.Status.Conditions, metav1.Condition{
				Type:    "Available",
				Status:  metav1.ConditionFalse,
				Reason:  "Reconciling",
				Message: fmt.Sprintf("Failed to create Deployment for the custom resource (%s): (%s)", workload.Name, err),
			})

			if err := r.Status().Update(ctx, workload); err != nil {
				log.Error(err, "Failed to update Workload status")
				return ctrl.Result{}, err
			}

			return ctrl.Result{}, err
		}

		log.Info("Creating a new ConfigMap", "ConfigMap.Namespace", cm.Namespace, "ConfigMap.Name", cm.Name)
		if err = r.Create(ctx, cm); err != nil {
			log.Error(err, "Failed to create new Deployment",
				"Deployment.Namespace", cm.Namespace,
				"Deployment.Name", cm.Name)
			return ctrl.Result{}, err
		}

		// Deployment created successfully
		// We will requeue the reconciliation so that we can ensure the state and move forward for the next operations
		return ctrl.Result{RequeueAfter: time.Minute}, nil
	} else if err != nil {
		log.Error(err, "Failed to get ConfigMap")
		return ctrl.Result{}, err
	}

	// The following implementation will update the status
	meta.SetStatusCondition(&workload.Status.Conditions, metav1.Condition{
		Type:    "Available",
		Status:  metav1.ConditionTrue,
		Reason:  "Reconciling",
		Message: fmt.Sprintf("ConfigMap for custom resource (%s) created successfully", workload.Name),
	})

	if err := r.Status().Update(ctx, workload); err != nil {
		log.Error(err, "Failed to update Workload status")
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *WorkloadReconciler) SetupWithManager(mgr ctrl.Manager) error {
	r.Recorder = mgr.GetEventRecorderFor("workload-controller")
	return ctrl.NewControllerManagedBy(mgr).
		For(&simulationv1alpha1.Workload{}).
		Complete(r)
}
