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
	"crypto/rand"
	"errors"
	"fmt"
	simulationv1alpha1 "github.com/930C/simulated-workload-operator/api/v1alpha1"
	"github.com/go-logr/logr"
	"io"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	rand2 "math/rand"
	"os"
	runtime2 "runtime"
	"strings"

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
		logger.Info("Workload instance deleted")
		return ctrl.Result{}, nil
	}

	// Run in-operator simulations
	err = r.RunWorkload(ctx, workload)
	if err != nil {
		logger.Error(err, "Failed to run simulation")
		return ctrl.Result{}, err
	}

	// Get the latest state of the Workload Custom Resource
	if err = r.Get(ctx, req.NamespacedName, workload); err != nil {
		if apierrors.IsNotFound(err) {
			logger.Info("Workload resource not found. Ignoring since object must be deleted.")
			return ctrl.Result{}, nil
		}
		logger.Error(err, "Failed to get Workload")
		return ctrl.Result{}, err
	}

	// Mark the workload as processed before updating its status.
	if workload.Annotations == nil {
		workload.Annotations = make(map[string]string)
	}
	workload.Annotations["processed"] = "true"

	// Update the Workload to save the annotations
	if err = r.Update(ctx, workload); err != nil {
		logger.Error(err, "Failed to update workload with annotation")
		return ctrl.Result{}, err
	}

	// The following implementation will update the status
	// of the Workload Custom Resource to indicate that the
	// reconciliation is successful.
	logger.Info("Updating final Workload status")
	meta.SetStatusCondition(&workload.Status.Conditions, metav1.Condition{
		Type:    "Available",
		Status:  metav1.ConditionTrue,
		Reason:  "Reconciling",
		Message: fmt.Sprintf("The workload has been successfully processed."),
	})

	if err = r.Status().Update(ctx, workload); err != nil {
		logger.Error(err, "Failed to update last Workload status")
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

func (r *WorkloadReconciler) RunWorkload(ctx context.Context, workload *simulationv1alpha1.Workload) error {
	logger := log.FromContext(ctx)

	// Check if the workload has already been processed and if there have been no spec changes.
	if val, ok := workload.Annotations["processed"]; ok && val == "true" {
		logger.Info("SKIPPING processing as workload is marked processed and no spec change")
		return nil
	}

	// Depending on the Workloadtype, run the workload
	logger.Info("Running workload", "Workload.Spec.SimulationType", workload.Spec.SimulationType)

	switch strings.ToLower(workload.Spec.SimulationType) {
	case "cpu":
		runCPUWorkload(workload.Spec.Duration)
	case "memory":
		runMemoryLoad(workload.Spec.Duration, workload.Spec.Intensity)
	case "io":
		if err := simulateIO(ctx, workload.Spec.Duration, workload.Spec.Intensity); err != nil {
			logger.Error(err, "Failed to simulate I/O workload")
			return err
		}
	case "sleep":
		sleepDuration := time.Duration(workload.Spec.Duration) * time.Second
		logger.Info("Sleeping for", "Duration", sleepDuration)
		time.Sleep(sleepDuration)
	default:
		logger.Info("No workload simulation required", "Workload.Spec.SimulationType", workload.Spec.SimulationType)
		return nil
	}

	logger.Info("Workload simulation completed", "Workload.Spec.SimulationType", workload.Spec.SimulationType)
	return nil
}

func runCPUWorkload(duration int) {
	// Run any CPU workload
	done := make(chan int)

	for i := 0; i < runtime2.NumCPU(); i++ {
		go func() {
			for {
				select {
				case <-done:
					return
				default:
				}
			}
		}()
	}

	time.Sleep(time.Duration(duration) * time.Second)
	close(done)
}

func runMemoryLoad(duration int, megabytes int) {
	type MemoryLoad struct {
		Block []byte
	}

	numBlocks := megabytes   // Adjust this value depending on how much memory you want to consume.
	blockSize := 1024 * 1024 // Each block is 1 megabyte.

	var blocks = make([]MemoryLoad, numBlocks)

	for i := 0; i < numBlocks; i++ {
		blocks[i] = MemoryLoad{make([]byte, blockSize)}

		if i%100 == 0 {
			fmt.Printf("Allocated %v MB\n", (i+1)*blockSize/(1024*1024))
		}
	}

	fmt.Println("Holding memory for ", duration, " seconds...")
	time.Sleep(time.Second * time.Duration(duration))
	fmt.Println("Memory sleep finished")
	runtime2.GC()
}

// simulateIO simulates file I/O operations by writing and reading back random data from a temporary file.
// `duration` specifies how long the I/O operations should run, and `sizeMB` specifies the size of the data to write (in each operation in megabytes)
func simulateIO(ctx context.Context, duration int, sizeMB int) error {
	logger := log.FromContext(ctx)
	logger.Info("Starting I/O simulation", "Duration", duration, "SizeMB", sizeMB)

	// Define the duration for the simulation
	endTime := time.Now().Add(time.Duration(duration) * time.Second)
	randomSource := rand2.New(rand2.NewSource(time.Now().UnixNano()))

	for time.Now().Before(endTime) {
		fileSize := randomSource.Intn(sizeMB) + 1 // Ensure non-zero file size
		data := make([]byte, fileSize*1024*1024)
		_, err := rand.Read(data)
		if err != nil {
			logger.Error(err, "Failed to generate random data")
			return err
		}

		// Simulate mixed read/write and random access by creating multiple files
		for i := 0; i < 5; i++ { // Create multiple files to simulate handling multiple resources
			fileName := fmt.Sprintf("simulate-io-%d", i)
			err := performFileOperations(ctx, fileName, data)
			if err != nil {
				return err
			}
		}
	}

	runtime2.GC()

	logger.Info("Complex I/O simulation completed")
	return nil
}

// performFileOperations handles the actual file writing and reading, simulating random access and mixed operations.
func performFileOperations(ctx context.Context, fileName string, data []byte) error {
	logger := log.FromContext(ctx)
	// Create a temporary file
	tmpfile, err := os.CreateTemp("", fileName)
	if err != nil {
		logger.Error(err, "Failed to create temporary file")
		return err
	}
	defer func(name string) {
		err := os.Remove(name)
		if err != nil {
			logger.Error(err, "Failed to remove temporary file")
		}
	}(tmpfile.Name()) // Clean up

	// Randomly choose to write or read first to simulate mixed operations
	if rand2.Int()%2 == 0 {
		if err := writeAndReadFile(tmpfile, data, logger); err != nil {
			return err
		}
	} else {
		if err := readFileAndWrite(tmpfile, data, logger); err != nil {
			return err
		}
	}

	return nil
}

func writeAndReadFile(file *os.File, data []byte, logger logr.Logger) error {
	defer file.Close()

	if _, err := file.Write(data); err != nil {
		logger.Error(err, "Failed to write to file")
		return err
	}

	if _, err := file.Seek(0, io.SeekStart); err != nil {
		logger.Error(err, "Failed to seek in file")
		return err
	}

	if _, err := io.ReadAll(file); err != nil {
		logger.Error(err, "Failed to read from file")
		return err
	}

	return nil
}

func readFileAndWrite(file *os.File, data []byte, logger logr.Logger) error {
	defer file.Close()

	if _, err := io.ReadAll(file); err != nil {
		logger.Error(err, "Failed to read from file")
		return err
	}

	if _, err := file.Write(data); err != nil {
		logger.Error(err, "Failed to write to file")
		return err
	}

	return nil
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
		if err := r.updateWithRetry(ctx, workload); err != nil {
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

	// First, check if the finalizer is already present to avoid unnecessary updates
	if controllerutil.ContainsFinalizer(workload, workloadFinalizer) {
		return nil
	}

	logger.Info("Adding Finalizer for Workload")

	// Get the latest state of the Workload Custom Resource
	if err := r.Get(ctx, types.NamespacedName{
		Namespace: workload.Namespace,
		Name:      workload.Name,
	}, workload); err != nil {
		logger.Error(err, "Failed to get Workload (In finalizer)")
		return err
	}

	controllerutil.AddFinalizer(workload, workloadFinalizer)

	if err := r.updateWithRetry(ctx, workload); err != nil {
		logger.Error(err, "Failed to add finalizer for Workload")
		return err
	}

	logger.Info("Finalizer added for Workload")
	return nil
}

func (r *WorkloadReconciler) handleWorkloadDeletion(ctx context.Context, workload *simulationv1alpha1.Workload) (bool, error) {
	logger := log.FromContext(ctx)

	isWorkloadMarkedToBeDeleted := workload.GetDeletionTimestamp() != nil
	if isWorkloadMarkedToBeDeleted {
		logger.Info("Workload is marked to be deleted")
		if controllerutil.ContainsFinalizer(workload, workloadFinalizer) {
			logger.Info("Performing Finalizer Operations for Workload before the deletion of the CR")

			// Add "Downgrade" status to indicate that this process is terminating
			meta.SetStatusCondition(&workload.Status.Conditions, metav1.Condition{
				Type:    "Degraded",
				Status:  metav1.ConditionUnknown,
				Reason:  "Finalizing",
				Message: fmt.Sprintf("Performing finalizer operations for the custom resource: %s ", workload.Name),
			})

			if err := r.updateStatusWithRetry(ctx, workload); err != nil {
				if apierrors.IsNotFound(err) {
					logger.Info("Workload resource not found. Ignoring since object must be deleted")
					return true, nil // Return empty result when the resource is not found to avoid requeue
				}
				logger.Error(err, "Failed to update Workload status")
				return false, err
			}

			r.Recorder.Event(workload, "Warning", "Deleting", fmt.Sprintf("Custom Resource %s is being deleted from the namespace %s", workload.Name, workload.Namespace))

			if err := r.Get(ctx, types.NamespacedName{
				Namespace: workload.Namespace,
				Name:      workload.Name,
			}, workload); err != nil {
				logger.Error(err, "failed to re-fetch")
				return false, err
			}

			meta.SetStatusCondition(&workload.Status.Conditions, metav1.Condition{
				Type:    "Downgraded",
				Status:  metav1.ConditionTrue,
				Reason:  "Finalizing",
				Message: fmt.Sprintf("Finalizer operations for custom resource %s name were successfully accomplished", workload.Name),
			})

			if err := r.updateStatusWithRetry(ctx, workload); err != nil {
				if apierrors.IsNotFound(err) {
					logger.Info("Workload resource not found. Ignoring since object must be deleted")
					return true, nil // Return empty result when the resource is not found to avoid requeue
				}

				errString := fmt.Sprintf("Failed to update status for Workload: %s using retry", workload.Name)
				logger.Error(err, errString)
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

			logger.Info("Finalizer removed for Workload")
			return true, nil
		}
	}
	return false, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *WorkloadReconciler) SetupWithManager(mgr ctrl.Manager) error {
	r.Recorder = mgr.GetEventRecorderFor("workload-controller")
	return ctrl.NewControllerManagedBy(mgr).
		For(&simulationv1alpha1.Workload{}).
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

func (r *WorkloadReconciler) updateStatusWithRetry(ctx context.Context, workload *simulationv1alpha1.Workload) error {
	return wait.ExponentialBackoff(wait.Backoff{
		Duration: 100 * time.Millisecond, // initial delay
		Factor:   2.0,                    // multiplier for subsequent delays
		Jitter:   0.1,                    // random variation in delay calculation
		Steps:    5,                      // max number of attempts
	}, func() (bool, error) {
		if err := r.Status().Update(ctx, workload); err != nil {
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
