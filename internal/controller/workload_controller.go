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
	"errors"
	"fmt"
	simulationv1alpha1 "github.com/930C/simulated-workload-operator/api/v1alpha1"
	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"time"
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
	logger := log.FromContext(ctx)

	// Fetch the Workload instance
	workload, err := r.getWorkloadInstance(ctx, req)
	if workload == nil || err != nil {
		return ctrl.Result{}, err // err can be nil, so we return it directly
	}

	// Let's just set the status as Unknown when no status is available
	err = r.handleInitialStatus(ctx, workload)
	if err != nil {
		return ctrl.Result{}, err
	}

	// Adds a finalizer to define some operations which should occur before the custom resource is deleted.
	// More info: https://kubernetes.io/docs/concepts/overview/working-with-objects/finalizers
	err = r.handleFinalizer(ctx, workload)
	if err != nil {
		return ctrl.Result{}, err
	}

	// Check if Workload instance is about to be deleted
	deleted, err := r.handleWorkloadDeletion(ctx, workload) // ?
	if err != nil {
		return ctrl.Result{}, err
	} else if deleted {
		return ctrl.Result{}, nil
	}

	requeue, err := r.handleConfigMapCreation(ctx, workload)
	if err != nil {
		return ctrl.Result{}, err
	} else if requeue {
		return ctrl.Result{RequeueAfter: time.Minute}, nil
	}

	err = r.handleConfigMapDrift(ctx, workload)
	if err != nil {
		return ctrl.Result{}, err
	}

	// Get the latest state of the Workload Custom Resource
	if err = r.Get(ctx, req.NamespacedName, workload); err != nil {
		if apierrors.IsNotFound(err) {
			// The Workload resource might have been deleted after reconcile request.
			// Return and don't requeue.
			logger.Info("Workload resource not found. Ignoring since object must be deleted.")
			return ctrl.Result{}, nil
		}
		logger.Error(err, "Failed to get Workload")
		return ctrl.Result{}, err
	}

	// The following implementation will update the status
	meta.SetStatusCondition(&workload.Status.Conditions, metav1.Condition{
		Type:    "Available",
		Status:  metav1.ConditionTrue,
		Reason:  "Reconciling",
		Message: fmt.Sprintf("ConfigMap for custom resource (%s) created successfully", workload.Name),
	})

	if err = r.Status().Update(ctx, workload); err != nil {
		logger.Error(err, "Failed to update Workload status")
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

func (r *WorkloadReconciler) getWorkloadInstance(ctx context.Context, req ctrl.Request) (*simulationv1alpha1.Workload, error) {
	logger := log.FromContext(ctx)
	workload := &simulationv1alpha1.Workload{}
	err := r.Get(ctx, req.NamespacedName, workload)
	if err != nil {
		if apierrors.IsNotFound(err) {
			logger.Info("simulation resource not found. It must have been deleted")
			return nil, nil // Return empty result when the resource is not found to avoid requeue
		}

		logger.Error(err, "failed to get workload")
		return nil, err // Return error when the resource is not found to requeue and try again
	}

	return workload, nil
}

func (r *WorkloadReconciler) handleInitialStatus(ctx context.Context, workload *simulationv1alpha1.Workload) error {
	logger := log.FromContext(ctx)
	if workload.Status.Conditions == nil || len(workload.Status.Conditions) == 0 {
		meta.SetStatusCondition(&workload.Status.Conditions, metav1.Condition{Type: "Available", Status: metav1.ConditionUnknown, Reason: "Reconciling", Message: "Starting reconciliation"})
		if err := r.Status().Update(ctx, workload); err != nil {
			logger.Error(err, "Failed to update Workload status")
			return err // Return error when the resource is not found to requeue and try again
		}

		// Let's re-fetch the Workload Custom Resource after updating the status
		// so that we have the latest state of the resource on the cluster and we will avoid
		// raising the error "the object has been modified, please apply
		// your changes to the latest version and try again" which would re-trigger the reconciliation
		// if we try to update it again in the following operations
		if err := r.Get(ctx, types.NamespacedName{
			Namespace: workload.Namespace,
			Name:      workload.Name,
		}, workload); err != nil {
			logger.Error(err, "Failed to re-fetch Workload")
			return err
		}
	}
	return nil
}

func (r *WorkloadReconciler) handleFinalizer(ctx context.Context, workload *simulationv1alpha1.Workload) error {
	logger := log.FromContext(ctx)

	if err := r.Get(ctx, types.NamespacedName{Namespace: workload.Namespace, Name: workload.Name}, workload); err != nil {
		return err
	}

	if !controllerutil.ContainsFinalizer(workload, workloadFinalizer) {
		logger.Info("Adding Finalizer for Workload")

		controllerutil.AddFinalizer(workload, workloadFinalizer)

		return r.Update(ctx, workload)
	}

	return nil
}

func (r *WorkloadReconciler) handleWorkloadDeletion(ctx context.Context, workload *simulationv1alpha1.Workload) (bool, error) {
	logger := log.FromContext(ctx)

	isWorkloadMarkedToBeDeleted := workload.GetDeletionTimestamp() != nil
	if isWorkloadMarkedToBeDeleted {
		if controllerutil.ContainsFinalizer(workload, workloadFinalizer) {
			logger.Info("Performing Finalizer Operations for Workload before the deletion of the CR")

			// Add "Downgrade" status to indicate that this process is terminating
			meta.SetStatusCondition(&workload.Status.Conditions, metav1.Condition{
				Type:    "Degraded",
				Status:  metav1.ConditionUnknown,
				Reason:  "Finalizing",
				Message: fmt.Sprintf("Performing finalizer operations for the custom resource: %s ", workload.Name),
			})

			if err := r.Status().Update(ctx, workload); err != nil {
				logger.Error(err, "Failed to update Workload status")
				return false, err
			}

			r.Recorder.Event(workload, "Warning", "Deleting", fmt.Sprintf("Custom Resource %s is being deleted from the namespace %s", workload.Name, workload.Namespace))

			if err := r.Get(ctx, types.NamespacedName{
				Namespace: workload.Namespace,
				Name:      workload.Name,
			}, workload); err != nil {
				logger.Error(err, "failed to re-fetch workload")
				return false, err
			}

			meta.SetStatusCondition(&workload.Status.Conditions, metav1.Condition{
				Type:    "Downgraded",
				Status:  metav1.ConditionTrue,
				Reason:  "Finalizing",
				Message: fmt.Sprintf("Finalizer operations for custom resource %s name were successfully accomplished", workload.Name),
			})

			if err := r.Status().Update(ctx, workload); err != nil {
				logger.Error(err, "Failed to update Workload status")
				return false, err
			}

			logger.Info("Removing Finalizer for Workload after successfully perform the operations")
			if ok := controllerutil.RemoveFinalizer(workload, workloadFinalizer); !ok {
				err := errors.New("failed to remove finalizer for Workload")
				logger.Error(err, "Failed to remove finalizer for Workload")
				return false, err
			}

			if err := r.Update(ctx, workload); err != nil {
				logger.Error(err, "Failed to remove finalizer for Workload")
				return false, err
			}

			return true, nil
		}
	}
	return false, nil
}

func (r *WorkloadReconciler) handleConfigMapCreation(ctx context.Context, workload *simulationv1alpha1.Workload) (bool, error) {
	logger := log.FromContext(ctx)
	configMap := &v1.ConfigMap{}
	err := r.Get(ctx, types.NamespacedName{Name: workload.Name, Namespace: workload.Namespace}, configMap)
	if err != nil && apierrors.IsNotFound(err) {
		// Define a new ConfigMap
		cm := r.createDesiredConfigMap(workload)

		if err = ctrl.SetControllerReference(workload, cm, r.Scheme); err != nil {
			logger.Error(err, "failed to set controller reference for the new ConfigMap for Workload CR")

			meta.SetStatusCondition(&workload.Status.Conditions, metav1.Condition{
				Type:    "Available",
				Status:  metav1.ConditionFalse,
				Reason:  "Reconciling",
				Message: fmt.Sprintf("Failed to create Deployment for the custom resource (%s): (%s)", workload.Name, err),
			})

			if err := r.Status().Update(ctx, workload); err != nil {
				logger.Error(err, "Failed to update Workload status")
				return false, err
			}

			return false, err
		}

		logger.Info("Creating a new ConfigMap", "ConfigMap.Namespace", cm.Namespace, "ConfigMap.Name", cm.Name)
		if err = r.Create(ctx, cm); err != nil {
			logger.Error(err, "Failed to create new Deployment",
				"Deployment.Namespace", cm.Namespace,
				"Deployment.Name", cm.Name)
			return false, err
		}

		// ConfigMap created successfully
		// We will requeue the reconciliation so that we can ensure the state and move forward for the next operations
		return true, nil
	} else if err != nil {
		logger.Error(err, "Failed to get ConfigMap")
		return false, err
	} else {
		logger.Info("ConfigMap already exists", "ConfigMap.Namespace", configMap.Namespace, "ConfigMap.Name", configMap.Name)
	}

	return false, nil
}

func (r *WorkloadReconciler) handleConfigMapDrift(ctx context.Context, workload *simulationv1alpha1.Workload) error {
	logger := log.FromContext(ctx)
	configMap := &v1.ConfigMap{}
	err := r.Get(ctx, types.NamespacedName{
		Namespace: workload.Namespace,
		Name:      workload.Name,
	}, configMap)
	if err != nil {
		logger.Error(err, "Failed to get ConfigMap")
		return err
	}

	// Check if the ConfigMap is up-to-date
	desiredConfigMap := r.createDesiredConfigMap(workload)
	if configMap.Data["key1"] != desiredConfigMap.Data["key1"] {
		logger.Info("ConfigMap is not up-to-date. Updating it now")

		// Update the ConfigMap
		configMap.Data["key1"] = desiredConfigMap.Data["key1"]
		if err = r.Update(ctx, configMap); err != nil {
			logger.Error(err, "Failed to update ConfigMap")
			return err
		}

		logger.Info("ConfigMap updated successfully")
	}

	return nil
}

func (r *WorkloadReconciler) createDesiredConfigMap(workload *simulationv1alpha1.Workload) *v1.ConfigMap {
	return &v1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      workload.Name,
			Namespace: workload.Namespace,
		},
		Data: map[string]string{
			"key1": "value1",
		},
	}
}

// SetupWithManager sets up the controller with the Manager.
func (r *WorkloadReconciler) SetupWithManager(mgr ctrl.Manager) error {
	r.Recorder = mgr.GetEventRecorderFor("workload-controller")
	return ctrl.NewControllerManagedBy(mgr).
		For(&simulationv1alpha1.Workload{}).
		Owns(&v1.ConfigMap{}).
		Complete(r)
}
